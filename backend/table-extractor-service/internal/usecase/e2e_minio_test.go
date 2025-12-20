package usecase

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"

	"github.com/mono-repo/table-extractor-service/db/migrations"
	"github.com/mono-repo/table-extractor-service/internal/domain"
	"github.com/mono-repo/table-extractor-service/internal/repository/sqlrepo"
	"github.com/mono-repo/table-extractor-service/internal/storage/database"
)

func TestEndToEndWithMinioFixture(t *testing.T) {
	ctx := context.Background()

	minioEndpoint := startMinioContainer(t, ctx)

	client, err := minio.New(minioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4("minioadmin", "minioadmin", ""),
		Secure: false,
	})
	require.NoError(t, err)

	originalBucket := "table-extractor-originals"
	artifactsBucket := "table-extractor-artifacts"
	for _, bucket := range []string{originalBucket, artifactsBucket} {
		err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
		if err != nil {
			resp := minio.ToErrorResponse(err)
			if resp.Code != "BucketAlreadyOwnedByYou" {
				require.NoError(t, err)
			}
		}
	}

	fixture := loadFixtureArtifacts(t, filepath.Join("..", "..", "fixtures", "regular"))

	originalKey := "input/document.pdf"
	_, err = client.PutObject(ctx, originalBucket, originalKey, bytes.NewReader(fixture.pdf), int64(len(fixture.pdf)), minio.PutObjectOptions{
		ContentType: "application/pdf",
	})
	require.NoError(t, err)

	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	require.NoError(t, migrations.Apply(ctx, db, zap.NewNop()))

	repo := sqlrepo.NewSQLRepository(db, database.DialectSQLite)
	storage := &minioStorage{client: client}
	extractor := &goldenExtractor{
		fixture: fixture,
		t:       t,
	}

	uc := NewTableExtractorUseCase(Dependencies{
		Queue:     repo,
		Storage:   storage,
		FileCache: newTestFileCache(storage),
		Extractor: extractor,
		Clock:     fixedClock{now: time.Date(2025, 1, 10, 12, 0, 0, 0, time.UTC)},
		IDGenerator: fixedIDGenerator{
			id: uuid.MustParse("00000000-0000-0000-0000-000000000111"),
		},
	}, Config{
		QueueLimit:             5,
		DefaultArtifactsBucket: artifactsBucket,
		DefaultArtifactsPrefix: "tables",
	}, zap.NewNop())

	job, err := uc.SubmitExtraction(ctx, SubmitExtractionRequest{
		DocumentID:        "doc-minio",
		OriginalBucket:    originalBucket,
		OriginalObjectKey: originalKey,
		CorrelationID:     "corr-e2e",
	})
	require.NoError(t, err)

	processed, err := uc.ProcessExtractionJob(ctx)
	require.NoError(t, err)
	require.True(t, processed)

	stored, err := uc.GetJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, domain.StatusDone, stored.Status)
	require.NotNil(t, stored.FinishedAt)

	assertObjectEquals(t, ctx, client, artifactsBucket, stored.Artifacts.RawJSON.ObjectKey, fixture.raw)
	assertObjectEquals(t, ctx, client, artifactsBucket, stored.Artifacts.NormalizedJSON.ObjectKey, fixture.normalized)
	assertObjectEquals(t, ctx, client, artifactsBucket, stored.Artifacts.Markdown.ObjectKey, fixture.markdown)
	assertObjectEquals(t, ctx, client, artifactsBucket, stored.Artifacts.Manifest.ObjectKey, fixture.manifest)

	for name, payload := range fixture.debug {
		objectKey := filepath.Join(stored.Artifacts.DebugPrefix.ObjectKey, filepath.Base(name))
		assertObjectEquals(t, ctx, client, stored.Artifacts.DebugPrefix.Bucket, objectKey, payload)
	}
}

func startMinioContainer(t *testing.T, ctx context.Context) string {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker binary not available for testcontainers")
	}

	infoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(infoCtx, "docker", "info").Run(); err != nil {
		t.Skip("docker daemon not available for testcontainers")
	}

	req := testcontainers.ContainerRequest{
		Image:        "quay.io/minio/minio:RELEASE.2024-11-07T23-46-52Z",
		ExposedPorts: []string{"9000/tcp"},
		Env: map[string]string{
			"MINIO_ROOT_USER":     "minioadmin",
			"MINIO_ROOT_PASSWORD": "minioadmin",
		},
		Cmd:        []string{"server", "/data"},
		WaitingFor: wait.ForHTTP("/minio/health/ready").WithPort("9000/tcp").WithStartupTimeout(2 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	host, err := container.Host(ctx)
	require.NoError(t, err)
	mappedPort, err := container.MappedPort(ctx, "9000/tcp")
	require.NoError(t, err)

	return host + ":" + mappedPort.Port()
}

type fixtureArtifacts struct {
	pdf        []byte
	raw        []byte
	normalized []byte
	markdown   []byte
	manifest   []byte
	debug      map[string][]byte
}

func loadFixtureArtifacts(t *testing.T, baseDir string) fixtureArtifacts {
	t.Helper()

	readFile := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join(baseDir, name))
		require.NoError(t, err)
		return data
	}

	pdfBase64 := readFile("document.base64")
	pdfBytes := make([]byte, base64.StdEncoding.DecodedLen(len(pdfBase64)))
	n, err := base64.StdEncoding.Decode(pdfBytes, bytes.TrimSpace(pdfBase64))
	require.NoError(t, err)
	pdfBytes = pdfBytes[:n]

	debugDir := filepath.Join(baseDir, "debug")
	debugFiles := make(map[string][]byte)
	entries, err := os.ReadDir(debugDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			debugFiles[entry.Name()] = readFile(filepath.Join("debug", entry.Name()))
		}
	}

	return fixtureArtifacts{
		pdf:        pdfBytes,
		raw:        readFile("tables.raw.json"),
		normalized: readFile("tables.normalized.json"),
		markdown:   readFile("tables.md"),
		manifest:   readFile("manifest.json"),
		debug:      debugFiles,
	}
}

type goldenExtractor struct {
	fixture fixtureArtifacts
	t       *testing.T
}

func (g *goldenExtractor) Extract(_ context.Context, pdfPath string) (ExtractionResult, error) {
	content, err := os.ReadFile(pdfPath)
	require.NoError(g.t, err)
	require.Equal(g.t, g.fixture.pdf, content)

	return ExtractionResult{
		RawJSON:        g.fixture.raw,
		NormalizedJSON: g.fixture.normalized,
		Markdown:       g.fixture.markdown,
		Manifest:       g.fixture.manifest,
		DebugFiles:     g.fixture.debug,
	}, nil
}

func assertObjectEquals(t *testing.T, ctx context.Context, client *minio.Client, bucket, key string, expected []byte) {
	t.Helper()

	reader, err := client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	require.NoError(t, err)
	defer reader.Close()

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, expected, data)
}

var _ Extractor = (*goldenExtractor)(nil)

type minioStorage struct {
	client *minio.Client
}

func (s *minioStorage) Download(ctx context.Context, bucket, objectKey string) (io.ReadCloser, error) {
	return s.client.GetObject(ctx, bucket, objectKey, minio.GetObjectOptions{})
}

func (s *minioStorage) Upload(ctx context.Context, bucket, objectKey, contentType string, content io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, bucket, objectKey, content, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}
