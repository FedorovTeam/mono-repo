package voting

import (
	"strings"
)

// ToMarkdown renders a Document to a Markdown string.
func ToMarkdown(doc Document) string {
	var b strings.Builder
	if doc.HeaderText != "" {
		b.WriteString(doc.HeaderText)
		b.WriteString("\n\n")
	}

	// Table header.
	b.WriteString("| № | Ф.И.О. |")
	for _, c := range doc.Columns {
		b.WriteString(" " + escapePipes(c.Title) + " |")
	}
	b.WriteString("\n")

	b.WriteString("|---|--------|")
	for range doc.Columns {
		b.WriteString("---|")
	}
	b.WriteString("\n")

	for _, r := range doc.Rows {
		if r.Number == 0 {
			continue
		}
		name := escapePipes(r.Name)
		b.WriteString("|")
		b.WriteString(" " + itoa(r.Number) + " |")
		b.WriteString(" " + name + " |")
		for _, c := range doc.Columns {
			if r.Marks != nil && r.Marks[c.Key] {
				b.WriteString(" ✓ |")
			} else {
				b.WriteString("  |")
			}
		}
		b.WriteString("\n")
	}

	return b.String()
}

func escapePipes(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

func itoa(n int) string {
	// faster than fmt.Sprintf for huge loops; safe for our sizes.
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [32]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
