package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConvertPayloadTableMetadataRoundTrip(t *testing.T) {
	payload := ConvertPayload{
		OriginalBucket: "orig",
		TableMetadata:  TableMetadata{TablesCount: 2, Pages: []int32{2, 1}},
		TablePages:     []int32{2, 3},
		TableCount:     1,
	}

	data := payload.ToMap()
	rawMeta, ok := data["table_metadata"]
	require.True(t, ok)
	meta, ok := rawMeta.(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, 2, meta["tables_count"])
	require.ElementsMatch(t, []int32{1, 2}, meta["pages"])

	restored := ConvertPayloadFromMap(data)
	require.EqualValues(t, 2, restored.TableMetadata.TablesCount)
	require.ElementsMatch(t, []int32{1, 2}, restored.TableMetadata.Pages)
	require.ElementsMatch(t, []int32{2, 3}, restored.TablePages)

	count, pages := restored.TableInfo()
	require.Equal(t, 2, count)
	require.Equal(t, []int32{1, 2, 3}, pages)
}

func TestConvertPayloadTableInfoUsesPagesWhenCountMissing(t *testing.T) {
	payload := ConvertPayload{
		TablePages: []int32{5, 5, 2},
	}

	count, pages := payload.TableInfo()
	require.Equal(t, 2, count)
	require.Equal(t, []int32{2, 5}, pages)
}

func TestConvertPayloadHasMarkdownTablesHint(t *testing.T) {
	payload := ConvertPayload{
		MarkdownObjectKey:        "reports/tables.md",
		DoclingMarkdownObjectKey: "reports/alt.md",
	}
	require.True(t, payload.HasMarkdownTablesHint())

	payload = ConvertPayload{
		TableCount: 0,
		TablePages: nil,
		TableMetadata: TableMetadata{
			TablesCount: 0,
			Pages:       nil,
		},
		MarkdownObjectKey: "reports/readme.md",
	}
	require.False(t, payload.HasMarkdownTablesHint())
}
