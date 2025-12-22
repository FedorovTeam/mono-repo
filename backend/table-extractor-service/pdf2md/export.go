package pdf2md

import (
	"image"

	inner "pdf2md/internal/pdf2md"
)

type (
	Word          = inner.Word
	PageBBox      = inner.PageBBox
	DetectParams  = inner.DetectParams
	Column        = inner.Column
	ColumnKey     = inner.ColumnKey
	Row           = inner.Row
	Document      = inner.Document
	RowNumberWord = inner.RowNumberWord
)

const (
	ColPresent  = inner.ColPresent
	ColZa       = inner.ColZa
	ColProtiv   = inner.ColProtiv
	ColVozderzh = inner.ColVozderzh
	ColNoVote   = inner.ColNoVote
)

func DefaultDetectParams() DetectParams {
	return inner.DefaultDetectParams()
}

func ParseBBoxHTML(xhtml string) (PageBBox, error) {
	return inner.ParseBBoxHTML(xhtml)
}

func ExtractColumns(page PageBBox) ([]Column, float64, float64, error) {
	return inner.ExtractColumns(page)
}

func ExtractRowNumberWords(page PageBBox, minYPt float64) []RowNumberWord {
	return inner.ExtractRowNumberWords(page, minYPt)
}

func ExtractNameForRow(rowNumWord Word, words []Word, nameXMaxPt float64) string {
	return inner.ExtractNameForRow(rowNumWord, words, nameXMaxPt)
}

func DetectMarks(gray *image.Gray, yPx int, columns []Column, xScale, yScale float64, params DetectParams) map[ColumnKey]bool {
	return inner.DetectMarks(gray, yPx, columns, xScale, yScale, params)
}

func ExtractHeaderText(layoutText string) string {
	return inner.ExtractHeaderText(layoutText)
}

func FindTableHeaderY(words []Word) (float64, bool) {
	return inner.FindTableHeaderY(words)
}

func ToMarkdown(doc Document) string {
	return inner.ToMarkdown(doc)
}
