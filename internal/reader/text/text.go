// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package text reads a text log: a file of lines, some of which go on from the
// one before. It reads UTF-8 and UTF-16 (with a byte-order mark), and it
// refuses input that holds NUL characters.
package text

import (
	"bufio"
	"bytes"
	"fmt"
	"io"

	"github.com/ckir/aigolog/internal/record"
	"github.com/ckir/aigolog/internal/timeparse"
)

const (
	// SampleBytes is how many bytes at the start of the text are looked at to
	// find the layout of the stamps and NUL characters.
	SampleBytes = 64 << 10
)

// MaxRecordBytes is the most text that a record being assembled may hold: a
// line that goes on from the record before is not joined to it when the record
// would then be longer than this, and it starts a new record instead (see
// Reader.Cut). A line that is longer than this by itself is never split: it is
// kept whole, as a record of its own.
const MaxRecordBytes = 1 << 20

// parseSpan is how many bytes of a line are given to Layout.Parse, which looks
// at the first bytes of a line only.
const parseSpan = 256

// NotTextError says that the input holds a NUL, so that it is not text.
type NotTextError struct {
	// Offset is the position of the NUL. See Reader for what it counts.
	Offset int64
}

// Error implements error.
func (e *NotTextError) Error() string {
	return fmt.Sprintf("input is not text: it holds a NUL byte at byte offset %d", e.Offset)
}

// Reader reads the records of a text log. It implements record.Reader.
//
// Positions: for UTF-8 (with or without a mark) the Offset and Length of a
// record count the bytes of the original input, and the mark counts. For
// UTF-16 they count the bytes of the decoded UTF-8 text, and the mark does not
// count, so that the first line is at offset 0. For every encoding Bytes
// counts the bytes of the original input that have been read, the mark
// included, and Length counts the original bytes of a line whose invalid UTF-8
// is replaced.
//
// A blank line (empty, or of white space only) that comes when no record is
// being assembled, such as at the start of the input, starts no record and is
// skipped; it is counted in Lines and in the positions of what follows. A
// record never holds more than MaxRecordBytes of text unless its first line is
// longer.
type Reader struct {
	text     *bufio.Reader // the text, decoded if need be
	encoding string
	layout   timeparse.Layout
	found    bool

	pos   int64 // position, as in the comment above, of the next byte of text
	orig  int64 // bytes of the original input read so far
	lines int64
	utf16 bool
	mark  int // length of the byte-order mark
	dec   *utf16Reader

	lineBuf []byte // the line being read: the pending line

	// The pending record: the one being assembled.
	have   bool
	recBuf []byte
	recLn  int64
	recOff int64
	recEnd int64
	stamp  record.Stamp

	maxRecord int   // the cap on the text of a record; see MaxRecordBytes
	cut       int64 // the records started because of the cap

	err error // what ends the reading; returned after the pending record
}

// New returns a Reader of the log that r holds. It looks at the first
// SampleBytes bytes, and returns a *NotTextError if there is a NUL in them,
// in an input that is not UTF-16.
func New(r io.Reader) (*Reader, error) {
	return newReader(r, MaxRecordBytes)
}

// newReader is New with the cap on a record given; the tests use a small one.
func newReader(r io.Reader, maxRecord int) (*Reader, error) {
	br := bufio.NewReaderSize(r, SampleBytes)
	head, err := br.Peek(3)
	if err != nil && err != io.EOF {
		return nil, err
	}
	rd := &Reader{encoding: "utf-8", maxRecord: maxRecord}
	var bigEndian bool
	var mark int
	switch {
	case bytes.HasPrefix(head, []byte{0xff, 0xfe}):
		rd.utf16, mark = true, 2
		rd.encoding = "utf-16le"
	case bytes.HasPrefix(head, []byte{0xfe, 0xff}):
		rd.utf16, bigEndian, mark = true, true, 2
		rd.encoding = "utf-16be"
	case bytes.HasPrefix(head, []byte{0xef, 0xbb, 0xbf}):
		mark = 3
	}
	if _, err := br.Discard(mark); err != nil {
		return nil, err
	}
	rd.orig, rd.mark = int64(mark), mark

	if rd.utf16 {
		rd.dec = newUTF16Reader(br, bigEndian)
		rd.text = bufio.NewReaderSize(rd.dec, SampleBytes)
	} else {
		rd.pos = int64(mark)
		rd.text = br
	}
	sample, err := rd.text.Peek(SampleBytes)
	ended := err == io.EOF
	if err != nil && !ended {
		return nil, err
	}
	if !rd.utf16 {
		// UTF-16 text legitimately holds zero bytes, so only here are the
		// original bytes of the sample looked at.
		if i := bytes.IndexByte(sample, 0); i >= 0 {
			return nil, &NotTextError{Offset: rd.pos + int64(i)}
		}
	}
	rd.layout, rd.found = timeparse.Detect(sampleLines(sample, ended))
	return rd, nil
}

