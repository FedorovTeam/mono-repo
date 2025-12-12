package usecase

import (
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

func TestConvertToUTF8_DetectsSingleByteEncodings(t *testing.T) {
	original := "Привет, мир!"
	cases := []struct {
		name    string
		encoder *charmap.Charmap
	}{
		{name: "cp1251", encoder: charmap.Windows1251},
		{name: "koi8r", encoder: charmap.KOI8R},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := tc.encoder.NewEncoder().Bytes([]byte(original))
			if err != nil {
				t.Fatalf("encode %s: %v", tc.name, err)
			}

			converted, err := convertToUTF8(encoded)
			if err != nil {
				t.Fatalf("convert %s: %v", tc.name, err)
			}

			if !utf8.ValidString(converted) {
				t.Fatalf("converted string from %s is not valid utf-8", tc.name)
			}
			if converted != original {
				t.Fatalf("expected %q from %s, got %q", original, tc.name, converted)
			}
		})
	}
}
