// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/query"
	"github.com/ckir/aigolog/internal/store"
)

// authLog has two kinds of line: three that accept a user and one that rejects
// one. The first kind is the pattern that "accepted" finds.
const authLog = "2026-01-05 10:00:01 svc login accepted for alice port 22\n" +
	"2026-01-05 10:00:02 svc login accepted for bob port 22\n" +
	"2026-01-05 10:00:03 svc login rejected port 80 for invalid user mallory\n" +
	"2026-01-05 10:00:04 svc login accepted for alice port 25\n"

var databaseLine = regexp.MustCompile(`(?m)^database: (.+\.db) \(`)

// analysed analyses body and returns the path of the database and the output.
func analysed(t *testing.T, h *harness, body string) (path, out string) {
	t.Helper()
	code, out, errOut := h.run("analyze", h.file("auth.log", body))
	if code != exitcode.OK {
		t.Fatalf("analyze: code %d, stderr %q", code, errOut)
	}
	m := databaseLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no database line in:\n%s", out)
	}
	return m[1], out
}

// queryDoc is the document that query --json prints.
type queryDoc struct {
	ID     string       `json:"id"`
	Stored bool         `json:"stored"`
	Rerun  bool         `json:"rerun"`
	Answer query.Answer `json:"answer"`
}

// decodeDoc reads the one document of out, and nothing after it.
func decodeDoc(t *testing.T, out string) queryDoc {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	var d queryDoc
	if err := dec.Decode(&d); err != nil {
		t.Fatalf("not a document: %v\n%s", err, out)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		t.Fatalf("more than one document:\n%s", out)
	}
	return d
}

// ask runs query on the database with --no-save --json and returns the answer.
func ask(t *testing.T, h *harness, path string, args ...string) query.Answer {
	t.Helper()
	full := append([]string{"query", "--db", path, "--no-save", "--json"}, args...)
	code, out, errOut := h.run(full...)
	if code != exitcode.OK {
		t.Fatalf("%v: code %d, stderr %q", args, code, errOut)
	}
	return decodeDoc(t, out).Answer
}

// slotsOf returns the numbers of the slot of a word and of the slot of an
// integer of the pattern that "accepted" finds, and the number of that pattern.
func slotsOf(t *testing.T, h *harness, path string) (word, number, pattern string) {
	t.Helper()
	ps := ask(t, h, path, "patterns", "accepted")
	if len(ps.Rows) != 1 {
		t.Fatalf("patterns of accepted = %v, want one", ps.Rows)
	}
	pattern = ps.Rows[0][0]
	ss := ask(t, h, path, "--pattern", pattern, "slots")
	for _, row := range ss.Rows {
		switch row[2] {
		case "word":
			word = row[0]
		case "int":
			number = row[0]
		}
	}
	if word == "" || number == "" {
		t.Fatalf("slots of the pattern %s = %v (columns %v), want a word and an int", pattern, ss.Rows, ss.Columns)
	}
	return word, number, pattern
}

