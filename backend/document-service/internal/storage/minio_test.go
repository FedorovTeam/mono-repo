package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildBasePathUsesDateAndEntityPath(t *testing.T) {
	manager := &MinioKeyManager{prefix: "documents"}
	data := KeyGenerationData{DocumentID: "doc-123", PublishPath: "content/docs/readme.md"}

	now := time.Now().UTC()
	base := manager.buildBasePath(data)

	expected := fmt.Sprintf("documents/year=%04d/month=%02d/day=%02d/doc-123/content/docs/readme.md", now.Year(), now.Month(), now.Day())
	require.Equal(t, expected, base)
}

func TestBuildEntityPathFallsBackToIdentifiers(t *testing.T) {
	manager := &MinioKeyManager{prefix: "documents"}
	data := KeyGenerationData{DocumentID: "doc-123", FileName: "note.docx"}

	now := time.Now().UTC()
	base := manager.buildBasePath(data)

	expected := fmt.Sprintf("documents/year=%04d/month=%02d/day=%02d/doc-123/note.docx", now.Year(), now.Month(), now.Day())
	require.Equal(t, expected, base)
}
