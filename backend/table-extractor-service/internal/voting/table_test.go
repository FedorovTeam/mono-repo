package voting

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractColumnsDetectsVotingHeader(t *testing.T) {
	page, err := ParseBBoxHTML(sampleVotingBBox())
	require.NoError(t, err)

	cols, nameXMax, headerY, err := ExtractColumns(page)
	require.NoError(t, err)
	require.Len(t, cols, 5)
	require.Greater(t, nameXMax, 0.0)
	require.InDelta(t, 20.0, headerY, 0.5)
	require.Equal(t, ColPresent, cols[0].Key)
	require.Equal(t, ColNoVote, cols[len(cols)-1].Key)
}

func TestExtractColumnsFailsWithoutKeywords(t *testing.T) {
	page, err := ParseBBoxHTML(`<page number="1" width="200" height="200"><word xMin="10" yMin="10" xMax="20" yMax="12">Заголовок</word></page>`)
	require.NoError(t, err)

	_, _, _, err = ExtractColumns(page)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Присутствует")
}

func TestToMarkdownOmitsHeaderText(t *testing.T) {
	doc := Document{
		HeaderText: "Интро",
		Columns:    []Column{{Key: ColPresent, Title: "Присутствует"}},
		Rows: []Row{{
			Number: 1,
			Name:   "Иванов И.И.",
			Marks:  map[ColumnKey]bool{ColPresent: true},
		}},
	}

	md := ToMarkdown(doc)

	require.NotContains(t, md, "Интро")
	require.Equal(t, 1, strings.Count(md, "<!-- tables:start -->"))
	require.Equal(t, 1, strings.Count(md, "<!-- tables:end -->"))
	require.Contains(t, md, "| № | Ф.И.О. | Присутствует |")
	require.Contains(t, md, "Иванов И.И.")
}

func sampleVotingBBox() string {
	return `<page number="1" width="450" height="450">
<word xMin="200" yMin="20" xMax="240" yMax="24">Присутствует</word>
<word xMin="250" yMin="20" xMax="260" yMax="24">За</word>
<word xMin="280" yMin="20" xMax="320" yMax="24">Против</word>
<word xMin="330" yMin="20" xMax="380" yMax="24">Воздержался</word>
<word xMin="390" yMin="20" xMax="400" yMax="24">Не</word>
<word xMin="405" yMin="20" xMax="440" yMax="24">голосовал</word>
<word xMin="20" yMin="80" xMax="30" yMax="84">1</word>
<word xMin="60" yMin="80" xMax="110" yMax="84">Иванов</word>
<word xMin="120" yMin="80" xMax="150" yMax="84">И.И.</word>
</page>`
}
