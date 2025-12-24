package extractor

import (
	"sort"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// DefaultParser extracts simple columnar tables from pdftotext -layout output.
type DefaultParser struct {
	filters TableFiltersConfig
}

type TableFiltersConfig struct {
	Enabled                    bool
	TwoColumnMedianLengthLimit int
	MaxRowsWithoutDelimiters   int
	MaxWordsWithoutDelimiters  int
}

type filterStats struct {
	total           int
	twoColumnMedian int
	noDelimiters    int
}

func NewDefaultParser(filters TableFiltersConfig) DefaultParser {
	return DefaultParser{filters: filters}
}

func (p DefaultParser) ParseTables(input ParseInput) (ParsedTables, error) {
	logger := resolveLogger(input.Logger)
	metrics := parseMetrics{}
	lines := strings.Split(input.LayoutText, "\n")
	current := make([]string, 0, len(lines))
	var rawTables []RawTable
	page := input.StartPage
	if page == 0 {
		page = 1
	}
	currentTablePage := page

	flush := func() {
		splitBefore := metrics.splitByGapLines
		table := buildTable(current, &metrics)
		if len(table.Headers) == 0 && len(table.Rows) == 0 {
			current = current[:0]
			return
		}
		table.splitByGapLines = metrics.splitByGapLines - splitBefore
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

	filtersEnabled := p.filters.Enabled && (p.filters.TwoColumnMedianLengthLimit > 0 || p.filters.MaxRowsWithoutDelimiters > 0 || p.filters.MaxWordsWithoutDelimiters > 0)
	rawBeforeFilter := len(rawTables)
	filtered := make([]RawTable, 0, len(rawTables))
	fStats := filterStats{}

	for idx, t := range rawTables {
		if filtersEnabled {
			if dropped, median := shouldDropTwoColumn(t, p.filters.TwoColumnMedianLengthLimit); dropped {
				fStats.total++
				fStats.twoColumnMedian++
				logger.Info("dropping table by median cell length", zap.Int("table_index", idx), zap.Int("page", t.Page),
					zap.Int("median_length", median), zap.Int("limit", p.filters.TwoColumnMedianLengthLimit))
				continue
			}

			if dropped, rowCount, wordCount := shouldDropNoDelimiter(t, p.filters.MaxRowsWithoutDelimiters, p.filters.MaxWordsWithoutDelimiters); dropped {
				fStats.total++
				fStats.noDelimiters++
				logger.Info("dropping table without delimiters", zap.Int("table_index", idx), zap.Int("page", t.Page),
					zap.Int("rows", rowCount), zap.Int("words", wordCount),
					zap.Int("max_rows", p.filters.MaxRowsWithoutDelimiters),
					zap.Int("max_words", p.filters.MaxWordsWithoutDelimiters),
					zap.Int("split_by_gap_lines", t.splitByGapLines))
				continue
			}
		}

		filtered = append(filtered, t)
	}

	rawTables = filtered
	normTables := normalizeTables(rawTables)

	stats := ParseStats{
		SplitByGapLines:         metrics.splitByGapLines,
		SplitByGapSamples:       metrics.samples(),
		FiltersEnabled:          filtersEnabled,
		FilteredTables:          fStats.total,
		FilteredTwoColumnMedian: fStats.twoColumnMedian,
		FilteredNoDelimiter:     fStats.noDelimiters,
	}

	logger.Debug("default parser stats",
		zap.Int("raw_tables", len(rawTables)),
		zap.Int("raw_tables_before_filters", rawBeforeFilter),
		zap.Int("normalized_tables", len(normTables)),
		zap.Bool("filters_enabled", filtersEnabled),
		zap.Int("filtered_tables", fStats.total),
		zap.Int("filtered_two_column", fStats.twoColumnMedian),
		zap.Int("filtered_no_delimiters", fStats.noDelimiters),
		zap.Int("split_by_gap_lines", metrics.splitByGapLines),
		zap.Strings("split_by_gap_samples", metrics.samples()),
	)

	return ParsedTables{
		RawTables:        rawTables,
		NormalizedTables: normTables,
		Stats:            stats,
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
			prev.splitByGapLines += table.splitByGapLines
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

func normalizeTables(rawTables []RawTable) []NormalizedTable {
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

	return normTables
}

func buildTable(lines []string, metrics *parseMetrics) RawTable {
	var rows [][]string
	for _, line := range lines {
		cells := splitColumns(line, metrics)
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

func shouldDropTwoColumn(table RawTable, limit int) (bool, int) {
	if limit <= 0 {
		return false, 0
	}

	columns := len(longestRow(append([][]string{table.Headers}, table.Rows...)))
	if columns != 2 {
		return false, 0
	}

	median := medianCellLength(table)
	if median <= limit {
		return false, median
	}

	return true, median
}

func shouldDropNoDelimiter(table RawTable, maxRows, maxWords int) (bool, int, int) {
	if maxRows <= 0 && maxWords <= 0 {
		return false, 0, 0
	}

	if table.splitByGapLines == 0 {
		return false, 0, 0
	}

	rowCount := len(table.Rows)
	if len(table.Headers) > 0 {
		rowCount++
	}

	words := countTableWords(table)

	if maxRows > 0 && rowCount > maxRows {
		return true, rowCount, words
	}

	if maxWords > 0 && words > maxWords {
		return true, rowCount, words
	}

	return false, rowCount, words
}

func splitColumns(line string, metrics *parseMetrics) []string {
	parts := strings.FieldsFunc(line, func(r rune) bool {
		return r == '\t'
	})
	if len(parts) <= 1 {
		if metrics != nil {
			metrics.recordSplitByGap(line)
		}
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

func medianCellLength(table RawTable) int {
	lengths := cellLengths(table)
	if len(lengths) == 0 {
		return 0
	}

	sort.Ints(lengths)
	mid := len(lengths) / 2
	if len(lengths)%2 == 0 {
		return (lengths[mid-1] + lengths[mid]) / 2
	}

	return lengths[mid]
}

func countTableWords(table RawTable) int {
	cells := collectTableCells(table)
	total := 0
	for _, cell := range cells {
		total += len(strings.Fields(cell))
	}

	return total
}

func cellLengths(table RawTable) []int {
	cells := collectTableCells(table)
	lengths := make([]int, 0, len(cells))
	for _, cell := range cells {
		lengths = append(lengths, len(strings.TrimSpace(cell)))
	}

	return lengths
}

func collectTableCells(table RawTable) []string {
	cells := make([]string, 0, len(table.Headers))
	cells = append(cells, table.Headers...)
	for _, row := range table.Rows {
		cells = append(cells, row...)
	}

	return cells
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

type parseMetrics struct {
	splitByGapLines   int
	splitByGapSamples []string
}

const maxSplitByGapSamples = 5
const maxSplitSampleLength = 200

func (m *parseMetrics) recordSplitByGap(line string) {
	m.splitByGapLines++
	if len(m.splitByGapSamples) >= maxSplitByGapSamples {
		return
	}
	sample := strings.TrimSpace(line)
	if sample == "" {
		sample = line
	}
	if len(sample) > maxSplitSampleLength {
		sample = sample[:maxSplitSampleLength] + "…"
	}
	m.splitByGapSamples = append(m.splitByGapSamples, sample)
}

func (m *parseMetrics) samples() []string {
	if len(m.splitByGapSamples) == 0 {
		return nil
	}
	result := make([]string, len(m.splitByGapSamples))
	copy(result, m.splitByGapSamples)
	return result
}
