// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/query"
	"github.com/ckir/aigolog/internal/testlog"
)

// This file proves the acceptance test A3 of the design through the command
// query, as a person would use it, and holds the helpers that
// queries_truth_test.go shares. Nothing here knows a slot number: the patterns
// and the slots are found with the verbs patterns and slots, and the columns of
// the questions are built from what they say.

// logWay is a way for a log to reach analyze: the name of the way, and how it
// is given to the command.
type logWay struct {
	name string
	// feed puts the log where the way reads it from and returns the argument of
	// analyze that names it.
	feed func(h *harness, data []byte) string
}

var logWays = []logWay{
	{"file", func(h *harness, data []byte) string { return h.file("generated.log", string(data)) }},
	{"stdin", func(h *harness, data []byte) string { h.stdin = string(data); return "-" }},
}

// analyzeVia runs analyze, with the year of the logs that have none worked out
// from the end of 2026, and returns the path of the database it made.
func analyzeVia(t *testing.T, w logWay, data []byte) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	code, out, errOut := h.run("analyze", "--as-of", "2026-12-31", w.feed(h, data))
	if code != exitcode.OK {
		t.Fatalf("analyze: code %d, stderr %q", code, errOut)
	}
	m := databaseLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no database line in:\n%s", out)
	}
	return h, m[1]
}

// patternRow is a row of the answer of the verb patterns.
type patternRow struct {
	ID      int64
	Records int64
	Text    string
}

// slotRow is a row of the answer of the verb slots.
type slotRow struct {
	Slot, Pos        int64
	Type             string
	Values, Distinct int64
	Min, Max         string
}

func whole(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("%q is not a whole number", s)
	}
	return n
}

// cell returns the cell of the row that is under the column called name.
func cell(t *testing.T, a query.Answer, row []string, name string) string {
	t.Helper()
	for i, c := range a.Columns {
		if c == name && i < len(row) {
			return row[i]
		}
	}
	t.Fatalf("no column %q in %v (row %v)", name, a.Columns, row)
	return ""
}

// patternsWith runs patterns on the words: the patterns whose text holds them.
func patternsWith(t *testing.T, h *harness, db string, words ...string) []patternRow {
	t.Helper()
	a := ask(t, h, db, append([]string{"patterns"}, words...)...)
	var out []patternRow
	for _, r := range a.Rows {
		out = append(out, patternRow{
			ID:      whole(t, cell(t, a, r, "pattern")),
			Records: whole(t, cell(t, a, r, "records")),
			Text:    cell(t, a, r, "text"),
		})
	}
	return out
}

// patternSlots runs slots on a pattern.
func patternSlots(t *testing.T, h *harness, db string, pattern int64) []slotRow {
	t.Helper()
	a := ask(t, h, db, "--pattern", strconv.FormatInt(pattern, 10), "slots")
	var out []slotRow
	for _, r := range a.Rows {
		out = append(out, slotRow{
			Slot:     whole(t, cell(t, a, r, "slot")),
			Pos:      whole(t, cell(t, a, r, "pos")),
			Type:     cell(t, a, r, "type"),
			Values:   whole(t, cell(t, a, r, "values")),
			Distinct: whole(t, cell(t, a, r, "distinct")),
			Min:      cell(t, a, r, "min"),
			Max:      cell(t, a, r, "max"),
		})
	}
	return out
}

// columnOf returns the numbers of the slots of type typ of a pattern, in the
// order of the pattern.
func columnOf(t *testing.T, h *harness, db string, pattern int64, typ string) []int64 {
	t.Helper()
	var out []int64
	for _, s := range patternSlots(t, h, db, pattern) {
		if s.Type == typ {
			out = append(out, s.Slot)
		}
	}
	return out
}

// placeholder is a marker of the text of a pattern: <*>, <int>, <ip4>, ...
var placeholder = regexp.MustCompile(`<(?:\*|[a-z0-9]+)>`)

