// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package report

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ckir/aigolog/internal/ingest"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/summary"
	"github.com/ckir/aigolog/internal/textsafe"
)

// sample is a small report whose strings are given by the caller, so that a
// test can put hostile text into every place a log's text can reach.
func sample(text string) Report {
	pat := store.PatternStat{ID: 1, Text: text, Records: 3, Share: 0.5}
	return Report{
		Format:  1,
		Program: Program{Name: "aigolog", Version: "test"},
		Sources: []SourceLine{{Name: text, Format: "text", Encoding: "utf-8", Layout: "none", Records: 3, Lines: 3, Bytes: 30}},
		Database: DatabaseLine{
			Path: text,
			Size: 4096,
			Kept: true,
		},
		Digest: summary.Digest{Records: 3, Patterns: 1, Groups: []store.PatternStat{pat}},
		Summary: summary.Summary{Blocks: []summary.Block{
			{ID: "S1", Kind: "patterns", Pattern: 0, Patterns: []store.PatternStat{pat}},
		}},
	}
}

func encode(t *testing.T, r Report) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := WriteJSON(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestJSONHasTheFormatTheSourcesTheDatabaseTheDigestAndTheSummary(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(encode(t, sample("plain")), &doc); err != nil {
		t.Fatal(err)
	}
	want := []string{"database", "digest", "format", "program", "sources", "summary"}
	var got []string
	for k := range doc {
		got = append(got, k)
	}
	if len(got) != len(want) {
		t.Fatalf("keys %v, want exactly %v", got, want)
	}
	for _, k := range want {
		if _, ok := doc[k]; !ok {
			t.Errorf("key %q is missing from %v", k, got)
		}
	}
	if doc["format"] != float64(1) {
		t.Errorf("format = %v, want 1", doc["format"])
	}
	db, _ := doc["database"].(map[string]any)
	if db["path"] != "plain" || db["kept"] != true || db["size"] != float64(4096) {
		t.Errorf("database = %v", db)
	}
	if p, _ := doc["program"].(map[string]any); p["name"] != "aigolog" || p["version"] != "test" {
		t.Errorf("program = %v", doc["program"])
	}
}

func TestJSONWritesPatternTextLiterallyAndIndentsByTwoSpaces(t *testing.T) {
	out := string(encode(t, sample("user <*> from <ip4> port <int>")))
	for _, want := range []string{"user <*> from <ip4> port <int>", "\n  \"format\": 1,\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("the document does not hold %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `\u003c`) || strings.Contains(out, `\u003e`) {
		t.Errorf("angle brackets were escaped as HTML:\n%s", out)
	}
}

func TestJSONNeverCarriesARawControlCharacter(t *testing.T) {
	// ESC and the other bytes below 0x20, U+2028 and U+2029, and a byte that is
	// not UTF-8, in every string a log can reach.
	hostile := "a\x1b[2J\x07b\x00c\td\ne\u2028f\u2029g\xffh$(reboot)"
	out := encode(t, sample(hostile))
	for i := 0; i < len(out); i++ {
		if (out[i] < 0x20 && out[i] != '\n') || out[i] == 0xff {
			t.Fatalf("raw byte %#x at %d: %q", out[i], i, out[max(0, i-20):min(len(out), i+20)])
		}
	}
	for _, raw := range []string{"\u2028", "\u2029", "\x1b"} {
		if strings.Contains(string(out), raw) {
			t.Errorf("the document holds %q raw", raw)
		}
	}
	if !utf8.Valid(out) {
		t.Error("the document is not valid UTF-8")
	}
	var back Report
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	// The text survives as far as JSON can carry it: the invalid byte is U+FFFD.
	if want := "a\x1b[2J\x07b\x00c\td\ne\u2028f\u2029g\ufffdh$(reboot)"; back.Sources[0].Name != want {
		t.Errorf("name = %q, want %q", back.Sources[0].Name, want)
	}
}

func TestJSONEndsWithOneNewline(t *testing.T) {
	out := string(encode(t, sample("x")))
	if !strings.HasSuffix(out, "}\n") || strings.HasSuffix(out, "\n\n") {
		t.Errorf("the document must end with one newline: %q", out[max(0, len(out)-10):])
	}
	if strings.Count(out, "\n") < 3 {
		t.Error("the document is not indented")
	}
}

func TestJSONWriteErrorIsReturned(t *testing.T) {
	if err := WriteJSON(failing{}, sample("x")); err == nil {
		t.Error("a failing writer gave no error")
	}
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestSourceLinesFollowTheResultInOrderAndAreNeverNil(t *testing.T) {
	if got := SourceLines(ingest.Result{}); got == nil || len(got) != 0 {
		t.Errorf("SourceLines of no sources = %#v, want an empty slice", got)
	}
	res := ingest.Result{Sources: []ingest.SourceReport{
		{ID: 1, Source: store.Source{Name: "a", Format: "text", Encoding: "utf-8", Layout: "syslog", TimeKind: "wall_year_inferred", AnchorKind: "as_of", AnchorNanos: 7},
			Totals: store.SourceTotals{Records: 2, Lines: 3, Bytes: 40, HasTimes: true, FirstNanos: 5, LastNanos: 9}, CutRecords: 1},
		{Source: store.Source{Name: "b"}},
	}}
	got := SourceLines(res)
	want := []SourceLine{
		{Name: "a", Format: "text", Encoding: "utf-8", Layout: "syslog", TimeKind: "wall_year_inferred", AnchorKind: "as_of", AnchorNanos: 7,
			Records: 2, Lines: 3, Bytes: 40, CutRecords: 1, FirstNanos: 5, LastNanos: 9, HasTimes: true},
		{Name: "b"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("SourceLines = %+v, want %+v", got, want)
	}
}

func TestTheTextFormsHaveTheirHeadingAfterABlankLine(t *testing.T) {
	r := sample("hello")
	var b bytes.Buffer
	WriteDigest(&b, r.Digest)
	if want := "\npatterns\n" + summary.RenderDigest(r.Digest); b.String() != want {
		t.Errorf("WriteDigest = %q, want %q", b.String(), want)
	}
	b.Reset()
	WriteSummary(&b, r.Summary)
	if want := "\nsummary\n" + summary.Render(r.Summary); b.String() != want {
		t.Errorf("WriteSummary = %q, want %q", b.String(), want)
	}
}

func TestJSONNeverCarriesARawDeleteOrC1ControlAndTheStringsComeBack(t *testing.T) {
	// DEL, U+0085 (next line), U+009B (the 8-bit CSI), the ends of the C1 range,
	// and neighbours that are not controls (U+00A0, U+00C2 and U+007E) in every
	// string a log can reach.
	hostile := "a\x7fb\u0085c\u009bd\u0080e\u009ff gÂh~iÂ\u009bj"
	out := encode(t, sample(hostile))
	for i := 0; i < len(out); i++ {
		if out[i] == 0x7f {
			t.Fatalf("raw DEL at %d: %q", i, out[max(0, i-20):min(len(out), i+20)])
		}
		if out[i] == 0xc2 && i+1 < len(out) && out[i+1] >= 0x80 && out[i+1] <= 0x9f {
			t.Fatalf("raw C1 control %#x at %d: %q", out[i+1], i, out[max(0, i-20):min(len(out), i+20)])
		}
	}
	if !utf8.Valid(out) {
		t.Error("the document is not valid UTF-8")
	}
	if !strings.Contains(string(out), `a\u007fb\u0085c\u009bd\u0080e\u009ff`) {
		t.Errorf("the escapes are not as expected:\n%s", out)
	}
	var back Report
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.Sources[0].Name != hostile || back.Database.Path != hostile ||
		back.Digest.Groups[0].Text != hostile || back.Summary.Blocks[0].Patterns[0].Text != hostile {
		t.Errorf("the strings did not come back as they went in: %q", back.Sources[0].Name)
	}
}

// TestJSONWritesNoRawBidiControlAndTheStringsComeBack: the direction controls
// that textsafe.Clean escapes for the text report (U+200E, U+200F, U+061C,
// U+202A to U+202E, U+2066 to U+2069) are escapes in the JSON too, so that a
// name cannot reorder the text of a terminal that prints the document.
func TestJSONWritesNoRawBidiControlAndTheStringsComeBack(t *testing.T) {
	hostile := "a\u202eb\u200ec\u200fd\u061ce\u202af\u2066g\u2069h \u2067\u2068\u202b\u202c\u202d i\u2028j\u2029k \u200dl\u2065m\u2070n\u061bo\u0600p"
	out := encode(t, sample(hostile))
	for _, r := range []rune{0x202e, 0x200e, 0x200f, 0x061c, 0x202a, 0x2066, 0x2069, 0x2067, 0x2068, 0x202b, 0x202c, 0x202d, 0x2028, 0x2029} {
		if strings.ContainsRune(string(out), r) {
			t.Errorf("raw U+%04X in the document", r)
		}
	}
	// Neighbours that are not controls stay as they are.
	for _, r := range []rune{0x200d, 0x2065, 0x2070, 0x061b, 0x0600} {
		if !strings.ContainsRune(string(out), r) {
			t.Errorf("U+%04X was escaped; it is not a direction control", r)
		}
	}
	if !strings.Contains(string(out), `a\u202eb\u200ec\u200fd\u061ce\u202af\u2066g\u2069h`) {
		t.Errorf("the escapes are not as expected:\n%s", out)
	}
	var back Report
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.Sources[0].Name != hostile || back.Database.Path != hostile ||
		back.Digest.Groups[0].Text != hostile || back.Summary.Blocks[0].Patterns[0].Text != hostile {
		t.Errorf("the strings did not come back as they went in: %q", back.Sources[0].Name)
	}
}

// TestJSONEscapesEveryCodePointThatTextsafeEscapes keeps the two sets equal: a
// code point that textsafe.Clean changes is never raw in the JSON. The test goes
// through every code point, so a rune added to textsafe fails it until
// escapeControls knows it.
func TestJSONEscapesEveryCodePointThatTextsafeEscapes(t *testing.T) {
	checked := 0
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue
		}
		s := string(r)
		if textsafe.Clean(s) == s {
			continue
		}
		checked++
		out := encode(t, sample("a"+s+"b"))
		// The line feed is the document's own (the indent); one inside a string
		// is the escape \n, which the round trip below shows.
		if r != '\n' && strings.ContainsRune(string(out), r) {
			t.Errorf("U+%04X is escaped by textsafe and raw in the JSON", r)
		}
		var back Report
		if err := json.Unmarshal(out, &back); err != nil || back.Sources[0].Name != "a"+s+"b" {
			t.Errorf("U+%04X does not come back: %v", r, err)
		}
	}
	if checked < 60 {
		t.Errorf("only %d code points are escaped by textsafe; the test is not looking at the set", checked)
	}
}

// countingWriter counts its writes.
type countingWriter struct {
	writes int
	bytes.Buffer
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes++
	return c.Buffer.Write(p)
}

func TestJSONIsWrittenWithOneWrite(t *testing.T) {
	var w countingWriter
	if err := WriteJSON(&w, sample("x")); err != nil {
		t.Fatal(err)
	}
	if w.writes != 1 {
		t.Errorf("%d writes, want 1", w.writes)
	}
}

// TestWriteValueEscapesTheSameControlsAsWriteJSON: the encoding of WriteJSON is
// WriteValue's, for any value, so that a document that is not a Report (an
// answer to a question) has the same escapes of DEL, the C1 controls and the
// direction controls, the same absence of HTML escaping, and one Write.
func TestWriteValueEscapesTheSameControlsAsWriteJSON(t *testing.T) {
	type doc struct {
		Text  string   `json:"text"`
		Cells []string `json:"cells"`
	}
	v := doc{Text: "a\x7fb\u0085c\u202ed <*>&", Cells: []string{"\u009b", "x"}}
	var w countingWriter
	if err := WriteValue(&w, v); err != nil {
		t.Fatal(err)
	}
	if w.writes != 1 {
		t.Errorf("%d writes, want 1", w.writes)
	}
	want := "{\n  \"text\": \"a\\u007fb\\u0085c\\u202ed <*>&\",\n  \"cells\": [\n    \"\\u009b\",\n    \"x\"\n  ]\n}\n"
	if got := w.String(); got != want {
		t.Errorf("WriteValue wrote\n%q\nwant\n%q", got, want)
	}
	var back doc
	if err := json.Unmarshal(w.Bytes(), &back); err != nil || back.Text != v.Text || back.Cells[0] != "\u009b" {
		t.Errorf("the value did not come back: %v %+v", err, back)
	}
	// WriteJSON is WriteValue of the report.
	var a, b bytes.Buffer
	if err := WriteJSON(&a, sample("a\x7f\u202e")); err != nil {
		t.Fatal(err)
	}
	if err := WriteValue(&b, sample("a\x7f\u202e")); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Error("WriteJSON and WriteValue of the same report differ")
	}
}

func TestWriteValueWritesNothingWhenTheValueCannotBeEncoded(t *testing.T) {
	var w countingWriter
	if err := WriteValue(&w, make(chan int)); err == nil {
		t.Error("a channel was encoded")
	}
	if w.writes != 0 {
		t.Errorf("%d writes after an encoding error, want 0", w.writes)
	}
}
