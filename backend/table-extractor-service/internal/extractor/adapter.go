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

	"github.com/mono-repo/table-extractor-service/internal/usecase"
	"pdf2md"
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
)

// Adapter orchestrates poppler utilities and converts their output into artifacts
// expected by the TableExtractor use-case.
type Adapter struct {
	runner CommandRunner
	parser TableParser
	cfg    Config
	now    func() time.Time
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
}

type ParsedTables struct {
	RawTables        []RawTable
	NormalizedTables []NormalizedTable
	HeaderText       string
	Document         *pdf2md.Document `json:"-"`
}

type RawTable struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
	Page    int        `json:"page,omitempty"`
}

type NormalizedTable struct {
	Name    string              `json:"name,omitempty"`
	Headers []string            `json:"headers"`
	Rows    []map[string]string `json:"rows"`
	Page    int                 `json:"page,omitempty"`
}

type Manifest struct {
	Source             string        `json:"source"`
	GeneratedAt        time.Time     `json:"generated_at"`
	DetectVotingTables bool          `json:"detect_voting_tables"`
	Pages              string        `json:"pages"`
	TableCount         int           `json:"table_count"`
	Processing         ProcessingLog `json:"processing"`
	PDFInfo            PDFInfo       `json:"pdfinfo"`
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

func NewAdapter(cfg Config) *Adapter {
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

	runner := NewExternalCommandRunner(cfg.MaxParallelCommands)

	var parser TableParser = DefaultParser{}
	if cfg.DetectVotingTables {
		cfg.Mode = modeBBox
		parser = NewVotingTableParser()
	}

	return &Adapter{
		cfg:    cfg,
		runner: runner,
		parser: parser,
		now:    time.Now,
	}
}

// Extract executes poppler utilities with timeouts, parses tables and returns
// artifacts for MinIO upload.
func (a *Adapter) Extract(ctx context.Context, pdfPath string) (usecase.ExtractionResult, error) {
	if strings.TrimSpace(pdfPath) == "" {
		return usecase.ExtractionResult{}, errors.New("pdf path is required")
	}

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
	infoRes, err := a.runner.Run(ctx, a.cfg.PdfInfoTimeout, a.cfg.PdfInfoPath, pdfPath)
	manifest.Processing.PdfInfoMs = durationMs(pdfInfoStart)
	if err != nil {
		return usecase.ExtractionResult{}, fmt.Errorf("pdfinfo: %w (stderr: %s)", err, string(infoRes.Stderr))
	}
	manifest.PDFInfo = parsePDFInfo(string(infoRes.Stdout))

	textStart := time.Now()
	pdftotextMode := a.cfg.Mode
	if a.cfg.DetectVotingTables {
		pdftotextMode = modeBBox
	}
	mainTextRes, err := a.runPdftotext(ctx, pdfPath, pdftotextMode)
	manifest.Processing.PdftotextMs = durationMs(textStart)
	if err != nil {
		return usecase.ExtractionResult{}, fmt.Errorf("pdftotext: %w (stderr: %s)", err, string(mainTextRes.Stderr))
	}
	layoutText := string(mainTextRes.Stdout)
	bboxHTML := ""
	layoutOutput := mainTextRes.Stdout
	layoutStderr := mainTextRes.Stderr
	bboxOutput := []byte(nil)
	bboxStderr := []byte(nil)

	if pdftotextMode == modeBBox {
		bboxOutput = mainTextRes.Stdout
		bboxStderr = mainTextRes.Stderr
		bboxHTML = layoutText

		layoutStart := time.Now()
		layoutRes, layoutErr := a.runPdftotext(ctx, pdfPath, modeLayout)
		manifest.Processing.PdftotextMs += durationMs(layoutStart)
		if layoutErr != nil {
			return usecase.ExtractionResult{}, fmt.Errorf("pdftotext (layout): %w (stderr: %s)", layoutErr, string(layoutRes.Stderr))
		}
		layoutText = string(layoutRes.Stdout)
		layoutOutput = layoutRes.Stdout
		layoutStderr = layoutRes.Stderr
	}

	ppmStart := time.Now()
	pagePNGs, debugFiles, err := a.renderCheckboxes(ctx, pdfPath)
	manifest.Processing.PdftoppmMs = durationMs(ppmStart)
	if err != nil {
		return usecase.ExtractionResult{}, err
	}

	parseStart := time.Now()
	parsed, err := a.parser.ParseTables(ParseInput{
		LayoutText: layoutText,
		BBoxHTML:   bboxHTML,
		PagePNGs:   pagePNGs,
		StartPage:  startPage,
	})
	if err != nil {
		return usecase.ExtractionResult{}, fmt.Errorf("parse tables: %w", err)
	}
	manifest.Processing.ParseMs = durationMs(parseStart)
	manifest.TableCount = len(parsed.NormalizedTables)

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

	md := a.parser.RenderMarkdown(parsed)
	manifestPayload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return usecase.ExtractionResult{}, fmt.Errorf("marshal manifest: %w", err)
	}

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
