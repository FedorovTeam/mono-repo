package extractor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultParserMergesTablesAcrossPages(t *testing.T) {
	parser := DefaultParser{}
	layout := "Name  Votes\nAlice  10\n\fName  Votes\nName  Votes\nBob  7\nCharlie  2\n"

	tables, err := parser.ParseTables(ParseInput{LayoutText: layout, StartPage: 2})
	require.NoError(t, err)

	require.Len(t, tables.RawTables, 1)
	raw := tables.RawTables[0]
	require.Equal(t, 2, raw.Page)
	require.ElementsMatch(t, []int{2, 3}, raw.Pages)
	require.Len(t, raw.Rows, 3)
	require.Equal(t, []string{"Alice", "10"}, raw.Rows[0])
	require.Equal(t, []string{"Bob", "7"}, raw.Rows[1])
	require.Equal(t, []string{"Charlie", "2"}, raw.Rows[2])

	require.Len(t, tables.NormalizedTables, 1)
	norm := tables.NormalizedTables[0]
	require.Equal(t, 2, norm.Page)
	require.ElementsMatch(t, []int{2, 3}, norm.Pages)
	require.Len(t, norm.Rows, 3)
	for _, row := range norm.Rows {
		require.NotEqual(t, "Name", row["Name"])
		require.NotEqual(t, "Votes", row["Votes"])
	}
	require.Equal(t, "Alice", norm.Rows[0]["Name"])
	require.Equal(t, "10", norm.Rows[0]["Votes"])
	require.Equal(t, "Bob", norm.Rows[1]["Name"])
	require.Equal(t, "7", norm.Rows[1]["Votes"])
	require.Equal(t, "Charlie", norm.Rows[2]["Name"])
	require.Equal(t, "2", norm.Rows[2]["Votes"])
}
