package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"backend-document-service/internal/domain"
	"backend-document-service/pkg/config"
)

func TestNewStageRouterUsesRouterConfig(t *testing.T) {
	router := newStageRouter(config.Router{
		TableExtractorEnabled:    true,
		TableExtractorExtensions: []string{"pdf"},
		ArtifactsBucket:          "global-bucket",
		ArtifactsPrefixTemplate:  "tables/{{ .DocumentID }}/",
		TableDetectionMinCount:   3,
		RepositoryRules: []config.RepositoryRouterRule{
			{
				Owner:                   "team",
				Repository:              "docs",
				ArtifactsBucket:         "repo-bucket",
				ArtifactsPrefixTemplate: "repo/{{ .DocumentID }}/",
			},
		},
	})

	doc := &domain.Document{
		Owner:             "team",
		Repository:        "docs",
		OriginalObjectKey: "report.pdf",
	}

	cfg := router.TableExtractionConfig(doc)

	require.Equal(t, "repo-bucket", cfg.ArtifactsBucket)
	require.Equal(t, "repo/{{ .DocumentID }}/", cfg.ArtifactsPrefixTmpl)

	require.False(t, router.ShouldExtractTablesAfterConvert(doc, true, 2, nil, false))
	require.True(t, router.ShouldExtractTablesAfterConvert(doc, true, 3, nil, false))
}
