package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"pdf2md/internal/pdf2md"
)

const DefaultPDF = "3617.pdf"

func main() {
	var (
		dpi      = flag.Int("dpi", 200, "Rendering DPI for pdftoppm (200 is a good default; higher = slower, but can be more robust)")
		tmpDir   = flag.String("tmp", "", "Temp directory for rendered PNGs (default: system temp/pdf2md)")
		outPath  = flag.String("o", "", "Output markdown file path (only used when exactly one input PDF is provided)")
		outDir   = flag.String("outdir", "", "Output directory (when converting multiple PDFs)")
		keepTmp  = flag.Bool("keep-tmp", false, "Do not delete temporary rendered images")
		validate = flag.Bool("validate", true, "Validate that detected checkmarks match header totals when possible; prints warnings, but does not fail")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage:\n  pdf2md [flags] <input1.pdf> [input2.pdf ...]\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "If no input is provided, DefaultPDF is used: %s\n\n", DefaultPDF)
		flag.PrintDefaults()
	}
	flag.Parse()

	inputs := flag.Args()
	if len(inputs) == 0 {
		// <-- вот оно: берём из константы
		inputs = []string{DefaultPDF}
	}

	opt := pdf2md.DefaultOptions()
	opt.DPI = *dpi
	opt.KeepTemps = *keepTmp
	if *tmpDir != "" {
		opt.TempDir = *tmpDir
	}

	// When one input and -o provided, write exactly there.
	if len(inputs) == 1 && *outPath != "" {
		if err := convertOne(inputs[0], *outPath, *validate, opt); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	// Otherwise, use -outdir or per-file next to inputs.
	for _, in := range inputs {
		out := ""
		if *outDir != "" {
			base := strings.TrimSuffix(filepath.Base(in), filepath.Ext(in))
			out = filepath.Join(*outDir, base+".md")
		} else {
			out = strings.TrimSuffix(in, filepath.Ext(in)) + ".md"
		}
		if err := convertOne(in, out, *validate, opt); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
}

func convertOne(inPDF, outMD string, validate bool, opt pdf2md.Options) error {
	doc, err := pdf2md.ExtractDocument(inPDF, opt)
	if err != nil {
		return err
	}
	if validate {
		if err := pdf2md.ValidateCounts(doc); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
	}
	md := pdf2md.ToMarkdown(doc)
	if err := os.MkdirAll(filepath.Dir(outMD), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(outMD, []byte(md), 0o644); err != nil {
		return fmt.Errorf("write %q: %w", outMD, err)
	}
	fmt.Printf("OK: %s -> %s\n", inPDF, outMD)
	return nil
}
