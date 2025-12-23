package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/mono-repo/table-extractor-service/internal/telemetry"
)

func TestAdapterProducesArtifacts(t *testing.T) {
	runner := &fakeRunner{
		responses: map[string]CommandResult{
			"pdfinfo":   {Stdout: []byte("Pages: 2\nProducer: Poppler\nTitle: Demo")},
			"pdftotext": {Stdout: []byte("Name   Votes\nAlice  10\nBob    5")},
			"pdftoppm":  {Stdout: []byte("")},
		},
	}

	now := time.Date(2024, 11, 1, 10, 0, 0, 0, time.UTC)
	adapter := &Adapter{
		cfg: Config{
			PdfInfoPath:         defaultPdfinfo,
			PdftotextPath:       defaultPdftotext,
			PdftoppmPath:        defaultPdftoppm,
			PdfInfoTimeout:      time.Second,
			PdftotextTimeout:    time.Second,
			PdftoppmTimeout:     time.Second,
			MaxParallelCommands: 1,
			Pages:               "all",
			TempDir:             t.TempDir(),
			Mode:                modeLayout,
		},
		runner: runner,
		parser: DefaultParser{},
		now:    func() time.Time { return now },
	}

	result, err := adapter.Extract(context.Background(), "/tmp/demo.pdf")
	require.NoError(t, err)

	require.Contains(t, string(result.Markdown), "table_1")
	require.NotEmpty(t, result.RawJSON)
	require.NotEmpty(t, result.NormalizedJSON)
	require.Contains(t, string(result.Manifest), `"table_count": 1`)

	var manifest Manifest
	require.NoError(t, json.Unmarshal(result.Manifest, &manifest))
	require.Equal(t, 1, manifest.TableCount)
	require.Equal(t, 2, manifest.PDFInfo.Pages)
	require.Equal(t, now.UTC(), manifest.GeneratedAt)

	require.Contains(t, runner.commands(), "pdfinfo")
	require.Contains(t, runner.commands(), "pdftotext")
	require.Contains(t, runner.commands(), "pdftoppm")

	require.Contains(t, result.DebugFiles, "pdfinfo.txt")
	require.Contains(t, result.DebugFiles, "pdftotext.layout.txt")
}

func TestAdapterPropagatesCommandError(t *testing.T) {
	runner := &fakeRunner{
		responses: map[string]CommandResult{
			"pdfinfo": {},
		},
		errs: map[string]error{
			"pdfinfo": errors.New("boom"),
		},
	}

	adapter := &Adapter{
		cfg: Config{
			PdfInfoPath:         defaultPdfinfo,
			PdftotextPath:       defaultPdftotext,
			PdftoppmPath:        defaultPdftoppm,
			PdfInfoTimeout:      time.Second,
			PdftotextTimeout:    time.Second,
			PdftoppmTimeout:     time.Second,
			MaxParallelCommands: 1,
			TempDir:             t.TempDir(),
			Mode:                modeLayout,
		},
		runner: runner,
		parser: DefaultParser{},
		now:    time.Now,
	}

	_, err := adapter.Extract(context.Background(), "/tmp/demo.pdf")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pdfinfo")
}

func TestAdapterUsesCustomBinariesAndBBoxMode(t *testing.T) {
	const (
		customPdfInfo   = "/opt/poppler/bin/pdfinfo"
		customPdftotext = "/opt/poppler/bin/pdftotext"
		customPdftoppm  = "/opt/poppler/bin/pdftoppm"
	)

	runner := &fakeRunner{
		responses: map[string]CommandResult{
			customPdfInfo:   {Stdout: []byte("Pages: 1")},
			customPdftotext: {Stdout: []byte("<bbox>data</bbox>")},
			customPdftoppm:  {Stdout: []byte("")},
		},
	}

	adapter := &Adapter{
		cfg: Config{
			PdfInfoPath:         customPdfInfo,
			PdftotextPath:       customPdftotext,
			PdftoppmPath:        customPdftoppm,
			PdfInfoTimeout:      time.Second,
			PdftotextTimeout:    time.Second,
			PdftoppmTimeout:     time.Second,
			MaxParallelCommands: 1,
			Pages:               "1-1",
			TempDir:             t.TempDir(),
			Mode:                modeBBox,
		},
		runner: runner,
		parser: DefaultParser{},
		now:    time.Now,
	}

	_, err := adapter.Extract(context.Background(), "/tmp/demo.pdf")
	require.NoError(t, err)

	require.Contains(t, runner.commands(), customPdfInfo)
	require.Contains(t, runner.commands(), customPdftotext)
	require.Contains(t, runner.commands(), customPdftoppm)

	args := runner.argsFor(customPdftotext)
	require.NotNil(t, args)
	require.Contains(t, args, "-bbox")
	require.NotContains(t, args, "-layout")
}

