package domain

import "path"

func BuildArtifacts(bucket, prefix string) TableArtifacts {
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
			Bucket:    bucket,
			ObjectKey: path.Join(prefix, "tables.md"),
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
