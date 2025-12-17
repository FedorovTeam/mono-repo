// backend/docling-transformer-service/internal/infrastructure/doclingjson/doclingjson.go
package doclingjson

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Document описывает часть JSON-ответа Docling, содержащую таблицы и отметки для них.
type Document struct {
	Tables []Table `json:"tables"`
	Marks  []Mark  `json:"marks"`
}

// Table описывает таблицу в Docling JSON.
type Table struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Caption  string     `json:"caption"`
	Markdown string     `json:"markdown"`
	Rows     []TableRow `json:"rows"`
}

// TableConflict описывает проблему при сопоставлении таблиц.
type TableConflict struct {
	MarkdownIndex int
	TableID       string
	Reason        string
}

// MergeStats содержит метрики объединения таблиц между проходами.
type MergeStats struct {
	TotalRows   int
	MergedRows  int
	MatchedRows int
}

// TableRow описывает строку таблицы.
type TableRow struct {
	Cells []TableCell `json:"cells"`
}

// TableCell описывает ячейку таблицы.
type TableCell struct {
	Text string `json:"text"`
}

// Mark связывает отметку в Markdown с таблицей в JSON.
type Mark struct {
	Mark     string         `json:"mark"`
	Name     string         `json:"name"`
	TargetID string         `json:"target_id"`
	Target   map[string]any `json:"target"`
}

// RenderMarkdown пытается собрать Markdown по таблицам из jsonContent.
// Возвращает сгенерированный Markdown и признак того, что он получен из JSON.
func RenderMarkdown(jsonContent any, mdFallback *string) (string, bool) {
	rendered, ok, _ := RenderMarkdownWithConflicts(jsonContent, mdFallback)
	return rendered, ok
}

// RenderMarkdownWithConflicts возвращает Markdown, признак использования JSON и конфликты сопоставления.
func RenderMarkdownWithConflicts(jsonContent any, mdFallback *string) (string, bool, []TableConflict) {
	doc, err := Parse(jsonContent)
	if err != nil {
		return "", false, nil
	}

	markdown := ""
	if mdFallback != nil && mdFallback != (*string)(nil) {
		markdown = strings.TrimSpace(*mdFallback)
	}

	tableMarkdowns := buildTableMarkdowns(doc.Tables)
	if len(tableMarkdowns) == 0 {
		if markdown != "" {
			return markdown, false, []TableConflict{{Reason: "no tables available for replacement"}}
		}
		return "", false, nil
	}

	if markdown != "" {
		filled, conflicts := fillTables(markdown, doc, tableMarkdowns)

		return filled, true, conflicts
	}

	rendered := renderTablesInOrder(doc.Marks, tableMarkdowns)
	return rendered, rendered != "", nil
}

// Parse пытается tolerant-распарсить json_content.
func Parse(jsonContent any) (Document, error) {
	var doc Document

	if jsonContent == nil {
		return doc, nil
	}

	var raw []byte
	switch v := jsonContent.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		marshaled, err := json.Marshal(v)
		if err != nil {
			return doc, err
		}
		raw = marshaled
	}

	if len(raw) == 0 {
		return doc, nil
	}

	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, err
	}

	return doc, nil
}

func buildTableMarkdowns(tables []Table) map[string]string {
	result := make(map[string]string, len(tables))

	for _, table := range tables {
		id := normalizeIdentifier(firstNonEmpty(table.ID, table.Name))
		if id == "" {
			continue
		}

		content := strings.TrimSpace(table.Markdown)
		if content == "" {
			content = renderTableRows(table.Rows)
		}

		if content == "" {
			continue
		}

		result[id] = content
	}

	return result
}

func renderTablesInOrder(marks []Mark, tableMarkdowns map[string]string) string {
	var parts []string
	used := map[string]bool{}

	for _, mark := range marks {
		id := normalizeIdentifier(resolveMarkTarget(mark))
		if id == "" || used[id] {
			continue
		}

		if tableMD, ok := tableMarkdowns[id]; ok {
			parts = append(parts, tableMD)
			used[id] = true
		}
	}

	for id, tableMD := range tableMarkdowns {
		if used[id] {
			continue
		}
		used[id] = true
		parts = append(parts, tableMD)
	}

	return strings.Join(parts, "\n\n")
}