func TestQueryRequiresADatabase(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run("query", "count")
	if code != exitcode.Usage || out != "" || !strings.Contains(errOut, "--db") {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, out, errOut = h.run("query", "--answer", "A1")
	if code != exitcode.Usage || out != "" || !strings.Contains(errOut, "--db") {
		t.Errorf("--answer: code %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestQueryAnswersAndStoresByDefaultWithAnIdentifier(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	code, out, errOut := h.run("query", "--db", path, "count")
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"answer A1: count\n", "rows covered 4\n", "re-run: aigolog query --db " + path + " --answer A1\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	code, out, errOut = h.run("query", "--db", path, "count")
	if code != exitcode.OK || errOut != "" || !strings.Contains(out, "answer A2: count\n") ||
		!strings.Contains(out, "--answer A2\n") {
		t.Errorf("second: code %d, stderr %q, output:\n%s", code, errOut, out)
	}
	code, out, _ = h.run("show", "--db", path)
	if code != exitcode.OK || !strings.Contains(out, "answers: 2 stored\n") ||
		!strings.Contains(out, "answer A1: count\n") || !strings.Contains(out, "answer A2: count\n") {
		t.Errorf("show: code %d, output:\n%s", code, out)
	}
}

func TestQueryNoSaveStoresNothing(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	code, out, errOut := h.run("query", "--db", path, "--no-save", "count")
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	if !strings.HasPrefix(out, "answer: count\n") || strings.Contains(out, "re-run:") {
		t.Errorf("output:\n%s", out)
	}
	code, out, _ = h.run("show", "--db", path)
	if code != exitcode.OK || !strings.Contains(out, "answers: 0 stored\n") {
		t.Errorf("show: code %d, output:\n%s", code, out)
	}
}

func TestQueryAnswerReRunsAStoredQuestionAndStoresNothing(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	word, _, pattern := slotsOf(t, h, path)
	first := []string{"query", "--db", path, "--json", "--pattern", pattern, "--col", "user=" + word, "--by", "user", "top"}
	code, out, errOut := h.run(first...)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	original := decodeDoc(t, out)
	if original.ID != "A1" || !original.Stored || len(original.Answer.Rows) != 2 {
		t.Fatalf("the first answer = %+v", original)
	}
	if code, _, _ := h.run("query", "--db", path, "count"); code != exitcode.OK {
		t.Fatal("the second answer was not made")
	}

	code, out, errOut = h.run("query", "--db", path, "--json", "--answer", "A1")
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("--answer: code %d, stderr %q", code, errOut)
	}
	again := decodeDoc(t, out)
	if again.ID != "A1" || again.Stored || !again.Rerun {
		t.Errorf("the re-run = id %q stored %v rerun %v, want A1 false true", again.ID, again.Stored, again.Rerun)
	}
	if !reflect.DeepEqual(again.Answer.Question, original.Answer.Question) ||
		!reflect.DeepEqual(again.Answer.Columns, original.Answer.Columns) ||
		!reflect.DeepEqual(again.Answer.Rows, original.Answer.Rows) {
		t.Errorf("the re-run gave another table:\n%+v\n%+v", again.Answer, original.Answer)
	}

	code, out, errOut = h.run("query", "--db", path, "--answer", "1")
	if code != exitcode.OK || errOut != "" || !strings.HasPrefix(out, "re-run of A1\nanswer A1: top\n") || strings.Contains(out, "re-run:") {
		t.Errorf("text: code %d, stderr %q, output:\n%s", code, errOut, out)
	}
	code, out, _ = h.run("show", "--db", path)
	if code != exitcode.OK || !strings.Contains(out, "answers: 2 stored\n") {
		t.Errorf("show: code %d, output:\n%s", code, out)
	}
}

func TestQueryAnswerWithAnUnknownIdIsAUsageError(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	for _, id := range []string{"A9", "9", "A0", "0", "x", "-", "A", "A-1", "A1x", ""} {
		code, out, errOut := h.run("query", "--db", path, "--answer", id)
		if code != exitcode.Usage || out != "" || !strings.HasPrefix(errOut, "aigolog: ") {
			t.Errorf("--answer %q: code %d, stdout %q, stderr %q", id, code, out, errOut)
		}
	}
}

func TestQueryJSONIsOneDocumentWithTheIdTheStoredFlagAndTheAnswer(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	code, out, errOut := h.run("query", "--db", path, "--json", "patterns", "accepted")
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("not one document: %v\n%s", err, out)
	}
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	if len(raw) != 3 || raw["id"] == nil || raw["stored"] == nil || raw["answer"] == nil {
		t.Errorf("the keys are %v, want id, stored and answer", keys)
	}
	d := decodeDoc(t, out)
	if d.ID != "A1" || !d.Stored || d.Rerun || d.Answer.Question.Verb != query.Patterns ||
		len(d.Answer.Rows) != 1 || d.Answer.Question.Words[0] != "accepted" {
		t.Errorf("document = %+v", d)
	}
	if strings.Contains(out, "answer A1:") || strings.Contains(out, "re-run") {
		t.Errorf("text in the JSON output:\n%s", out)
	}
	// What was stored reads back as the same answer.
	code, shown, _ := h.run("show", "--db", path)
	if code != exitcode.OK || !strings.Contains(shown, "answer A1: patterns\n") {
		t.Errorf("show: code %d, output:\n%s", code, shown)
	}
}

func TestQueryPrintsNoLogValueInTheRerunLine(t *testing.T) {
	h := newHarness(t)
	const addr = "10.9.8.7"
	path, _ := analysed(t, h, "2026-01-05 10:00:01 svc connect from "+addr+" port 22\n2026-01-05 10:00:02 svc connect from "+addr+" port 23\n")
	for _, args := range [][]string{
		{"count"},
		{"patterns", "connect"},
		{"where", addr},
		{"patterns", addr},
	} {
		full := append([]string{"query", "--db", path}, args...)
		code, out, errOut := h.run(full...)
		if code != exitcode.OK || errOut != "" {
			t.Fatalf("%v: code %d, stderr %q", args, code, errOut)
		}
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "re-run:") {
				found = true
				if strings.Contains(line, addr) || strings.Contains(line, "connect") {
					t.Errorf("%v: a log value is in %q", args, line)
				}
			}
		}
		if !found {
			t.Errorf("%v: no re-run line in:\n%s", args, out)
		}
	}
}

