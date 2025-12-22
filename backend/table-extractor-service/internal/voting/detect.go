package voting

import (
	"image"
	"math"
)

// DetectParams controls how checkmarks are detected in the rendered page image.
type DetectParams struct {
	PatchHalfSize       int   // half-size of the square patch, in pixels
	ScanRangeX          int   // horizontal search range around the estimated column center
	ScanRangeY          int   // vertical search range around the estimated row baseline
	BrightnessThreshold uint8 // pixels below this value are considered "ink"
	MarkThreshold       int   // minimal ink score to treat as a checkmark
}

func DefaultDetectParams() DetectParams {
	return DetectParams{
		PatchHalfSize:       9,
		ScanRangeX:          30,
		ScanRangeY:          6,
		BrightnessThreshold: 160,
		MarkThreshold:       25,
	}
}

// DetectMarks scans the image at a given row Y (in pixels) and returns which columns contain a checkmark.
//
// We scan in a small X/Y window because PDF renderers place marks slightly off-center
// and the first/last rows can have small baseline shifts.
func DetectMarks(gray *image.Gray, yPx int, columns []Column, xScale, yScale float64, params DetectParams) map[ColumnKey]bool {
	res := make(map[ColumnKey]bool, len(columns))
	for _, col := range columns {
		xCenterPx := int(math.Round(col.XCenterPt * xScale))
		score := bestInkScore(gray, xCenterPx, yPx, params)
		res[col.Key] = score >= params.MarkThreshold
	}
	return res
}

func bestInkScore(gray *image.Gray, xPx, yPx int, params DetectParams) int {
	best := 0
	for dy := -params.ScanRangeY; dy <= params.ScanRangeY; dy++ {
		for dx := -params.ScanRangeX; dx <= params.ScanRangeX; dx++ {
			s := inkScore(gray, xPx+dx, yPx+dy, params.PatchHalfSize, params.BrightnessThreshold)
			if s > best {
				best = s
			}
		}
	}
	return best
}

func inkScore(gray *image.Gray, xPx, yPx int, half int, thr uint8) int {
	b := gray.Bounds()
	left := xPx - half
	right := xPx + half
	top := yPx - half
	bottom := yPx + half
	if left < b.Min.X {
		left = b.Min.X
	}
	if right > b.Max.X {
		right = b.Max.X
	}
	if top < b.Min.Y {
		top = b.Min.Y
	}
	if bottom > b.Max.Y {
		bottom = b.Max.Y
	}
	if left >= right || top >= bottom {
		return 0
	}

	cnt := 0
	stride := gray.Stride
	pix := gray.Pix
	minX := b.Min.X
	minY := b.Min.Y
	for y := top; y < bottom; y++ {
		rowOff := (y-minY)*stride + (left - minX)
		for x := left; x < right; x++ {
			if pix[rowOff] < thr {
				cnt++
			}
			rowOff++
		}
	}
	return cnt
}
