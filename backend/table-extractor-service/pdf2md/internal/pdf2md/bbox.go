package pdf2md

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Word is a text fragment extracted by pdftotext -bbox.
type Word struct {
	Text       string
	XMin, YMin float64
	XMax, YMax float64
}

// PageBBox represents one PDF page with its words.
type PageBBox struct {
	WidthPt  float64
	HeightPt float64
	Words    []Word
}

var (
	pageRe = regexp.MustCompile(`<page[^>]*width="([0-9.]+)"[^>]*height="([0-9.]+)"`)
	wordRe = regexp.MustCompile(`<word xMin="([0-9.]+)" yMin="([0-9.]+)" xMax="([0-9.]+)" yMax="([0-9.]+)">([^<]*)</word>`)
)

// ParseBBoxHTML parses the XHTML produced by `pdftotext -bbox`.
func ParseBBoxHTML(xhtml string) (PageBBox, error) {
	pm := pageRe.FindStringSubmatch(xhtml)
	if len(pm) != 3 {
		return PageBBox{}, fmt.Errorf("failed to parse <page> width/height")
	}
	wpt, err := strconv.ParseFloat(pm[1], 64)
	if err != nil {
		return PageBBox{}, fmt.Errorf("parse page width: %w", err)
	}
	hpt, err := strconv.ParseFloat(pm[2], 64)
	if err != nil {
		return PageBBox{}, fmt.Errorf("parse page height: %w", err)
	}

	words := make([]Word, 0, 2048)
	for _, m := range wordRe.FindAllStringSubmatch(xhtml, -1) {
		if len(m) != 6 {
			continue
		}
		xmin, _ := strconv.ParseFloat(m[1], 64)
		ymin, _ := strconv.ParseFloat(m[2], 64)
		xmax, _ := strconv.ParseFloat(m[3], 64)
		ymax, _ := strconv.ParseFloat(m[4], 64)
		text := html.UnescapeString(m[5])
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		words = append(words, Word{Text: text, XMin: xmin, YMin: ymin, XMax: xmax, YMax: ymax})
	}

	return PageBBox{WidthPt: wpt, HeightPt: hpt, Words: words}, nil
}