func TestQueryCleansHostileValues(t *testing.T) {
	h := newHarness(t)
	const hostile = "evil\x1b[31m\aX\u202eY\x7fZ\u009bW"
	path, _ := analysed(t, h, "2026-01-05 10:00:01 svc login accepted for "+hostile+"1 port 22\n"+
		"2026-01-05 10:00:02 svc login accepted for "+hostile+"2 port 22\n"+
		"2026-01-05 10:00:03 svc login accepted for "+hostile+"3 port 22\n")
	word, _, pattern := slotsOf(t, h, path)
	args := []string{"--pattern", pattern, "--col", "user=" + word, "--by", "user", "top"}

	code, out, errOut := h.run(append([]string{"query", "--db", path}, args...)...)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("text: code %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "evil") {
		t.Fatalf("the value is not in the output, so the test shows nothing:\n%q", out)
	}
	assertNoRawControls(t, "query text", out)

	code, shown, errOut := h.run("show", "--db", path)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("show: code %d, stderr %q", code, errOut)
	}
	if !strings.Contains(shown, "evil") {
		t.Fatalf("the value is not in what show printed:\n%q", shown)
	}
	assertNoRawControls(t, "show", shown)

	code, out, errOut = h.run(append([]string{"query", "--db", path, "--json"}, args...)...)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("json: code %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "evil") {
		t.Fatalf("the value is not in the JSON:\n%q", out)
	}
	for _, bad := range []string{"\x7f", "\u0080", "\u009b", "\u009f", "\u202a", "\u202e", "\u2066", "\u2069", "\u200e", "\u061c"} {
		if strings.Contains(out, bad) {
			t.Errorf("the JSON holds %q raw:\n%q", bad, out)
		}
	}
	assertNoRawControls(t, "query json", out)
}

// assertNoRawControls fails when s holds a character below 0x20 other than the line
// feed and the tab that separates the cells, DEL, a C1 control or a direction control.
func assertNoRawControls(t *testing.T, what, s string) {
	t.Helper()
	for _, r := range s {
		switch {
		case r == '\n', r == '\t': // the table is tab separated; a tab in a cell is escaped
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x200e, r == 0x200f, r == 0x061c:
			t.Errorf("%s holds the raw character %U:\n%q", what, r, s)
			return
		}
	}
}