func TestAdapterUsesBBoxAndLayoutWhenVotingDetectionEnabled(t *testing.T) {
	runner := &fakeRunner{
		responses: map[string]CommandResult{
			"pdfinfo":   {Stdout: []byte("Pages: 1")},
			"pdftotext": {Stdout: []byte(`<page number="1" width="100" height="100"><word xMin="10" yMin="10" xMax="20" yMax="12">Присутствует</word></page>`)},
			"pdftoppm":  {Stdout: []byte("")},
		},
	}

	adapter := &Adapter{
		cfg: Config{
			PdfInfoPath:         defaultPdfinfo,
			PdftotextPath:       defaultPdftotext,
			PdftoppmPath:        defaultPdftoppm,
			PdfInfoTimeout:      time.Second,
			PdftotextTimeout:    time.Second,
			PdftoppmTimeout:     time.Second,
			MaxParallelCommands: 1,
			Pages:               "1-1",
			TempDir:             t.TempDir(),
			Mode:                modeLayout,
			DetectVotingTables:  true,
		},
		runner: runner,
		parser: staticParser{
			parsed: ParsedTables{
				NormalizedTables: []NormalizedTable{{Headers: []string{"a"}, Rows: []map[string]string{{"a": "b"}}}},
			},
		},
		now: time.Now,
	}

	result, err := adapter.Extract(context.Background(), "/tmp/demo.pdf")
	require.NoError(t, err)

	require.GreaterOrEqual(t, runner.count("pdftotext"), 2)
	first := runner.argsForOccurrence("pdftotext", 0)
	second := runner.argsForOccurrence("pdftotext", 1)
	require.Contains(t, first, "-bbox")
	require.Contains(t, second, "-layout")
	require.Contains(t, result.DebugFiles, "pdftotext.bbox.html")
	require.Contains(t, result.DebugFiles, "pdftotext.layout.txt")
}

func TestAdapterFallsBackWhenVotingHeaderMissing(t *testing.T) {
	runner := &votingFallbackRunner{
		t:          t,
		layoutText: "Name Votes\nAlice 1\nBob 2\n",
		bboxHTML:   `<page number="1" width="200" height="200"><word xMin="10" yMin="10" xMax="20" yMax="12">Header</word></page>`,
	}
	adapter := &Adapter{
		cfg: Config{
			PdfInfoPath:         defaultPdfinfo,
			PdftotextPath:       defaultPdftotext,
			PdftoppmPath:        defaultPdftoppm,
			PdfInfoTimeout:      time.Second,
			PdftotextTimeout:    time.Second,
			PdftoppmTimeout:     time.Second,
			MaxParallelCommands: 1,
			Pages:               "all",
			TempDir:             t.TempDir(),
			Mode:                modeLayout,
			DetectVotingTables:  true,
		},
		runner: runner,
		parser: NewVotingTableParser(),
		now:    time.Now,
	}

	result, err := adapter.Extract(context.Background(), "/tmp/demo.pdf")
	require.NoError(t, err)

	var normalized struct {
		Tables []NormalizedTable `json:"tables"`
	}
	require.NoError(t, json.Unmarshal(result.NormalizedJSON, &normalized))
	require.Len(t, normalized.Tables, 1)
	require.Equal(t, "table_1", normalized.Tables[0].Name)
	require.Contains(t, string(result.Markdown), "table_1")
	require.NotContains(t, string(result.Markdown), "voting_table")
}

