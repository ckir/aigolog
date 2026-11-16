// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package text

import (
	"io"
	"unicode/utf16"
	"unicode/utf8"
)

// chunkSize is how many bytes the utf16Reader takes from its source at a time.
const chunkSize = 4096

// utf16Reader turns UTF-16 text into UTF-8 text. It reads from the buffered
// reader that holds the input, after the byte-order mark has been discarded
// from it. A lone surrogate becomes U+FFFD, and so does a last byte that is
// not a whole code unit.
type utf16Reader struct {
	src       io.Reader
	bigEndian bool
	consumed  int64 // bytes taken from src so far

	raw   [chunkSize]byte
	carry int  // bytes of an unfinished code unit, held at the start of raw
	high  rune // a high surrogate waiting for its low one, or 0

	out []byte // decoded text not yet handed out
	off int    // how much of out has been handed out
	err error  // what src ended with, once the text in out is handed out
}

func newUTF16Reader(src io.Reader, bigEndian bool) *utf16Reader {
	return &utf16Reader{src: src, bigEndian: bigEndian}
}

// Read implements io.Reader.
func (d *utf16Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for d.off == len(d.out) {
		if d.err != nil {
			return 0, d.err
		}
		d.fill()
	}
	n := copy(p, d.out[d.off:])
	d.off += n
	return n, nil
}

// fill takes one chunk from the source and decodes it into out. It is called
// only when all of out has been handed out.
func (d *utf16Reader) fill() {
	d.out = d.out[:0]
	d.off = 0
	n, err := d.src.Read(d.raw[d.carry:])
	d.consumed += int64(n)
	have := d.carry + n
	i := 0
	for ; i+2 <= have; i += 2 {
		var u uint16
		if d.bigEndian {
			u = uint16(d.raw[i])<<8 | uint16(d.raw[i+1])
		} else {
			u = uint16(d.raw[i+1])<<8 | uint16(d.raw[i])
		}
		d.unit(rune(u))
	}
	d.carry = copy(d.raw[:], d.raw[i:have])
	if err == nil {
		return
	}
	if err == io.EOF {
		// The stream ends: whatever is unfinished becomes a replacement
		// character each.
		if d.high != 0 {
			d.out = utf8.AppendRune(d.out, utf8.RuneError)
			d.high = 0
		}
		if d.carry != 0 {
			d.out = utf8.AppendRune(d.out, utf8.RuneError)
			d.carry = 0
		}
	}
	d.err = err
}

// unit decodes one code unit into out.
func (d *utf16Reader) unit(u rune) {
	if d.high != 0 {
		if utf16.IsSurrogate(u) && u >= 0xdc00 {
			d.out = utf8.AppendRune(d.out, utf16.DecodeRune(d.high, u))
			d.high = 0
			return
		}
		d.out = utf8.AppendRune(d.out, utf8.RuneError) // the high one was alone
		d.high = 0
	}
	switch {
	case u >= 0xd800 && u < 0xdc00:
		d.high = u
	case u >= 0xdc00 && u < 0xe000:
		d.out = utf8.AppendRune(d.out, utf8.RuneError) // a low one on its own
	default:
		d.out = utf8.AppendRune(d.out, u)
	}
}

// originalLen returns how many bytes of UTF-16 text hold the UTF-8 text s,
// which the utf16Reader produced: two for each character, four for a
// character beyond the first plane.
func originalLen(s []byte) int64 {
	var n int64
	for _, b := range s {
		switch {
		case b&0xc0 == 0x80: // the rest of a character
		case b >= 0xf0:
			n += 4
		default:
			n += 2
		}
	}
	return n
}
