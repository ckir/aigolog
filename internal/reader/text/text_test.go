// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package text

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf16"
	"unicode/utf8"

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

func TestALoneCarriageReturnAtTheVeryEndIsDroppedWithTheLineEnd(t *testing.T) {
	_, recs := readAll(t, []byte("a\nb\r"))
	if want := []string{"a", "b"}; !sameStrings(raws(recs), want) {
		t.Errorf("got %q, want %q", raws(recs), want)
	}
}

// A line that the underlying reader cuts short by failing is dropped, and the
// error follows the complete records. This is pinned, not wanted: a failed
// read fails the run and the database is removed, so nothing can observe the
// lost line (a known minor of B1; see Reader.readLine).
func TestAPartialLineBeforeAReadErrorIsDroppedAndTheErrorFollowsTheCompleteRecords(t *testing.T) {
	disk := errors.New("disk")
	// The failure comes past the first block, which New reads, so that New
	// itself does not fail.
	prefix := strings.Repeat("0123456789abcdef\n", (SampleBytes/17)+500)
	r, err := New(io.MultiReader(strings.NewReader(prefix+"one\ntwo"), iotest.ErrReader(disk)))
	if err != nil {
		t.Fatalf("New must not fail: the error is past the sample: %v", err)
	}
	var got []string
	var last error
	for last == nil {
		rec, err := r.Next()
		if err != nil {
			last = err
			break
		}
		got = append(got, rec.Raw)
	}
	if want := strings.Count(prefix, "\n") + 1; len(got) != want || got[len(got)-1] != "one" {
		t.Errorf("got %d records, the last %q; want %d, the last \"one\"", len(got), got[len(got)-1], want)
	}
	for _, g := range got {
		if g == "two" {
			t.Error("the partial line \"two\" was returned")
		}
	}
	if !errors.Is(last, disk) {
		t.Errorf("error = %v, want one that wraps %v", last, disk)
	}
}

// exactLines returns text of whole lines, each "2026-01-05 10:00:01 " and
// filler, that is n bytes long, and the lines without their ends. Every line
// is 32 bytes with its newline except the last, which takes what is left.
func exactLines(n int, finalNewline bool) (string, []string) {
	const width = 32
	count := n / width
	var b strings.Builder
	var lines []string
	for i := 0; i < count; i++ {
		size := width
		if i == count-1 {
			size = n - width*(count-1)
		}
		line := fmt.Sprintf("2026-01-05 10:00:01 %04d", i)
		line += strings.Repeat("x", size-1-len(line))
		lines = append(lines, line)
		b.WriteString(line)
		if i < count-1 || finalNewline {
			b.WriteByte('\n')
		}
	}
	return b.String(), lines
}

func TestASampleOfExactly64KiBTreatsTheLastLineAsWhole(t *testing.T) {
	for _, n := range []int{SampleBytes - 1, SampleBytes, SampleBytes + 1} {
		for _, final := range []bool{true, false} {
			input, want := exactLines(n, final)
			if wantLen := n; !final {
				wantLen = n - 1
				if len(input) != wantLen {
					t.Fatalf("n=%d: generator made %d bytes, want %d", n, len(input), wantLen)
				}
			} else if len(input) != n {
				t.Fatalf("n=%d: generator made %d bytes", n, len(input))
			}
			r, recs := readAll(t, []byte(input))
			layout, found := r.Layout()
			if !found || layout.Name != "iso-space" {
				t.Errorf("n=%d final newline %v: layout = %q, %v; want iso-space, true", n, final, layout.Name, found)
			}
			if !sameStrings(raws(recs), want) {
				t.Errorf("n=%d final newline %v: %d records, want %d whole lines", n, final, len(recs), len(want))
			}
			for _, rec := range recs {
				if rec.Stamp.State != record.Resolved {
					t.Errorf("n=%d final newline %v: record %d has no stamp", n, final, rec.Line)
					break
				}
			}
		}
	}
}

