// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ckir/aigolog/internal/query"
	"github.com/ckir/aigolog/internal/store"
)

// A user is named in a different slot by the two kinds of line of authBody, as
// in the sshd sample: "accepted for <user> port <n>" has it first, and
// "rejected port <n> for invalid user <user>" has it second. The two kinds
// have different token counts, so that they are two patterns.
func acceptedLine(user string, port int) string {
	return fmt.Sprintf("svc login accepted for %s port %d", user, port)
}

func rejectedLine(user string, port int) string {
	return fmt.Sprintf("svc login rejected port %d for invalid user %s", port, user)
}

// login is one line of a body: its kind, the user and the port.
type login struct {
	accepted bool
	user     string
	port     int
}

// authBody writes the lines of ls, one second apart, from t0; line n (from 1)
// is ls[n-1].
func authBody(ls []login) string {
	var b strings.Builder
	for i, l := range ls {
		if l.accepted {
			b.WriteString(withStamp(i, acceptedLine(l.user, l.port)))
		} else {
			b.WriteString(withStamp(i, rejectedLine(l.user, l.port)))
		}
	}
	return b.String()
}

// authLogins is the body most of the tests read: alice is named four times,
// twice in each pattern.
var authLogins = []login{
	{true, "alice", 22}, {true, "bob", 22}, {false, "alice", 80}, {true, "alice", 25},
	{false, "dave", 80}, {false, "alice", 22}, {true, "carol", 25}, {false, "bob", 80},
}

// authPatterns returns the number of the pattern of each kind of line.
func authPatterns(t *testing.T, d *store.DB) (accepted, rejected store.PatternStat) {
	t.Helper()
	ps, err := d.PatternStats(bg, 0)
	if err != nil {
		t.Fatal(err)
	}
	return patternWith(t, ps, "accepted"), patternWith(t, ps, "rejected")
}

// userSlot returns the number of the slot of the pattern that holds the user:
// the one slot that is not a number.
func userSlot(t *testing.T, d *store.DB, pattern int64) int64 {
	t.Helper()
	ss, err := d.SlotStats(bg, pattern, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []int64
	for _, s := range ss {
		if !s.Numeric {
			found = append(found, s.Slot)
		}
	}
	if len(found) != 1 {
		t.Fatalf("pattern %d has %d slots that are not numbers (%+v), want one", pattern, len(found), ss)
	}
	return found[0]
}

// acceptedColumn is the column "user" of a body that has only accepted lines.
func acceptedColumn(t *testing.T, d *store.DB) query.Column {
	t.Helper()
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) != 1 {
		t.Fatalf("patterns = %+v, %v; want one", ps, err)
	}
	return query.Column{Name: "user", Slots: []int64{userSlot(t, d, ps[0].ID)}}
}

// userColumn is the column "user" of both patterns of authBody.
func userColumn(t *testing.T, d *store.DB) (query.Column, int64, int64) {
	t.Helper()
	a, r := authPatterns(t, d)
	sa, sr := userSlot(t, d, a.ID), userSlot(t, d, r.ID)
	return query.Column{Name: "user", Slots: []int64{sa, sr}}, a.ID, r.ID
}

func ask(q query.Question) query.Question {
	q.Format = 1
	return q
}

func run(t *testing.T, d *store.DB, q query.Question, lim query.Limits) query.Answer {
	t.Helper()
	a, err := d.Run(bg, ask(q), lim)
	if err != nil {
		t.Fatalf("Run(%s) = %v", ask(q).Canonical(), err)
	}
	return a
}

func cells(a query.Answer) string {
	var rows []string
	for _, r := range a.Rows {
		rows = append(rows, strings.Join(r, "|"))
	}
	return strings.Join(rows, "\n")
}

