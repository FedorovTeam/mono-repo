package extractor

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mono-repo/table-extractor-service/internal/voting"
)

func TestVotingParserParsesVotingTable(t *testing.T) {
	parser := NewVotingTableParser()
	tables, err := parser.ParseTables(ParseInput{
		LayoutText: sampleVotingLayoutText(),
		BBoxHTML:   sampleVotingBBoxHTML(),
		PagePNGs:   map[int][]byte{1: sampleVotingPNG(t)},
		StartPage:  1,
	})
	require.NoError(t, err)
	require.NotNil(t, tables.Document)
	require.Equal(t, "voting_table", tables.NormalizedTables[0].Name)
	require.Equal(t, "Иванов И.И.", tables.Document.Rows[0].Name)
	require.True(t, tables.Document.Rows[0].Marks[voting.ColPresent])
	require.True(t, tables.Document.Rows[0].Marks[voting.ColZa])
	require.False(t, tables.Document.Rows[0].Marks[voting.ColProtiv])
	require.Contains(t, tables.HeaderText, "Голосование")
}

func sampleVotingLayoutText() string {
	return "Повестка дня\nГолосование по вопросу\n\nФ.И.О. Присутствует За Против Воздержался Не голосовал\n1 Иванов И.И.\n"
}

func sampleVotingBBoxHTML() string {
	return `<page number="1" width="450" height="450">
<word xMin="200" yMin="20" xMax="240" yMax="24">Присутствует</word>
<word xMin="270" yMin="20" xMax="280" yMax="24">За</word>
<word xMin="320" yMin="20" xMax="340" yMax="24">Против</word>
<word xMin="380" yMin="20" xMax="410" yMax="24">Воздержался</word>
<word xMin="420" yMin="20" xMax="430" yMax="24">Не</word>
<word xMin="435" yMin="20" xMax="445" yMax="24">голосовал</word>
<word xMin="20" yMin="80" xMax="30" yMax="84">1</word>
<word xMin="60" yMin="80" xMax="110" yMax="84">Иванов</word>
<word xMin="120" yMin="80" xMax="150" yMax="84">И.И.</word>
</page>`
}

func sampleVotingPNG(t testing.TB) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 450, 450))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	putMark := func(x, y int) {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				px := x + dx
				py := y + dy
				if px >= 0 && px < img.Rect.Max.X && py >= 0 && py < img.Rect.Max.Y {
					img.SetGray(px, py, color.Gray{Y: 0})
				}
			}
		}
	}
	putMark(220, 82) // Присутствует
	putMark(275, 82) // За

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestVotingParserMergesRowsAcrossPages(t *testing.T) {
	parser := NewVotingTableParser()
	tables, err := parser.ParseTables(ParseInput{
		LayoutText: sampleVotingLayoutTwoPages(),
		BBoxHTML:   sampleVotingBBoxHTMLTwoPages(),
		PagePNGs:   sampleVotingPNGsTwoPages(t),
		StartPage:  1,
	})
	require.NoError(t, err)

	require.NotNil(t, tables.Document)
	require.Len(t, tables.Document.Rows, 3)
	require.Equal(t, 1, tables.Document.Rows[0].Number)
	require.True(t, tables.Document.Rows[0].Marks[voting.ColPresent])
	require.True(t, tables.Document.Rows[0].Marks[voting.ColZa])

	require.Equal(t, 2, tables.Document.Rows[1].Number)
	require.Equal(t, "Петров Петр Петрович", tables.Document.Rows[1].Name)
	require.True(t, tables.Document.Rows[1].Marks[voting.ColPresent])
	require.True(t, tables.Document.Rows[1].Marks[voting.ColZa])

	require.Equal(t, 3, tables.Document.Rows[2].Number)
	require.True(t, tables.Document.Rows[2].Marks[voting.ColZa])
	require.True(t, tables.Document.Rows[2].Marks[voting.ColProtiv])

	markdown := parser.RenderMarkdown(tables)
	expected := `<!-- tables:start -->
| № | Ф.И.О. | Присутствует | За | Против | Воздержался | Не голосовал |
|---|--------|---|---|---|---|---|
| 1 | Иванов И.И. | ✓ | ✓ |  |  |  |
| 2 | Петров Петр Петрович | ✓ | ✓ |  |  |  |
| 3 | Сидоров С.С. |  | ✓ | ✓ |  |  |
<!-- tables:end -->`
	require.Equal(t, expected, markdown)
}

