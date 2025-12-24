package extractor

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mono-repo/table-extractor-service/internal/voting"
)

type VotingTableParser struct {
	params voting.DetectParams
}

func NewVotingTableParser() VotingTableParser {
	return VotingTableParser{params: voting.DefaultDetectParams()}
}

func (p VotingTableParser) ParseTables(input ParseInput) (ParsedTables, error) {
	if input.BBoxHTML == "" {
		return ParsedTables{}, errors.New("bbox html is required for voting parser")
	}
	if len(input.PagePNGs) == 0 {
		return ParsedTables{}, errors.New("rendered page PNGs are required for voting parser")
	}

	pageSegments := splitBBoxPages(input.BBoxHTML, input.StartPage)
	if len(pageSegments) == 0 {
		return ParsedTables{}, errors.New("bbox html does not contain <page> tags")
	}

	pageBBoxes := make(map[int]voting.PageBBox, len(pageSegments))
	for _, seg := range pageSegments {
		page, err := voting.ParseBBoxHTML(seg.HTML)
		if err != nil {
			return ParsedTables{}, fmt.Errorf("parse bbox page %d: %w", seg.Number, err)
		}
		pageBBoxes[seg.Number] = page
	}

	firstPage := pageBBoxes[pageSegments[0].Number]
	cols, nameXMaxPt, tableHeaderYPt, err := voting.ExtractColumns(firstPage)
	if err != nil {
		return ParsedTables{}, err
	}

	rows := make(map[int]*voting.Row, 128)
	pageNumbers := make([]int, 0, len(pageBBoxes))
	for n := range pageBBoxes {
		pageNumbers = append(pageNumbers, n)
	}
	sort.Ints(pageNumbers)

	maxRowNumber := 0
	for _, num := range pageNumbers {
		pageBBox := pageBBoxes[num]
		imgBytes, ok := input.PagePNGs[num]
		if !ok {
			return ParsedTables{}, fmt.Errorf("rendered PNG for page %d is missing", num)
		}
		gray, err := loadGrayFromBytes(imgBytes)
		if err != nil {
			return ParsedTables{}, fmt.Errorf("load rendered page %d: %w", num, err)
		}

		xScale := float64(gray.Bounds().Dx()) / pageBBox.WidthPt
		yScale := float64(gray.Bounds().Dy()) / pageBBox.HeightPt

		minYPt := 0.0
		if num == pageSegments[0].Number {
			minYPt = tableHeaderYPt
		} else {
			if y, ok := voting.FindTableHeaderY(pageBBox.Words); ok {
				minYPt = y
			}
		}

		rowNumWords := filterRowNumbers(voting.ExtractRowNumberWords(pageBBox, minYPt), maxRowNumber)
		for _, rn := range rowNumWords {
			n := rn.Number
			row := rows[n]
			if row == nil {
				row = &voting.Row{Number: n, Marks: make(map[voting.ColumnKey]bool, len(cols))}
				rows[n] = row
			}
			candidate := sanitizeRowName(voting.ExtractNameForRow(rn.Word, pageBBox.Words, nameXMaxPt))
			if candidate != "" && (row.Name == "" || len(candidate) > len(row.Name)) {
				row.Name = candidate
			}

			yCenterPt := (rn.Word.YMin + rn.Word.YMax) / 2
			yPx := int((yCenterPt * yScale) + 0.5)

			marks := voting.DetectMarks(gray, yPx, cols, xScale, yScale, p.params)
			for k, v := range marks {
				if v {
					row.Marks[k] = true
				}
			}
		}
		if len(rowNumWords) > 0 {
			last := rowNumWords[len(rowNumWords)-1].Number
			if last > maxRowNumber {
				maxRowNumber = last
			}
		}
	}

	rowList := make([]voting.Row, 0, len(rows))
	for _, r := range rows {
		rowList = append(rowList, *r)
	}
	sort.Slice(rowList, func(i, j int) bool { return rowList[i].Number < rowList[j].Number })

	doc := voting.Document{
		HeaderText: voting.ExtractHeaderText(input.LayoutText),
		Columns:    cols,
		Rows:       rowList,
	}

	return convertDocument(doc), nil
}