func TestRunPatternsFindsPatternsByWords(t *testing.T) {
	var ls []login
	for i := 0; i < 5; i++ {
		ls = append(ls, login{true, fmt.Sprintf("u%c", 'a'+i), 22})
	}
	for i := 0; i < 3; i++ {
		ls = append(ls, login{false, fmt.Sprintf("u%c", 'a'+i), 22})
	}
	d := mined(t, authBody(ls))
	acc, rej := authPatterns(t, d)

	both := run(t, d, query.Question{Verb: query.Patterns, Words: []string{"login"}}, query.Limits{})
	if strings.Join(both.Columns, ",") != "pattern,records,text" {
		t.Errorf("columns = %v", both.Columns)
	}
	want := fmt.Sprintf("%d|5|%s\n%d|3|%s", acc.ID, acc.Text, rej.ID, rej.Text)
	if cells(both) != want || both.Total != 2 || both.Cut != "" {
		t.Errorf("one word: rows\n%s\ntotal %d cut %q; want\n%s\ntotal 2 cut \"\"", cells(both), both.Total, both.Cut, want)
	}

	// Both words are required.
	one := run(t, d, query.Question{Verb: query.Patterns, Words: []string{"login", "rejected"}}, query.Limits{})
	if want := fmt.Sprintf("%d|3|%s", rej.ID, rej.Text); cells(one) != want || one.Total != 1 {
		t.Errorf("two words: rows %q total %d, want %q and 1", cells(one), one.Total, want)
	}
	none := run(t, d, query.Question{Verb: query.Patterns, Words: []string{"login", "nowhere"}}, query.Limits{})
	if len(none.Rows) != 0 || none.Total != 0 || none.Cut != "" {
		t.Errorf("a word in no pattern: rows %v total %d cut %q, want none, 0 and \"\"", none.Rows, none.Total, none.Cut)
	}
	// The match is of the bytes: another case is another word.
	if c := run(t, d, query.Question{Verb: query.Patterns, Words: []string{"LOGIN"}}, query.Limits{}); c.Total != 0 {
		t.Errorf("LOGIN matched %d patterns", c.Total)
	}
}

func TestRunSlotsReturnsTheStatisticsOfOnePattern(t *testing.T) {
	d := mined(t, portBody([]string{"alice", "bob", "alice", "Zed"}, []int{22, 22, 80, 25}))
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) != 1 {
		t.Fatalf("patterns = %+v, %v", ps, err)
	}
	ss, err := d.SlotStats(bg, ps[0].ID, 0)
	if err != nil || len(ss) != 2 {
		t.Fatalf("slots = %+v, %v", ss, err)
	}
	a := run(t, d, query.Question{Verb: query.Slots, Patterns: []int64{ps[0].ID}}, query.Limits{})
	if strings.Join(a.Columns, ",") != "slot,pos,type,values,distinct,min,max,top" {
		t.Errorf("columns = %v", a.Columns)
	}
	want := fmt.Sprintf("%d|0|word|4|3|||alice (2), Zed (1), bob (1)\n%d|1|int|4|3|22|80|22 (2), 25 (1), 80 (1)", ss[0].Slot, ss[1].Slot)
	if cells(a) != want || a.Total != 2 {
		t.Errorf("rows\n%s\ntotal %d; want\n%s\ntotal 2", cells(a), a.Total, want)
	}
	if a.Facts.Rows != 0 || a.Facts.First != nil || a.Facts.Last != nil {
		t.Errorf("facts = %+v, want none: slots covers no record set", a.Facts)
	}
}

