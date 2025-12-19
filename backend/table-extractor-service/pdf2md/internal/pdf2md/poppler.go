package pdf2md

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// EnsureExternalTools checks that required external binaries are available.
// This project intentionally uses Poppler utils because they provide reliable
// PDF rendering (pdftoppm) and text bounding boxes (pdftotext -bbox).
func EnsureExternalTools() error {
	for _, bin := range []string{"pdfinfo", "pdftotext", "pdftoppm"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("required external tool %q not found in PATH: %w", bin, err)
		}
	}
	return nil
}

// PDFInfo contains a subset of `pdfinfo` fields we need.
type PDFInfo struct {
	Pages int
}

var pagesRe = regexp.MustCompile(`(?m)^Pages:\s+(\d+)\s*$`)

// ReadPDFInfo runs `pdfinfo` and parses the number of pages.
func ReadPDFInfo(pdfPath string) (PDFInfo, error) {
	cmd := exec.Command("pdfinfo", pdfPath)
	out, err := cmd.Output()
	if err != nil {
		return PDFInfo{}, fmt.Errorf("pdfinfo failed: %w", err)
	}
	m := pagesRe.FindSubmatch(out)
	if len(m) != 2 {
		return PDFInfo{}, errors.New("pdfinfo output did not contain Pages")
	}
	pages, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return PDFInfo{}, fmt.Errorf("failed to parse Pages: %w", err)
	}
	return PDFInfo{Pages: pages}, nil
}

// ExtractLayoutText runs `pdftotext -layout` and returns plain text.
func ExtractLayoutText(pdfPath string, firstPage, lastPage int) (string, error) {
	args := []string{"-layout"}
	if firstPage > 0 {
		args = append(args, "-f", strconv.Itoa(firstPage))
	}
	if lastPage > 0 {
		args = append(args, "-l", strconv.Itoa(lastPage))
	}
	args = append(args, pdfPath, "-")
	cmd := exec.Command("pdftotext", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext -layout failed: %w", err)
	}
	return string(out), nil
}

// ExtractBBoxHTML runs `pdftotext -bbox` for a single page and returns XHTML.
func ExtractBBoxHTML(pdfPath string, page int) (string, error) {
	cmd := exec.Command("pdftotext", "-bbox", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), pdfPath, "-")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext -bbox failed (page %d): %w", page, err)
	}
	return string(out), nil
}

// RenderPagesToPNG renders all pages to PNG files using `pdftoppm`.
//
// It returns the file paths (1-indexed pages correspond to slice indexes 0..n-1).
func RenderPagesToPNG(pdfPath string, outDir, prefix string, dpi int) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("create outDir: %w", err)
	}
	basePrefix := filepath.Join(outDir, prefix)
	cmd := exec.Command("pdftoppm", "-png", "-r", strconv.Itoa(dpi), pdfPath, basePrefix)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("pdftoppm failed: %s", msg)
	}
	// Collect generated files: Poppler uses suffix = page number.
	info, err := ReadPDFInfo(pdfPath)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, info.Pages)
	for p := 1; p <= info.Pages; p++ {
		paths = append(paths, fmt.Sprintf("%s-%d.png", basePrefix, p))
	}
	return paths, nil
}
