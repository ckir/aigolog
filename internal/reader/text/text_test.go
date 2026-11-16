// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package text

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/ckir/aigolog/internal/record"
)

func readAll(t *testing.T, input []byte) (*Reader, []record.Record) {
	t.Helper()
	r, err := New(bytes.NewReader(input))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, drain(t, r)
}

func drain(t *testing.T, r *Reader) []record.Record {
	t.Helper()
	var out []record.Record
	for {
		rec, err := r.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, rec)
	}
}

func raws(recs []record.Record) []string {
	var s []string
	for _, r := range recs {
		s = append(s, r.Raw)
	}
	return s
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPositionsOfPlainLines(t *testing.T) {
	_, recs := readAll(t, []byte("alpha\nbe\r\ngamma"))
	want := []record.Record{
		{Line: 1, Offset: 0, Length: 5, Raw: "alpha"},
		{Line: 2, Offset: 6, Length: 2, Raw: "be"},
		{Line: 3, Offset: 10, Length: 5, Raw: "gamma"},
	}
	if len(recs) != len(want) {
		t.Fatalf("got %d records, want %d: %+v", len(recs), len(want), recs)
	}
	for i := range want {
		if recs[i] != want[i] {
			t.Errorf("record %d = %+v, want %+v", i+1, recs[i], want[i])
		}
	}
}

func TestTheLastRecordSurvivesAMissingFinalNewlineAndAFinalNewlineAddsNone(t *testing.T) {
	_, a := readAll(t, []byte("one\ntwo"))
	_, b := readAll(t, []byte("one\ntwo\n"))
	if !sameStrings(raws(a), []string{"one", "two"}) || !sameStrings(raws(b), []string{"one", "two"}) {
		t.Errorf("without final newline %v, with it %v", raws(a), raws(b))
	}
}

func TestLinesStartingWithWhiteSpaceJoinTheRecordBefore(t *testing.T) {
	r, recs := readAll(t, []byte("boom\n\tat a.go:1\n    at b.go:2\nnext\n"))
	if want := []string{"boom\n\tat a.go:1\n    at b.go:2", "next"}; !sameStrings(raws(recs), want) {
		t.Fatalf("got %q, want %q", raws(recs), want)
	}
	if recs[0].Line != 1 || recs[0].Length != int64(len("boom\n\tat a.go:1\n    at b.go:2")) || recs[1].Line != 4 {
		t.Errorf("positions: %+v", recs)
	}
	if r.Lines() != 4 {
		t.Errorf("Lines() = %d, want 4", r.Lines())
	}
}

func TestBlankLinesJoinTheRecordBeforeAndAFirstContinuationStartsOne(t *testing.T) {
	_, recs := readAll(t, []byte("a\n\nb\n"))
	if want := []string{"a\n", "b"}; !sameStrings(raws(recs), want) {
		t.Errorf("got %q, want %q", raws(recs), want)
	}
	_, recs = readAll(t, []byte("  indented first\nsecond\n"))
	if want := []string{"  indented first", "second"}; !sameStrings(raws(recs), want) {
		t.Errorf("got %q, want %q", raws(recs), want)
	}
}

func TestInAFileWithStampsALineWithoutOneJoinsTheRecordBefore(t *testing.T) {
	in := "2026-01-05 10:00:01 start\ncontinued without a stamp\n2026-01-05 10:00:02 next\n"
	r, recs := readAll(t, []byte(in))
	if l, ok := r.Layout(); !ok || l.Name != "iso-space" {
		t.Fatalf("layout = %q, %v", l.Name, ok)
	}
	if want := []string{"2026-01-05 10:00:01 start\ncontinued without a stamp", "2026-01-05 10:00:02 next"}; !sameStrings(raws(recs), want) {
		t.Fatalf("got %q, want %q", raws(recs), want)
	}
	if recs[0].Stamp.State != record.Resolved || recs[1].Stamp.State != record.Resolved {
		t.Errorf("stamps: %+v %+v", recs[0].Stamp, recs[1].Stamp)
	}
}

func TestAFileWithNoStampsHasNoLayoutAndNoStamps(t *testing.T) {
	r, recs := readAll(t, []byte("plain one\nplain two\n"))
	if _, ok := r.Layout(); ok {
		t.Error("a layout was found")
	}
	if recs[0].Stamp.State != record.NoStamp {
		t.Errorf("stamp = %+v", recs[0].Stamp)
	}
}

func TestOneVeryLongLineIsKeptWhole(t *testing.T) {
	long := strings.Repeat("x", 5<<20)
	_, recs := readAll(t, []byte("short\n"+long))
	if len(recs) != 2 || recs[1].Raw != long || recs[1].Length != int64(len(long)) {
		t.Fatalf("got %d records; second holds %d bytes", len(recs), len(recs[len(recs)-1].Raw))
	}
}

func TestFilesFromWindowsGiveTheSameRecordsAsPlainUTF8(t *testing.T) {
	plain := "Jan  5 10:00:01 host a\nJan  5 10:00:02 host b\n"
	want := []string{"Jan  5 10:00:01 host a", "Jan  5 10:00:02 host b"}
	crlf := strings.ReplaceAll(plain, "\n", "\r\n")
	withBOM := "\xef\xbb\xbf" + plain
	cases := map[string][]byte{"crlf": []byte(crlf), "utf-8 mark": []byte(withBOM), "utf-16le": utf16le(plain), "utf-16be": utf16be(plain)}
	for name, in := range cases {
		r, recs := readAll(t, in)
		if !sameStrings(raws(recs), want) {
			t.Errorf("%s: got %q, want %q", name, raws(recs), want)
		}
		if l, ok := r.Layout(); !ok || l.Name != "syslog" {
			t.Errorf("%s: layout = %q, %v", name, l.Name, ok)
		}
	}
	r, _ := readAll(t, []byte(withBOM))
	if r.Encoding() != "utf-8" {
		t.Errorf("encoding = %q", r.Encoding())
	}
	r, _ = readAll(t, utf16le(plain))
	if r.Encoding() != "utf-16le" {
		t.Errorf("encoding = %q", r.Encoding())
	}
	_, recs := readAll(t, []byte(withBOM))
	if recs[0].Offset != 3 {
		t.Errorf("the mark counts in offsets: first offset = %d, want 3", recs[0].Offset)
	}
}

func utf16le(s string) []byte {
	b := []byte{0xff, 0xfe}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

func utf16be(s string) []byte {
	b := []byte{0xfe, 0xff}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u>>8), byte(u))
	}
	return b
}

