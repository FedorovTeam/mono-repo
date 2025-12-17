// backend/docling-transformer-service/internal/infrastructure/doclingjson/doclingjson.go
package doclingjson

import (
	"encoding/json"
	"strings"
)

const defaultTableSimilarityThreshold = 0.45

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
	doc, err := Parse(jsonContent)
	if err != nil {
		return "", false
	}

	tableMarkdowns := buildTableMarkdowns(doc.Tables)
	if len(tableMarkdowns) == 0 {
		return "", false
	}

	markdown := ""
	if mdFallback != nil && mdFallback != (*string)(nil) {
		markdown = strings.TrimSpace(*mdFallback)
	}

	if markdown != "" {
		used := map[string]bool{}
		for _, mark := range doc.Marks {
			id := normalizeIdentifier(resolveMarkTarget(mark))
			tableMD, ok := tableMarkdowns[id]
			if !ok {
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

		for id, tableMD := range tableMarkdowns {
			if used[id] {
				continue
			}

			if markdown != "" {
				markdown += "\n\n"
			}
			markdown += tableMD
		}

		return markdown, true
	}

	rendered := renderTablesInOrder(doc.Marks, tableMarkdowns)
	return rendered, rendered != ""
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
	lines := strings.Split(md, "\n")
	var tables []Table

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

		rows := []TableRow{{Cells: wrapCells(header)}}

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
			i = j
		}

		tables = append(tables, Table{Rows: rows})
	}

	return tables
}

// ----------------------
// Voting overlay helpers
// ----------------------

// detectVotingColumns возвращает индексы колонки "№" и "ФИО".
// ВАЖНО: поддерживает кейс, когда № и ФИО находятся В ОДНОЙ КОЛОНКЕ (fioIdx == numIdx),
// например заголовок "№ Ф.И.О." и строки "1 Иванов Иван".
func detectVotingColumns(table Table) (int, int) {
	if len(table.Rows) == 0 || len(table.Rows[0].Cells) == 0 {
		return -1, -1
	}

	numIdx := -1
	fioIdx := -1

	for idx, cell := range table.Rows[0].Cells {
		header := strings.ToLower(strings.TrimSpace(cell.Text))

		hasNum := strings.Contains(header, "№") || strings.Contains(header, "no") || strings.Contains(header, "номер")
		hasFio := strings.Contains(header, "фио") || strings.Contains(header, "ф.и.о") || strings.Contains(header, "имя") || strings.Contains(header, "name")

		// Частый кейс: "№ Ф.И.О." в одном столбце.
		if hasNum && hasFio {
			return idx, idx
		}

		if hasNum && numIdx == -1 {
			numIdx = idx
		}
		if hasFio && fioIdx == -1 {
			fioIdx = idx
		}
	}

	// Фоллбек: если отдельной колонки ФИО нет, но по строкам видно "1 Иванов Иван"
	// тогда считаем, что № и ФИО лежат в одном столбце numIdx.
	if numIdx != -1 && fioIdx == -1 {
		if looksLikeCombinedNumberNameColumn(table, numIdx) {
			return numIdx, numIdx
		}
	}

	return numIdx, fioIdx
}

func looksLikeCombinedNumberNameColumn(table Table, idx int) bool {
	// проверим несколько строк
	limit := len(table.Rows)
	if limit > 6 {
		limit = 6
	}
	for r := 1; r < limit; r++ {
		txt := safeCellText(table.Rows[r].Cells, idx)
		num, name := splitNumberAndName(txt)
		if num != "" && name != "" {
			return true
		}
	}
	return false
}

// splitNumberAndName("14 Иванов Иван") => ("14", "Иванов Иван")
// если не похоже — вернёт ("", originalTrimmed)
func splitNumberAndName(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}

	// вытащим ведущие цифры
	i := 0
	for i < len(value) && value[i] >= '0' && value[i] <= '9' {
		i++
	}
	if i == 0 {
		return "", value
	}

	num := strings.TrimSpace(value[:i])
	rest := strings.TrimSpace(value[i:])
	rest = strings.TrimLeft(rest, ".") // иногда "1. Иванов"
	rest = strings.TrimSpace(rest)
	return num, rest
}

