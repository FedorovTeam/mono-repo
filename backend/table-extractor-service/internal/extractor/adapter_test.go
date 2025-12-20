package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
			PdfInfoTimeout:      time.Second,
			PdftotextTimeout:    time.Second,
			PdftoppmTimeout:     time.Second,
			MaxParallelCommands: 1,
			Pages:               "all",
			TempDir:             t.TempDir(),
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
			PdfInfoTimeout:      time.Second,
			PdftotextTimeout:    time.Second,
			PdftoppmTimeout:     time.Second,
			MaxParallelCommands: 1,
			TempDir:             t.TempDir(),
		},
		runner: runner,
		parser: DefaultParser{},
		now:    time.Now,
	}

	_, err := adapter.Extract(context.Background(), "/tmp/demo.pdf")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pdfinfo")
}

func TestExternalRunnerHonorsTimeout(t *testing.T) {
	runner := NewExternalCommandRunner(1)

	ctx := context.Background()
	start := time.Now()
	_, err := runner.Run(ctx, 50*time.Millisecond, "sleep", "1")
	require.Error(t, err)
	require.Less(t, time.Since(start), 500*time.Millisecond)
}

type fakeRunner struct {
	responses map[string]CommandResult
	errs      map[string]error
	calls     []string
}

func (f *fakeRunner) Run(_ context.Context, _ time.Duration, name string, _ ...string) (CommandResult, error) {
	f.calls = append(f.calls, name)
	if err := f.errs[name]; err != nil {
		return CommandResult{}, err
	}
	if res, ok := f.responses[name]; ok {
		return res, nil
	}
	return CommandResult{}, nil
}

func (f *fakeRunner) commands() []string {
	return f.calls
}
