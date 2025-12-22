package extractor

import (
	"strconv"
	"strings"
)

// DefaultParser extracts simple columnar tables from pdftotext -layout output.
type DefaultParser struct{}

func (DefaultParser) ParseTables(input ParseInput) (ParsedTables, error) {
	lines := strings.Split(input.LayoutText, "\n")
	current := make([]string, 0, len(lines))
	var rawTables []RawTable

	flush := func() {
		table := buildTable(current)
		if len(table.Headers) == 0 && len(table.Rows) == 0 {
			current = current[:0]
			return
		}
		rawTables = append(rawTables, table)
		current = current[:0]
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if len(current) > 0 {
				flush()
			}
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		flush()
	}

	normTables := make([]NormalizedTable, 0, len(rawTables))
	for idx, t := range rawTables {
		headers := t.Headers
		if len(headers) == 0 {
			headers = make([]string, len(longestRow(t.Rows)))
			for i := range headers {
				headers[i] = columnName(i)
			}
		}
		norm := NormalizedTable{
			Name:    makeTableName(idx),
			Headers: headers,
			Page:    t.Page,
		}
		for _, row := range t.Rows {
			record := make(map[string]string, len(headers))
			for i, h := range headers {
				if i < len(row) {
					record[h] = strings.TrimSpace(row[i])
				} else {
					record[h] = ""
				}
			}
			norm.Rows = append(norm.Rows, record)
		}
		normTables = append(normTables, norm)
	}

	return ParsedTables{
		RawTables:        rawTables,
		NormalizedTables: normTables,
	}, nil
}

func (DefaultParser) RenderMarkdown(tables ParsedTables) string {
	var b strings.Builder
	if strings.TrimSpace(tables.HeaderText) != "" {
		b.WriteString(tables.HeaderText)
		b.WriteString("\n\n")
	}
	for i, t := range tables.NormalizedTables {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if t.Name != "" {
			b.WriteString("### " + t.Name + "\n\n")
		}
		headers := t.Headers
		if len(headers) == 0 && len(t.Rows) > 0 {
			for k := range t.Rows[0] {
				headers = append(headers, k)
			}
		}
		if len(headers) == 0 {
			b.WriteString("_empty table_")
			continue
		}
		b.WriteString("| " + strings.Join(headers, " | ") + " |\n")
		divider := make([]string, len(headers))
		for i := range divider {
			divider[i] = "---"
		}
		b.WriteString("| " + strings.Join(divider, " | ") + " |\n")
		for _, row := range t.Rows {
			cells := make([]string, len(headers))
			for i, h := range headers {
				cells[i] = escapeMarkdown(row[h])
			}
			b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		}
	}
	return b.String()
}

func buildTable(lines []string) RawTable {
	var rows [][]string
	for _, line := range lines {
		cells := splitColumns(line)
		if len(cells) == 0 {
			continue
		}
		rows = append(rows, cells)
	}

	if len(rows) == 0 {
		return RawTable{}
	}

	table := RawTable{}
	if len(rows) > 1 {
		table.Headers = trimCells(rows[0])
		table.Rows = trimRows(rows[1:])
	} else {
		table.Headers = trimCells(rows[0])
	}
	return table
}

func splitColumns(line string) []string {
	parts := strings.FieldsFunc(line, func(r rune) bool {
		return r == '\t'
	})
	if len(parts) <= 1 {
		parts = splitByGap(line)
	}
	return parts
}

func splitByGap(line string) []string {
	var buf strings.Builder
	var cells []string
	spaceCount := 0
	for _, r := range line {
		if r == ' ' {
			spaceCount++
			if spaceCount >= 2 {
				if buf.Len() > 0 {
					cells = append(cells, buf.String())
					buf.Reset()
				}
				continue
			}
		} else {
			if spaceCount > 0 && len(cells) > 0 {
				buf.WriteString(strings.Repeat(" ", min(spaceCount, 1)))
			}
			spaceCount = 0
		}
		buf.WriteRune(r)
	}
	if buf.Len() > 0 {
		cells = append(cells, buf.String())
	}
	return trimCells(cells)
}

func trimCells(cells []string) []string {
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

func trimRows(rows [][]string) [][]string {
	for i, row := range rows {
		rows[i] = trimCells(row)
	}
	return rows
}

func longestRow(rows [][]string) []string {
	var longest []string
	for _, r := range rows {
		if len(r) > len(longest) {
			longest = r
		}
	}
	return longest
}

func columnName(idx int) string {
	return "col_" + strings.ToLower(string(rune('a'+idx)))
}

func makeTableName(idx int) string {
	return "table_" + strconv.Itoa(idx+1)
}

func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer("|", "\\|")
	return replacer.Replace(strings.TrimSpace(s))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
