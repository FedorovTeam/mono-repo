package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mono-repo/table-extractor-service/internal/telemetry"
	"github.com/mono-repo/table-extractor-service/internal/usecase"
	"github.com/mono-repo/table-extractor-service/internal/voting"
	"go.uber.org/zap"
)

const (
	defaultPdfInfoTimeout  = 10 * time.Second
	defaultPdftotextTimout = 20 * time.Second
	defaultPdftoppmTimeout = 30 * time.Second
	defaultPdftoppmDPI     = 200
	defaultPdfinfo         = "pdfinfo"
	defaultPdftotext       = "pdftotext"
	defaultPdftoppm        = "pdftoppm"
	modeLayout             = "layout"
	modeBBox               = "bbox"
	defaultTwoColumnMedian = 160
	defaultMaxGapRows      = 150
	defaultMaxGapWords     = 2000
)

// Adapter orchestrates poppler utilities and converts their output into artifacts
// expected by the TableExtractor use-case.
type Adapter struct {
	runner CommandRunner
	parser TableParser
	cfg    Config
	now    func() time.Time
	logger *zap.Logger
}

type Config struct {
	PdfInfoPath         string
	PdftotextPath       string
	PdftoppmPath        string
	PdfInfoTimeout      time.Duration
	PdftotextTimeout    time.Duration
	PdftoppmTimeout     time.Duration
	PdftoppmDPI         int
	MaxParallelCommands int
	Pages               string
	DetectVotingTables  bool
	TempDir             string
	Mode                string
	Filters             TableFiltersConfig
}

// CommandRunner abstracts execution of external utilities and can be replaced by mocks in tests.
type CommandRunner interface {
	Run(ctx context.Context, timeout time.Duration, name string, args ...string) (CommandResult, error)
}

type CommandResult struct {
	Stdout []byte
	Stderr []byte
}

// TableParser converts extracted poppler artifacts into structured tables and Markdown.
// Keeping this interface separate makes it easier to replace parsing logic in tests.
type TableParser interface {
	ParseTables(input ParseInput) (ParsedTables, error)
	RenderMarkdown(tables ParsedTables) string
}

// ParseInput aggregates text and rendering artifacts required for table parsing.
// LayoutText is a plain-text representation (from `pdftotext -layout`), BBoxHTML
// keeps the raw XHTML from `pdftotext -bbox`, and PagePNGs map page numbers to
// rendered PNG bytes from `pdftoppm`.
type ParseInput struct {
	LayoutText string
	BBoxHTML   string
	PagePNGs   map[int][]byte
	StartPage  int
	Logger     *zap.Logger
}

type ParsedTables struct {
	RawTables        []RawTable
	NormalizedTables []NormalizedTable
	HeaderText       string
	Document         *voting.Document `json:"-"`
	Stats            ParseStats       `json:"-"`
	DroppedTables    []FilteredTable  `json:"-"`
}

type ParseStats struct {
	SplitByGapLines         int
	SplitByGapSamples       []string
	FiltersEnabled          bool
	FiltersApplied          bool
	FilteredTables          int
	FilteredTwoColumnMedian int
	FilteredNoDelimiter     int
	FilterThresholds        TableFiltersConfig
}

type RawTable struct {
	Headers         []string   `json:"headers"`
	Rows            [][]string `json:"rows"`
	Page            int        `json:"page,omitempty"`
	Pages           []int      `json:"pages,omitempty"`
	splitByGapLines int
}

type NormalizedTable struct {
	Name    string              `json:"name,omitempty"`
	Headers []string            `json:"headers"`
	Rows    []map[string]string `json:"rows"`
	Page    int                 `json:"page,omitempty"`
	Pages   []int               `json:"pages,omitempty"`
}

