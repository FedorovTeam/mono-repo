package charsetautodetect

import (
	"errors"
	"strings"
	"unicode"

	"github.com/saintfish/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

var ErrUnknownEncoding = errors.New("unknown or unsupported charset")

func DetectEncoding(data []byte) (encoding.Encoding, error) {
	if enc := detectWithChardet(data); enc != nil {
		return enc, nil
	}

	candidates := []encoding.Encoding{
		charmap.Windows1251,
		charmap.KOI8R,
	}

	bestScore := -1
	var best encoding.Encoding

	for _, candidate := range candidates {
		decoded, err := candidate.NewDecoder().Bytes(data)
		if err != nil {
			continue
		}

		score := scoreText(decoded)
		if score > bestScore {
			bestScore = score
			best = candidate
		}
	}

	if best == nil || bestScore < 0 {
		return nil, ErrUnknownEncoding
	}

	return best, nil
}

func detectWithChardet(data []byte) encoding.Encoding {
	detector := chardet.NewTextDetector()
	if detector == nil {
		return nil
	}

	result, err := detector.DetectBest(data)
	if err != nil {
		return nil
	}

	return encodingByName(result.Charset)
}

func encodingByName(name string) encoding.Encoding {
	switch strings.ToLower(name) {
	case "windows-1251", "cp1251", "1251", "windows1251":
		return charmap.Windows1251
	case "koi8-r", "koi8r":
		return charmap.KOI8R
	default:
		return nil
	}
}

func scoreText(data []byte) int {
	score := 0
	for _, r := range string(data) {
		switch {
		case unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t':
			score -= 2
		case unicode.In(r, unicode.Cyrillic):
			score += 2
		case r == '?':
			score--
		default:
			score++
		}
	}
	return score
}
