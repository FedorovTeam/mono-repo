package domain

import "path"

func BuildArtifacts(bucket, prefix string) TableArtifacts {
	return BuildArtifactsWithMarkdown(bucket, prefix, ArtifactLocation{})
}

func BuildArtifactsWithMarkdown(bucket, prefix string, markdown ArtifactLocation) TableArtifacts {
	markdownBucket := markdown.Bucket
	if markdownBucket == "" {
		markdownBucket = bucket
	}
	markdownKey := markdown.ObjectKey
	if markdownKey == "" {
		markdownKey = path.Join(prefix, "tables.md")
	}

	return TableArtifacts{
		RawJSON: ArtifactLocation{
			Bucket:    bucket,
			ObjectKey: path.Join(prefix, "tables.raw.json"),
		},
		NormalizedJSON: ArtifactLocation{
			Bucket:    bucket,
			ObjectKey: path.Join(prefix, "tables.normalized.json"),
		},
		Markdown: ArtifactLocation{
			Bucket:    markdownBucket,
			ObjectKey: markdownKey,
		},
		Manifest: ArtifactLocation{
			Bucket:    bucket,
			ObjectKey: path.Join(prefix, "manifest.json"),
		},
		DebugPrefix: ArtifactLocation{
			Bucket:    bucket,
			ObjectKey: path.Join(prefix, "debug"),
		},
	}
}

func ActiveStatuses() []TableExtractionStatus {
	return []TableExtractionStatus{StatusPending, StatusProcessing}
}
