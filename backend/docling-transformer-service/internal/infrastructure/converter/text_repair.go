package converter

import "strings"

const (
	// Docling (или его PDF-extractor) иногда возвращает кириллицу,
	// "сдвинутую" в диапазон Latin Extended-B / IPA Extensions.
	// Пример: 'Ɋ' (U+024A) вместо 'Р' (U+0420).
	// Смещение, которое возвращает кириллицу обратно:
	cyrillicShiftDelta = 0x1D6

	shiftedRangeStart = 0x022A
	shiftedRangeEnd   = 0x02FF

	cyrillicRangeStart = 0x0400
	cyrillicRangeEnd   = 0x04FF
)

// repairShiftedCyrillic чинит "сдвинутую кириллицу" в тексте Docling.
// Возвращает (исправленный текст, был_ли_ремонт).
func repairShiftedCyrillic(input string) (string, bool) {
	if input == "" {
		return input, false
	}

	// Эвристика, чтобы не ломать редкие легитимные IPA/LatinExt символы:
	// если "подозрительных" символов мало — не трогаем.
	// Смотрим первые N рун.
	const sampleLimit = 8192
	const minSuspicious = 8

	suspicious := 0
	scanned := 0
	for _, r := range input {
		if isShiftedCyrillicRune(r) {
			suspicious++
			if suspicious >= minSuspicious {
				break
			}
		}
		scanned++
		if scanned >= sampleLimit {
			break
		}
	}

	if suspicious < minSuspicious {
		return input, false
	}

	var b strings.Builder
	b.Grow(len(input))

	changed := false
	for _, r := range input {
		if isShiftedCyrillicRune(r) {
			b.WriteRune(rune(int(r) + cyrillicShiftDelta))
			changed = true
			continue
		}
		b.WriteRune(r)
	}

	if !changed {
		return input, false
	}
	return b.String(), true
}

func isShiftedCyrillicRune(r rune) bool {
	cp := int(r)
	if cp < shiftedRangeStart || cp > shiftedRangeEnd {
		return false
	}
	mapped := cp + cyrillicShiftDelta
	return mapped >= cyrillicRangeStart && mapped <= cyrillicRangeEnd
}
