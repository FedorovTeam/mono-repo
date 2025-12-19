package main

import (
	"strings"
	"testing"
)

func TestParseProtocolHTML(t *testing.T) {
	html := `<!doctype html>
<html>
<head><title>Протокол заседания</title></head>
<body>
<h1>Очередное заседание</h1>
<table>
  <caption>Результаты голосования</caption>
  <thead>
    <tr><th>№</th><th>Депутат</th><th>Голос</th></tr>
  </thead>
  <tbody>
    <tr><td>1</td><td>Иванов<br>И.И.</td><td>За</td></tr>
    <tr><td>2</td><td>Петров</td><td>Против</td></tr>
  </tbody>
</table>
</body>
</html>`

	title, tables, err := parseProtocolHTML(strings.NewReader(html))
	if err != nil {
		t.Fatalf("parseProtocolHTML returned error: %v", err)
	}

	if title != "Очередное заседание" {
		t.Fatalf("unexpected title: %q", title)
	}

	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}

	table := tables[0]
	if table.Caption != "Результаты голосования" {
		t.Fatalf("unexpected caption: %q", table.Caption)
	}

	expectedHeaders := []string{"№", "Депутат", "Голос"}
	if !equalSlices(table.Headers, expectedHeaders) {
		t.Fatalf("headers mismatch: %v vs %v", table.Headers, expectedHeaders)
	}

	expectedRow := []string{"1", "Иванов<br>И.И.", "За"}
	if len(table.Rows) == 0 || !equalSlices(table.Rows[0], expectedRow) {
		t.Fatalf("row mismatch: %v", table.Rows)
	}
}

func TestRenderMarkdownTable(t *testing.T) {
	headers := []string{"Имя", "Комментарий"}
	rows := [][]string{{"Пайп", "Текст с | символом"}}

	md := renderMarkdownTable(headers, rows)

	if !strings.Contains(md, "\\|") {
		t.Fatalf("markdown must escape pipe: %s", md)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