func TestRunCountCountsThePatternsAndTheFiltersTogether(t *testing.T) {
	d := mined(t, authBody(authLogins))
	col, accID, rejID := userColumn(t, d)
	infos, err := d.SourceInfos(bg)
	if err != nil || len(infos) != 1 {
		t.Fatalf("sources = %+v, %v", infos, err)
	}
	src := infos[0].ID

	all := run(t, d, query.Question{Verb: query.Count}, query.Limits{})
	if strings.Join(all.Columns, ",") != "rows" || cells(all) != "8" || all.Total != 1 || all.Facts.Rows != 8 {
		t.Errorf("count of everything: columns %v rows %q total %d facts %+v", all.Columns, cells(all), all.Total, all.Facts)
	}

	q := query.Question{
		Verb: query.Count, Columns: []query.Column{col},
		Filters: []query.Filter{{Column: "user", Value: "alice"}},
	}
	a := run(t, d, q, query.Limits{})
	// alice is on lines 1, 3, 4 and 6: the sum of two in each pattern.
	if cells(a) != "4" || a.Facts.Rows != 4 || a.Total != 1 {
		t.Errorf("count of alice = %q, facts %+v; want 4", cells(a), a.Facts)
	}
	f := a.Facts
	if !f.HasTimes || f.FirstNanos != t0.UnixNano() || f.LastNanos != t0.Add(5e9).UnixNano() {
		t.Errorf("times = %v %d %d, want the stamps of lines 1 and 6", f.HasTimes, f.FirstNanos, f.LastNanos)
	}
	if f.First == nil || *f.First != (query.Ref{Source: int64(src), Line: 1}) || f.Last == nil || *f.Last != (query.Ref{Source: int64(src), Line: 6}) {
		t.Errorf("first and last = %+v %+v, want lines 1 and 6 of source %d", f.First, f.Last, src)
	}
	if a.Question.Canonical() != ask(q).Canonical() {
		t.Errorf("the answer holds the question %s, want %s", a.Question.Canonical(), ask(q).Canonical())
	}

	// A pattern number restricts the set: alice in the accepted lines only.
	q.Patterns = []int64{accID}
	if a := run(t, d, q, query.Limits{}); cells(a) != "2" || a.Facts.Last == nil || a.Facts.Last.Line != 4 {
		t.Errorf("alice in pattern %d = %q, facts %+v; want 2 ending at line 4", accID, cells(a), a.Facts)
	}
	q.Patterns = []int64{rejID}
	if a := run(t, d, q, query.Limits{}); cells(a) != "2" || a.Facts.First == nil || a.Facts.First.Line != 3 {
		t.Errorf("alice in pattern %d = %q, facts %+v; want 2 starting at line 3", rejID, cells(a), a.Facts)
	}

	// Two filters are both required; a value nobody has gives an empty set
	// whose facts are empty.
	q.Patterns = nil
	q.Filters = append(q.Filters, query.Filter{Column: "user", Value: "bob"})
	e := run(t, d, q, query.Limits{})
	if cells(e) != "0" || e.Facts.Rows != 0 || e.Facts.HasTimes || e.Facts.FirstNanos != 0 || e.Facts.LastNanos != 0 || e.Facts.First != nil || e.Facts.Last != nil {
		t.Errorf("alice and bob at once = %q, facts %+v; want 0 and empty facts", cells(e), e.Facts)
	}
}