// slotBefore returns the number of the word slot of a pattern whose marker is
// the one that the regular expression places: the expression matches the text
// of the pattern from that marker, which is the first thing it matches. It
// returns 0 when the pattern has no such marker, for instance when the text
// holds the word itself. The slots come in the order of the markers.
func slotBefore(t *testing.T, h *harness, db string, p patternRow, from *regexp.Regexp) int64 {
	t.Helper()
	loc := from.FindStringIndex(p.Text)
	if loc == nil {
		return 0
	}
	pos := int64(len(placeholder.FindAllStringIndex(p.Text[:loc[0]], -1)))
	for _, s := range patternSlots(t, h, db, p.ID) {
		if s.Pos == pos {
			if s.Type != "word" {
				t.Fatalf("pattern %d, %q: the slot at position %d is of type %s, not word", p.ID, p.Text, pos, s.Type)
			}
			return s.Slot
		}
	}
	t.Fatalf("pattern %d, %q: no slot at position %d", p.ID, p.Text, pos)
	return 0
}

// userName is the marker that holds the name of a login: the one that comes
// just before " from <ip4>", after "for" or after "for invalid user".
var userName = regexp.MustCompile(`<\*> from <ip4>`)

// failedLogins is the view of the Failed password records through two columns:
// addr, the addresses, and names, the user names. The name sits in a different
// slot of each shape of the line, so each column is made of several slots
// (design 7.6), found here through patterns and slots.
type failedLogins struct {
	h        *harness
	db       string
	patterns []patternRow
	addr     []int64
	names    []int64
}

// findFailedLogins finds the patterns of "Failed password" and builds the
// columns. A pattern whose text holds the name of the user itself, as in
// "message repeated 5 times: [ Failed password for root from <ip4> ...]", has no
// slot for the name: it adds its address slot to addr and nothing to names.
func findFailedLogins(t *testing.T, h *harness, db string) failedLogins {
	t.Helper()
	f := failedLogins{h: h, db: db, patterns: patternsWith(t, h, db, "Failed", "password")}
	if len(f.patterns) == 0 {
		t.Fatalf("no pattern holds Failed password")
	}
	for _, p := range f.patterns {
		ips := columnOf(t, h, db, p.ID, "ip4")
		if len(ips) != 1 {
			t.Fatalf("pattern %d, %q: %d ip4 slots, want 1", p.ID, p.Text, len(ips))
		}
		f.addr = append(f.addr, ips...)
		if n := slotBefore(t, h, db, p, userName); n != 0 {
			f.names = append(f.names, n)
		}
	}
	if len(f.names) < 2 {
		t.Fatalf("%d patterns of Failed password have a slot for the name, want at least the two shapes (for, for invalid user): %+v", len(f.names), f.patterns)
	}
	return f
}

// ids is the value of --pattern for the patterns.
func ids(ps []patternRow) string {
	var s []string
	for _, p := range ps {
		s = append(s, strconv.FormatInt(p.ID, 10))
	}
	return strings.Join(s, ",")
}

// colArg is the value of --col for a column.
func colArg(name string, slots []int64) string {
	var s []string
	for _, n := range slots {
		s = append(s, strconv.FormatInt(n, 10))
	}
	return name + "=" + strings.Join(s, ",")
}

// ask runs query with the columns addr and names, and the arguments.
func (f failedLogins) ask(t *testing.T, args ...string) query.Answer {
	t.Helper()
	full := append([]string{"--col", colArg("addr", f.addr), "--col", colArg("names", f.names)}, args...)
	return ask(t, f.h, f.db, full...)
}

// row returns the row of the answer whose first cell is value, or nil.
func row(a query.Answer, value string) []string {
	for _, r := range a.Rows {
		if len(r) > 0 && r[0] == value {
			return r
		}
	}
	return nil
}

// lines returns the lines of the first and of the last record of an answer.
func lines(t *testing.T, a query.Answer) (first, last int64) {
	t.Helper()
	if a.Facts.First == nil || a.Facts.Last == nil {
		t.Fatalf("the answer has no first and last record: %+v", a.Facts)
	}
	return a.Facts.First.Line, a.Facts.Last.Line
}

