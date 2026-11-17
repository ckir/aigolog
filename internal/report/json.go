// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// WriteJSON writes r as one document, indented by two spaces, that ends with a
// newline, with one Write. Nothing is written when r cannot be encoded.
//
// HTML escaping is off: the text of a pattern holds <*>, <int> and <ip4>, which
// the default encoder would write as \u003c*\u003e. The encoder writes every
// byte below 0x20, U+2028 and U+2029 as an escape and a byte that is not UTF-8
// as U+FFFD, but it leaves DEL (0x7f) and the C1 controls (U+0080 to U+009F,
// among them the 8-bit CSI, U+009B) as they are, and the document may be
// printed to a terminal. escapeControls writes those as escapes too, so that
// no raw control character is in the document.
func WriteJSON(w io.Writer, r Report) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return err
	}
	_, err := w.Write(escapeControls(buf.Bytes()))
	return err
}

// escapeControls replaces the byte 0x7f by \u007f and each pair C2 80 to C2 9F
// (U+0080 to U+009F) by \u0080 to \u009f. The encoder writes nothing but
// ASCII outside the strings of the document (braces, brackets, commas, colons,
// spaces, numbers, true, false, null, line feeds), and the strings it writes
// are valid UTF-8, so these bytes can only be inside a string, where the
// escape means the same character; the byte C2 in valid UTF-8 only begins a
// two-byte character, so a pair is never the end of one character and the
// start of the next.
func escapeControls(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == 0x7f:
			out = append(out, `\u007f`...)
		case b[i] == 0xc2 && i+1 < len(b) && b[i+1] >= 0x80 && b[i+1] <= 0x9f:
			out = fmt.Appendf(out, `\u%04x`, int(b[i+1]))
			i++
		default:
			out = append(out, b[i])
		}
	}
	return out
}
