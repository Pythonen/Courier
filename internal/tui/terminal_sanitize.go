package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// sanitizeTerminalText makes untrusted text safe to include in terminal UI
// output. Newlines remain structural, while terminal controls and Unicode
// directionality controls are rendered visibly instead of being interpreted.
func sanitizeTerminalText(value string) string {
	if value == "" {
		return ""
	}

	var output strings.Builder
	output.Grow(len(value))
	for offset := 0; offset < len(value); {
		r, size := utf8.DecodeRuneInString(value[offset:])
		if r == utf8.RuneError && size == 1 {
			fmt.Fprintf(&output, `\x%02x`, value[offset])
			offset++
			continue
		}

		switch {
		case r == '\n':
			output.WriteByte('\n')
		case r == '\r':
			// Normalize CRLF to a single newline. A bare carriage return can
			// otherwise overwrite content already displayed on the same row.
			if offset+size >= len(value) || value[offset+size] != '\n' {
				output.WriteString(`\r`)
			}
		case r == '\t':
			output.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&output, `\x%02x`, r)
		case r >= 0x80 && r <= 0x9f:
			fmt.Fprintf(&output, `\u%04x`, r)
		case terminalDirectionControl(r):
			fmt.Fprintf(&output, `\u%04x`, r)
		default:
			output.WriteRune(r)
		}
		offset += size
	}
	return output.String()
}

func terminalDirectionControl(r rune) bool {
	switch r {
	case 0x061c, 0x200e, 0x200f, 0x2028, 0x2029,
		0x202a, 0x202b, 0x202c, 0x202d, 0x202e,
		0x2066, 0x2067, 0x2068, 0x2069:
		return true
	default:
		return false
	}
}