func renderTableRows(rows []TableRow) string {
	if len(rows) == 0 {
		return ""
	}

	headers := rows[0]
	if len(headers.Cells) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.WriteString("| ")
	builder.WriteString(strings.Join(renderCells(headers.Cells), " | "))
	builder.WriteString(" |\n")

	builder.WriteString("| ")
	for i := range headers.Cells {
		if i > 0 {
			builder.WriteString(" | ")
		}
		builder.WriteString("---")
	}
	builder.WriteString(" |\n")

	for _, row := range rows[1:] {
		if len(row.Cells) == 0 {
			continue
		}

		builder.WriteString("| ")
		builder.WriteString(strings.Join(renderCells(row.Cells), " | "))
		builder.WriteString(" |\n")
	}

	return strings.TrimSpace(builder.String())
}

func renderCells(cells []TableCell) []string {
	rendered := make([]string, 0, len(cells))
	for _, cell := range cells {
		text := strings.TrimSpace(cell.Text)
		text = strings.ReplaceAll(text, "|", "\\|")
		rendered = append(rendered, text)
	}

	return rendered
}

func resolveMarkTarget(mark Mark) string {
	if mark.TargetID != "" {
		return mark.TargetID
	}

	if id := mark.Target["id"]; id != nil {
		if s, ok := id.(string); ok {
			return s
		}
	}

	if name := firstNonEmpty(mark.Name, mark.Mark); name != "" {
		return name
	}

	return ""
}

func markPlaceholders(mark Mark) []string {
	placeholders := []string{}

	for _, value := range []string{mark.Mark, mark.Name, mark.TargetID} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		placeholders = append(placeholders, value)
	}

	normalized := normalizeIdentifier(resolveMarkTarget(mark))
	if normalized != "" {
		placeholders = append(placeholders, normalized, "{{"+normalized+"}}")
	}

	return unique(placeholders)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func normalizeIdentifier(value string) string {
	cleaned := strings.Trim(value, " {}[]()")
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.ToLower(cleaned)
	return cleaned
}

func unique(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

// ParseMarkdownTables пытается извлечь таблицы из Markdown для использования во втором проходе.
func ParseMarkdownTables(md string) []Table {
	parsed := parseMarkdownTablesWithSpans(md)
	tables := make([]Table, 0, len(parsed))
	for _, item := range parsed {
		tables = append(tables, item.Table)
	}

	return tables
}

type markdownTable struct {
	Table     Table
	startLine int
	endLine   int
}

func fillTables(markdown string, doc Document, tableMarkdowns map[string]string) (string, []TableConflict) {
	mdTables := parseMarkdownTablesWithSpans(markdown)
	used := map[string]bool{}
	blocked := map[string]bool{}
	conflicts := []TableConflict{}

	replacements := map[int]string{}
	orderHints := markOrder(doc.Marks)

	for idx, mdTable := range mdTables {
		matchID, conflict := findBestTableMatch(mdTable, doc.Tables, tableMarkdowns, used, orderHints, idx)
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
			if conflict.TableID != "" {
				blocked[conflict.TableID] = true
			}
			continue
		}

		if matchID == "" {
			conflicts = append(conflicts, TableConflict{MarkdownIndex: idx, Reason: "no matching table found"})
			continue
		}

		used[matchID] = true
		replacements[idx] = tableMarkdowns[matchID]
	}

	markdown = replaceMarkdownTables(markdown, mdTables, replacements)

	markdown, placeholderConflicts := replacePlaceholders(markdown, doc.Marks, tableMarkdowns, used)
	conflicts = append(conflicts, placeholderConflicts...)

	for id, tableMD := range tableMarkdowns {
		if used[id] || blocked[id] {
			continue
		}

		if markdown != "" {
			markdown += "\n\n"
		}
		markdown += tableMD
	}

	return markdown, conflicts
}

