// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package textsafe makes text taken from outside harmless to print. A file
// name, a log line or a command-line argument may hold bytes that act on a
// terminal (escape sequences, line breaks, direction overrides); Clean writes
// each of them out as a visible escape instead.
package textsafe

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Clean returns s with every character that can act on a terminal replaced by
// a visible escape. A rune below U+0020, U+007F, or U+0080 to U+009F becomes
// \x and two lowercase hex digits; each byte that is not valid UTF-8 becomes
// \x and its two lowercase hex digits; U+2028, U+2029, U+200E, U+200F, U+061C,
// U+202A to U+202E and U+2066 to U+2069 become \u and four lowercase hex
// digits. Everything else, the backslash included, is kept as it is.
func Clean(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r == 0x2028, r == 0x2029, r == 0x200e, r == 0x200f, r == 0x061c,
			r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}