// ExtractVotingKey извлекает (номер, нормализованное_ФИО) из строки.
// Если fioIdx == numIdx — парсим из одной ячейки "N ФИО".
func ExtractVotingKey(row TableRow, numIdx, fioIdx int) (string, string) {
	if numIdx < 0 || fioIdx < 0 || len(row.Cells) == 0 {
		return "", ""
	}

	if fioIdx == numIdx {
		raw := safeCellText(row.Cells, numIdx)
		num, name := splitNumberAndName(raw)
		return num, normalizeName(name)
	}

	num := strings.TrimSpace(safeCellText(row.Cells, numIdx))
	name := normalizeName(safeCellText(row.Cells, fioIdx))
	return num, name
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

		matchedTableB, numB, fioB, tableScore := findMatchingTable(tableA, passBTables, defaultTableSimilarityThreshold)
		if matchedTableB == nil || tableScore < defaultTableSimilarityThreshold {
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

		number, fioNorm := ExtractVotingKey(row, numA, fioA)
		if fioNorm == "" {
			rows = append(rows, row)
			continue
		}

		candidates := keyB[number]
		bestIdx := -1
		bestScore := 0.0

		for idx, candidate := range candidates {
			score := similarityScore(fioNorm, candidate.normalizedName)
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

			// заполняем только пустые ячейки base
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

		number, fioNorm := ExtractVotingKey(row, numIdx, fioIdx)
		if fioNorm == "" {
			continue
		}

		index[number] = append(index[number], indexedRow{normalizedName: fioNorm, row: row})
	}
	return index
}

func findMatchingTable(base Table, candidates []Table, minScore float64) (*Table, int, int, float64) {
	bestScore := 0.0
	bestNum := -1
	bestFio := -1
	var best *Table

	for i := range candidates {
		candidate := &candidates[i]

		numIdx, fioIdx := detectVotingColumns(*candidate)
		if numIdx == -1 || fioIdx == -1 {
			continue
		}

		score := tableSimilarity(base, *candidate)
		if score > bestScore {
			bestScore = score
			best = candidate
			bestNum = numIdx
			bestFio = fioIdx
		}
	}

	if bestScore < minScore {
		return nil, -1, -1, 0
	}

	return best, bestNum, bestFio, bestScore
}

func tableSimilarity(base, candidate Table) float64 {
	headersA := normalizedHeaders(base)
	headersB := normalizedHeaders(candidate)

	headerScore := headerSimilarityScore(headersA, headersB)
	rowsScore := rowCountScore(len(base.Rows), len(candidate.Rows))
	keywordScore := votingKeywordScore(headersA, headersB)

	votingColumnsScore := 0.0
	if hasVotingColumns(base) && hasVotingColumns(candidate) {
		votingColumnsScore = 1
	}

	return headerScore*0.55 + rowsScore*0.2 + keywordScore*0.2 + votingColumnsScore*0.05
}

func normalizedHeaders(table Table) []string {
	if len(table.Rows) == 0 {
		return nil
	}

	headers := make([]string, 0, len(table.Rows[0].Cells))
	for _, cell := range table.Rows[0].Cells {
		headers = append(headers, normalizeHeader(cell.Text))
	}

	return headers
}

func normalizeHeader(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(
		"|", " ",
		",", " ",
		".", " ",
		";", " ",
		":", " ",
	)
	value = replacer.Replace(value)
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func headerSimilarityScore(headersA, headersB []string) float64 {
	if len(headersA) == 0 || len(headersB) == 0 {
		return 0
	}

	total := 0.0
	for _, headerA := range headersA {
		best := 0.0
		for _, headerB := range headersB {
			if score := similarityScore(headerA, headerB); score > best {
				best = score
			}
		}
		total += best
	}

	return total / float64(len(headersA))
}

func rowCountScore(rowsA, rowsB int) float64 {
	if rowsA == 0 && rowsB == 0 {
		return 0
	}

	diff := rowsA - rowsB
	if diff < 0 {
		diff = -diff
	}

	max := rowsA
	if rowsB > max {
		max = rowsB
	}

	if max == 0 {
		return 0
	}

	return 1 - float64(diff)/float64(max)
}

func votingKeywordScore(headersA, headersB []string) float64 {
	keywordsA := collectVotingKeywords(headersA)
	keywordsB := collectVotingKeywords(headersB)

	if len(keywordsA) == 0 || len(keywordsB) == 0 {
		return 0
	}

	matches := 0
	for kw := range keywordsA {
		if _, ok := keywordsB[kw]; ok {
			matches++
		}
	}

	return float64(matches) / float64(len(keywordsA))
}

func collectVotingKeywords(headers []string) map[string]struct{} {
	keywords := []string{
		"за", "против", "воздерж", "присутств", "отсутств", "голоса", "итог", "for", "against", "abstain", "present",
	}

	normalized := make(map[string]struct{})
	for _, header := range headers {
		for _, kw := range keywords {
			if strings.Contains(header, kw) {
				normalized[kw] = struct{}{}
			}
		}
	}

	return normalized
}

func hasVotingColumns(table Table) bool {
	numIdx, fioIdx := detectVotingColumns(table)
	return numIdx != -1 && fioIdx != -1
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
// Поддерживает кейс "№ ФИО" в одном столбце.
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
			num, fio := ExtractVotingKey(row, numIdx, fioIdx)
			if strings.TrimSpace(num) != "" && strings.TrimSpace(fio) != "" {
				filled++
			}
		}
	}

	if total == 0 {
		return 0
	}

	return float64(filled) / float64(total)
}
