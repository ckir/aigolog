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
// among them the 8-bit CSI, U+009B) as they are, and so the direction controls
// (U+061C, U+200E, U+200F, U+202A to U+202E, U+2066 to U+2069), and the
// document may be printed to a terminal. escapeControls writes those as
// escapes too, so that no raw control character is in the document.
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

// escapeControls replaces the byte 0x7f by \u007f, each pair C2 80 to C2 9F
// (U+0080 to U+009F) by \u0080 to \u009f, and the direction controls that
// textsafe.Clean escapes for the text report by their \uXXXX escapes: U+061C
// (D8 9C), U+200E and U+200F (E2 80 8E, E2 80 8F), U+202A to U+202E (E2 80 AA
// to E2 80 AE) and U+2066 to U+2069 (E2 81 A6 to E2 81 A9). The encoder writes
// nothing but ASCII outside the strings of the document (braces, brackets,
// commas, colons, spaces, numbers, true, false, null, line feeds), and the
// strings it writes are valid UTF-8, so these bytes can only be inside a
// string, where the escape means the same character; the bytes C2, D8 and E2
// in valid UTF-8 only begin a character (a continuation byte is 80 to BF), so
// a sequence is never the end of one character and the start of the next. A
// test keeps this set equal to that of textsafe.
func escapeControls(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == 0x7f:
			out = append(out, `\u007f`...)
		case b[i] == 0xc2 && i+1 < len(b) && b[i+1] >= 0x80 && b[i+1] <= 0x9f:
			out = fmt.Appendf(out, `\u%04x`, int(b[i+1]))
			i++
		case b[i] == 0xd8 && i+1 < len(b) && b[i+1] == 0x9c:
			out = append(out, `\u061c`...)
			i++
		case b[i] == 0xe2 && i+2 < len(b) && b[i+1] == 0x80 && (b[i+2] == 0x8e || b[i+2] == 0x8f || (b[i+2] >= 0xaa && b[i+2] <= 0xae)):
			out = fmt.Appendf(out, `\u%04x`, 0x2000|int(b[i+2]&0x3f))
			i += 2
		case b[i] == 0xe2 && i+2 < len(b) && b[i+1] == 0x81 && b[i+2] >= 0xa6 && b[i+2] <= 0xa9:
			out = fmt.Appendf(out, `\u%04x`, 0x2040|int(b[i+2]&0x3f))
			i += 2
		default:
			out = append(out, b[i])
		}
	}
	return out
}
