package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/mono-repo/table-extractor-service/internal/extractor"
)

func main() {
	var (
		forceTableExtraction = flag.Bool("force-table-extraction", true, "симулировать ForceTableExtractionPDF=true")
		markdownTableHints   = flag.Bool("markdown-table-hints", true, "указывать наличие markdown table hints")
		outputName           = flag.String("output", "stenogramma-04", "папка в backend/test/runs для сохранения артефактов")
		pages                = flag.String("pages", "1-10", "диапазон страниц для pdftotext/table-extractor")
		inputPathFlag        = flag.String("input", filepath.Join("..", "test", "Регламент - в ред. от 08.06.2023.pdf"), "путь к PDF")
	)
	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("cannot get cwd: %v", err)
	}

	inputPath := *inputPathFlag
	if !filepath.IsAbs(inputPath) {
		inputPath = filepath.Join(cwd, inputPath)
	}
	outputDir := filepath.Join(cwd, "..", "test", "runs", *outputName)

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		log.Fatalf("cannot create output dir: %v", err)
	}

	logger, _ := zap.NewDevelopment()
	logger = logger.With(
		zap.Bool("force_table_extraction_pdf", *forceTableExtraction),
		zap.Bool("markdown_table_hints", *markdownTableHints),
	)

	ctx := context.Background()
	shouldExtract := *forceTableExtraction && *markdownTableHints
	var baseMarkdown []byte

	if shouldExtract {
		adapter := extractor.NewAdapter(extractor.Config{
			DetectVotingTables: true,
			Mode:               "layout",
			Pages:              *pages,
		}, logger)

		logger.Info("stage store -> convert -> extract_tables -> merge: starting extract_tables", zap.String("pdf", inputPath))
		result, extractErr := adapter.Extract(ctx, inputPath)
		if extractErr != nil {
			log.Fatalf("extraction failed: %v", extractErr)
		}

		writeFile(outputDir, "tables.md", result.Markdown)
		writeFile(outputDir, "tables.raw.json", result.RawJSON)
		writeFile(outputDir, "tables.normalized.json", result.NormalizedJSON)
		writeFile(outputDir, "manifest.json", result.Manifest)

		if layout, ok := result.DebugFiles["pdftotext.layout.txt"]; ok {
			writeFile(outputDir, "layout.txt", layout)
			baseMarkdown = append([]byte{}, layout...)
		}
		if bbox, ok := result.DebugFiles["pdftotext.bbox.html"]; ok {
			writeFile(outputDir, "bbox.html", bbox)
		}

		if len(baseMarkdown) == 0 {
			log.Fatalf("layout markdown is missing in extractor output")
		}

		merged, mergeErr := mergeMarkdownWithTables(string(baseMarkdown), string(result.Markdown))
		if mergeErr != nil {
			log.Fatalf("merge failed: %v", mergeErr)
		}
		writeFile(outputDir, "base.md", baseMarkdown)
		writeFile(outputDir, "merged.md", []byte(merged))

		fallbackHit := strings.Contains(merged, "tables:appended")
		logDecision(outputDir, shouldExtract, *forceTableExtraction, *markdownTableHints, fallbackHit)
		logger.Info("merge completed", zap.Bool("fallback_appended", fallbackHit))
	} else {
		logger.Info("skipping extract_tables due to router flags", zap.Bool("force_table_extraction_pdf", *forceTableExtraction), zap.Bool("markdown_table_hints", *markdownTableHints))
		layout, layoutErr := extractLayoutMarkdown(ctx, inputPath, *pages)
		if layoutErr != nil {
			log.Fatalf("pdftotext layout failed: %v", layoutErr)
		}
		baseMarkdown = layout
		writeFile(outputDir, "base.md", baseMarkdown)
		writeFile(outputDir, "merged.md", baseMarkdown)
		logDecision(outputDir, shouldExtract, *forceTableExtraction, *markdownTableHints, false)
	}

	logger.Info("artifacts saved", zap.String("dir", outputDir))
}

func logDecision(outputDir string, shouldExtract, forceExtraction, markdownHints, fallback bool) {
	content := fmt.Sprintf("force_table_extraction_pdf=%v\nmarkdown_table_hints=%v\nshould_extract_tables=%v\nfallback_appended=%v\n", forceExtraction, markdownHints, shouldExtract, fallback)
	if err := os.WriteFile(filepath.Join(outputDir, "pipeline.log"), []byte(content), 0o644); err != nil {
		log.Fatalf("cannot write pipeline log: %v", err)
	}
}