func replacePlaceholders(markdown string, marks []Mark, tableMarkdowns map[string]string, used map[string]bool) (string, []TableConflict) {
	conflicts := []TableConflict{}

	for _, mark := range marks {
		id := normalizeIdentifier(resolveMarkTarget(mark))
		if id == "" {
			continue
		}

		tableMD, ok := tableMarkdowns[id]
		if !ok {
			conflicts = append(conflicts, TableConflict{TableID: id, Reason: "table markdown not found"})
			continue
		}

		replaced := false
		for _, placeholder := range markPlaceholders(mark) {
			if placeholder == "" {
				continue
			}

			if strings.Contains(markdown, placeholder) {
				markdown = strings.ReplaceAll(markdown, placeholder, tableMD)
				replaced = true
			}
		}

		if replaced {
			used[id] = true
		}
	}

	return markdown, conflicts
}

func replaceMarkdownTables(markdown string, tables []markdownTable, replacements map[int]string) string {
	if len(replacements) == 0 {
		return markdown
	}

	lines := strings.Split(markdown, "\n")

	for i := len(tables) - 1; i >= 0; i-- {
		table := tables[i]
		replacement, ok := replacements[i]
		if !ok {
			continue
		}

		replacementLines := strings.Split(strings.TrimSpace(replacement), "\n")
		start := table.startLine
		end := table.endLine
		if start < 0 || end >= len(lines) || start > end {
			continue
		}

		lines = append(lines[:start], append(replacementLines, lines[end+1:]...)...)
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func findBestTableMatch(mdTable markdownTable, candidates []Table, tableMarkdowns map[string]string, used map[string]bool, orderHints map[string]int, mdIndex int) (string, *TableConflict) {
	bestIdx := -1
	bestScore := 0.0

	for idx, candidate := range candidates {
		candidateID := normalizeIdentifier(firstNonEmpty(candidate.ID, candidate.Name))
		if candidateID == "" || used[candidateID] {
			continue
		}

		if _, ok := tableMarkdowns[candidateID]; !ok {
			continue
		}

		order := idx
		if hint, ok := orderHints[candidateID]; ok {
			order = hint
		}

		score := scoreTableCandidate(mdTable.Table, candidate, mdIndex, order)
		if score > bestScore {
			bestScore = score
			bestIdx = idx
		}
	}

	if bestIdx == -1 || bestScore == 0 {
		return "", nil
	}

	candidate := candidates[bestIdx]
	candidateID := normalizeIdentifier(firstNonEmpty(candidate.ID, candidate.Name))
	if !sameShape(mdTable.Table, candidate) {
		mdRows, mdCols := tableShape(mdTable.Table)
		candRows, candCols := tableShape(candidate)
		reason := fmt.Sprintf("table shape mismatch: md %dx%d vs json %dx%d", mdRows, mdCols, candRows, candCols)
		return "", &TableConflict{MarkdownIndex: mdIndex, TableID: candidateID, Reason: reason}
	}

	return candidateID, nil
}

func scoreTableCandidate(mdTable Table, candidate Table, mdIndex int, order int) float64 {
	score := 0.0

	mdRows, mdCols := tableShape(mdTable)
	candRows, candCols := tableShape(candidate)

	switch {
	case mdRows == candRows:
		score += 2
	case abs(mdRows-candRows) == 1:
		score += 1
	}

	switch {
	case mdCols == candCols:
		score += 2
	case abs(mdCols-candCols) == 1:
		score += 1
	}

	score += headerOverlapScore(mdTable, candidate)

	if order >= 0 {
		distance := abs(order - mdIndex)
		score += 1.0 / float64(distance+1)
	}

	return score
}

func headerOverlapScore(mdTable Table, candidate Table) float64 {
	mdHeaders := normalizeHeaders(mdTable)
	candidateHeaders := normalizeHeaders(candidate)

	if len(mdHeaders) == 0 || len(candidateHeaders) == 0 {
		return 0
	}

	matches := 0
	for header := range mdHeaders {
		if _, ok := candidateHeaders[header]; ok {
			matches++
		}
	}

	maxHeaders := len(mdHeaders)
	if len(candidateHeaders) > maxHeaders {
		maxHeaders = len(candidateHeaders)
	}

	if maxHeaders == 0 {
		return 0
	}

	return float64(matches) / float64(maxHeaders)
}

func normalizeHeaders(table Table) map[string]struct{} {
	headers := map[string]struct{}{}
	if len(table.Rows) == 0 || len(table.Rows[0].Cells) == 0 {
		return headers
	}

	for _, cell := range table.Rows[0].Cells {
		header := strings.ToLower(strings.TrimSpace(cell.Text))
		if header == "" {
			continue
		}
		headers[header] = struct{}{}
	}

	return headers
}

func parseMarkdownTablesWithSpans(md string) []markdownTable {
	lines := strings.Split(md, "\n")
	var tables []markdownTable

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") || !strings.Contains(line, "|") {
			continue
		}

		header := parseMarkdownRow(line)
		if len(header) == 0 {
			continue
		}

		if i+1 >= len(lines) || !strings.Contains(lines[i+1], "-") {
			continue
		}

		start := i
		rows := []TableRow{{Cells: wrapCells(header)}}
		end := i

		for j := i + 2; j < len(lines); j++ {
			rowLine := strings.TrimSpace(lines[j])
			if !strings.HasPrefix(rowLine, "|") || !strings.Contains(rowLine, "|") {
				break
			}

			cells := parseMarkdownRow(rowLine)
			if len(cells) == 0 {
				continue
			}
			rows = append(rows, TableRow{Cells: wrapCells(cells)})
			end = j
			i = j
		}

		tables = append(tables, markdownTable{Table: Table{Rows: rows}, startLine: start, endLine: end})
	}

	return tables
}