func (p VotingTableParser) RenderMarkdown(tables ParsedTables) string {
	if tables.Document != nil {
		return voting.ToMarkdown(*tables.Document)
	}
	return DefaultParser{}.RenderMarkdown(tables)
}

type bboxPage struct {
	Number int
	HTML   string
}

var (
	pageTagRe    = regexp.MustCompile(`(?s)<page[^>]*>.*?</page>`)
	pageNumberRe = regexp.MustCompile(`number="(\d+)"`)
)

func splitBBoxPages(xhtml string, startPage int) []bboxPage {
	matches := pageTagRe.FindAllString(xhtml, -1)
	if len(matches) == 0 {
		return nil
	}
	pages := make([]bboxPage, 0, len(matches))
	for i, raw := range matches {
		num := startPage + i
		if m := pageNumberRe.FindStringSubmatch(raw); len(m) == 2 {
			num = parseInt(m[1])
		}
		pages = append(pages, bboxPage{Number: num, HTML: raw})
	}
	return pages
}

func filterRowNumbers(words []voting.RowNumberWord, prevMax int) []voting.RowNumberWord {
	filtered := make([]voting.RowNumberWord, 0, len(words))
	for _, rn := range words {
		if rn.Number <= 0 {
			continue
		}
		filtered = append(filtered, rn)
	}
	if prevMax == 0 || len(filtered) == 0 {
		return filtered
	}

	start := 0
	for start < len(filtered) && filtered[start].Number < prevMax {
		start++
	}
	if start < len(filtered) && filtered[start].Number == prevMax {
		return filtered[start:]
	}

	for start < len(filtered) && filtered[start].Number < prevMax+1 {
		start++
	}
	return filtered[start:]
}

var headerTokens = map[string]struct{}{
	"ф.и.о.":       {},
	"ф.и.о":        {},
	"присутствует": {},
	"за":           {},
	"против":       {},
	"воздержался":  {},
	"не":           {},
	"голосовал":    {},
}

func sanitizeRowName(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return ""
	}
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		token := strings.Trim(strings.ToLower(p), "()")
		if _, ok := headerTokens[token]; ok {
			continue
		}
		filtered = append(filtered, p)
	}
	return strings.Join(filtered, " ")
}

func loadGrayFromBytes(data []byte) (*image.Gray, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode PNG: %w", err)
	}
	b := img.Bounds()
	g := image.NewGray(b)
	draw.Draw(g, b, img, b.Min, draw.Src)
	return g, nil
}

func convertDocument(doc voting.Document) ParsedTables {
	headers := []string{"№", "Ф.И.О."}
	for _, c := range doc.Columns {
		headers = append(headers, c.Title)
	}

	raw := RawTable{Headers: headers}
	norm := NormalizedTable{
		Name:    "voting_table",
		Headers: headers,
	}

	for _, r := range doc.Rows {
		if r.Number == 0 {
			continue
		}
		marks := make([]string, 0, len(doc.Columns))
		rowMap := make(map[string]string, len(headers))
		rowMap["№"] = strconv.Itoa(r.Number)
		rowMap["Ф.И.О."] = r.Name
		for _, c := range doc.Columns {
			val := ""
			if r.Marks != nil && r.Marks[c.Key] {
				val = "✓"
			}
			rowMap[c.Title] = val
			marks = append(marks, val)
		}

		rawRow := make([]string, 0, len(headers))
		rawRow = append(rawRow, rowMap["№"], rowMap["Ф.И.О."])
		rawRow = append(rawRow, marks...)
		raw.Rows = append(raw.Rows, rawRow)
		norm.Rows = append(norm.Rows, rowMap)
	}

	return ParsedTables{
		RawTables:        []RawTable{raw},
		NormalizedTables: []NormalizedTable{norm},
		HeaderText:       doc.HeaderText,
		Document:         &doc,
	}
}
