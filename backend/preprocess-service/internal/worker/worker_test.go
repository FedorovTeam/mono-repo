package worker

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeConfigDefaults(t *testing.T) {
	cfg := normalizeConfig(Config{})

	if cfg.Concurrency != 1 {
		t.Fatalf("expected default concurrency 1, got %d", cfg.Concurrency)
	}
	if cfg.ConvertTimeout <= 0 {
		t.Fatalf("expected convert timeout to be set")
	}
	if cfg.Backoff <= 0 {
		t.Fatalf("expected backoff to be set")
	}
	if cfg.PollInterval <= 0 {
		t.Fatalf("expected poll interval to be set")
	}
	if cfg.TargetMediaType != defaultTargetMediaType {
		t.Fatalf("unexpected media type: %s", cfg.TargetMediaType)
	}
}

func TestOutputFilePath(t *testing.T) {
	w := Worker{}
	result := w.outputFilePath("/tmp/sample/input.odt", "/tmp/out")
	expected := "/tmp/out/input.docx"
	if result != expected {
		t.Fatalf("expected %s, got %s", expected, result)
	}
}

func TestFailureDecisionTemporary(t *testing.T) {
	job := QueueJob{Attempts: 1, MaxAttempts: 3}
	before := time.Now()

	status, nextRun, code, message := failureDecision(job, temporaryError("download_failed", errors.New("network")), time.Second)

	if status != statusFailed {
		t.Fatalf("expected status %s, got %s", statusFailed, status)
	}
	if code != "download_failed" {
		t.Fatalf("unexpected code: %s", code)
	}
	if message == "" {
		t.Fatal("expected message to be filled")
	}
	if nextRun.Before(before.Add(900*time.Millisecond)) || nextRun.After(before.Add(1100*time.Millisecond)) {
		t.Fatalf("unexpected next run at: %s", nextRun)
	}
}

func TestFailureDecisionPermanent(t *testing.T) {
	job := QueueJob{Attempts: 2, MaxAttempts: 3}
	status, nextRun, code, _ := failureDecision(job, permanentError("convert_failed", errors.New("broken")), time.Minute)

	if status != statusStopped {
		t.Fatalf("expected status %s, got %s", statusStopped, status)
	}
	if code != "convert_failed" {
		t.Fatalf("unexpected code: %s", code)
	}
	if nextRun.IsZero() {
		t.Fatalf("expected next run timestamp")
	}
}

func TestFailureDecisionMaxAttempts(t *testing.T) {
	job := QueueJob{Attempts: 5, MaxAttempts: 5}
	status, _, _, _ := failureDecision(job, temporaryError("convert_timeout", errors.New("deadline")), time.Second)

	if status != statusStopped {
		t.Fatalf("expected stopped when attempts exhausted, got %s", status)
	}
}