func TestInvalidBytesAreReplacedNotFatal(t *testing.T) {
	_, recs := readAll(t, []byte("bad \xff\xfe bytes\n"))
	if len(recs) != 1 || recs[0].Raw != "bad \ufffd bytes" || recs[0].Length != int64(len("bad \xff\xfe bytes")) {
		t.Errorf("got %+v", recs)
	}
}

func TestANulByteInTheFirstBlockRefusesTheInput(t *testing.T) {
	_, err := New(strings.NewReader("ok line\nbad\x00line\n"))
	var nt *NotTextError
	if !errors.As(err, &nt) || nt.Offset != int64(len("ok line\nbad")) {
		t.Errorf("err = %v, want a NotTextError at offset %d", err, len("ok line\nbad"))
	}
}

func TestANulByteFarPastTheFirstBlockIsFoundWithItsOffset(t *testing.T) {
	prefix := strings.Repeat("0123456789abcdef\n", (SampleBytes/17)+500)
	r, err := New(strings.NewReader(prefix + "late\x00nul\n"))
	if err != nil {
		t.Fatalf("New must not refuse: the NUL is past the sample: %v", err)
	}
	var got error
	for got == nil {
		_, got = r.Next()
	}
	var nt *NotTextError
	if !errors.As(got, &nt) || nt.Offset != int64(len(prefix)+len("late")) {
		t.Errorf("err = %v, want a NotTextError at offset %d", got, len(prefix)+len("late"))
	}
}

func TestBrokenUTF16BecomesReplacementCharactersWithoutAPanic(t *testing.T) {
	_, recs := readAll(t, append(utf16le("ab"), 0x41)) // an odd number of bytes
	if len(recs) != 1 || recs[0].Raw != "ab\ufffd" {
		t.Errorf("a stream cut in a code unit: %+v", recs)
	}
	_, recs = readAll(t, append([]byte{0xff, 0xfe, 0x00, 0xd8}, 'a', 0x00)) // U+D800 on its own, then "a"
	if len(recs) != 1 || recs[0].Raw != "\ufffda" {
		t.Errorf("a lone surrogate: %+v", recs)
	}
}

func TestEmptyInputGivesNoRecords(t *testing.T) {
	r, recs := readAll(t, nil)
	if len(recs) != 0 || r.Lines() != 0 || r.Bytes() != 0 {
		t.Errorf("records %d, lines %d, bytes %d", len(recs), r.Lines(), r.Bytes())
	}
}

func TestBytesCountsTheOriginalInput(t *testing.T) {
	in := utf16le("a\nb\n")
	r, _ := readAll(t, in)
	if r.Bytes() != int64(len(in)) {
		t.Errorf("Bytes() = %d, want %d", r.Bytes(), len(in))
	}
}

func TestALoneCarriageReturnIsNotALineEnd(t *testing.T) {
	_, recs := readAll(t, []byte("a\rb\nc\n"))
	if want := []string{"a\rb", "c"}; !sameStrings(raws(recs), want) {
		t.Errorf("got %q, want %q", raws(recs), want)
	}
}