type FilteredTable struct {
	Reason          string `json:"reason"`
	Index           int    `json:"index"`
	Page            int    `json:"page,omitempty"`
	Pages           []int  `json:"pages,omitempty"`
	MedianLength    int    `json:"median_length,omitempty"`
	Rows            int    `json:"rows,omitempty"`
	Words           int    `json:"words,omitempty"`
	SplitByGapLines int    `json:"split_by_gap_lines,omitempty"`
}

type Manifest struct {
	Source              string             `json:"source"`
	GeneratedAt         time.Time          `json:"generated_at"`
	DetectVotingTables  bool               `json:"detect_voting_tables"`
	Pages               string             `json:"pages"`
	TableCount          int                `json:"table_count"`
	FiltersEnabled      bool               `json:"filters_enabled"`
	FilterThresholds    TableFiltersConfig `json:"filter_thresholds"`
	FilteredTables      int                `json:"filtered_tables"`
	FilteredTwoColumn   int                `json:"filtered_two_column"`
	FilteredNoDelimiter int                `json:"filtered_no_delimiter"`
	DroppedTables       []FilteredTable    `json:"dropped_tables,omitempty"`
	Processing          ProcessingLog      `json:"processing"`
	PDFInfo             PDFInfo            `json:"pdfinfo"`
}

type ProcessingLog struct {
	PdfInfoMs   int64 `json:"pdfinfo_ms"`
	PdftotextMs int64 `json:"pdftotext_ms"`
	PdftoppmMs  int64 `json:"pdftoppm_ms"`
	ParseMs     int64 `json:"parse_ms"`
}

type PDFInfo struct {
	Pages        int    `json:"pages"`
	Raw          string `json:"raw"`
	Producer     string `json:"producer,omitempty"`
	PDFVersion   string `json:"pdf_version,omitempty"`
	Title        string `json:"title,omitempty"`
	Author       string `json:"author,omitempty"`
	CreationDate string `json:"creation_date,omitempty"`
}

func NewAdapter(cfg Config, logger *zap.Logger) *Adapter {
	cfg.Mode = normalizeMode(cfg.Mode)
	if cfg.PdfInfoTimeout <= 0 {
		cfg.PdfInfoTimeout = defaultPdfInfoTimeout
	}
	if cfg.PdftotextTimeout <= 0 {
		cfg.PdftotextTimeout = defaultPdftotextTimout
	}
	if cfg.PdftoppmTimeout <= 0 {
		cfg.PdftoppmTimeout = defaultPdftoppmTimeout
	}
	if cfg.MaxParallelCommands <= 0 {
		cfg.MaxParallelCommands = 1
	}
	if cfg.TempDir == "" {
		cfg.TempDir = os.TempDir()
	}
	if cfg.PdfInfoPath == "" {
		cfg.PdfInfoPath = defaultPdfinfo
	}
	if cfg.PdftotextPath == "" {
		cfg.PdftotextPath = defaultPdftotext
	}
	if cfg.PdftoppmPath == "" {
		cfg.PdftoppmPath = defaultPdftoppm
	}
	if cfg.PdftoppmDPI <= 0 {
		cfg.PdftoppmDPI = defaultPdftoppmDPI
	}

	if cfg.Filters.TwoColumnMedianLengthLimit <= 0 {
		cfg.Filters.TwoColumnMedianLengthLimit = defaultTwoColumnMedian
	}
	if cfg.Filters.MaxRowsWithoutDelimiters <= 0 {
		cfg.Filters.MaxRowsWithoutDelimiters = defaultMaxGapRows
	}
	if cfg.Filters.MaxWordsWithoutDelimiters <= 0 {
		cfg.Filters.MaxWordsWithoutDelimiters = defaultMaxGapWords
	}

	logger = resolveLogger(logger)

	runner := NewExternalCommandRunner(cfg.MaxParallelCommands, logger)

	var parser TableParser = NewDefaultParser(cfg.Filters)
	if cfg.DetectVotingTables {
		cfg.Mode = modeBBox
		parser = NewVotingTableParser()
	}

	return &Adapter{
		cfg:    cfg,
		runner: runner,
		parser: parser,
		now:    time.Now,
		logger: logger,
	}
}