// smallCap is the cap on a record in the tests of the cap.
const smallCap = 100

func readAllCapped(t *testing.T, input string, maxRecord int) (*Reader, []record.Record) {
	t.Helper()
	r, err := newReader(strings.NewReader(input), maxRecord)
	if err != nil {
		t.Fatalf("newReader: %v", err)
	}
	return r, drain(t, r)
}

// indented returns the lines "  01xxx..." to "  NNxxx...", each 30 bytes, and
// the text of them joined by newlines.
func indented(n int) []string {
	var lines []string
	for i := 1; i <= n; i++ {
		lines = append(lines, fmt.Sprintf("  %02d%s", i, strings.Repeat("x", 26)))
	}
	return lines
}

func TestAJoinedRecordIsCutAtTheCapAndNoByteIsLostOrRepeated(t *testing.T) {
	stamped := "2026-01-05 10:00:01 start" // 25 bytes
	cont := indented(10)                   // 30 bytes each
	input := stamped + "\n" + strings.Join(cont, "\n") + "\n"
	r, recs := readAllCapped(t, input, smallCap)
	// The stamped line and lines 1-2 make 25+31+31 = 87 bytes; line 3 would make
	// 118. So the records are: stamp+1-2 (87), 3-5 (92), 6-8 (92), 9-10 (61):
	// 4 records, 3 of them started by a cut.
	if len(recs) != 4 {
		t.Fatalf("got %d records, want 4: %q", len(recs), raws(recs))
	}
	if r.Cut() != 3 {
		t.Errorf("Cut() = %d, want 3", r.Cut())
	}
	for i, rec := range recs {
		if len(rec.Raw) > smallCap {
			t.Errorf("record %d holds %d bytes, over the cap", i+1, len(rec.Raw))
		}
	}
	if got, want := strings.Join(raws(recs), "\n"), strings.TrimSuffix(input, "\n"); got != want {
		t.Errorf("the records joined by newlines differ from the input:\n got %q\nwant %q", got, want)
	}
	if recs[0].Stamp.State != record.Resolved {
		t.Errorf("first stamp = %+v, want Resolved", recs[0].Stamp)
	}
	for _, rec := range recs[1:] {
		if rec.Stamp.State != record.NoStamp {
			t.Errorf("a cut record has the stamp %+v, want NoStamp", rec.Stamp)
		}
	}
	// The second record starts at the 4th line, after 26 + 31 + 31 bytes, and
	// holds three lines: 30*3 + 2 newlines.
	if got := recs[1]; got.Line != 4 || got.Offset != 88 || got.Length != 92 {
		t.Errorf("second record: line %d, offset %d, length %d; want 4, 88, 92", got.Line, got.Offset, got.Length)
	}
	if r.Lines() != 11 {
		t.Errorf("Lines() = %d, want 11", r.Lines())
	}
}

func TestALineLongerThanTheCapIsNeverSplit(t *testing.T) {
	long := strings.Repeat("y", 3*smallCap)
	r, recs := readAllCapped(t, long+"\n", smallCap)
	if len(recs) != 1 || recs[0].Raw != long || r.Cut() != 0 {
		t.Errorf("alone: %d records, Cut() = %d", len(recs), r.Cut())
	}
	// After a stamped line it is a continuation that does not fit: it starts a
	// record of its own, whole.
	r, recs = readAllCapped(t, "2026-01-05 10:00:01 start\n  "+long+"\n", smallCap)
	if len(recs) != 2 || recs[1].Raw != "  "+long || r.Cut() != 1 {
		t.Errorf("after a stamped line: %d records, Cut() = %d", len(recs), r.Cut())
	}
	// A short line after the long one does not join it either.
	_, recs = readAllCapped(t, "2026-01-05 10:00:01 start\n  "+long+"\n  short\n", smallCap)
	if len(recs) != 3 || recs[2].Raw != "  short" {
		t.Errorf("got %d records: %q", len(recs), raws(recs))
	}
}

