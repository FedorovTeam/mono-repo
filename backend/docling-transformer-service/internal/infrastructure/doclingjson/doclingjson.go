package doclingjson

import (
	"encoding/json"
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