// Extract executes poppler utilities with timeouts, parses tables and returns
// artifacts for MinIO upload.
func (a *Adapter) Extract(ctx context.Context, pdfPath string) (usecase.ExtractionResult, error) {
	if strings.TrimSpace(pdfPath) == "" {
		return usecase.ExtractionResult{}, errors.New("pdf path is required")
	}

	logger := a.baseLogger(ctx, pdfPath)
	logger.Debug("starting extraction",
		zap.String("pages", a.cfg.Pages),
		zap.Bool("detect_voting_tables", a.cfg.DetectVotingTables),
		zap.String("mode", a.cfg.Mode),
	)

	startPage, _ := parsePageRange(a.cfg.Pages)
	if startPage == 0 {
		startPage = 1
	}

	manifest := Manifest{
		Source:             pdfPath,
		DetectVotingTables: a.cfg.DetectVotingTables,
		Pages:              a.cfg.Pages,
		GeneratedAt:        a.now().UTC(),
	}

	pdfInfoStart := time.Now()
	pdfInfoLogger := logger.With(zap.String("step", "pdfinfo"))
	pdfInfoLogger.Debug("running pdfinfo",
		zap.Duration("timeout", a.cfg.PdfInfoTimeout),
	)
	infoRes, err := a.runner.Run(ctx, a.cfg.PdfInfoTimeout, a.cfg.PdfInfoPath, pdfPath)
	manifest.Processing.PdfInfoMs = durationMs(pdfInfoStart)
	if err != nil {
		pdfInfoLogger.Error("pdfinfo failed",
			zap.Error(err),
			zap.Int64("duration_ms", manifest.Processing.PdfInfoMs),
			zap.Int("stdout_bytes", len(infoRes.Stdout)),
			zap.Int("stderr_bytes", len(infoRes.Stderr)),
		)
		return usecase.ExtractionResult{}, fmt.Errorf("pdfinfo: %w (stderr: %s)", err, string(infoRes.Stderr))
	}
	manifest.PDFInfo = parsePDFInfo(string(infoRes.Stdout))
	pdfInfoLogger.Debug("pdfinfo parsed",
		zap.Int("pages", manifest.PDFInfo.Pages),
		zap.String("producer", manifest.PDFInfo.Producer),
		zap.Int64("duration_ms", manifest.Processing.PdfInfoMs),
		zap.Int("stdout_bytes", len(infoRes.Stdout)),
		zap.Int("stderr_bytes", len(infoRes.Stderr)),
	)

	textStart := time.Now()
	pdftotextMode := a.cfg.Mode
	if a.cfg.DetectVotingTables {
		pdftotextMode = modeBBox
	}
	logger.Debug("pdftotext mode selected", zap.String("pdftotext_mode", pdftotextMode))
	pdftotextLogger := logger.With(
		zap.String("step", "pdftotext"),
		zap.String("mode", pdftotextMode),
	)
	pdftotextLogger.Debug("running pdftotext",
		zap.Duration("timeout", a.cfg.PdftotextTimeout),
		zap.String("pages", a.cfg.Pages),
	)
	mainTextRes, err := a.runPdftotext(ctx, pdfPath, pdftotextMode)
	manifest.Processing.PdftotextMs = durationMs(textStart)
	if err != nil {
		pdftotextLogger.Error("pdftotext failed",
			zap.Error(err),
			zap.Int("stdout_bytes", len(mainTextRes.Stdout)),
			zap.Int("stderr_bytes", len(mainTextRes.Stderr)),
			zap.Int64("duration_ms", manifest.Processing.PdftotextMs),
		)
		return usecase.ExtractionResult{}, fmt.Errorf("pdftotext: %w (stderr: %s)", err, string(mainTextRes.Stderr))
	}
	layoutText := string(mainTextRes.Stdout)
	bboxHTML := ""
	layoutOutput := mainTextRes.Stdout
	layoutStderr := mainTextRes.Stderr
	bboxOutput := []byte(nil)
	bboxStderr := []byte(nil)
	pdftotextLogger.Debug("pdftotext completed",
		zap.Int("stdout_bytes", len(mainTextRes.Stdout)),
		zap.Int("stderr_bytes", len(mainTextRes.Stderr)),
		zap.Int64("duration_ms", manifest.Processing.PdftotextMs),
	)

	if pdftotextMode == modeBBox {
		bboxOutput = mainTextRes.Stdout
		bboxStderr = mainTextRes.Stderr
		bboxHTML = layoutText

		layoutStart := time.Now()
		layoutLogger := logger.With(
			zap.String("step", "pdftotext"),
			zap.String("mode", modeLayout),
		)
		layoutLogger.Debug("running pdftotext layout",
			zap.Duration("timeout", a.cfg.PdftotextTimeout),
			zap.String("pages", a.cfg.Pages),
		)
		layoutRes, layoutErr := a.runPdftotext(ctx, pdfPath, modeLayout)
		layoutDuration := durationMs(layoutStart)
		manifest.Processing.PdftotextMs += layoutDuration
		if layoutErr != nil {
			layoutLogger.Error("pdftotext layout failed",
				zap.Error(layoutErr),
				zap.Int("stdout_bytes", len(layoutRes.Stdout)),
				zap.Int("stderr_bytes", len(layoutRes.Stderr)),
				zap.Int64("duration_ms", layoutDuration),
			)
			return usecase.ExtractionResult{}, fmt.Errorf("pdftotext (layout): %w (stderr: %s)", layoutErr, string(layoutRes.Stderr))
		}
		layoutText = string(layoutRes.Stdout)
		layoutOutput = layoutRes.Stdout
		layoutStderr = layoutRes.Stderr
		layoutLogger.Debug("pdftotext layout completed",
			zap.Int("stdout_bytes", len(layoutRes.Stdout)),
			zap.Int("stderr_bytes", len(layoutRes.Stderr)),
			zap.Int64("duration_ms", layoutDuration),
		)
	}

	ppmStart := time.Now()
	ppmLogger := logger.With(zap.String("step", "pdftoppm"))
	ppmLogger.Debug("rendering checkboxes",
		zap.Duration("timeout", a.cfg.PdftoppmTimeout),
		zap.Int("dpi", a.cfg.PdftoppmDPI),
		zap.String("pages", a.cfg.Pages),
	)
	pagePNGs, debugFiles, err := a.renderCheckboxes(ctx, pdfPath)
	manifest.Processing.PdftoppmMs = durationMs(ppmStart)
	if err != nil {
		ppmLogger.Error("pdftoppm failed",
			zap.Error(err),
			zap.Int64("duration_ms", manifest.Processing.PdftoppmMs),
		)
		return usecase.ExtractionResult{}, err
	}
	ppmLogger.Debug("pdftoppm completed",
		zap.Int("pages_rendered", len(pagePNGs)),
		zap.Int("debug_files", len(debugFiles)),
		zap.Int64("duration_ms", manifest.Processing.PdftoppmMs),
	)

	parseStart := time.Now()
	parseLogger := logger.With(zap.String("step", "parse_tables"))
	parseLogger.Debug("parsing tables",
		zap.Int("start_page", startPage),
		zap.Int("layout_text_len", len(layoutText)),
		zap.Int("page_pngs", len(pagePNGs)),
		zap.Bool("has_bbox_html", strings.TrimSpace(bboxHTML) != ""),
	)
	parseInput := ParseInput{
		LayoutText: layoutText,
		BBoxHTML:   bboxHTML,
		PagePNGs:   pagePNGs,
		StartPage:  startPage,
		Logger:     parseLogger,
	}
	parser := a.parser
	if a.cfg.DetectVotingTables {
		if _, ok := parser.(VotingTableParser); ok {
			useVoting, detectErr := canUseVotingParser(parseInput)
			if detectErr != nil {
				parseLogger.Error("voting parser precheck failed", zap.Error(detectErr))
				return usecase.ExtractionResult{}, fmt.Errorf("parse tables: %w", detectErr)
			}
			if !useVoting {
				parseLogger.Info("voting headers not found, falling back to default parser")
				parser = NewDefaultParser(a.cfg.Filters)
			}
		}
	}
	parsed, err := parser.ParseTables(parseInput)
	if err != nil {
		parseLogger.Error("parse tables failed",
			zap.Error(err),
			zap.Int64("duration_ms", durationMs(parseStart)),
		)
		return usecase.ExtractionResult{}, fmt.Errorf("parse tables: %w", err)
	}
	parsed = applyFilters(parsed, a.cfg.Filters, parseLogger)
	manifest.Processing.ParseMs = durationMs(parseStart)
	manifest.TableCount = len(parsed.NormalizedTables)
	manifest.FiltersEnabled = parsed.Stats.FiltersEnabled
	manifest.FilterThresholds = parsed.Stats.FilterThresholds
	manifest.FilteredTables = parsed.Stats.FilteredTables
	manifest.FilteredTwoColumn = parsed.Stats.FilteredTwoColumnMedian
	manifest.FilteredNoDelimiter = parsed.Stats.FilteredNoDelimiter
	manifest.DroppedTables = parsed.DroppedTables
	parseLogger.Debug("tables parsed",
		zap.Int("raw_tables", len(parsed.RawTables)),
		zap.Int("normalized_tables", len(parsed.NormalizedTables)),
		zap.Int("normalized_columns", countNormalizedColumns(parsed.NormalizedTables)),
		zap.Int("normalized_rows", countNormalizedRows(parsed.NormalizedTables)),
		zap.Bool("filters_enabled", parsed.Stats.FiltersEnabled),
		zap.Int("filtered_tables", parsed.Stats.FilteredTables),
		zap.Int("filtered_two_column", parsed.Stats.FilteredTwoColumnMedian),
		zap.Int("filtered_no_delimiters", parsed.Stats.FilteredNoDelimiter),
		zap.Int("split_by_gap_lines", parsed.Stats.SplitByGapLines),
		zap.Strings("split_by_gap_samples", parsed.Stats.SplitByGapSamples),
		zap.Int64("duration_ms", manifest.Processing.ParseMs),
	)

	rawPayload, err := json.MarshalIndent(struct {
		PDFInfo            PDFInfo    `json:"pdfinfo"`
		DetectVotingTables bool       `json:"detect_voting_tables"`
		Pages              string     `json:"pages"`
		LayoutText         string     `json:"layout_text"`
		BBoxHTML           string     `json:"bbox_html,omitempty"`
		Tables             []RawTable `json:"tables"`
	}{
		PDFInfo:            manifest.PDFInfo,
		DetectVotingTables: a.cfg.DetectVotingTables,
		Pages:              a.cfg.Pages,
		LayoutText:         layoutText,
		BBoxHTML:           bboxHTML,
		Tables:             parsed.RawTables,
	}, "", "  ")
	if err != nil {
		return usecase.ExtractionResult{}, fmt.Errorf("marshal raw json: %w", err)
	}

	normalizedPayload, err := json.MarshalIndent(struct {
		Tables []NormalizedTable `json:"tables"`
	}{
		Tables: parsed.NormalizedTables,
	}, "", "  ")
	if err != nil {
		return usecase.ExtractionResult{}, fmt.Errorf("marshal normalized json: %w", err)
	}

	md := parser.RenderMarkdown(parsed)
	manifestPayload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return usecase.ExtractionResult{}, fmt.Errorf("marshal manifest: %w", err)
	}
	logger.Debug("manifest and markdown generated",
		zap.Int("table_count", manifest.TableCount),
		zap.Int("raw_tables", len(parsed.RawTables)),
		zap.Int("normalized_tables", len(parsed.NormalizedTables)),
	)

	if debugFiles == nil {
		debugFiles = make(map[string][]byte)
	}
	debugFiles["pdfinfo.txt"] = infoRes.Stdout
	debugFiles["pdftotext.layout.txt"] = layoutOutput
	if len(layoutStderr) > 0 {
		debugFiles["pdftotext.stderr.txt"] = layoutStderr
	}
	if len(bboxOutput) > 0 {
		debugFiles["pdftotext.bbox.html"] = bboxOutput
	}
	if len(bboxStderr) > 0 {
		debugFiles["pdftotext.bbox.stderr.txt"] = bboxStderr
	}
	if len(infoRes.Stderr) > 0 {
		debugFiles["pdfinfo.stderr.txt"] = infoRes.Stderr
	}

	return usecase.ExtractionResult{
		RawJSON:        rawPayload,
		NormalizedJSON: normalizedPayload,
		Markdown:       []byte(md),
		Manifest:       manifestPayload,
		DebugFiles:     debugFiles,
	}, nil
}