func TestRunTopCountsValuesOfAColumnOfSlotsOfSeveralPatterns(t *testing.T) {
	d := mined(t, authBody(authLogins))
	col, _, _ := userColumn(t, d)
	q := query.Question{Verb: query.Top, Columns: []query.Column{col}, By: "user"}
	a := run(t, d, q, query.Limits{})
	if strings.Join(a.Columns, ",") != "value,rows,share" {
		t.Errorf("columns = %v", a.Columns)
	}
	// alice 4, bob 2, then carol and dave with one each, by byte order.
	want := "alice|4|0.5000\nbob|2|0.2500\ncarol|1|0.1250\ndave|1|0.1250"
	if cells(a) != want || a.Total != 4 || a.Cut != "" || a.Facts.Rows != 8 {
		t.Errorf("rows\n%s\ntotal %d cut %q facts %+v; want\n%s\ntotal 4", cells(a), a.Total, a.Cut, a.Facts, want)
	}

	// The same column over the records of a filter on another column.
	port := query.Column{Name: "port", Slots: nil}
	for _, p := range []string{"accepted", "rejected"} {
		ps, _ := d.PatternStats(bg, 0)
		ss, err := d.SlotStats(bg, patternWith(t, ps, p).ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range ss {
			if s.Numeric {
				port.Slots = append(port.Slots, s.Slot)
			}
		}
	}
	q.Columns = append(q.Columns, port)
	q.Filters = []query.Filter{{Column: "port", Value: "80"}}
	b := run(t, d, q, query.Limits{})
	// Port 80 is on lines 3 (alice), 5 (dave) and 8 (bob).
	if want := "alice|1|0.3333\nbob|1|0.3333\ndave|1|0.3333"; cells(b) != want || b.Total != 3 || b.Facts.Rows != 3 {
		t.Errorf("rows\n%s\ntotal %d facts %+v; want\n%s", cells(b), b.Total, b.Facts, want)
	}
}

func TestAFilterValueThatIsSQLMatchesOnlyItself(t *testing.T) {
	ls := []login{{true, "alice", 22}, {true, "'OR1=1--", 22}, {true, "bob", 22}, {true, "alice", 22}}
	d := mined(t, authBody(ls))
	col := acceptedColumn(t, d)
	count := func(value string) query.Answer {
		return run(t, d, query.Question{Verb: query.Count, Columns: []query.Column{col},
			Filters: []query.Filter{{Column: "user", Value: value}}}, query.Limits{})
	}
	if got := count("'OR1=1--"); cells(got) != "1" || got.Facts.First == nil || got.Facts.First.Line != 2 {
		t.Errorf("the value that is SQL: %q, facts %+v; want one record, line 2", cells(got), got.Facts)
	}
	for _, v := range []string{"'OR2=2--", "' OR 1=1 --", "alice' OR '1'='1", "alice'--", "%", "al%", "_lice"} {
		if got := count(v); cells(got) != "0" || got.Facts.Rows != 0 {
			t.Errorf("the value %q matched %q records, want none", v, cells(got))
		}
	}
	// The same through the verb that reads values: nothing was run as SQL, the
	// table is whole.
	top := run(t, d, query.Question{Verb: query.Top, Columns: []query.Column{col}, By: "user",
		Filters: []query.Filter{{Column: "user", Value: "x'; DROP TABLE slot_values; --"}}}, query.Limits{})
	if len(top.Rows) != 0 {
		t.Errorf("top rows = %v", top.Rows)
	}
	if got := count("alice"); cells(got) != "2" {
		t.Errorf("alice = %q after the attempts, want 2", cells(got))
	}
}

func TestAColumnWithAnUnknownSlotIsAUsageErrorThatNamesTheSlot(t *testing.T) {
	d := mined(t, authBody(authLogins))
	col, _, _ := userColumn(t, d)
	col.Slots = append(col.Slots, 99999)
	for _, q := range []query.Question{
		{Verb: query.Top, Columns: []query.Column{col}, By: "user"},
		{Verb: query.Count, Columns: []query.Column{col}}, // a column that is defined and not used is checked too
	} {
		_, err := d.Run(bg, ask(q), query.Limits{})
		var ue *query.UsageError
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "slot 99999 does not exist") {
			t.Errorf("Run(%s) = %v, want a UsageError that says slot 99999 does not exist", ask(q).Verb, err)
		}
	}
}

func TestAPatternThatDoesNotExistIsAUsageErrorThatNamesIt(t *testing.T) {
	d := mined(t, authBody(authLogins))
	for _, q := range []query.Question{
		{Verb: query.Count, Patterns: []int64{7777}},
		{Verb: query.Slots, Patterns: []int64{7777}},
	} {
		_, err := d.Run(bg, ask(q), query.Limits{})
		var ue *query.UsageError
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "pattern 7777 does not exist") {
			t.Errorf("Run(%s) = %v, want a UsageError that says pattern 7777 does not exist", ask(q).Verb, err)
		}
	}
	// The catch-all, number 0, is a pattern that exists.
	if a := run(t, d, query.Question{Verb: query.Count, Patterns: []int64{0}}, query.Limits{}); cells(a) != "0" {
		t.Errorf("count in the catch-all = %q, want 0", cells(a))
	}
	// A question that is invalid by the rules of the model is refused before
	// the database is read, with the error of the model.
	_, err := d.Run(bg, query.Question{Format: 2, Verb: query.Count}, query.Limits{})
	var ue *query.UsageError
	if !errors.As(err, &ue) || !strings.HasPrefix(ue.Msg, "format") {
		t.Errorf("Run of format 2 = %v, want a UsageError on format", err)
	}
}

