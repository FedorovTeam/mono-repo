//go:build integration
// +build integration

package worker

import (
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"preprocess-service/internal/adapter/libreoffice"
	storagefs "preprocess-service/internal/adapter/storage/fs"
	"preprocess-service/internal/adapter/workspace"
	"preprocess-service/internal/config"
	"preprocess-service/internal/domain"
	"preprocess-service/internal/storage/memory"
	"preprocess-service/internal/usecase/ports"
	"preprocess-service/internal/usecase/preprocess"
	"preprocess-service/internal/usecase/submit"
)

func TestLibreOfficeLocalConversion(t *testing.T) {
	cfg := config.LoadTestConfig(t)

	if cfg.Preprocess.StorageModeOrDefault() != config.StorageModeFilesystem {
		t.Skip("integration test requires storage_mode=filesystem")
	}

	if _, err := os.Stat(cfg.Preprocess.LibreOfficePath); err != nil {
		t.Skipf("libreoffice binary is unavailable at %s: %v", cfg.Preprocess.LibreOfficePath, err)
	}

	if cfg.Preprocess.LocalSourceDir == "" || cfg.Preprocess.OutputDir == "" {
		t.Fatalf("local_source_dir and output_dir must be configured")
	}

	if cfg.Preprocess.LocalTargetDir == "" {
		t.Fatalf("local_target_dir must be configured")
	}

	jobID := "job-libreoffice-local"
	sourceKey := path.Join("testdata", "sample.doc")

	cleanupPaths := []string{
		filepath.Join(cfg.Preprocess.OutputDir, jobID),
		filepath.Join(cfg.Preprocess.WorkingDir),
	}

	if cfg.Preprocess.LocalTargetDir != "" {
		cleanupPaths = append(cleanupPaths, filepath.Join(cfg.Preprocess.LocalTargetDir, cfg.Preprocess.TargetBucket))
	}

	for _, p := range cleanupPaths {
		_ = os.RemoveAll(p)
	}

	localSourcePath := filepath.Join(cfg.Preprocess.LocalSourceDir, "local-bucket", filepath.FromSlash(sourceKey))
	if err := os.MkdirAll(filepath.Dir(localSourcePath), 0o755); err != nil {
		t.Fatalf("prepare local source dir: %v", err)
	}
	fixture := filepath.Join(filepath.Dir(cfg.Preprocess.LocalSourceDir), "sample.doc")
	// при твоём конфиге LocalSourceDir = .../preprocess-service/testdata/source
	// значит fixture = .../preprocess-service/testdata/sample.doc

	if err := copyFile(fixture, localSourcePath); err != nil {
		t.Fatalf("copy source file (%s): %v", fixture, err)
	}

	wsManager, err := workspace.NewFSManager(cfg.Preprocess.WorkingDir, cfg.Preprocess.OutputDir)
	if err != nil {
		t.Fatalf("init workspace manager: %v", err)
	}

	storageAdapter, err := storagefs.NewAdapter(cfg.Preprocess.LocalSourceDir, cfg.Preprocess.OutputDir)
	if err != nil {
		t.Fatalf("init fs storage adapter: %v", err)
	}

	logger := zap.NewNop()

	converter, err := libreoffice.NewExecConverter(cfg.Preprocess.LibreOfficePath, logger)
	if err != nil {
		t.Fatalf("init libreoffice converter: %v", err)
	}

	repo := memory.NewRepository()

	job := preprocess.Job{
		ID:              jobID,
		DocumentID:      "doc-libreoffice-local",
		SourceBucket:    "local-bucket",
		SourceKey:       sourceKey,
		SourceETag:      "etag-local-1",
		TargetBucket:    cfg.Preprocess.TargetBucket,
		TargetKey:       path.Join(jobID, "converted.docx"),
		TargetMediaType: cfg.Preprocess.TargetMediaType,
		CorrelationID:   "corr-local-1",
		Status:          domain.StatusPending,
		ErrorDetails:    map[string]string{},
		Attempts:        0,
		MaxAttempts:     domain.DefaultMaxAttempts,
		NextRunAt:       time.Now().UTC(),
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatalf("save job: %v", err)
	}

	serviceCfg := preprocess.Config{
		LibreOfficePath: cfg.Preprocess.LibreOfficePath,
		WorkingDir:      cfg.Preprocess.WorkingDir,
		OutputDir:       cfg.Preprocess.OutputDir,
		TargetBucket:    cfg.Preprocess.TargetBucket,
		TargetMediaType: cfg.Preprocess.TargetMediaType,
		ConvertTimeout:  cfg.Preprocess.ConvertTimeout,
		Backoff:         cfg.Preprocess.Backoff,
	}

	svc, err := preprocess.NewService(repo, storageAdapter, storageAdapter, converter, wsManager, noopCallback{}, serviceCfg, logger)
	if err != nil {
		t.Fatalf("init preprocess service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := svc.Process(ctx, job); err != nil {
		t.Fatalf("process job: %v", err)
	}

	expectedOutput := filepath.Join(cfg.Preprocess.OutputDir, jobID, "sample.docx")
	info, err := os.Stat(expectedOutput)
	if err != nil {
		t.Fatalf("expected output file not found at %s: %v", expectedOutput, err)
	}

	if info.Size() == 0 {
		t.Fatalf("output file is empty at %s", expectedOutput)
	}

	uploadedPath := filepath.Join(cfg.Preprocess.OutputDir, cfg.Preprocess.TargetBucket, filepath.FromSlash(job.TargetKey))
	if _, err := os.Stat(uploadedPath); err != nil {
		t.Fatalf("uploaded file not found at %s: %v", uploadedPath, err)
	}

	t.Logf("Converted file is available at %s", expectedOutput)
}

func TestLibreOfficeLocalConversionFailureStoresOutput(t *testing.T) {
	cfg := config.LoadTestConfig(t)

	if cfg.Preprocess.StorageModeOrDefault() != config.StorageModeFilesystem {
		t.Skip("integration test requires storage_mode=filesystem")
	}

	if cfg.Preprocess.LocalSourceDir == "" || cfg.Preprocess.OutputDir == "" {
		t.Fatalf("local_source_dir and output_dir must be configured")
	}

	if cfg.Preprocess.LocalTargetDir == "" {
		t.Fatalf("local_target_dir must be configured")
	}

	jobID := "job-libreoffice-failed"
	sourceKey := path.Join("testdata", "sample.doc")

	cleanupPaths := []string{
		filepath.Join(cfg.Preprocess.OutputDir, jobID),
		filepath.Join(cfg.Preprocess.WorkingDir),
	}

	if cfg.Preprocess.LocalTargetDir != "" {
		cleanupPaths = append(cleanupPaths, filepath.Join(cfg.Preprocess.LocalTargetDir, cfg.Preprocess.TargetBucket))
	}

	for _, p := range cleanupPaths {
		_ = os.RemoveAll(p)
	}

	localSourcePath := filepath.Join(cfg.Preprocess.LocalSourceDir, "local-bucket", filepath.FromSlash(sourceKey))
	if err := os.MkdirAll(filepath.Dir(localSourcePath), 0o755); err != nil {
		t.Fatalf("prepare local source dir: %v", err)
	}
	fixture := filepath.Join(filepath.Dir(cfg.Preprocess.LocalSourceDir), "sample.doc")
	if err := copyFile(fixture, localSourcePath); err != nil {
		t.Fatalf("copy source file (%s): %v", fixture, err)
	}

	wsManager, err := workspace.NewFSManager(cfg.Preprocess.WorkingDir, cfg.Preprocess.OutputDir)
	if err != nil {
		t.Fatalf("init workspace manager: %v", err)
	}

	storageAdapter, err := storagefs.NewAdapter(cfg.Preprocess.LocalSourceDir, cfg.Preprocess.OutputDir)
	if err != nil {
		t.Fatalf("init fs storage adapter: %v", err)
	}

	logger := zap.NewNop()

	scriptPath := filepath.Join(t.TempDir(), "fail.sh")
	script := "#!/bin/sh\necho libreoffice stdout\n echo libreoffice stderr 1>&2\n exit 9\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write failing script: %v", err)
	}

	converter, err := libreoffice.NewExecConverter(scriptPath, logger)
	if err != nil {
		t.Fatalf("init libreoffice converter: %v", err)
	}

	repo := memory.NewRepository()

	job := preprocess.Job{
		ID:              jobID,
		DocumentID:      "doc-libreoffice-failed",
		SourceBucket:    "local-bucket",
		SourceKey:       sourceKey,
		SourceETag:      "etag-local-2",
		TargetBucket:    cfg.Preprocess.TargetBucket,
		TargetKey:       path.Join(jobID, "converted.docx"),
		TargetMediaType: cfg.Preprocess.TargetMediaType,
		CorrelationID:   "corr-local-2",
		Status:          domain.StatusPending,
		ErrorDetails:    map[string]string{},
		Attempts:        0,
		MaxAttempts:     domain.DefaultMaxAttempts,
		NextRunAt:       time.Now().UTC(),
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatalf("save job: %v", err)
	}

	serviceCfg := preprocess.Config{
		LibreOfficePath: scriptPath,
		WorkingDir:      cfg.Preprocess.WorkingDir,
		OutputDir:       cfg.Preprocess.OutputDir,
		TargetBucket:    cfg.Preprocess.TargetBucket,
		TargetMediaType: cfg.Preprocess.TargetMediaType,
		ConvertTimeout:  cfg.Preprocess.ConvertTimeout,
		Backoff:         cfg.Preprocess.Backoff,
	}

	svc, err := preprocess.NewService(repo, storageAdapter, storageAdapter, converter, wsManager, noopCallback{}, serviceCfg, logger)
	if err != nil {
		t.Fatalf("init preprocess service: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := svc.Process(ctx, job); err == nil {
		t.Fatalf("expected process to fail")
	}

	storedJob, found, err := repo.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if !found {
		t.Fatalf("expected job to be stored")
	}

	if storedJob.ErrorDetails["lo_output"] == "" {
		t.Fatalf("expected libreoffice output in error details, got %+v", storedJob.ErrorDetails)
	}
	if storedJob.ErrorDetails["lo_exit_code"] != "9" {
		t.Fatalf("expected libreoffice exit code in error details, got %+v", storedJob.ErrorDetails)
	}
	if storedJob.ErrorDetails["lo_duration_ms"] == "" {
		t.Fatalf("expected libreoffice duration in error details, got %+v", storedJob.ErrorDetails)
	}

	submitService, err := submit.NewService(repo, cfg.Preprocess.TargetBucket, cfg.Preprocess.TargetKeyPrefix, logger)
	if err != nil {
		t.Fatalf("init submit service: %v", err)
	}

	status, err := submitService.GetStatus(ctx, submit.StatusQuery{JobID: jobID})
	if err != nil {
		t.Fatalf("get status: %v", err)
	}

	if status.ErrorDetails["lo_output"] == "" || status.ErrorDetails["lo_exit_code"] == "" {
		t.Fatalf("expected status to include libreoffice error details, got %+v", status.ErrorDetails)
	}
}

type noopCallback struct{}

func (noopCallback) Send(_ context.Context, _ string, _ ports.CallbackPayload, _ int) (bool, error) {
	return false, nil
}

func copyFile(src, dst string) error {
	source, err := os.Open(filepath.Clean(src))
	if err != nil {
		return err
	}
	defer source.Close()

	target, err := os.Create(filepath.Clean(dst))
	if err != nil {
		return err
	}
	defer target.Close()

	if _, err := io.Copy(target, source); err != nil {
		return err
	}

	return nil
}
