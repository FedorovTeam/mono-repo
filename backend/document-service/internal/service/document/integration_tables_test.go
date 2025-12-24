package document

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"backend-document-service/internal/domain"
	"github.com/stretchr/testify/require"
)

type manifestStub struct {
	TableCount int `json:"table_count"`
}

func loadFixture(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(parts...)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read fixture %s", path)
	return string(data)
}

func loadManifest(t *testing.T, parts ...string) manifestStub {
	t.Helper()
	path := filepath.Join(parts...)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "read manifest %s", path)
	var manifest manifestStub
	require.NoError(t, json.Unmarshal(data, &manifest), "unmarshal manifest %s", path)
	return manifest
}

func withCapturedLogs(t *testing.T, fn func()) string {
	t.Helper()
	buf := &bytes.Buffer{}
	prevWriter := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	defer log.SetOutput(prevWriter)
	defer log.SetFlags(prevFlags)
	fn()
	return buf.String()
}

func TestMergeSkipsTablesWhenDoclingReportsZeroAndTablesDominate(t *testing.T) {
	base := loadFixture(t, "..", "..", "..", "..", "test", "runs", "docling-zero-ratio", "base.md")
	tables := loadFixture(t, "..", "..", "..", "..", "test", "runs", "docling-zero-ratio", "tables.md")
	manifest := loadManifest(t, "..", "..", "..", "..", "test", "runs", "docling-zero-ratio", "manifest.json")

	settings := domain.NormalizeMergeSettings(domain.MergeSettings{MaxTablesShare: 0.9, SkipRatioThreshold: 0.9, SkipWhenDoclingZero: true, LogMode: domain.MergeLogModeDecision})

	var result mergeResult
	logs := withCapturedLogs(t, func() {
		var err error
		result, err = mergeMarkdownWithTablesWithOptions(base, tables, mergeOptions{
			TableMetadata: domain.TableMetadata{TablesCount: int32(manifest.TableCount)},
			Settings:      settings,
			DocumentID:    "docling-zero-ratio",
		})
		require.NoError(t, err)
	})

	require.False(t, result.TablesMerged)
	require.NotEmpty(t, result.SkipReason)
	require.Contains(t, result.SkipReason, "table metadata is empty")
	require.Equal(t, strings.TrimSpace(base), strings.TrimSpace(result.Content))

	pipeline := loadFixture(t, "..", "..", "..", "..", "test", "runs", "docling-zero-ratio", "pipeline.log")
	require.Contains(t, pipeline, "table_count=0")
	require.Contains(t, pipeline, "skip_reason")
	require.Contains(t, logs, "skip_tables")
}

func TestVotingTablesStayForIltumen3617(t *testing.T) {
	base := loadFixture(t, "..", "..", "..", "..", "test", "runs", "iltumen-3617", "base.md")
	tables := loadFixture(t, "..", "..", "..", "..", "test", "runs", "iltumen-3617", "tables.md")
	manifest := loadManifest(t, "..", "..", "..", "..", "test", "runs", "iltumen-3617", "manifest.json")

	settings := domain.NormalizeMergeSettings(domain.MergeSettings{MaxTablesShare: 15, SkipRatioThreshold: 15, LogMode: domain.MergeLogModeDecision})

	var result mergeResult
	withCapturedLogs(t, func() {
		var err error
		result, err = mergeMarkdownWithTablesWithOptions(base, tables, mergeOptions{
			TableMetadata: domain.TableMetadata{TablesCount: int32(manifest.TableCount)},
			Settings:      settings,
			DocumentID:    "iltumen-3617",
		})
		require.NoError(t, err)
	})

	require.Empty(t, result.SkipReason)
	require.True(t, strings.HasPrefix(strings.TrimSpace(result.Content), strings.TrimSpace(base)))
	require.Contains(t, result.Content, "<!-- tables:start -->")
	require.Contains(t, result.Content, "| 1 | Алексеев Гаврил Николаевич | ✓ | ✓ |")
	require.Contains(t, result.Content, "| 22 | Заморщикова Нюргуяна Юрьевна | ✓ |  |  |  | ✓ |")
	require.Contains(t, result.Content, "| 27 | Константинов Эдгар Валентинович | ✓ |  | ✓ |  |  |")

	pipeline := loadFixture(t, "..", "..", "..", "..", "test", "runs", "iltumen-3617", "pipeline.log")
	require.Contains(t, pipeline, "table_count=70")
	require.Contains(t, pipeline, "tables_merged=true")
}