func sampleVotingLayoutTwoPages() string {
	return "Повестка дня\nГолосование по вопросу\n\nФ.И.О. Присутствует За Против Воздержался Не голосовал\n1 Иванов И.И.\n"
}

func sampleVotingBBoxHTMLTwoPages() string {
	return `<page number="1" width="450" height="450">
<word xMin="200" yMin="20" xMax="240" yMax="24">Присутствует</word>
<word xMin="270" yMin="20" xMax="280" yMax="24">За</word>
<word xMin="320" yMin="20" xMax="340" yMax="24">Против</word>
<word xMin="380" yMin="20" xMax="410" yMax="24">Воздержался</word>
<word xMin="420" yMin="20" xMax="430" yMax="24">Не</word>
<word xMin="435" yMin="20" xMax="445" yMax="24">голосовал</word>
<word xMin="20" yMin="80" xMax="30" yMax="84">1</word>
<word xMin="60" yMin="80" xMax="110" yMax="84">Иванов</word>
<word xMin="120" yMin="80" xMax="150" yMax="84">И.И.</word>
<word xMin="20" yMin="100" xMax="30" yMax="104">2</word>
<word xMin="60" yMin="100" xMax="110" yMax="104">Петров</word>
<word xMin="120" yMin="100" xMax="140" yMax="104">П.</word>
</page>
<page number="2" width="450" height="450">
<word xMin="200" yMin="20" xMax="240" yMax="24">Присутствует</word>
<word xMin="270" yMin="20" xMax="280" yMax="24">За</word>
<word xMin="320" yMin="20" xMax="340" yMax="24">Против</word>
<word xMin="380" yMin="20" xMax="410" yMax="24">Воздержался</word>
<word xMin="420" yMin="20" xMax="430" yMax="24">Не</word>
<word xMin="435" yMin="20" xMax="445" yMax="24">голосовал</word>
<word xMin="20" yMin="60" xMax="30" yMax="64">0</word>
<word xMin="20" yMin="70" xMax="30" yMax="74">1</word>
<word xMin="20" yMin="90" xMax="30" yMax="94">2</word>
<word xMin="60" yMin="90" xMax="110" yMax="94">Петров</word>
<word xMin="120" yMin="90" xMax="150" yMax="94">Петр</word>
<word xMin="160" yMin="90" xMax="190" yMax="94">Петрович</word>
<word xMin="80" yMin="90" xMax="150" yMax="94">Присутствует</word>
<word xMin="20" yMin="140" xMax="30" yMax="144">3</word>
<word xMin="60" yMin="140" xMax="110" yMax="144">Сидоров</word>
<word xMin="120" yMin="140" xMax="150" yMax="144">С.С.</word>
</page>`
}

func sampleVotingPNGsTwoPages(t testing.TB) map[int][]byte {
	t.Helper()
	return map[int][]byte{
		1: buildVotingPagePNG(t, []markSpec{
			{y: 82, cols: []voting.ColumnKey{voting.ColPresent, voting.ColZa}},
			{y: 102, cols: []voting.ColumnKey{voting.ColPresent}},
		}),
		2: buildVotingPagePNG(t, []markSpec{
			{y: 92, cols: []voting.ColumnKey{voting.ColZa}},
			{y: 142, cols: []voting.ColumnKey{voting.ColZa, voting.ColProtiv}},
		}),
	}
}

type markSpec struct {
	y    int
	cols []voting.ColumnKey
}

func buildVotingPagePNG(t testing.TB, marks []markSpec) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 450, 450))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	colX := map[voting.ColumnKey]int{
		voting.ColPresent:  220,
		voting.ColZa:       275,
		voting.ColProtiv:   330,
		voting.ColVozderzh: 395,
		voting.ColNoVote:   433,
	}
	putMark := func(x, y int) {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				px := x + dx
				py := y + dy
				if px >= 0 && px < img.Rect.Max.X && py >= 0 && py < img.Rect.Max.Y {
					img.SetGray(px, py, color.Gray{Y: 0})
				}
			}
		}
	}
	for _, spec := range marks {
		for _, col := range spec.cols {
			if x, ok := colX[col]; ok {
				putMark(x, spec.y)
			}
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}