func canUseVotingParser(input ParseInput) (bool, error) {
	if strings.TrimSpace(input.BBoxHTML) == "" {
		return false, errors.New("bbox html is required for voting parser")
	}
	pageSegments := splitBBoxPages(input.BBoxHTML, input.StartPage)
	if len(pageSegments) == 0 {
		return false, errors.New("bbox html does not contain <page> tags")
	}
	firstPage, err := voting.ParseBBoxHTML(pageSegments[0].HTML)
	if err != nil {
		return false, fmt.Errorf("parse bbox page %d: %w", pageSegments[0].Number, err)
	}
	if _, _, _, err := voting.ExtractColumns(firstPage); err != nil {
		return false, nil
	}
	return true, nil
}

func (a *Adapter) runPdftotext(ctx context.Context, pdfPath string, mode string) (CommandResult, error) {
	args := []string{pdftotextModeArg(mode)}
	start, end := parsePageRange(a.cfg.Pages)
	if start > 0 {
		args = append(args, "-f", fmt.Sprint(start))
	}
	if end > 0 {
		args = append(args, "-l", fmt.Sprint(end))
	}
	args = append(args, pdfPath, "-")
	return a.runner.Run(ctx, a.cfg.PdftotextTimeout, a.cfg.PdftotextPath, args...)
}