func TestQueryNotesThatAnAnswerWasNotStoredWhenTheLockIsHeld(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	db, err := store.Create(ctx, store.Options{Dir: h.cache, Now: func() time.Time { return h.now }})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.MarkComplete(ctx); err != nil {
		t.Fatal(err)
	}
	const note = "aigolog: note: the answer was not stored: the database is in use by another process\n"

	code, out, errOut := h.run("query", "--db", db.Path(), "count")
	if code != exitcode.OK || errOut != note {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	if !strings.HasPrefix(out, "answer: count\n") || strings.Contains(out, "re-run:") {
		t.Errorf("output:\n%s", out)
	}

	code, out, errOut = h.run("query", "--db", db.Path(), "--json", "count")
	if code != exitcode.OK || errOut != note {
		t.Fatalf("json: code %d, stderr %q", code, errOut)
	}
	if d := decodeDoc(t, out); d.ID != "-" || d.Stored || d.Rerun {
		t.Errorf("document = id %q stored %v rerun %v, want - false false", d.ID, d.Stored, d.Rerun)
	}

	code, _, errOut = h.run("query", "--db", db.Path(), "--no-save", "count")
	if code != exitcode.OK || errOut != "" {
		t.Errorf("--no-save: code %d, stderr %q", code, errOut)
	}
}

func TestQueryNotesThatAnAnswerWasNotStoredWhenThereIsNoLockFile(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	if err := os.Remove(strings.TrimSuffix(path, ".db") + ".lock"); err != nil {
		t.Skipf("the lock file cannot be removed here: %v", err)
	}
	code, out, errOut := h.run("query", "--db", path, "count")
	if code != exitcode.OK || errOut != "aigolog: note: the answer was not stored: the database has no lock file\n" ||
		!strings.HasPrefix(out, "answer: count\n") {
		t.Errorf("code %d, stderr %q, output:\n%s", code, errOut, out)
	}
}

func TestQueryNotesTheErrorWhenTheWriteFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a file cannot be made unwritable here")
	}
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := h.run("query", "--db", path, "count")
	if code != exitcode.OK || !strings.HasPrefix(errOut, "aigolog: note: the answer was not stored: ") ||
		strings.Count(errOut, "\n") != 1 || !strings.HasPrefix(out, "answer: count\n") {
		t.Errorf("code %d, stderr %q, output:\n%s", code, errOut, out)
	}
}