func TestExternalRunnerHonorsTimeout(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	runner := NewExternalCommandRunner(1, zap.New(core))

	ctx := telemetry.ContextWithCorrelationID(context.Background(), "corr-timeout")
	start := time.Now()
	_, err := runner.Run(ctx, 50*time.Millisecond, "sleep", "1")
	require.Error(t, err)
	require.Less(t, time.Since(start), 500*time.Millisecond)

	timeoutLogs := observed.FilterField(zap.Bool("timed_out", true))
	require.Greater(t, timeoutLogs.Len(), 0)
	entry := timeoutLogs.All()[timeoutLogs.Len()-1]
	require.Equal(t, zap.ErrorLevel, entry.Level)
	require.Equal(t, "sleep", entry.ContextMap()["command"])
	require.Equal(t, "corr-timeout", entry.ContextMap()["correlation_id"])
}

func TestExternalRunnerLogsCommandError(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	runner := NewExternalCommandRunner(1, zap.New(core))

	ctx := telemetry.ContextWithCorrelationID(context.Background(), "corr-error")
	_, err := runner.Run(ctx, time.Second, "false")
	require.Error(t, err)

	errorLogs := observed.FilterMessage("command run failed")
	require.Greater(t, errorLogs.Len(), 0)
	entry := errorLogs.All()[errorLogs.Len()-1]
	require.Equal(t, zap.ErrorLevel, entry.Level)
	require.Equal(t, "false", entry.ContextMap()["command"])
	require.Equal(t, "corr-error", entry.ContextMap()["correlation_id"])
	require.Equal(t, false, entry.ContextMap()["timed_out"])
}

type votingFallbackRunner struct {
	t          testing.TB
	layoutText string
	bboxHTML   string
	calls      []commandCall
}

func (r *votingFallbackRunner) Run(_ context.Context, _ time.Duration, name string, args ...string) (CommandResult, error) {
	r.calls = append(r.calls, commandCall{name: name, args: args})
	switch name {
	case defaultPdfinfo:
		return CommandResult{Stdout: []byte("Pages: 1")}, nil
	case defaultPdftotext:
		for _, arg := range args {
			if arg == "-bbox" {
				return CommandResult{Stdout: []byte(r.bboxHTML)}, nil
			}
		}
		return CommandResult{Stdout: []byte(r.layoutText)}, nil
	case defaultPdftoppm:
		if len(args) == 0 {
			return CommandResult{}, nil
		}
		prefix := args[len(args)-1]
		img := blankPNG(r.t)
		return CommandResult{}, os.WriteFile(prefix+"-1.png", img, 0o644)
	default:
		return CommandResult{}, nil
	}
}

func blankPNG(t testing.TB) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 50, 50))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

type fakeRunner struct {
	responses map[string]CommandResult
	errs      map[string]error
	calls     []commandCall
}

func (f *fakeRunner) Run(_ context.Context, _ time.Duration, name string, args ...string) (CommandResult, error) {
	f.calls = append(f.calls, commandCall{name: name, args: args})
	if err := f.errs[name]; err != nil {
		return CommandResult{}, err
	}
	if res, ok := f.responses[name]; ok {
		return res, nil
	}
	return CommandResult{}, nil
}

func (f *fakeRunner) commands() []string {
	names := make([]string, 0, len(f.calls))
	for _, call := range f.calls {
		names = append(names, call.name)
	}
	return names
}

func (f *fakeRunner) argsFor(name string) []string {
	for _, call := range f.calls {
		if call.name == name {
			return call.args
		}
	}
	return nil
}

func (f *fakeRunner) argsForOccurrence(name string, idx int) []string {
	cnt := 0
	for _, call := range f.calls {
		if call.name == name {
			if cnt == idx {
				return call.args
			}
			cnt++
		}
	}
	return nil
}

func (f *fakeRunner) count(name string) int {
	cnt := 0
	for _, call := range f.calls {
		if call.name == name {
			cnt++
		}
	}
	return cnt
}

type commandCall struct {
	name string
	args []string
}

type staticParser struct {
	parsed ParsedTables
	err    error
}

func (s staticParser) ParseTables(ParseInput) (ParsedTables, error) {
	return s.parsed, s.err
}

func (staticParser) RenderMarkdown(t ParsedTables) string {
	return DefaultParser{}.RenderMarkdown(t)
}
