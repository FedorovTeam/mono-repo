package pdf2md

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ExtractColumns detects voting table column positions and titles from page 1.
//
// It returns:
//   - the list of voting columns (in the expected order)
//   - the maximum X coordinate (in points) that should still be considered part of the name column
//   - the Y coordinate (in points) of the table header row (useful to filter out numbers in the top text)
func ExtractColumns(page PageBBox) (cols []Column, nameXMaxPt float64, tableHeaderYPt float64, err error) {
	presence, ok := findWord(page.Words, "Присутствует")
	if !ok {
		return nil, 0, 0, errors.New("failed to find table header word 'Присутствует'")
	}
	headerY := presence.YMin
	yTol := 0.8

	za, ok := findWordNearY(page.Words, "За", headerY, yTol)
	if !ok {
		return nil, 0, 0, errors.New("failed to find table header word 'За'")
	}
	protiv, ok := findWordNearY(page.Words, "Против", headerY, yTol)
	if !ok {
		return nil, 0, 0, errors.New("failed to find table header word 'Против'")
	}
	voz, ok := findWordNearY(page.Words, "Воздержался", headerY, yTol)
	if !ok {
		return nil, 0, 0, errors.New("failed to find table header word 'Воздержался'")
	}
	ne, ok := findWordNearY(page.Words, "Не", headerY, yTol)
	if !ok {
		return nil, 0, 0, errors.New("failed to find table header word 'Не'")
	}
	golos, ok := findWordNearY(page.Words, "голосовал", headerY, yTol)
	if !ok {
		return nil, 0, 0, errors.New("failed to find table header word 'голосовал'")
	}

	// Counts: '(N)' words are separate.
	pCount := findParenAfter(page.Words, headerY, presence.XMax, yTol)
	zaCount := findParenAfter(page.Words, headerY, za.XMax, yTol)
	protCount := findParenAfter(page.Words, headerY, protiv.XMax, yTol)
	vozCount := findParenAfter(page.Words, headerY, voz.XMax, yTol)
	noVoteCount := findParenAfter(page.Words, headerY, golos.XMax, yTol)

	cols = []Column{
		{Key: ColPresent, Title: joinTitle("Присутствует", pCount), XCenterPt: centerX(presence)},
		{Key: ColZa, Title: joinTitle("За", zaCount), XCenterPt: centerX(za)},
		{Key: ColProtiv, Title: joinTitle("Против", protCount), XCenterPt: centerX(protiv)},
		{Key: ColVozderzh, Title: joinTitle("Воздержался", vozCount), XCenterPt: centerX(voz)},
		{Key: ColNoVote, Title: joinTitle("Не голосовал", noVoteCount), XCenterPt: centerXUnion(ne, golos)},
	}

	// Name column ends right before the presence column begins.
	nameXMaxPt = presence.XMin - 2
	return cols, nameXMaxPt, headerY, nil
}

func centerX(w Word) float64 { return (w.XMin + w.XMax) / 2 }

func centerXUnion(a, b Word) float64 {
	xmin := math.Min(a.XMin, b.XMin)
	xmax := math.Max(a.XMax, b.XMax)
	return (xmin + xmax) / 2
}

func joinTitle(label, count string) string {
	count = strings.TrimSpace(count)
	if count == "" {
		return label
	}
	return label + " " + count
}

func findParenAfter(words []Word, y float64, afterX float64, yTol float64) string {
	bestX := math.Inf(1)
	best := ""
	for _, w := range words {
		if math.Abs(w.YMin-y) > yTol {
			continue
		}
		if w.XMin <= afterX {
			continue
		}
		if !strings.HasPrefix(w.Text, "(") || !strings.HasSuffix(w.Text, ")") {
			continue
		}
		if w.XMin < bestX {
			bestX = w.XMin
			best = w.Text
		}
	}
	return best
}

func findWord(words []Word, text string) (Word, bool) {
	// If there are several, prefer the one with the smallest Y (header is near top).
	bestY := math.Inf(1)
	bestX := math.Inf(1)
	var best Word
	ok := false
	for _, w := range words {
		if w.Text != text {
			continue
		}
		if w.YMin < bestY || (w.YMin == bestY && w.XMin < bestX) {
			bestY = w.YMin
			bestX = w.XMin
			best = w
			ok = true
		}
	}
	return best, ok
}

func findWordNearY(words []Word, text string, y float64, yTol float64) (Word, bool) {
	bestX := math.Inf(1)
	var best Word
	ok := false
	for _, w := range words {
		if w.Text != text {
			continue
		}
		if math.Abs(w.YMin-y) > yTol {
			continue
		}
		if w.XMin < bestX {
			bestX = w.XMin
			best = w
			ok = true
		}
	}
	return best, ok
}