// fiveUsers is a body with the five users u1 to u5, named 5, 4, 3, 2 and 1 times.
func fiveUsers() []login {
	var ls []login
	for u := 1; u <= 5; u++ {
		for n := 0; n < 6-u; n++ {
			ls = append(ls, login{true, fmt.Sprintf("u%d", u), 22})
		}
	}
	return ls
}

func TestRunCutsRowsAndSaysSo(t *testing.T) {
	d := mined(t, authBody(fiveUsers()))
	col := acceptedColumn(t, d)
	q := query.Question{Verb: query.Top, Columns: []query.Column{col}, By: "user"}

	a := run(t, d, q, query.Limits{Rows: 2})
	if len(a.Rows) != 2 || a.Cut != "rows" || a.Total != 5 || a.Rows[0][0] != "u1" || a.Rows[1][0] != "u2" {
		t.Errorf("Rows 2: rows %v cut %q total %d; want u1 and u2, \"rows\" and 5", a.Rows, a.Cut, a.Total)
	}
	// The limit of the question cuts as well, when it is the smaller.
	q.Limit = 3
	if b := run(t, d, q, query.Limits{Rows: 10}); len(b.Rows) != 3 || b.Cut != "rows" || b.Total != 5 {
		t.Errorf("Limit 3: %d rows, cut %q, total %d; want 3, \"rows\" and 5", len(b.Rows), b.Cut, b.Total)
	}
	if b := run(t, d, q, query.Limits{Rows: 2}); len(b.Rows) != 2 || b.Cut != "rows" {
		t.Errorf("Limit 3, Rows 2: %d rows, cut %q; want 2 and \"rows\"", len(b.Rows), b.Cut)
	}
	// Nothing is cut when everything fits.
	q.Limit = 5
	if b := run(t, d, q, query.Limits{Rows: 5}); len(b.Rows) != 5 || b.Cut != "" || b.Total != 5 {
		t.Errorf("exactly 5: %d rows, cut %q, total %d; want 5, \"\" and 5", len(b.Rows), b.Cut, b.Total)
	}
	// The facts are those of the set, not of the rows that are shown.
	if a.Facts.Rows != 15 {
		t.Errorf("facts rows = %d, want 15", a.Facts.Rows)
	}
}

func TestRunCutsBytesAndSaysSo(t *testing.T) {
	d := mined(t, authBody(fiveUsers()))
	col := acceptedColumn(t, d)
	q := query.Question{Verb: query.Top, Columns: []query.Column{col}, By: "user"}
	full := run(t, d, q, query.Limits{})
	if len(full.Rows) != 5 {
		t.Fatalf("%d rows, want 5", len(full.Rows))
	}
	size := func(rows [][]string) int {
		n := 0
		for _, r := range rows {
			for _, c := range r {
				n += len(c)
			}
		}
		return n
	}
	three := size(full.Rows[:3])
	a := run(t, d, q, query.Limits{Bytes: three})
	if len(a.Rows) != 3 || a.Cut != "bytes" || a.Total != 5 {
		t.Errorf("Bytes %d: %d rows, cut %q, total %d; want 3, \"bytes\" and 5", three, len(a.Rows), a.Cut, a.Total)
	}
	// One byte less drops one more row.
	if b := run(t, d, q, query.Limits{Bytes: three - 1}); len(b.Rows) != 2 || b.Cut != "bytes" {
		t.Errorf("Bytes %d: %d rows, cut %q; want 2 and \"bytes\"", three-1, len(b.Rows), b.Cut)
	}
	// A cap that nothing reaches does not cut; the rows cap wins when both apply.
	if b := run(t, d, q, query.Limits{Bytes: size(full.Rows)}); len(b.Rows) != 5 || b.Cut != "" {
		t.Errorf("Bytes of the whole: %d rows, cut %q; want 5 and \"\"", len(b.Rows), b.Cut)
	}
	if b := run(t, d, q, query.Limits{Rows: 4, Bytes: three}); len(b.Rows) != 3 || b.Cut != "rows" {
		t.Errorf("Rows 4 and Bytes %d: %d rows, cut %q; want 3 and \"rows\"", three, len(b.Rows), b.Cut)
	}
}