func extractLayoutMarkdown(ctx context.Context, pdfPath, pages string) ([]byte, error) {
	args := []string{"-layout"}
	start, end := parsePages(pages)
	if start > 0 {
		args = append(args, "-f", fmt.Sprint(start))
	}
	if end > 0 {
		args = append(args, "-l", fmt.Sprint(end))
	}
	args = append(args, pdfPath, "-")

	cmd := exec.CommandContext(ctx, "pdftotext", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pdftotext: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func parsePages(pages string) (int, int) {
	parts := strings.Split(strings.TrimSpace(pages), "-")
	if len(parts) != 2 {
		return 0, 0
	}
	start := parseInt(parts[0])
	end := parseInt(parts[1])
	return start, end
}

func parseInt(value string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}

func writeFile(dir, name string, content []byte) {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		log.Fatalf("cannot write %s: %v", path, err)
	}
}

// mergeMarkdownWithTables скопирован из document-service, чтобы воспроизвести
// логику объединения базового Markdown с извлечёнными таблицами и fallback-маркером.
func mergeMarkdownWithTables(baseMarkdown, tablesMarkdown string) (string, error) {
	if strings.TrimSpace(tablesMarkdown) == "" {
		return baseMarkdown, nil
	}
	const (
		tablesStart = "<!-- tables:start -->"
		tablesEnd   = "<!-- tables:end -->"
	)

	trimmedTables := strings.TrimSpace(tablesMarkdown)
	markerStart := regexp.MustCompile(`<!--\s*tables:start(?:\s*#\d+)?\s*-->`)
	markerEnd := regexp.MustCompile(`<!--\s*tables:end(?:\s*#\d+)?\s*-->`)

	isTableLine := func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return strings.HasPrefix(trimmed, "|")
	}

	tableSignature := func(header string) string {
		trimmed := strings.Trim(header, "|")
		if strings.TrimSpace(trimmed) == "" {
			return ""
		}
		parts := strings.Split(trimmed, "|")
		cols := make([]string, 0, len(parts))
		for _, part := range parts {
			cols = append(cols, strings.ToLower(strings.TrimSpace(part)))
		}
		if len(cols) == 0 {
			return ""
		}
		return fmt.Sprintf("%d:%s", len(cols), strings.Join(cols, "|"))
	}

	type tableBlock struct {
		start, end int
		lines      []string
		signature  string
	}

	findTables := func(md string) []tableBlock {
		lines := strings.Split(md, "\n")
		blocks := make([]tableBlock, 0)
		for i := 0; i < len(lines); i++ {
			if !isTableLine(lines[i]) {
				continue
			}
			start := i
			j := i
			for j < len(lines) && isTableLine(lines[j]) {
				j++
			}
			blockLines := lines[start:j]
			signature := tableSignature(blockLines[0])
			if signature == "" {
				i = j - 1
				continue
			}
			blocks = append(blocks, tableBlock{start: start, end: j - 1, lines: blockLines, signature: signature})
			i = j - 1
		}
		return blocks
	}

	columnCount := func(block tableBlock) int {
		header := strings.Trim(block.lines[0], "|")
		if header == "" {
			return 0
		}
		parts := strings.Split(header, "|")
		return len(parts)
	}

	replaceTablesBySignature := func(md string, newTables []tableBlock) (string, bool) {
		lines := strings.Split(md, "\n")
		baseTables := findTables(md)
		if len(baseTables) == 0 || len(newTables) == 0 {
			return md, false
		}
		used := make(map[int]struct{})
		replaced := false
		for idx := len(baseTables) - 1; idx >= 0; idx-- {
			baseBlock := baseTables[idx]
			match := -1
			for newIdx, tbl := range newTables {
				if tbl.signature == baseBlock.signature {
					if _, ok := used[newIdx]; ok {
						continue
					}
					match = newIdx
					break
				}
			}
			if match == -1 {
				continue
			}
			used[match] = struct{}{}
			replaced = true
			lines = append(append(append([]string{}, lines[:baseBlock.start]...), newTables[match].lines...), lines[baseBlock.end+1:]...)
		}
		return strings.Join(lines, "\n"), replaced
	}

	deduplicateTables := func(md string) string {
		lines := strings.Split(md, "\n")
		tables := findTables(md)
		seen := make(map[string]struct{})
		for i := len(tables) - 1; i >= 0; i-- {
			block := tables[i]
			if _, ok := seen[block.signature]; ok {
				lines = append(lines[:block.start], lines[block.end+1:]...)
				continue
			}
			seen[block.signature] = struct{}{}
		}
		return strings.Join(lines, "\n")
	}

	markerBlocks := func(md string) ([]string, [][2]int) {
		blocks := make([]string, 0)
		ranges := make([][2]int, 0)
		pos := 0
		for {
			startLoc := markerStart.FindStringIndex(md[pos:])
			if startLoc == nil {
				break
			}
			startIdx := pos + startLoc[0]
			startEnd := pos + startLoc[1]
			endLoc := markerEnd.FindStringIndex(md[startEnd:])
			if endLoc == nil {
				break
			}
			endIdx := startEnd + endLoc[1]
			blocks = append(blocks, strings.TrimSpace(md[startIdx:endIdx]))
			ranges = append(ranges, [2]int{startIdx, endIdx})
			pos = endIdx
		}
		return blocks, ranges
	}

	extractContent := func(md string) (string, bool) {
		blocks, _ := markerBlocks(md)
		if len(blocks) > 0 {
			return strings.Join(blocks, "\n\n"), true
		}
		start := strings.Index(md, tablesStart)
		end := strings.Index(md, tablesEnd)
		if start != -1 && end != -1 && end >= start {
			content := strings.TrimSpace(md[start+len(tablesStart) : end])
			return content, true
		}
		return strings.TrimSpace(md), false
	}

	tablesContent, hasMarkers := extractContent(trimmedTables)
	if !hasMarkers {
		if tablesBlocks := findTables(trimmedTables); len(tablesBlocks) > 0 {
			lines := strings.Split(trimmedTables, "\n")
			start := tablesBlocks[0].start
			end := tablesBlocks[len(tablesBlocks)-1].end
			tablesContent = strings.TrimSpace(strings.Join(lines[start:end+1], "\n"))
		} else {
			return "", fmt.Errorf("tables block not found in artifact")
		}
	}

	tablesBlock := tablesContent
	if !hasMarkers {
		tablesBlock = fmt.Sprintf("%s\n%s\n%s", tablesStart, tablesContent, tablesEnd)
	}

	if _, baseRanges := markerBlocks(baseMarkdown); hasMarkers && len(baseRanges) > 0 {
		start := baseRanges[0][0]
		end := baseRanges[len(baseRanges)-1][1]
		merged := fmt.Sprintf("%s%s%s", baseMarkdown[:start], tablesContent, baseMarkdown[end:])
		return deduplicateTables(merged), nil
	}

	if start := strings.Index(baseMarkdown, tablesStart); start != -1 {
		if end := strings.Index(baseMarkdown, tablesEnd); end != -1 && end >= start {
			prefix := baseMarkdown[:start+len(tablesStart)]
			suffix := baseMarkdown[end:]
			merged := fmt.Sprintf("%s\n\n%s\n%s", prefix, tablesContent, suffix)
			return deduplicateTables(merged), nil
		}
	}

	newTables := findTables(tablesContent)
	if mergedBySignature, replaced := replaceTablesBySignature(baseMarkdown, newTables); replaced {
		return deduplicateTables(mergedBySignature), nil
	}

	baseTables := findTables(baseMarkdown)
	if len(baseTables) > 0 && len(newTables) > 0 {
		targetIdx := -1
		newCols := columnCount(newTables[0])
		for idx, tbl := range baseTables {
			if tbl.signature == newTables[0].signature {
				targetIdx = idx
				break
			}
			if targetIdx == -1 && newCols > 0 && columnCount(tbl) == newCols {
				targetIdx = idx
			}
		}
		if targetIdx == -1 {
			targetIdx = 0
		}
		lines := strings.Split(baseMarkdown, "\n")
		start := baseTables[targetIdx].start
		end := baseTables[targetIdx].end
		mergedLines := append(append([]string{}, lines[:start]...), strings.Split(tablesBlock, "\n")...)
		mergedLines = append(mergedLines, lines[end+1:]...)
		return deduplicateTables(strings.Join(mergedLines, "\n")), nil
	}

	if strings.TrimSpace(baseMarkdown) == "" {
		return tablesBlock, nil
	}

	fallbackNote := "<!-- tables:appended (no anchor found) -->"
	return deduplicateTables(fmt.Sprintf("%s\n\n%s\n%s\n", strings.TrimSpace(baseMarkdown), fallbackNote, tablesBlock)), nil
}