func tableShape(table Table) (int, int) {
	rows := len(table.Rows)
	cols := 0
	if len(table.Rows) > 0 {
		cols = len(table.Rows[0].Cells)
		return rows, cols
	}

	md := strings.TrimSpace(table.Markdown)
	if md != "" {
		parsed := ParseMarkdownTables(md)
		if len(parsed) > 0 {
			return tableShape(parsed[0])
		}
	}
	return rows, cols
}

func sameShape(a Table, b Table) bool {
	rowsA, colsA := tableShape(a)
	rowsB, colsB := tableShape(b)
	return rowsA == rowsB && colsA == colsB
}

func markOrder(marks []Mark) map[string]int {
	order := map[string]int{}

	for idx, mark := range marks {
		id := normalizeIdentifier(resolveMarkTarget(mark))
		if id == "" {
			continue
		}
		if _, ok := order[id]; ok {
			continue
		}
		order[id] = idx
	}

	return order
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// MergeVotingTables заполняет таблицы Pass A данными Pass B на основании номера и ФИО.
func MergeVotingTables(passATables, passBTables []Table, similarityThreshold float64) ([]Table, MergeStats) {
	if similarityThreshold <= 0 {
		similarityThreshold = 0.6
	}

	stats := MergeStats{}
	merged := make([]Table, 0, len(passATables))

	for _, tableA := range passATables {
		numA, fioA := detectVotingColumns(tableA)
		if numA == -1 || fioA == -1 {
			merged = append(merged, tableA)
			continue
		}

		matchedTableB, numB, fioB := findMatchingTable(passBTables)
		if matchedTableB == nil {
			merged = append(merged, tableA)
			continue
		}

		mergedTable, tableStats := mergeTableRows(tableA, *matchedTableB, numA, fioA, numB, fioB, similarityThreshold)
		stats.TotalRows += tableStats.TotalRows
		stats.MergedRows += tableStats.MergedRows
		stats.MatchedRows += tableStats.MatchedRows

		merged = append(merged, mergedTable)
	}

	return merged, stats
}

func mergeTableRows(passA, passB Table, numA, fioA, numB, fioB int, threshold float64) (Table, MergeStats) {
	stats := MergeStats{}
	keyB := buildRowIndex(passB, numB, fioB)

	rows := make([]TableRow, 0, len(passA.Rows))
	for rowIdx, row := range passA.Rows {
		if rowIdx == 0 {
			rows = append(rows, row)
			continue
		}

		stats.TotalRows++
		if len(row.Cells) == 0 {
			rows = append(rows, row)
			continue
		}

		number := safeCellText(row.Cells, numA)
		fio := normalizeName(safeCellText(row.Cells, fioA))
		if fio == "" {
			rows = append(rows, row)
			continue
		}

		candidates := keyB[number]
		bestIdx := -1
		bestScore := 0.0
		for idx, candidate := range candidates {
			score := similarityScore(fio, candidate.normalizedName)
			if score >= threshold && score > bestScore {
				bestIdx = idx
				bestScore = score
			}
		}

		if bestIdx == -1 {
			rows = append(rows, row)
			continue
		}

		stats.MatchedRows++
		candidate := candidates[bestIdx]
		mergedCells := make([]TableCell, len(row.Cells))
		copy(mergedCells, row.Cells)

		for idx, cell := range candidate.row.Cells {
			if idx >= len(mergedCells) {
				mergedCells = append(mergedCells, cell)
				continue
			}

			if strings.TrimSpace(mergedCells[idx].Text) == "" && strings.TrimSpace(cell.Text) != "" {
				mergedCells[idx] = cell
				stats.MergedRows++
			}
		}

		rows = append(rows, TableRow{Cells: mergedCells})
	}

	return Table{ID: passA.ID, Name: passA.Name, Caption: passA.Caption, Markdown: passA.Markdown, Rows: rows}, stats
}

type indexedRow struct {
	normalizedName string
	row            TableRow
}

func buildRowIndex(table Table, numIdx, fioIdx int) map[string][]indexedRow {
	index := map[string][]indexedRow{}
	for rowIdx, row := range table.Rows {
		if rowIdx == 0 {
			continue
		}
		number := safeCellText(row.Cells, numIdx)
		fio := normalizeName(safeCellText(row.Cells, fioIdx))
		if fio == "" {
			continue
		}

		index[number] = append(index[number], indexedRow{normalizedName: fio, row: row})
	}

	return index
}

func findMatchingTable(candidates []Table) (*Table, int, int) {
	for _, candidate := range candidates {
		numIdx, fioIdx := detectVotingColumns(candidate)
		if numIdx == -1 || fioIdx == -1 {
			continue
		}
		return &candidate, numIdx, fioIdx
	}

	return nil, -1, -1
}

func detectVotingColumns(table Table) (int, int) {
	if len(table.Rows) == 0 || len(table.Rows[0].Cells) == 0 {
		return -1, -1
	}

	numIdx := -1
	fioIdx := -1
	for idx, cell := range table.Rows[0].Cells {
		header := strings.ToLower(strings.TrimSpace(cell.Text))
		switch {
		case strings.Contains(header, "№") || strings.Contains(header, "no") || strings.Contains(header, "номер"):
			numIdx = idx
		case strings.Contains(header, "фио") || strings.Contains(header, "имя") || strings.Contains(header, "name"):
			fioIdx = idx
		}
	}

	return numIdx, fioIdx
}

func safeCellText(cells []TableCell, idx int) string {
	if idx < 0 || idx >= len(cells) {
		return ""
	}
	return strings.TrimSpace(cells[idx].Text)
}

func parseMarkdownRow(line string) []string {
	parts := strings.Split(line, "|")
	var cells []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cells = append(cells, part)
	}
	return cells
}

