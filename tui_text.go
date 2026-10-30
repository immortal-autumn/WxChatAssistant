package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// chatDisplayText gives terminal layout and older VTE terminals the same cell
// boundaries. VTE 0.76 renders joined emoji as separate characters and ignores
// emoji presentation selectors; the layout library instead measures clusters.
// This is a display conversion only: stored contacts, drafts and sends keep
// their original Unicode. ANSI sequences (including hyperlink URLs) pass intact.
func chatDisplayText(text string) string {
	if !strings.ContainsAny(text, "\u200d\u2028\u2029\ufe0e\ufe0f\U0001f3fb\U0001f3fc\U0001f3fd\U0001f3fe\U0001f3ff") {
		return text
	}
	var out strings.Builder
	var state byte
	for len(text) > 0 {
		seq, _, n, next := ansi.GraphemeWidth.DecodeSequenceInString(text, state, nil)
		if n == 0 {
			break
		}
		text, state = text[n:], next
		if seq[0] == '\x1b' {
			out.WriteString(seq)
			continue
		}
		emoji := strings.ContainsAny(seq, "\ufe0e\ufe0f")
		for _, r := range seq {
			if r >= 0x1f000 && r <= 0x1faff || r >= 0x2600 && r <= 0x27bf {
				emoji = true
			}
		}
		for _, r := range seq {
			switch {
			case r == '\u2028' || r == '\u2029':
				// These separators have zero layout width but may occupy a cell
				// in a terminal. Use a physical line break before measuring.
				out.WriteByte('\n')
			case r == '\ufe0e' || r == '\ufe0f':
				// Render the base character in its terminal text presentation.
			case r == '\u200d' && emoji:
				// Keep all components of a joined emoji, without joining widths.
			case r >= 0x1f3fb && r <= 0x1f3ff:
				// Separate the modifier so terminals agree on both cell widths.
				if !strings.HasSuffix(out.String(), " ") {
					out.WriteByte(' ')
				}
				out.WriteRune(r)
			default:
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}

func chatDisplayLabel(text string) string { return chatDisplayText(oneLine(text)) }
