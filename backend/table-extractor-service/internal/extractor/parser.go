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
	page := input.StartPage
	if page == 0 {
		page = 1
	}
	currentTablePage := page

	flush := func() {
		table := buildTable(current)
		if len(table.Headers) == 0 && len(table.Rows) == 0 {
			current = current[:0]
			return
		}
		table.Page = currentTablePage
		table.Pages = []int{currentTablePage}
		rawTables = append(rawTables, table)
		current = current[:0]
	}

	for _, rawLine := range lines {
		segments := strings.Split(rawLine, "\f")
		for i, line := range segments {
			if strings.TrimSpace(line) == "" {
				if len(current) > 0 {
					flush()
				}
			} else {
				if len(current) == 0 {
					currentTablePage = page
				}
				current = append(current, line)
			}

			if i < len(segments)-1 {
				if len(current) > 0 {
					flush()
				}
				page++
				currentTablePage = page
			}
		}
	}
	if len(current) > 0 {
		flush()
	}

	rawTables = mergeAdjacentTables(rawTables)

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
			Pages:   t.Pages,
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

func mergeAdjacentTables(raw []RawTable) []RawTable {
	if len(raw) == 0 {
		return raw
	}

	merged := make([]RawTable, 0, len(raw))
	for _, table := range raw {
		table.Rows = dropHeaderRows(table.Rows, table.Headers)
		if len(merged) == 0 {
			merged = append(merged, table)
			continue
		}

		prev := &merged[len(merged)-1]
		if shouldMergeTables(*prev, table) {
			prev.Rows = append(prev.Rows, table.Rows...)
			prev.Pages = appendUniqueInt(prev.Pages, table.Pages...)
			if prev.Page == 0 {
				prev.Page = table.Page
			}
			continue
		}

		merged = append(merged, table)
	}

	return merged
}

func shouldMergeTables(prev, next RawTable) bool {
	if !headersSimilar(prev.Headers, next.Headers) {
		return false
	}

	prevLastPage := maxInt(prev.Pages)
	nextFirstPage := minPositiveInt(next.Pages)
	if prevLastPage == 0 || nextFirstPage == 0 {
		return true
	}

	return nextFirstPage-prevLastPage <= 1 && nextFirstPage >= prevLastPage
}

func headersSimilar(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(strings.TrimSpace(a[i]), strings.TrimSpace(b[i])) {
			return false
		}
	}
	return true
}

func dropHeaderRows(rows [][]string, headers []string) [][]string {
	filtered := rows[:0]
	for _, row := range rows {
		if headersSimilar(headers, row) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func appendUniqueInt(base []int, values ...int) []int {
	existing := make(map[int]struct{}, len(base))
	for _, v := range base {
		existing[v] = struct{}{}
	}
	for _, v := range values {
		if _, ok := existing[v]; ok {
			continue
		}
		existing[v] = struct{}{}
		base = append(base, v)
	}
	return base
}

func maxInt(values []int) int {
	max := 0
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	return max
}

func minPositiveInt(values []int) int {
	min := 0
	for _, v := range values {
		if v <= 0 {
			continue
		}
		if min == 0 || v < min {
			min = v
		}
	}
	return min
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
