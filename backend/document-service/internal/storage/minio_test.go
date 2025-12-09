package storage

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildBasePathUsesDocumentIDAndUUIDFileName(t *testing.T) {
	manager := &MinioKeyManager{prefix: "documents"}
	data := KeyGenerationData{DocumentID: "doc-123", PublishPath: "content/docs/readme.md", EntitySpecificPath: "domain/readme"}

	now := time.Now().UTC()
	base := manager.buildBasePath(data)

	segments := strings.Split(base, "/")
	require.Len(t, segments, 6)
	require.Equal(t, "documents", segments[0])
	require.Equal(t, fmt.Sprintf("year=%04d", now.Year()), segments[1])
	require.Equal(t, fmt.Sprintf("month=%02d", int(now.Month())), segments[2])
	require.Equal(t, fmt.Sprintf("day=%02d", now.Day()), segments[3])
	require.Equal(t, data.DocumentID, segments[4])
	fileName := segments[5]

	require.True(t, strings.HasSuffix(fileName, ".md"))
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.md$`), fileName)
	require.NotContains(t, base, "domain/readme")
}

func TestBuildEntityPathFallsBackToUUIDWithoutDocumentID(t *testing.T) {
	manager := &MinioKeyManager{prefix: "documents"}
	data := KeyGenerationData{FileName: "note.docx"}

	now := time.Now().UTC()
	base := manager.buildBasePath(data)

	segments := strings.Split(base, "/")
	require.Len(t, segments, 5)
	require.Equal(t, "documents", segments[0])
	require.Equal(t, fmt.Sprintf("year=%04d", now.Year()), segments[1])
	require.Equal(t, fmt.Sprintf("month=%02d", int(now.Month())), segments[2])
	require.Equal(t, fmt.Sprintf("day=%02d", now.Day()), segments[3])

	fileName := segments[4]
	require.NotEqual(t, "note.docx", fileName)
	require.True(t, strings.HasSuffix(fileName, ".docx"))
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.docx$`), fileName)
}

func TestEnsureMarkdownExtension_ReplacesExistingExtension(t *testing.T) {
	base := "documents/doc-1.docx"

	markdown := ensureMarkdownExtension(base)

	if markdown != "documents/doc-1.md" {
		t.Fatalf("unexpected markdown key: %s", markdown)
	}
}

func TestEnsureMarkdownExtension_AppendsMarkdownExtension(t *testing.T) {
	base := "documents/doc-1"

	markdown := ensureMarkdownExtension(base)

	if markdown != "documents/doc-1.md" {
		t.Fatalf("unexpected markdown key: %s", markdown)
	}
}
