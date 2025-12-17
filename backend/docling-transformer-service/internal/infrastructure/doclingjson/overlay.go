// backend/docling-transformer-service/internal/infrastructure/doclingjson/overlay.go
package doclingjson

import (
	"strings"
)

type MarkdownTableBlock struct {
	Table     Table
	StartLine int
	EndLine   int // inclusive
}

func ParseMarkdownTableBlocks(md string) []MarkdownTableBlock {
	lines := strings.Split(md, "\n")
	var blocks []MarkdownTableBlock

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "|") || !strings.Contains(line, "|") {
			continue
		}

		header := parseMarkdownRow(line)
		if len(header) == 0 {
			continue
		}

		// separator line
		if i+1 >= len(lines) || !looksLikeSeparatorLine(lines[i+1]) {
			continue
		}

		rows := []TableRow{{Cells: wrapCells(header)}}

		end := i + 1
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
		}

		blocks = append(blocks, MarkdownTableBlock{
			Table:     Table{Rows: rows},
			StartLine: i,
			EndLine:   end,
		})

		i = end
	}

	return blocks
}

func looksLikeSeparatorLine(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// простой детектор: много '-' и есть '|'
	if !strings.Contains(s, "|") {
		return false
	}
	return strings.Contains(s, "-")
}

// OverlayVotingTablesInMarkdown: оставляет baseMD как есть,
// но заменяет таблицы внутри на версии, “дозаполненные” из ocrMD.
// Заполняем ТОЛЬКО пустые ячейки base.
func OverlayVotingTablesInMarkdown(baseMD, ocrMD string, matchRate float64) (string, MergeStats) {
	baseBlocks := ParseMarkdownTableBlocks(baseMD)
	if len(baseBlocks) == 0 {
		return baseMD, MergeStats{}
	}

	ocrTables := ParseMarkdownTables(ocrMD)
	if len(ocrTables) == 0 {
		return baseMD, MergeStats{}
	}

	lines := strings.Split(baseMD, "\n")
	totalStats := MergeStats{}

	// идём с конца, чтобы замена диапазонов строк не ломала индексы
	for bi := len(baseBlocks) - 1; bi >= 0; bi-- {
		block := baseBlocks[bi]

		numA, fioA := detectVotingColumns(block.Table)
		if numA == -1 || fioA == -1 {
			continue
		}

		matchB, numB, fioB := findMatchingTable(ocrTables)
		if matchB == nil {
			continue
		}

		merged, stats := mergeTableRows(block.Table, *matchB, numA, fioA, numB, fioB, matchRate)
		totalStats.TotalRows += stats.TotalRows
		totalStats.MatchedRows += stats.MatchedRows
		totalStats.MergedRows += stats.MergedRows

		replacement := renderTableRows(merged.Rows)
		repLines := strings.Split(replacement, "\n")

		// replace [StartLine..EndLine]
		start := block.StartLine
		end := block.EndLine
		if start < 0 || end >= len(lines) || start > end {
			continue
		}

		newLines := make([]string, 0, len(lines)-((end-start)+1)+len(repLines))
		newLines = append(newLines, lines[:start]...)
		newLines = append(newLines, repLines...)
		newLines = append(newLines, lines[end+1:]...)
		lines = newLines
	}

	return strings.Join(lines, "\n"), totalStats
}