// findTableHeaderY tries to locate the voting table header row on a page.
// It returns the YMin (points) of the word "Присутствует" if present.
func findTableHeaderY(words []Word) (float64, bool) {
	w, ok := findWord(words, "Присутствует")
	if !ok {
		return 0, false
	}
	// A sanity filter: the header row should be in the upper part of the page.
	if w.YMin > 200 {
		return 0, false
	}
	return w.YMin, true
}

// RowNumberWord ties a parsed row number to its bbox Word.
type RowNumberWord struct {
	Number int
	Word   Word
}

// ExtractRowNumberWords finds all row-number words on the page.
// If minYPt > 0, numbers with Y below that threshold are ignored (useful to
// exclude things like "1 чтение" in the header area).
func ExtractRowNumberWords(page PageBBox, minYPt float64) []RowNumberWord {
	cands := make([]RowNumberWord, 0, 128)
	for _, w := range page.Words {
		if minYPt > 0 {
			// Filter out row-like numbers that occur in the top descriptive text
			// (e.g. "1 чтение"), by requiring the number to be below the table header.
			if w.YMin <= minYPt+0.5 {
				continue
			}
		}
		// Row numbers are placed in the first column; their X is small.
		if w.XMin > 60 {
			continue
		}
		n, err := strconv.Atoi(w.Text)
		if err != nil {
			continue
		}
		// Expected range for these protocols.
		if n <= 0 || n > 300 {
			continue
		}
		cands = append(cands, RowNumberWord{Number: n, Word: w})
	}

	// Deduplicate: in rare cases, the same number can appear twice (e.g. artifacts).
	// Keep the one with the smaller X (more likely the actual row number).
	m := make(map[int]RowNumberWord, len(cands))
	for _, c := range cands {
		prev, ok := m[c.Number]
		if !ok || c.Word.XMin < prev.Word.XMin {
			m[c.Number] = c
		}
	}
	out := make([]RowNumberWord, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}

// ExtractNameForRow reconstructs the full name of a row by collecting words
// to the right of the row number within the same row band.
func ExtractNameForRow(rowNumWord Word, words []Word, nameXMaxPt float64) string {
	yTol := 0.8
	cands := make([]Word, 0, 8)
	for _, w := range words {
		// Overlap in Y.
		if w.YMax < rowNumWord.YMin-yTol || w.YMin > rowNumWord.YMax+yTol {
			continue
		}
		// Must be to the right of the row number.
		if w.XMin <= rowNumWord.XMax+1 {
			continue
		}
		// Stay within the name column.
		if w.XMax > nameXMaxPt {
			continue
		}
		// Guard: skip obvious non-name tokens.
		if w.Text == "№" {
			continue
		}
		cands = append(cands, w)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].XMin < cands[j].XMin })

	parts := make([]string, 0, len(cands))
	for _, w := range cands {
		parts = append(parts, w.Text)
	}
	name := strings.Join(parts, " ")
	name = strings.TrimSpace(name)
	return name
}

// ValidateCounts can be used by callers to verify that detected marks match the header totals.
// It returns nil when counts match; otherwise it returns an error with details.
func ValidateCounts(doc Document) error {
	totals := map[ColumnKey]int{}
	for _, r := range doc.Rows {
		for _, c := range doc.Columns {
			if r.Marks[c.Key] {
				totals[c.Key]++
			}
		}
	}

	// Extract counts from titles like "За (55)".
	expected := map[ColumnKey]int{}
	for _, c := range doc.Columns {
		if n, ok := parseParenCount(c.Title); ok {
			expected[c.Key] = n
		}
	}

	var diffs []string
	for _, c := range doc.Columns {
		exp, ok := expected[c.Key]
		if !ok {
			continue
		}
		got := totals[c.Key]
		if got != exp {
			diffs = append(diffs, fmt.Sprintf("%s: got %d, expected %d", c.Title, got, exp))
		}
	}
	if len(diffs) > 0 {
		return errors.New("detected mark totals do not match header: " + strings.Join(diffs, "; "))
	}
	return nil
}

func parseParenCount(title string) (int, bool) {
	start := strings.LastIndex(title, "(")
	end := strings.LastIndex(title, ")")
	if start < 0 || end <= start {
		return 0, false
	}
	nStr := strings.TrimSpace(title[start+1 : end])
	n, err := strconv.Atoi(nStr)
	if err != nil {
		return 0, false
	}
	return n, true
}
