package voting

import (
	"regexp"
	"strings"
)

var multiSpaceRe = regexp.MustCompile(`\s{2,}`)

// ExtractHeaderText returns the text block above the deputy table.
// It uses pdftotext -layout output and stops at the first table header line.
func ExtractHeaderText(layoutText string) string {
	lines := strings.Split(layoutText, "\n")
	var out []string
	emptyStreak := 0
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		// Stop at the start of the table header.
		if strings.Contains(line, "Ф.И.О") && strings.Contains(line, "Присутствует") {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			emptyStreak++
			if emptyStreak <= 1 {
				out = append(out, "")
			}
			continue
		}
		emptyStreak = 0
		line = multiSpaceRe.ReplaceAllString(line, " ")
		out = append(out, line)
	}

	// Trim leading / trailing empty lines.
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