func wrapCells(values []string) []TableCell {
	cells := make([]TableCell, 0, len(values))
	for _, v := range values {
		cells = append(cells, TableCell{Text: v})
	}
	return cells
}

func normalizeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "")
	return value
}

func similarityScore(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	distance := levenshteinDistance(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 1
	}
	return 1 - float64(distance)/float64(maxLen)
}

func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}

	for i := 0; i <= len(a); i++ {
		dp[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		dp[0][j] = j
	}

	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}

			dp[i][j] = min(
				dp[i-1][j]+1,
				dp[i][j-1]+1,
				dp[i-1][j-1]+cost,
			)
		}
	}

	return dp[len(a)][len(b)]
}

func min(values ...int) int {
	if len(values) == 0 {
		return 0
	}

	result := values[0]
	for _, v := range values[1:] {
		if v < result {
			result = v
		}
	}

	return result
}

// CalculateVotingFillRate вычисляет долю строк, где заполнены номер и ФИО.
func CalculateVotingFillRate(tables []Table) float64 {
	total := 0
	filled := 0

	for _, table := range tables {
		numIdx, fioIdx := detectVotingColumns(table)
		if numIdx == -1 || fioIdx == -1 {
			continue
		}

		for _, row := range table.Rows[1:] {
			total++
			if strings.TrimSpace(safeCellText(row.Cells, numIdx)) != "" && strings.TrimSpace(safeCellText(row.Cells, fioIdx)) != "" {
				filled++
			}
		}
	}

	if total == 0 {
		return 0
	}

	return float64(filled) / float64(total)
}
