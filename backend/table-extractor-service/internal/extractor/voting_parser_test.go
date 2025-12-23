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
	putMark(255, 82) // За

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}
