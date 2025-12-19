package pdf2md

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Options controls extraction behavior.
type Options struct {
	DPI       int
	TempDir   string
	Detect    DetectParams
	KeepTemps bool
}

func DefaultOptions() Options {
	tmp := filepath.Join(os.TempDir(), "pdf2md")
	return Options{
		DPI:     200,
		TempDir: tmp,
		Detect:  DefaultDetectParams(),
	}
}

// ExtractDocument converts a single PDF file into a Markdown-ready Document.
func ExtractDocument(pdfPath string, opt Options) (Document, error) {
	if opt.DPI <= 0 {
		opt.DPI = 200
	}
	if opt.TempDir == "" {
		opt.TempDir = filepath.Join(os.TempDir(), "pdf2md")
	}
	if opt.Detect.PatchHalfSize == 0 {
		opt.Detect = DefaultDetectParams()
	}

	if err := EnsureExternalTools(); err != nil {
		return Document{}, err
	}
	info, err := ReadPDFInfo(pdfPath)
	if err != nil {
		return Document{}, err
	}

	tmpDir := filepath.Join(opt.TempDir, filepath.Base(pdfPath))
	pngPaths, err := RenderPagesToPNG(pdfPath, tmpDir, "page", opt.DPI)
	if err != nil {
		return Document{}, err
	}
	if !opt.KeepTemps {
		defer os.RemoveAll(tmpDir)
	}

	layoutText, err := ExtractLayoutText(pdfPath, 1, 1)
	if err != nil {
		return Document{}, err
	}
	headerText := ExtractHeaderText(layoutText)

	// Columns are detected from page 1.
	bboxHTML1, err := ExtractBBoxHTML(pdfPath, 1)
	if err != nil {
		return Document{}, err
	}
	page1, err := ParseBBoxHTML(bboxHTML1)
	if err != nil {
		return Document{}, err
	}
	cols, nameXMaxPt, tableHeaderYPt, err := ExtractColumns(page1)
	if err != nil {
		return Document{}, err
	}

	rows := make(map[int]*Row, 128)

	for p := 1; p <= info.Pages; p++ {
		bboxHTML, err := ExtractBBoxHTML(pdfPath, p)
		if err != nil {
			return Document{}, err
		}
		pageBBox, err := ParseBBoxHTML(bboxHTML)
		if err != nil {
			return Document{}, err
		}
		imgPath := pngPaths[p-1]
		gray, err := LoadGrayPNG(imgPath)
		if err != nil {
			return Document{}, fmt.Errorf("load rendered page %d: %w", p, err)
		}

		xScale := float64(gray.Bounds().Dx()) / pageBBox.WidthPt
		yScale := float64(gray.Bounds().Dy()) / pageBBox.HeightPt

		minYPt := 0.0
		if p == 1 {
			minYPt = tableHeaderYPt
		} else {
			// Only apply the "skip top text" threshold on pages that actually contain
			// the table header. Some protocols split the table across pages and the
			// continuation page may start right away with row 70.
			if y, ok := findTableHeaderY(pageBBox.Words); ok {
				minYPt = y
			}
		}
		rowNumWords := ExtractRowNumberWords(pageBBox, minYPt)
		for _, rn := range rowNumWords {
			n := rn.Number
			row := rows[n]
			if row == nil {
				row = &Row{Number: n, Marks: make(map[ColumnKey]bool, len(cols))}
				rows[n] = row
			}

			if row.Name == "" {
				row.Name = ExtractNameForRow(rn.Word, pageBBox.Words, nameXMaxPt)
			}

			yCenterPt := (rn.Word.YMin + rn.Word.YMax) / 2
			yPx := int((yCenterPt * yScale) + 0.5)

			marks := DetectMarks(gray, yPx, cols, xScale, yScale, opt.Detect)
			for k, v := range marks {
				row.Marks[k] = v
			}
		}
	}

	// Convert map -> sorted slice.
	rowList := make([]Row, 0, len(rows))
	for _, r := range rows {
		rowList = append(rowList, *r)
	}
	sort.Slice(rowList, func(i, j int) bool { return rowList[i].Number < rowList[j].Number })

	return Document{HeaderText: headerText, Columns: cols, Rows: rowList}, nil
}
