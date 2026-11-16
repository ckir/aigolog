// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package textsafe

import "testing"

func TestCleanLeavesPlainTextAlone(t *testing.T) {
	for _, s := range []string{"", "auth.log", "/var/log/syslog", "κόσμος ✓", `C:\logs\a b.log`} {
		if got := Clean(s); got != s {
			t.Errorf("Clean(%q) = %q, want it unchanged", s, got)
		}
	}
}

func TestCleanEscapesWhatActsOnATerminal(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\x1b[31mb", `a\x1b[31mb`},
		{"bell\x07", `bell\x07`},
		{"line\nbreak", `line\x0abreak`},
		{"tab\there", `tab\x09here`},
		{"nul\x00", `nul\x00`},
		{"del\x7f", `del\x7f`},
		{"c1\u009b", `c1\x9b`},
		{"sep\u2028", `sep\u2028`},
		{"para\u2029", `para\u2029`},
		{"bidi\u202etxt", `bidi\u202etxt`},
		{"iso\u2066x\u2069", `iso\u2066x\u2069`},
		{"mark\u200e", `mark\u200e`},
		{"bad\xffbyte", `bad\xffbyte`},
	}
	for _, c := range cases {
		if got := Clean(c.in); got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCleanNeverReturnsAControlCharacter(t *testing.T) {
	for r := rune(0); r <= 0x9f; r++ {
		got := Clean("x" + string(r) + "y")
		for _, c := range got {
			if c < 0x20 || (c >= 0x7f && c <= 0x9f) {
				t.Errorf("Clean of U+%04X still holds U+%04X: %q", r, c, got)
			}
		}
	}
}