func (a *Adapter) renderCheckboxes(ctx context.Context, pdfPath string) (map[int][]byte, map[string][]byte, error) {
	tmpDir, err := os.MkdirTemp(a.cfg.TempDir, "tables-extractor-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	prefix := filepath.Join(tmpDir, "page")
	args := []string{"-png", "-r", fmt.Sprint(a.cfg.PdftoppmDPI)}
	start, end := parsePageRange(a.cfg.Pages)
	if start > 0 {
		args = append(args, "-f", fmt.Sprint(start))
	}
	if end > 0 {
		args = append(args, "-l", fmt.Sprint(end))
	}
	args = append(args, pdfPath, prefix)

	res, err := a.runner.Run(ctx, a.cfg.PdftoppmTimeout, a.cfg.PdftoppmPath, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("pdftoppm: %w (stderr: %s)", err, string(res.Stderr))
	}

	files, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		return nil, nil, fmt.Errorf("scan pdftoppm output: %w", err)
	}
	pagePNGs := make(map[int][]byte, len(files))
	debug := make(map[string][]byte, len(files))
	for _, p := range files {
		base := filepath.Base(p)
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil, nil, fmt.Errorf("read rendered page %s: %w", base, readErr)
		}
		debug[filepath.Join("checkboxes", base)] = data
		if n := parseSuffixPage(base); n > 0 {
			pagePNGs[n] = data
		}
	}
	return pagePNGs, debug, nil
}