func TestWithTheRealCapNoRecordPassesOneMiB(t *testing.T) {
	line := "  " + strings.Repeat("z", 97) // 99 bytes and a newline
	var b strings.Builder
	b.WriteString("2026-01-05 10:00:01 start\n")
	for b.Len() < 3<<20 {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	r, recs := readAllCapped(t, b.String(), MaxRecordBytes)
	if r.Cut() < 2 {
		t.Errorf("Cut() = %d, want at least 2", r.Cut())
	}
	if int(r.Cut()) != len(recs)-1 {
		t.Errorf("Cut() = %d for %d records", r.Cut(), len(recs))
	}
	for i, rec := range recs {
		if len(rec.Raw) > MaxRecordBytes {
			t.Errorf("record %d holds %d bytes", i+1, len(rec.Raw))
		}
	}
}

func TestLeadingBlankLinesStartNoRecordButAreCounted(t *testing.T) {
	r, recs := readAll(t, []byte("\n  \n\t\r\nhello\nworld\n"))
	if want := []string{"hello", "world"}; !sameStrings(raws(recs), want) {
		t.Fatalf("got %q, want %q", raws(recs), want)
	}
	// "\n" is 1 byte, "  \n" 3, "\t\r\n" 3: the first record is on line 4 at offset 7.
	if recs[0].Line != 4 || recs[0].Offset != 7 || recs[0].Length != 5 {
		t.Errorf("first record = %+v, want line 4, offset 7, length 5", recs[0])
	}
	if r.Lines() != 5 {
		t.Errorf("Lines() = %d, want 5", r.Lines())
	}

	r, recs = readAll(t, []byte("\n\n2026-01-05 10:00:01 a\n\n2026-01-05 10:00:02 b\n"))
	if want := []string{"2026-01-05 10:00:01 a\n", "2026-01-05 10:00:02 b"}; !sameStrings(raws(recs), want) {
		t.Fatalf("with stamps: got %q, want %q", raws(recs), want)
	}
	if recs[0].Line != 3 || recs[0].Offset != 2 {
		t.Errorf("with stamps: first record = %+v, want line 3, offset 2", recs[0])
	}
}

func TestAnInputOfBlankLinesOnlyHasNoRecords(t *testing.T) {
	r, recs := readAll(t, []byte("\n \t\n\r\n\n"))
	if len(recs) != 0 {
		t.Errorf("got %d records: %q", len(recs), raws(recs))
	}
	if r.Lines() != 4 {
		t.Errorf("Lines() = %d, want 4", r.Lines())
	}
}

// checkCapInvariant fails the test when a record of a capped reader is longer
// than the cap and is not a single line, or when the records, joined by
// newlines, are not the input: no byte lost or repeated.
func checkCapInvariant(t *testing.T, recs []record.Record, input string, maxRecord int) {
	t.Helper()
	for i, rec := range recs {
		if len(rec.Raw) > maxRecord && strings.Contains(rec.Raw, "\n") {
			t.Errorf("record %d holds %d bytes in several lines, over the cap of %d", i+1, len(rec.Raw), maxRecord)
		}
	}
	if got, want := strings.Join(raws(recs), "\n"), strings.TrimSuffix(input, "\n"); got != want {
		t.Errorf("the records joined by newlines differ from the input:\n got %q\nwant %q", got, want)
	}
}

func TestACutThatLandsOnAnEmptyLineDoesNotLetALongLineJoinIt(t *testing.T) {
	// The stamped line (25 bytes) and the 74-byte line make exactly 100 bytes,
	// the cap, so the empty line after them is the one that is cut. It starts a
	// record of its own, and the 300-byte line after it must not be joined to
	// that empty record.
	input := "2026-01-05 10:00:01 start\n" +
		"  " + strings.Repeat("a", 72) + "\n" +
		"\n" +
		"  " + strings.Repeat("b", 298) + "\n" +
		"  tail\n"
	r, recs := readAllCapped(t, input, smallCap)
	checkCapInvariant(t, recs, input, smallCap)
	if r.Cut() < 1 {
		t.Errorf("Cut() = %d, want at least 1", r.Cut())
	}
	for _, rec := range recs {
		if strings.Contains(rec.Raw, strings.Repeat("b", 298)) && rec.Raw != "  "+strings.Repeat("b", 298) {
			t.Errorf("the long line was joined to something: %q", rec.Raw)
		}
	}
}

func TestUTF16PositionsOfSeveralLines(t *testing.T) {
	// The decoded text, with the byte-order mark not counted:
	//   "héllo\n"        7 bytes (é is two), at 0; 6 without the newline
	//   "wörld 😀\r\n"   13 bytes (ö two, the emoji four), at 7; 11 without CR LF
	//   "last€"          7 bytes (€ is three), at 20
	const text = "héllo\nwörld 😀\r\nlast€"
	want := []record.Record{
		{Line: 1, Offset: 0, Length: 6, Raw: "héllo"},
		{Line: 2, Offset: 7, Length: 11, Raw: "wörld 😀"},
		{Line: 3, Offset: 20, Length: 7, Raw: "last€"},
	}
	for name, in := range map[string][]byte{"utf-16le": utf16le(text), "utf-16be": utf16be(text)} {
		r, recs := readAll(t, in)
		if len(recs) != len(want) {
			t.Errorf("%s: got %d records, want %d: %+v", name, len(recs), len(want), recs)
			continue
		}
		for i := range want {
			if recs[i] != want[i] {
				t.Errorf("%s: record %d = %+v, want %+v", name, i+1, recs[i], want[i])
			}
		}
		if r.Bytes() != int64(len(in)) {
			t.Errorf("%s: Bytes() = %d, want the original %d", name, r.Bytes(), len(in))
		}
		if r.Lines() != 3 {
			t.Errorf("%s: Lines() = %d, want 3", name, r.Lines())
		}
	}
}

func TestADecodedNulInUTF16IsRefusedWithItsDecodedOffset(t *testing.T) {
	// "ab\n" is 3 decoded bytes, "cd\n" 3 more, "ef" 2: the NUL is at 8.
	const text = "ab\ncd\nef\x00gh\nafter\n"
	for name, in := range map[string][]byte{"utf-16le": utf16le(text), "utf-16be": utf16be(text)} {
		r, err := New(bytes.NewReader(in))
		if err != nil {
			t.Errorf("%s: New refused the input, but a NUL is only looked for in the lines of UTF-16: %v", name, err)
			continue
		}
		var got []string
		var last error
		for last == nil {
			var rec record.Record
			if rec, last = r.Next(); last == nil {
				got = append(got, rec.Raw)
			}
		}
		if want := []string{"ab", "cd"}; !sameStrings(got, want) {
			t.Errorf("%s: records before the NUL = %q, want %q (last error %v)", name, got, want, last)
		}
		var nt *NotTextError
		if !errors.As(last, &nt) {
			t.Errorf("%s: Next ended with %v, want a *NotTextError", name, last)
			continue
		}
		if nt.Offset != 8 {
			t.Errorf("%s: NotTextError.Offset = %d, want the decoded offset 8", name, nt.Offset)
		}
	}
}

func TestASurrogatePairSplitAcrossTheDecodersChunkIsKept(t *testing.T) {
	readers := map[string]func([]byte) io.Reader{
		"bytes.Reader":  func(b []byte) io.Reader { return bytes.NewReader(b) },
		"one-byte read": func(b []byte) io.Reader { return iotest.OneByteReader(bytes.NewReader(b)) },
	}
	// The decoder takes chunkSize bytes, chunkSize/2 code units, from its
	// source at a time (after the mark). With chunkSize/2-1 units before it,
	// the high surrogate of the emoji is the last unit of the first chunk and
	// the low one is the first unit of the second.
	for _, pad := range []int{chunkSize/2 - 2, chunkSize/2 - 1, chunkSize / 2} {
		line := strings.Repeat("a", pad) + "😀"
		text := line + "\ntail\n"
		for _, enc := range []struct {
			name string
			in   []byte
		}{{"utf-16le", utf16le(text)}, {"utf-16be", utf16be(text)}} {
			for rname, mk := range readers {
				name := fmt.Sprintf("%s pad %d %s", enc.name, pad, rname)
				r, err := New(mk(enc.in))
				if err != nil {
					t.Errorf("%s: New: %v", name, err)
					continue
				}
				recs := drain(t, r)
				if len(recs) != 2 {
					t.Errorf("%s: got %d records, want 2", name, len(recs))
					continue
				}
				if recs[0].Raw != line || strings.ContainsRune(recs[0].Raw, utf8.RuneError) {
					t.Errorf("%s: the first record does not end in the whole emoji (%d bytes, tail %q)", name, len(recs[0].Raw), recs[0].Raw[len(recs[0].Raw)-8:])
				}
				if recs[0].Offset != 0 || recs[0].Length != int64(pad+4) {
					t.Errorf("%s: first record at %d, length %d; want 0 and %d", name, recs[0].Offset, recs[0].Length, pad+4)
				}
				if recs[1].Raw != "tail" || recs[1].Line != 2 || recs[1].Offset != int64(pad+5) {
					t.Errorf("%s: second record = %+v, want tail at line 2 offset %d", name, recs[1], pad+5)
				}
				if r.Bytes() != int64(len(enc.in)) {
					t.Errorf("%s: Bytes() = %d, want %d", name, r.Bytes(), len(enc.in))
				}
			}
		}
	}
}

func TestAStampedRecordCarriesTheLengthOfItsStamp(t *testing.T) {
	_, recs := readAll(t, []byte("2026-11-16 08:15:22 hello\n  more\n"))
	if len(recs) != 1 {
		t.Fatalf("got %d records: %q", len(recs), raws(recs))
	}
	if got := recs[0].Stamp.Len; got != 19 {
		t.Errorf("Stamp.Len = %d, want 19", got)
	}
	if got, want := recs[0].Raw[recs[0].Stamp.Len:], " hello\n  more"; got != want {
		t.Errorf("Raw[Stamp.Len:] = %q, want %q", got, want)
	}
}

func TestARecordWithNoStampHasLengthZero(t *testing.T) {
	_, recs := readAll(t, []byte("plain one\nplain two\n"))
	if len(recs) != 2 {
		t.Fatalf("got %d records: %q", len(recs), raws(recs))
	}
	for i, rec := range recs {
		if rec.Stamp.Len != 0 {
			t.Errorf("record %d: Stamp.Len = %d, want 0", i+1, rec.Stamp.Len)
		}
	}
	// A record that the cap cut has no stamp, so its length is 0; the record
	// before it keeps its own.
	input := "2026-11-16 08:15:22 start\n" + strings.Join(indented(10), "\n") + "\n"
	r, recs := readAllCapped(t, input, smallCap)
	if r.Cut() == 0 || len(recs) < 2 {
		t.Fatalf("the cap cut nothing: Cut() = %d, %d records", r.Cut(), len(recs))
	}
	if recs[0].Stamp.Len != 19 {
		t.Errorf("first record: Stamp.Len = %d, want 19", recs[0].Stamp.Len)
	}
	for i, rec := range recs[1:] {
		if rec.Stamp.Len != 0 {
			t.Errorf("cut record %d: Stamp.Len = %d, want 0", i+2, rec.Stamp.Len)
		}
	}
}