func TestRunCutsEachCellAtTheCellLimitOnACharacterBoundary(t *testing.T) {
	long := strings.Repeat("€", 200) // 600 bytes, three for each character
	d := mined(t, authBody([]login{{true, long, 22}, {true, "bob", 22}, {true, long, 22}}))
	col := acceptedColumn(t, d)
	a := run(t, d, query.Question{Verb: query.Top, Columns: []query.Column{col}, By: "user"}, query.Limits{})
	if len(a.Rows) != 2 {
		t.Fatalf("rows = %v, want the long value and bob", a.Rows)
	}
	got := a.Rows[0][0]
	if want := strings.Repeat("€", 170); got != want || !utf8.ValidString(got) || len(got) > query.MaxCellBytes {
		t.Errorf("the long value is %d bytes (valid %v), want 170 characters of 3 bytes", len(got), utf8.ValidString(got))
	}
	if a.Rows[0][1] != "2" || a.Total != 2 {
		t.Errorf("rows %v total %d: the count is of the whole value", a.Rows[0], a.Total)
	}
}

func TestRunNeverGivesNilRowsOrColumns(t *testing.T) {
	d := mined(t, authBody(authLogins))
	a := run(t, d, query.Question{Verb: query.Patterns, Words: []string{"nowhere"}}, query.Limits{})
	if a.Rows == nil || a.Columns == nil {
		t.Fatalf("rows %#v, columns %#v: both must be non-nil", a.Rows, a.Columns)
	}
	out, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"rows":[]`) || strings.Contains(string(out), "null") {
		t.Errorf("the answer encodes as %s, want \"rows\":[] and no null", out)
	}
	// A cut that drops every row leaves an empty list, not nil.
	b := run(t, d, query.Question{Verb: query.Patterns, Words: []string{"login"}}, query.Limits{Bytes: 1})
	if b.Rows == nil || len(b.Rows) != 0 || b.Cut != "bytes" || b.Total != 2 {
		t.Errorf("Bytes 1: rows %#v cut %q total %d; want [] \"bytes\" 2", b.Rows, b.Cut, b.Total)
	}
}

func TestRunAnswersOnADatabaseThatWasOpenedForReading(t *testing.T) {
	d := mined(t, authBody(authLogins))
	path := d.Path()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	o, err := store.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	col, _, _ := userColumn(t, o)
	a := run(t, o, query.Question{Verb: query.Top, Columns: []query.Column{col}, By: "user", Limit: 1}, query.Limits{})
	if cells(a) != "alice|4|0.5000" || a.Total != 4 || a.Cut != "rows" {
		t.Errorf("top on an opened database = %q total %d cut %q", cells(a), a.Total, a.Cut)
	}
}

func TestRunAfterCloseReturnsErrClosed(t *testing.T) {
	d := mined(t, authBody(authLogins))
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := d.Run(bg, ask(query.Question{Verb: query.Count}), query.DefaultLimits())
	if !errors.Is(err, store.ErrClosed) {
		t.Errorf("Run after Close = %v, want ErrClosed", err)
	}
	if a.Rows != nil || a.Facts.Rows != 0 {
		t.Errorf("answer = %+v, want the zero value", a)
	}
}

func TestRunTopCountsARecordOnceWhenTwoSlotsOfTheColumnHoldTheValue(t *testing.T) {
	d := mined(t, authBody([]login{{true, "alice", 22}, {true, "22", 22}, {true, "bob", 22}}))
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) != 1 {
		t.Fatalf("patterns = %+v, %v; want one", ps, err)
	}
	ss, err := d.SlotStats(bg, ps[0].ID, 0)
	if err != nil || len(ss) != 2 {
		t.Fatalf("slots = %+v, %v", ss, err)
	}
	col := query.Column{Name: "both", Slots: []int64{ss[0].Slot, ss[1].Slot}}
	a := run(t, d, query.Question{Verb: query.Top, Columns: []query.Column{col}, By: "both"}, query.Limits{})
	// The record "22 port 22" has the value 22 twice and counts once.
	if want := "22|3|1.0000\nalice|1|0.3333\nbob|1|0.3333"; cells(a) != want || a.Total != 3 {
		t.Errorf("rows\n%s\ntotal %d; want\n%s\ntotal 3", cells(a), a.Total, want)
	}
}

func TestRunTopPutsTheValueWithTheHigherCountFirstWhateverItsByteOrder(t *testing.T) {
	// zed sorts after abe and is named more often: the count comes before the
	// value in the order.
	d := mined(t, authBody([]login{{true, "zed", 22}, {true, "abe", 22}, {true, "zed", 22}, {true, "zed", 22}}))
	col := acceptedColumn(t, d)
	a := run(t, d, query.Question{Verb: query.Top, Columns: []query.Column{col}, By: "user"}, query.Limits{})
	if want := "zed|3|0.7500\nabe|1|0.2500"; cells(a) != want {
		t.Errorf("rows\n%s\nwant\n%s", cells(a), want)
	}
}

func TestRunPatternsPutsThePatternWithMoreRecordsFirstWhateverItsID(t *testing.T) {
	// The rejected line comes first in the body, so that its pattern has the
	// lower id; the accepted pattern has more records.
	ls := []login{{false, "alice", 80}, {true, "alice", 22}, {true, "bob", 22}, {true, "carol", 22}, {true, "dave", 25}}
	d := mined(t, authBody(ls))
	acc, rej := authPatterns(t, d)
	if acc.ID <= rej.ID {
		t.Fatalf("the accepted pattern has id %d, the rejected one %d: the test needs the first line's pattern to have the lower id", acc.ID, rej.ID)
	}
	a := run(t, d, query.Question{Verb: query.Patterns, Words: []string{"login"}}, query.Limits{})
	if want := fmt.Sprintf("%d|4|%s\n%d|1|%s", acc.ID, acc.Text, rej.ID, rej.Text); cells(a) != want {
		t.Errorf("rows\n%s\nwant\n%s", cells(a), want)
	}
}

func TestACancelledContextReturnsItsError(t *testing.T) {
	d := mined(t, authBody(authLogins))
	ctx, cancel := context.WithCancel(bg)
	cancel()
	a, err := d.Run(ctx, ask(query.Question{Verb: query.Count}), query.Limits{Time: time.Minute})
	if !errors.Is(err, context.Canceled) || errors.Is(err, query.ErrTimeLimit) {
		t.Errorf("Run with a cancelled context = %v, want context.Canceled and not ErrTimeLimit", err)
	}
	if !reflect.DeepEqual(a, query.Answer{}) {
		t.Errorf("answer = %+v, want the zero value", a)
	}
}

func TestAQuestionWhoseOwnTimeIsOverGivesErrTimeLimitAndNoFigures(t *testing.T) {
	d := mined(t, authBody(authLogins))
	// A nanosecond is over before the first statement is stepped.
	a, err := d.Run(bg, ask(query.Question{Verb: query.Count}), query.Limits{Time: time.Nanosecond})
	if !errors.Is(err, query.ErrTimeLimit) {
		t.Errorf("Run with Time 1ns = %v, want ErrTimeLimit", err)
	}
	if !reflect.DeepEqual(a, query.Answer{}) {
		t.Errorf("answer = %+v, want the zero value", a)
	}
	// The caller's own deadline is not the limit of the question: its error
	// is returned as it is.
	ctx, cancel := context.WithTimeout(bg, time.Nanosecond)
	defer cancel()
	if _, err := d.Run(ctx, ask(query.Question{Verb: query.Count}), query.Limits{Time: time.Minute}); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, query.ErrTimeLimit) {
		t.Errorf("Run past the deadline of the caller = %v, want context.DeadlineExceeded", err)
	}
}