func (a *Adapter) baseLogger(ctx context.Context, pdfPath string) *zap.Logger {
	logger := resolveLogger(a.logger)
	fields := []zap.Field{
		zap.String("pdf_path", pdfPath),
	}
	if correlationID := telemetry.CorrelationIDFromContext(ctx); correlationID != "" {
		fields = append(fields, zap.String("correlation_id", correlationID))
	}
	return logger.With(fields...)
}

func resolveLogger(logger *zap.Logger) *zap.Logger {
	if logger != nil {
		return logger
	}
	return zap.NewNop()
}

func countNormalizedRows(tables []NormalizedTable) int {
	total := 0
	for _, table := range tables {
		total += len(table.Rows)
	}
	return total
}

func countNormalizedColumns(tables []NormalizedTable) int {
	total := 0
	for _, table := range tables {
		total += len(table.Headers)
	}
	return total
}

func durationMs(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

var (
	rePages      = regexp.MustCompile(`(?i)^pages:\s+(\d+)`)
	reProducer   = regexp.MustCompile(`(?i)^producer:\s+(.*)$`)
	reVersion    = regexp.MustCompile(`(?i)^pdf version:\s+(.*)$`)
	reTitle      = regexp.MustCompile(`(?i)^title:\s+(.*)$`)
	reAuthor     = regexp.MustCompile(`(?i)^author:\s+(.*)$`)
	reCreateDate = regexp.MustCompile(`(?i)^creationdate:\s+(.*)$`)
	pageRangeRe  = regexp.MustCompile(`^\s*(\d+)(?:\s*-\s*(\d+))?\s*$`)
	pageSuffixRe = regexp.MustCompile(`-(\d+)\.png$`)
)

func parsePDFInfo(output string) PDFInfo {
	info := PDFInfo{Raw: output}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		l := strings.TrimSpace(line)
		switch {
		case rePages.MatchString(l):
			info.Pages = parseIntGroup(rePages, l)
		case reProducer.MatchString(l):
			info.Producer = strings.TrimSpace(reProducer.FindStringSubmatch(l)[1])
		case reVersion.MatchString(l):
			info.PDFVersion = strings.TrimSpace(reVersion.FindStringSubmatch(l)[1])
		case reTitle.MatchString(l):
			info.Title = strings.TrimSpace(reTitle.FindStringSubmatch(l)[1])
		case reAuthor.MatchString(l):
			info.Author = strings.TrimSpace(reAuthor.FindStringSubmatch(l)[1])
		case reCreateDate.MatchString(l):
			info.CreationDate = strings.TrimSpace(reCreateDate.FindStringSubmatch(l)[1])
		}
	}
	return info
}

func parseIntGroup(re *regexp.Regexp, value string) int {
	m := re.FindStringSubmatch(value)
	if len(m) < 2 {
		return 0
	}
	var result int
	fmt.Sscanf(m[1], "%d", &result)
	return result
}

func parsePageRange(raw string) (int, int) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "all") {
		return 0, 0
	}
	m := pageRangeRe.FindStringSubmatch(raw)
	if len(m) == 0 {
		return 0, 0
	}
	start := parseInt(m[1])
	end := start
	if m[2] != "" {
		end = parseInt(m[2])
	}
	return start, end
}

func parseInt(value string) int {
	var result int
	fmt.Sscanf(value, "%d", &result)
	return result
}

func parseSuffixPage(name string) int {
	m := pageSuffixRe.FindStringSubmatch(name)
	if len(m) != 2 {
		return 0
	}
	return parseInt(m[1])
}

func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case modeBBox:
		return modeBBox
	default:
		return modeLayout
	}
}

func pdftotextModeArg(mode string) string {
	if normalizeMode(mode) == modeBBox {
		return "-bbox"
	}
	return "-layout"
}
