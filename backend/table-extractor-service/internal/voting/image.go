package voting

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
)

// LoadGrayPNG loads a PNG file and converts it into *image.Gray for fast scanning.
func LoadGrayPNG(path string) (*image.Gray, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open PNG %q: %w", path, err)
	}
	defer f.Close()

	src, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode PNG %q: %w", path, err)
	}
	b := src.Bounds()
	g := image.NewGray(b)
	draw.Draw(g, b, src, b.Min, draw.Src)
	return g, nil
}