// TestTheSshdSampleAnswersTheQuestionsOfA3 is the acceptance test A3 of the
// design, asked of the real sshd sample (LogHub OpenSSH_2k, 2,000 lines)
// through the command query, with the log given as a file and on standard
// input. It is skipped, with a notice, while the sample is not fetched: run
// "just fetch-openssh".
func TestTheSshdSampleAnswersTheQuestionsOfA3(t *testing.T) {
	data := testlog.OpenSSH2k(t)
	for _, w := range logWays {
		t.Run(w.name, func(t *testing.T) {
			h, db := analyzeVia(t, w, data)
			f := findFailedLogins(t, h, db)
			t.Logf("patterns of Failed password: %+v; --col %s --col %s", f.patterns, colArg("addr", f.addr), colArg("names", f.names))

			// 520 lines of Failed password.
			c := ask(t, h, db, "--pattern", ids(f.patterns), "count")
			if len(c.Rows) != 1 || c.Rows[0][0] != "520" || c.Facts.Rows != 520 {
				t.Errorf("count of the Failed password patterns: %v, covering %d rows; want 520", c.Rows, c.Facts.Rows)
			}

			// 23 addresses and 63 user names.
			if a := f.ask(t, "top", "--by", "addr", "--limit", "1"); a.Total != 23 {
				t.Errorf("top by addr has %d values, want 23", a.Total)
			}
			if a := f.ask(t, "top", "--by", "names", "--limit", "1"); a.Total != 63 {
				t.Errorf("top by names has %d values, want 63", a.Total)
			}

			// One accepted login, at line 956.
			acc := patternsWith(t, h, db, "Accepted")
			if len(acc) != 1 || acc[0].Records != 1 {
				t.Fatalf("the patterns of Accepted are %+v, want one, with one record", acc)
			}
			r := ask(t, h, db, "--pattern", ids(acc), "rows", "--limit", "1")
			if len(r.Rows) != 1 || cell(t, r, r.Rows[0], "line") != "956" {
				t.Errorf("the Accepted record is %v, want one, at line 956", r.Rows)
			}

			for _, c := range []struct {
				address     string
				rows        int64
				names       int64
				root        int64 // 0 when the design gives none
				first, last int64 // 0 when the design gives none
			}{
				{"183.62.140.253", 286, 10, 276, 1024, 1997},
				{"187.141.143.180", 80, 28, 0, 0, 0},
			} {
				who := "addr=" + c.address
				// where finds the address in the addr slots of the Failed
				// password patterns, and nowhere else among them.
				wh := ask(t, h, db, "where", c.address)
				inAddr := map[int64]bool{}
				for _, s := range f.addr {
					inAddr[s] = true
				}
				var found int64
				for _, wr := range wh.Rows {
					if inAddr[whole(t, cell(t, wh, wr, "slot"))] {
						found += whole(t, cell(t, wh, wr, "rows"))
					}
				}
				if found != c.rows {
					t.Errorf("%s: where finds %d rows in the addr slots, want %d (%v)", c.address, found, c.rows, wh.Rows)
				}

				a := f.ask(t, "--where", who, "count")
				if len(a.Rows) != 1 || a.Rows[0][0] != strconv.FormatInt(c.rows, 10) {
					t.Errorf("%s: count is %v, want %d", c.address, a.Rows, c.rows)
				}
				if c.first != 0 {
					if first, last := lines(t, a); first != c.first || last != c.last {
						t.Errorf("%s: first and last line %d and %d, want %d and %d", c.address, first, last, c.first, c.last)
					}
				}

				p := f.ask(t, "--where", who, "pairs", "--by", "addr", "--with", "names")
				pr := row(p, c.address)
				if pr == nil || cell(t, p, pr, "rows") != strconv.FormatInt(c.rows, 10) || cell(t, p, pr, "distinct") != strconv.FormatInt(c.names, 10) {
					t.Errorf("%s: pairs says %v (columns %v), want %d rows on %d names", c.address, p.Rows, p.Columns, c.rows, c.names)
				}

				if c.root != 0 {
					n := f.ask(t, "--where", who, "top", "--by", "names", "--limit", "100")
					nr := row(n, "root")
					if n.Total != c.names || nr == nil || cell(t, n, nr, "rows") != strconv.FormatInt(c.root, 10) {
						t.Errorf("%s: top by names has %d values and root is %v, want %d values and root on %d", c.address, n.Total, nr, c.names, c.root)
					}
				}
			}
		})
	}
}