// sampleLines returns the lines of the sample, cleaned as the lines of records
// are. A last line that the end of the sample cuts is left out, unless the
// input ended inside the sample.
func sampleLines(sample []byte, ended bool) []string {
	var lines []string
	for len(sample) > 0 {
		i := bytes.IndexByte(sample, '\n')
		var line []byte
		if i < 0 {
			if !ended {
				break
			}
			line, sample = sample, nil
		} else {
			line, sample = sample[:i], sample[i+1:]
		}
		lines = append(lines, string(clean(trimCR(line))))
	}
	return lines
}

// trimCR removes the carriage return that ends a line, if there is one.
func trimCR(line []byte) []byte {
	if n := len(line); n > 0 && line[n-1] == '\r' {
		return line[:n-1]
	}
	return line
}

// clean replaces invalid UTF-8 with U+FFFD.
func clean(line []byte) []byte {
	return bytes.ToValidUTF8(line, []byte("�"))
}

// Layout returns the layout of the stamps of the log, and false when the log
// has none.
func (r *Reader) Layout() (timeparse.Layout, bool) { return r.layout, r.found }

// Encoding returns utf-8, utf-16le or utf-16be.
func (r *Reader) Encoding() string { return r.encoding }

// Lines returns the number of lines read so far.
func (r *Reader) Lines() int64 { return r.lines }

// Bytes returns the number of bytes of the original input read so far.
func (r *Reader) Bytes() int64 { return r.orig }

// Cut returns the number of records that were started because the record
// before them would have grown past MaxRecordBytes: the line that would have
// been joined to it has no stamp of its own, so the record it starts has none
// either.
func (r *Reader) Cut() int64 { return r.cut }

// Next returns the next record, and io.EOF after the last one. A record that
// was assembled before a *NotTextError, or another error, is returned first.
func (r *Reader) Next() (record.Record, error) {
	for {
		if r.err != nil {
			if r.have {
				return r.take(), nil
			}
			return record.Record{}, r.err
		}
		start, err := r.readLine()
		if err != nil {
			r.err = err
			continue
		}
		raw := trimCR(r.lineBuf)
		line := clean(raw)
		end := start + int64(len(raw))
		if !r.have && len(bytes.TrimSpace(line)) == 0 {
			continue // a blank line starts no record
		}
		stamp, parsed := r.parse(line)
		if r.have && !r.startsRecord(line, parsed) {
			if len(r.recBuf) == 0 || len(r.recBuf)+1+len(line) <= r.maxRecord {
				r.recBuf = append(r.recBuf, '\n')
				r.recBuf = append(r.recBuf, line...)
				r.recEnd = end
				continue
			}
			// The record would grow past the cap: it ends here, and this
			// line, which has no stamp, starts the next one.
			stamp = record.Stamp{}
			r.cut++
		}
		var out record.Record
		had := r.have
		if had {
			out = r.take()
		}
		r.have = true
		r.recBuf = append(r.recBuf[:0], line...)
		r.recLn, r.recOff, r.recEnd = r.lines, start, end
		r.stamp = stamp
		if had {
			return out, nil
		}
	}
}

// parse returns the stamp of a line when it begins with one in the layout.
func (r *Reader) parse(line []byte) (record.Stamp, bool) {
	if !r.found {
		return record.Stamp{}, false
	}
	head := line
	if len(head) > parseSpan {
		head = head[:parseSpan]
	}
	return r.layout.Parse(string(head))
}

// startsRecord says whether a line starts a record rather than going on from
// the one before. parsed says whether it begins with a stamp in the layout.
func (r *Reader) startsRecord(line []byte, parsed bool) bool {
	if len(line) == 0 || line[0] == ' ' || line[0] == '\t' || len(bytes.TrimSpace(line)) == 0 {
		return false
	}
	return parsed || !r.found
}

// take hands out the pending record.
func (r *Reader) take() record.Record {
	rec := record.Record{
		Line:   r.recLn,
		Offset: r.recOff,
		Length: r.recEnd - r.recOff,
		Stamp:  r.stamp,
		Raw:    string(r.recBuf),
	}
	r.have = false
	return rec
}

// readLine reads the next line into lineBuf, with its terminator, and returns
// its position. It returns io.EOF when there is no further line, and a
// *NotTextError when the line holds a NUL.
func (r *Reader) readLine() (int64, error) {
	r.lineBuf = r.lineBuf[:0]
	for {
		piece, err := r.text.ReadSlice('\n')
		if i := bytes.IndexByte(piece, 0); i >= 0 {
			return 0, &NotTextError{Offset: r.pos + int64(len(r.lineBuf)+i)}
		}
		r.lineBuf = append(r.lineBuf, piece...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF {
			if len(r.lineBuf) == 0 {
				if r.utf16 {
					// Counting by characters over-counts a last byte
					// that is half a code unit; the reader knows the
					// real total.
					r.orig = int64(r.mark) + r.dec.consumed
				}
				return 0, io.EOF
			}
		} else if err != nil {
			return 0, err
		}
		break
	}
	start := r.pos
	r.pos += int64(len(r.lineBuf))
	if r.utf16 {
		r.orig += originalLen(r.lineBuf)
	} else {
		r.orig = r.pos
	}
	r.lines++
	if n := len(r.lineBuf); r.lineBuf[n-1] == '\n' {
		r.lineBuf = r.lineBuf[:n-1]
	}
	return start, nil
}

var _ record.Reader = (*Reader)(nil)