func TestQueryWithAnUnknownSlotIsExitTwoAndNamesTheSlot(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	code, out, errOut := h.run("query", "--db", path, "--col", "user=99999", "--by", "user", "top")
	if code != exitcode.Usage || out != "" || !strings.Contains(errOut, "99999") || !strings.HasPrefix(errOut, "aigolog: ") {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, out, errOut = h.run("query", "--db", path, "--pattern", "7777", "slots")
	if code != exitcode.Usage || out != "" || !strings.Contains(errOut, "7777") {
		t.Errorf("pattern: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	if _, shown, _ := h.run("show", "--db", path); !strings.Contains(shown, "answers: 0 stored\n") {
		t.Errorf("something was stored:\n%s", shown)
	}
}

func TestQueryOnAnIncompleteDatabaseIsExitOne(t *testing.T) {
	h := newHarness(t)
	db, err := store.Create(context.Background(), store.Options{Dir: h.cache, Now: func() time.Time { return h.now }})
	if err != nil {
		t.Fatal(err)
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"query", "--db", path, "count"},
		{"query", "--db", path, "--answer", "A1"},
		{"query", "--db", filepath.Join(h.cache, "missing.db"), "count"},
	} {
		code, out, errOut := h.run(args...)
		if code != exitcode.Failure || out != "" || !strings.HasPrefix(errOut, "aigolog: ") {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

// The end-to-end test below needs a limit of 1ns to be over before the question
// ends. That holds where the clock is fine, not on Windows, whose clock moves
// on a system tick (about 15.6 ms); this test pins the message and the exit
// code of the limit on every platform.
func TestQuestionFailedReportsTheTimeLimitAndExitsWithOne(t *testing.T) {
	var errOut bytes.Buffer
	code := questionFailed(Env{Stderr: &errOut}, context.Background(), query.ErrTimeLimit, query.Limits{Time: 250 * time.Millisecond}, "")
	if code != exitcode.Failure || errOut.String() != "aigolog: the question ran out of time (250ms)\n" {
		t.Errorf("code %d, stderr %q", code, errOut.String())
	}
}

func TestQueryTimeLimitIsExitOneAndStoresNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the clock of Windows is too coarse for a limit of 1ns; TestQuestionFailedReportsTheTimeLimitAndExitsWithOne covers the report")
	}
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	word, number, pattern := slotsOf(t, h, path)
	code, out, errOut := h.run("query", "--db", path, "--timeout", "1ns", "--pattern", pattern,
		"--col", "user="+word, "--col", "port="+number, "--by", "user", "--with", "port", "pairs")
	if code != exitcode.Failure || out != "" || errOut != "aigolog: the question ran out of time (1ns)\n" {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	if _, shown, _ := h.run("show", "--db", path); !strings.Contains(shown, "answers: 0 stored\n") {
		t.Errorf("something was stored:\n%s", shown)
	}
}

func TestQueryRepeatedColAndWhereAccumulate(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	word, number, pattern := slotsOf(t, h, path)
	a := ask(t, h, path, "--pattern", pattern, "--col", "user="+word, "--col", "port="+number,
		"--where", "user=alice", "--where", "port=22", "count")
	q := a.Question
	wantCols := []query.Column{{Name: "user", Slots: []int64{mustInt(t, word)}}, {Name: "port", Slots: []int64{mustInt(t, number)}}}
	wantFilters := []query.Filter{{Column: "user", Value: "alice"}, {Column: "port", Value: "22"}}
	if !reflect.DeepEqual(q.Columns, wantCols) || !reflect.DeepEqual(q.Filters, wantFilters) {
		t.Errorf("columns %+v, filters %+v", q.Columns, q.Filters)
	}
	if a.Facts.Rows != 1 {
		t.Errorf("the two filters cover %d rows, want 1 (alice on port 22)", a.Facts.Rows)
	}
}

func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestQueryAnswerWithAVerbOrAQuestionFlagIsAUsageError(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	for _, extra := range [][]string{
		{"count"},
		{"patterns", "accepted"},
		{"--col", "a=1"},
		{"--where", "a=b"},
		{"--pattern", "1"},
		{"--by", "a"},
		{"--with", "a"},
		{"--buckets", "3"},
		{"--last"},
		{"--limit", "5"},
	} {
		args := append([]string{"query", "--db", path, "--answer", "A1"}, extra...)
		code, out, errOut := h.run(args...)
		if code != exitcode.Usage || out != "" || !strings.HasPrefix(errOut, "aigolog: ") {
			t.Errorf("%v: code %d, stdout %q, stderr %q", extra, code, out, errOut)
		}
	}
}

func TestQueryMixesFlagsAndTheVerb(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	word, _, pattern := slotsOf(t, h, path)
	for _, args := range [][]string{
		{"--json", "--no-save", "--pattern", pattern, "--col", "user=" + word, "--by", "user", "top"},
		{"top", "--json", "--no-save", "--pattern", pattern, "--col", "user=" + word, "--by", "user"},
		{"--json", "--no-save", "top", "--pattern", pattern, "--col", "user=" + word, "--by", "user", "--limit", "5"},
		{"--json", "--no-save", "--col", "user=" + word, "--by", "user", "--pattern", pattern, "--limit", "5", "--", "top"},
	} {
		full := append([]string{"query", "--db", path}, args...)
		code, out, errOut := h.run(full...)
		if code != exitcode.OK || errOut != "" {
			t.Errorf("%v: code %d, stderr %q", args, code, errOut)
			continue
		}
		if d := decodeDoc(t, out); d.Answer.Question.Verb != query.Top || len(d.Answer.Rows) != 2 {
			t.Errorf("%v: answer %+v", args, d.Answer)
		}
	}

	// A word that starts with a dash needs -- before it.
	a := ask(t, h, path, "patterns", "--", "-weird", "accepted")
	if !reflect.DeepEqual(a.Question.Words, []string{"-weird", "accepted"}) {
		t.Errorf("words = %q", a.Question.Words)
	}
	a = ask(t, h, path, "where", "--", "-5")
	if a.Question.Value != "-5" {
		t.Errorf("value = %q", a.Question.Value)
	}
	// Without --, a word that starts with a dash is a flag that does not exist.
	code, _, errOut := h.run("query", "--db", path, "patterns", "-weird")
	if code != exitcode.Usage || !strings.Contains(errOut, "weird") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
	// After --, everything is an argument, also a flag of ours.
	a = ask(t, h, path, "patterns", "--", "--json")
	if !reflect.DeepEqual(a.Question.Words, []string{"--json"}) {
		t.Errorf("words = %q", a.Question.Words)
	}
}

func TestQueryRefusesWhatIsNotAQuestion(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	for _, args := range [][]string{
		{},
		{"nonsense"},
		{"nonsense", "word"},
		{"count", "extra"},
		{"patterns"},
		{"where"},
		{"where", "a", "b"},
		{"--bogus", "count"},
		{"--timeout", "0", "count"},
		{"--timeout", "-1s", "count"},
		{"--timeout", "soon", "count"},
		{"--limit", "x", "count"},
		{"--limit", "5000", "rows"},
		{"--buckets", "x", "buckets"},
		{"--col", "noequals", "count"},
		{"--col", "=1", "count"},
		{"--col", "a=", "count"},
		{"--col", "a=0", "count"},
		{"--col", "a=x", "count"},
		{"--col", "a=1,,2", "count"},
		{"--col", "a=1", "--col", "a=2", "count"},
		{"--where", "noequals", "count"},
		{"--where", "a=b", "count"},
		{"--pattern", "x", "count"},
		{"--pattern", "1,", "count"},
		{"--by", "ghost", "top"},
		{"--last", "count"},
		{"--db"},
	} {
		full := append([]string{"query", "--db", path}, args...)
		code, out, errOut := h.run(full...)
		if code != exitcode.Usage || out != "" || !strings.HasPrefix(errOut, "aigolog: ") {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	if _, shown, _ := h.run("show", "--db", path); !strings.Contains(shown, "answers: 0 stored\n") {
		t.Errorf("something was stored:\n%s", shown)
	}
}

func TestParseQueryArgsBuildsTheQuestion(t *testing.T) {
	var errOut bytes.Buffer
	a, code, ok := parseQueryArgs(Env{Stderr: &errOut}, []string{
		"--db", "x.db", "--limit", "7", "--timeout", "2s", "--col", "u=1,2", "--col", "p=3",
		"--where", "u=a=b", "--where", "p=", "--pattern", "0,3", "--pattern", "5", "--by", "u", "top",
	})
	if !ok || code != exitcode.OK {
		t.Fatalf("code %d, ok %v, stderr %q", code, ok, errOut.String())
	}
	want := query.Question{
		Format: 1, Verb: query.Top, Patterns: []int64{0, 3, 5},
		Columns: []query.Column{{Name: "u", Slots: []int64{1, 2}}, {Name: "p", Slots: []int64{3}}},
		Filters: []query.Filter{{Column: "u", Value: "a=b"}, {Column: "p", Value: ""}},
		By:      "u", Limit: 7,
	}
	if !reflect.DeepEqual(a.question, want) || a.limits.Time != 2*time.Second || a.db != "x.db" {
		t.Errorf("question %+v\nlimits %+v, db %q\nwant %+v", a.question, a.limits, a.db, want)
	}
}

func TestParseQueryArgsReadsTheOtherFlagsOfTheOtherVerbs(t *testing.T) {
	var errOut bytes.Buffer
	for _, c := range []struct {
		args []string
		want query.Question
	}{
		{[]string{"--db", "x", "--buckets", "9", "buckets"}, query.Question{Format: 1, Verb: query.Buckets, Buckets: 9}},
		{[]string{"--db", "x", "rows", "--last", "--limit", "3"}, query.Question{Format: 1, Verb: query.Rows, Last: true, Limit: 3}},
		{[]string{"--db", "x", "--col", "u=1", "--by", "u", "--with", "u", "pairs"}, query.Question{Format: 1, Verb: query.Pairs,
			Columns: []query.Column{{Name: "u", Slots: []int64{1}}}, By: "u", With: "u"}},
		{[]string{"--db", "x", "where", "a b"}, query.Question{Format: 1, Verb: query.Where, Value: "a b"}},
		{[]string{"--db", "x", "patterns", "a", "b"}, query.Question{Format: 1, Verb: query.Patterns, Words: []string{"a", "b"}}},
		{[]string{"--db", "x", "--pattern", "4", "slots"}, query.Question{Format: 1, Verb: query.Slots, Patterns: []int64{4}}},
	} {
		a, code, ok := parseQueryArgs(Env{Stderr: &errOut}, c.args)
		if !ok || code != exitcode.OK || !reflect.DeepEqual(a.question, c.want) {
			t.Errorf("%v: ok %v, code %d, question %+v, want %+v (stderr %q)", c.args, ok, code, a.question, c.want, errOut.String())
		}
	}
}

func TestQueryReportsAWriteErrorOnStandardOutput(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	var e bytes.Buffer
	vars := map[string]string{"AIGOLOG_CACHE_DIR": h.cache}
	code := RunEnv(Env{
		Stdout: failingWriter{}, Stderr: &e,
		LookupEnv: func(k string) (string, bool) { v, ok := vars[k]; return v, ok },
		Now:       func() time.Time { return h.now },
	}, []string{"query", "--db", path, "--no-save", "count"})
	if code != exitcode.Failure || !strings.HasPrefix(e.String(), "aigolog: ") {
		t.Errorf("code %d, stderr %q", code, e.String())
	}
}

func TestQueryStopsWhenTheContextIsCancelled(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.ctx = ctx
	code, out, errOut := h.run("query", "--db", path, "count")
	if code != exitcode.Failure || out != "" || !strings.HasPrefix(errOut, "aigolog: ") {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	h.ctx = nil
	if _, shown, _ := h.run("show", "--db", path); !strings.Contains(shown, "answers: 0 stored\n") {
		t.Errorf("something was stored:\n%s", shown)
	}
}

func TestQueryAnswerOfAStoredQuestionThatCannotBeReadSaysSoAndStoresNothing(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	for _, question := range []string{"this is not a question", `{"format":1,"verb":"nonsense"}`, `{"format":9,"verb":"count"}`} {
		if _, err := store.SaveAnswer(context.Background(), path, question, "{}", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"A1", "A2", "A3"} {
		code, out, errOut := h.run("query", "--db", path, "--answer", id)
		if code != exitcode.Usage && code != exitcode.Failure {
			t.Errorf("%s: code %d", id, code)
		}
		if out != "" || !strings.HasPrefix(errOut, "aigolog: the stored question of "+id+" cannot be read: ") {
			t.Errorf("%s: code %d, stdout %q, stderr %q", id, code, out, errOut)
		}
	}
	code, out, _ := h.run("show", "--db", path)
	if code != exitcode.OK || !strings.Contains(out, "answers: 3 stored\n") {
		t.Errorf("show: code %d, output:\n%s", code, out)
	}
}

func TestQueryRunsTheBucketsVerbAndTheLastFlagOnADatabase(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)

	// Four records over three seconds, in three buckets of one second.
	b := ask(t, h, path, "--buckets", "3", "buckets")
	wantBuckets := [][]string{
		{"2026-01-05 10:00:01", "2026-01-05 10:00:02", "1"},
		{"2026-01-05 10:00:02", "2026-01-05 10:00:03", "1"},
		{"2026-01-05 10:00:03", "2026-01-05 10:00:04", "2"},
	}
	if !reflect.DeepEqual(b.Columns, []string{"start", "end", "rows"}) || !reflect.DeepEqual(b.Rows, wantBuckets) {
		t.Errorf("buckets: columns %v, rows %v; want rows %v", b.Columns, b.Rows, wantBuckets)
	}

	// --last takes the newest records first: the ids 4 and 3, not 1 and 2.
	first := ask(t, h, path, "rows", "--limit", "2")
	last := ask(t, h, path, "rows", "--last", "--limit", "2")
	ids := func(a query.Answer) []string {
		var out []string
		for _, r := range a.Rows {
			out = append(out, r[0])
		}
		return out
	}
	if got := ids(first); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Errorf("rows --limit 2: ids %v, want [1 2]", got)
	}
	if got := ids(last); !reflect.DeepEqual(got, []string{"4", "3"}) {
		t.Errorf("rows --last --limit 2: ids %v, want [4 3]", got)
	}

	// The text of the command shows the table and stores the answer.
	code, out, errOut := h.run("query", "--db", path, "--buckets", "3", "buckets")
	if code != exitcode.OK || errOut != "" ||
		!strings.Contains(out, "start\tend\trows\n2026-01-05 10:00:01\t2026-01-05 10:00:02\t1\n") ||
		!strings.Contains(out, "rows covered 4\n") {
		t.Errorf("text: code %d, stderr %q, output:\n%s", code, errOut, out)
	}
	if code, out, _ := h.run("show", "--db", path); code != exitcode.OK || !strings.Contains(out, "answer A1: buckets\n") {
		t.Errorf("show: code %d, output:\n%s", code, out)
	}
}
