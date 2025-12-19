package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

const (
	protocolURL    = "https://iltumen.ru/active-session/185/protocol/3617"
	outputMarkdown = "protocol.md"
	requestTimeout = 30 * time.Second
)

type tableData struct {
	Caption string
	Headers []string
	Rows    [][]string
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	markdown, err := fetchMarkdown(ctx, protocolURL)
	if err != nil {
		panic(err)
	}

	if err := os.WriteFile(outputMarkdown, []byte(markdown), 0o644); err != nil {
		panic(err)
	}

	fmt.Printf("Markdown сохранён в %s\n", outputMarkdown)
}

func fetchMarkdown(ctx context.Context, url string) (string, error) {
	client := &http.Client{Timeout: requestTimeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "protocol-md-scraper/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("http status: %d", resp.StatusCode)
	}

	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, resp.Body); err != nil {
		return "", err
	}

	title, tables, err := parseProtocolHTML(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return "", err
	}

	return buildMarkdownDocument(title, url, tables), nil
}

func parseProtocolHTML(r io.Reader) (string, []tableData, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return "", nil, err
	}

	title := extractTitle(doc)

	var tables []tableData
	doc.Find("table").Each(func(i int, tbl *goquery.Selection) {
		tables = append(tables, extractTable(tbl))
	})

	if len(tables) == 0 {
		return "", nil, errors.New("на странице нет таблиц")
	}

	return title, tables, nil
}

func extractTitle(doc *goquery.Document) string {
	if h1 := strings.TrimSpace(doc.Find("h1").First().Text()); h1 != "" {
		return h1
	}
	if h2 := strings.TrimSpace(doc.Find("h2").First().Text()); h2 != "" {
		return h2
	}
	if headTitle := strings.TrimSpace(doc.Find("title").First().Text()); headTitle != "" {
		return headTitle
	}
	return "Протокол"
}

func extractTable(sel *goquery.Selection) tableData {
	caption := strings.TrimSpace(sel.Find("caption").First().Text())

	headerCells := sel.Find("thead tr").First().Find("th,td")
	usedFirstRow := false

	if headerCells.Length() == 0 {
		firstRow := sel.Find("tr").First()
		headerCells = firstRow.Find("th,td")
		usedFirstRow = headerCells.Length() > 0
	}

	headers := collectCells(headerCells)

	rowsSel := sel.Find("tbody tr")
	if rowsSel.Length() == 0 {
		rowsSel = sel.Find("tr")
		if usedFirstRow && rowsSel.Length() > 0 {
			rowsSel = rowsSel.Slice(1, rowsSel.Length())
		}
	}

	rows := collectRows(rowsSel, len(headers))

	if len(headers) == 0 {
		headers = autoHeaders(rows)
	}

	return tableData{
		Caption: caption,
		Headers: headers,
		Rows:    rows,
	}
}

func collectRows(rowsSel *goquery.Selection, headerCount int) [][]string {
	var rows [][]string

	rowsSel.Each(func(_ int, tr *goquery.Selection) {
		cellsSel := tr.Find("td")
		if cellsSel.Length() == 0 {
			cellsSel = tr.Find("th")
		}
		cells := collectCells(cellsSel)
		if len(cells) == 0 {
			return
		}
		if headerCount > 0 && len(cells) < headerCount {
			cells = padCells(cells, headerCount)
		}
		rows = append(rows, cells)
	})

	return rows
}

func collectCells(cellsSel *goquery.Selection) []string {
	var cells []string
	cellsSel.Each(func(_ int, cell *goquery.Selection) {
		cells = append(cells, normalizeCellText(cell))
	})
	return cells
}

func normalizeCellText(cell *goquery.Selection) string {
	clone := cell.Clone()
	clone.Find("br").Each(func(_ int, br *goquery.Selection) {
		br.ReplaceWithHtml("\n")
	})

	text := clone.Text()
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")

	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.Join(strings.Fields(line), " ")
		cleaned = append(cleaned, line)
	}

	return strings.Join(cleaned, "<br>")
}

func escapeMarkdown(text string) string {
	text = strings.ReplaceAll(text, "|", "\\|")
	text = strings.ReplaceAll(text, "\n", " ")
	return text
}

func padCells(cells []string, size int) []string {
	if len(cells) >= size {
		return cells
	}
	padded := make([]string, size)
	copy(padded, cells)
	return padded
}

func autoHeaders(rows [][]string) []string {
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	headers := make([]string, maxCols)
	for i := range headers {
		headers[i] = fmt.Sprintf("Колонка %d", i+1)
	}
	return headers
}

func buildMarkdownDocument(title string, sourceURL string, tables []tableData) string {
	var sb strings.Builder

	if title != "" {
		sb.WriteString("# ")
		sb.WriteString(title)
		sb.WriteString("\n\n")
	}

	sb.WriteString("Источник: ")
	sb.WriteString(fmt.Sprintf("[%s](%s)", sourceURL, sourceURL))
	sb.WriteString("\n\n")

	for i, tbl := range tables {
		sectionTitle := tbl.Caption
		if sectionTitle == "" && len(tables) > 1 {
			sectionTitle = fmt.Sprintf("Таблица %d", i+1)
		}
		if sectionTitle != "" {
			sb.WriteString("## ")
			sb.WriteString(sectionTitle)
			sb.WriteString("\n\n")
		}

		sb.WriteString(renderMarkdownTable(tbl.Headers, tbl.Rows))
		sb.WriteString("\n\n")
	}

	return sb.String()
}

func renderMarkdownTable(headers []string, rows [][]string) string {
	var sb strings.Builder

	colCount := len(headers)
	if colCount == 0 {
		colCount = len(autoHeaders(rows))
		headers = autoHeaders(rows)
	}

	headers = escapeSlice(headers)

	sb.WriteString("| ")
	sb.WriteString(strings.Join(headers, " | "))
	sb.WriteString(" |\n")

	separators := make([]string, len(headers))
	for i := range separators {
		separators[i] = "---"
	}
	sb.WriteString("| ")
	sb.WriteString(strings.Join(separators, " | "))
	sb.WriteString(" |\n")

	for _, row := range rows {
		row = padCells(row, colCount)
		row = escapeSlice(row)
		sb.WriteString("| ")
		sb.WriteString(strings.Join(row, " | "))
		sb.WriteString(" |\n")
	}

	return sb.String()
}

func escapeSlice(values []string) []string {
	escaped := make([]string, len(values))
	for i, v := range values {
		escaped[i] = escapeMarkdown(strings.TrimSpace(v))
	}
	return escaped
}
