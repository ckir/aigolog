// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store_test

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/ingest"
	"github.com/ckir/aigolog/internal/miner"
	"github.com/ckir/aigolog/internal/query"
	"github.com/ckir/aigolog/internal/record"
	"github.com/ckir/aigolog/internal/store"
)

// minedSources runs ingest over several inputs, source i named "s<i+1>.log".
func minedSources(t *testing.T, bodies ...string) *store.DB {
	t.Helper()
	d := newStore(t)
	var in []ingest.Input
	for i, b := range bodies {
		in = append(in, ingest.Input{Name: fmt.Sprintf("s%d.log", i+1), R: strings.NewReader(b)})
	}
	_, err := ingest.Run(bg, d, in, ingest.Options{AsOf: asOf(), Now: time.Now(), Zone: time.UTC, Miner: miner.New(miner.DefaultConfig())})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// slotOfType returns the number of the slot of the pattern that has the type.
func slotOfType(t *testing.T, d *store.DB, pattern int64, typ string) int64 {
	t.Helper()
	ss, err := d.SlotStats(bg, pattern, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []int64
	for _, s := range ss {
		if s.Type == typ {
			found = append(found, s.Slot)
		}
	}
	if len(found) != 1 {
		t.Fatalf("pattern %d has %d slots of type %s (%+v), want one", pattern, len(found), typ, ss)
	}
	return found[0]
}

// try is one line of the body of the pairs tests: who tried to log in from
// which address, and whether it was accepted.
type try struct {
	accepted bool
	user, ip string
}

func tryBody(ts []try) string {
	var b strings.Builder
	for i, x := range ts {
		if x.accepted {
			b.WriteString(withStamp(i, fmt.Sprintf("svc auth accepted for %s from %s", x.user, x.ip)))
		} else {
			b.WriteString(withStamp(i, fmt.Sprintf("svc auth rejected for invalid user %s from %s", x.user, x.ip)))
		}
	}
	return b.String()
}

// tries is a body of 36 lines over two patterns: the addresses 10.0.0.1 to
// 10.0.0.4, the users u0 to u6, and one line in three accepted.
func tries() []try {
	var ts []try
	for i := 0; i < 36; i++ {
		ts = append(ts, try{accepted: i%3 == 0, user: fmt.Sprintf("u%d", (i*i+i/4)%7), ip: fmt.Sprintf("10.0.0.%d", 1+(i*5+i/9)%4)})
	}
	return ts
}

// columnsOf returns the columns "ip" and "user" of the two patterns of tryBody.
func columnsOf(t *testing.T, d *store.DB) (ip, user query.Column) {
	t.Helper()
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) != 2 {
		t.Fatalf("patterns = %+v, %v; want two", ps, err)
	}
	ip, user = query.Column{Name: "ip"}, query.Column{Name: "user"}
	for _, p := range ps {
		ip.Slots = append(ip.Slots, slotOfType(t, d, p.ID, "ip4"))
		user.Slots = append(user.Slots, slotOfType(t, d, p.ID, "word"))
	}
	return ip, user
}

func TestRunPairsCountsRowsAndDistinctOtherValuesForEachValue(t *testing.T) {
	ts := tries()
	d := mined(t, tryBody(ts))
	ip, user := columnsOf(t, d)

	// What the body says: the lines of each address and the names tried.
	rowsOf := map[string]int{}
	namesOf := map[string]map[string]bool{}
	for _, x := range ts {
		rowsOf[x.ip]++
		if namesOf[x.ip] == nil {
			namesOf[x.ip] = map[string]bool{}
		}
		namesOf[x.ip][x.user] = true
	}
	ips := make([]string, 0, len(rowsOf))
	for k := range rowsOf {
		ips = append(ips, k)
	}
	sort.Slice(ips, func(i, j int) bool {
		if rowsOf[ips[i]] != rowsOf[ips[j]] {
			return rowsOf[ips[i]] > rowsOf[ips[j]]
		}
		return ips[i] < ips[j]
	})
	var want []string
	for _, k := range ips {
		want = append(want, fmt.Sprintf("%s|%d|%d", k, rowsOf[k], len(namesOf[k])))
	}

	q := query.Question{Verb: query.Pairs, Columns: []query.Column{ip, user}, By: "ip", With: "user"}
	a := run(t, d, q, query.Limits{})
	if strings.Join(a.Columns, ",") != "value,rows,distinct" {
		t.Errorf("columns = %v", a.Columns)
	}
	if cells(a) != strings.Join(want, "\n") || a.Total != int64(len(want)) || a.Cut != "" || a.Facts.Rows != int64(len(ts)) {
		t.Errorf("rows\n%s\ntotal %d cut %q facts %+v; want\n%s\ntotal %d", cells(a), a.Total, a.Cut, a.Facts, strings.Join(want, "\n"), len(want))
	}
	// The ties are by value, so that the two orders are told apart: at least two
	// addresses have the same count only when the body says so, and then the
	// lower address is first (the want above is sorted the same way).

	// A cut by the question's limit keeps the first and says how many there were.
	q.Limit = 2
	if c := run(t, d, q, query.Limits{}); cells(c) != strings.Join(want[:2], "\n") || c.Total != int64(len(want)) || c.Cut != "rows" {
		t.Errorf("limit 2: rows\n%s\ntotal %d cut %q", cells(c), c.Total, c.Cut)
	}

	// With a filter, the figures are those of the records of the set only.
	q.Limit = 0
	q.Filters = []query.Filter{{Column: "user", Value: "u1"}}
	f := run(t, d, q, query.Limits{})
	var fr int64
	for _, r := range f.Rows {
		n, _ := strconv.ParseInt(r[1], 10, 64)
		fr += n
		if r[2] != "1" {
			t.Errorf("row %v under the filter user=u1: distinct is %q, want 1 (only u1 is in those records)", r, r[2])
		}
	}
	var u1 int64
	for _, x := range ts {
		if x.user == "u1" {
			u1++
		}
	}
	if fr != u1 || f.Facts.Rows != u1 {
		t.Errorf("under the filter the rows add up to %d and the facts say %d records; the body has %d", fr, f.Facts.Rows, u1)
	}
}

func TestRunPairsRowsDoNotDoubleCountARecordWithTwoValuesInTheOtherColumn(t *testing.T) {
	// The record "22 port 22" holds the value 22 in both slots, and "alice
	// port 22" and "bob port 22" hold a different value in each: each record
	// has two values in the column "both".
	d := mined(t, authBody([]login{{true, "alice", 22}, {true, "22", 22}, {true, "bob", 22}}))
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) != 1 {
		t.Fatalf("patterns = %+v, %v; want one", ps, err)
	}
	ss, err := d.SlotStats(bg, ps[0].ID, 0)
	if err != nil || len(ss) != 2 {
		t.Fatalf("slots = %+v, %v", ss, err)
	}
	by := query.Column{Name: "user", Slots: []int64{ss[0].Slot}}
	both := query.Column{Name: "both", Slots: []int64{ss[0].Slot, ss[1].Slot}}
	a := run(t, d, query.Question{Verb: query.Pairs, Columns: []query.Column{by, both}, By: "user", With: "both"}, query.Limits{})
	// A join row for each pair of slots would say 2 records for "22".
	if want := "22|1|1\nalice|1|2\nbob|1|2"; cells(a) != want || a.Total != 3 {
		t.Errorf("rows\n%s\ntotal %d; want\n%s\ntotal 3", cells(a), a.Total, want)
	}
}

func TestRunPairsKeepsAValueWhoseRecordsHaveNoValueInTheOtherColumn(t *testing.T) {
	d := mined(t, authBody(authLogins))
	user, accID, rejID := userColumn(t, d)
	_ = accID
	// The column "port" is the port of the rejected pattern only: carol is
	// named in accepted lines only, and has no port in the column.
	port := query.Column{Name: "port", Slots: []int64{slotOfType(t, d, rejID, "int")}}
	a := run(t, d, query.Question{Verb: query.Pairs, Columns: []query.Column{user, port}, By: "user", With: "port"}, query.Limits{})
	// alice: lines 3 and 6 have port 80 and 22; bob: line 8 has 80; dave: line 5
	// has 80; carol has none. The ties at one row are by value.
	if want := "alice|4|2\nbob|2|1\ncarol|1|0\ndave|1|1"; cells(a) != want || a.Total != 4 {
		t.Errorf("rows\n%s\ntotal %d; want\n%s\ntotal 4", cells(a), a.Total, want)
	}
}

func TestRunBucketsCoversEveryTimedRecordOnce(t *testing.T) {
	// 8 lines one second apart, then 3 records of another source that has no
	// time.
	untimed := "svc login accepted for zed port 22\nsvc login accepted for zed port 25\nsvc login accepted for zed port 80\n"
	d := minedSources(t, authBody(authLogins), untimed)

	a := run(t, d, query.Question{Verb: query.Buckets, Buckets: 7}, query.Limits{})
	if strings.Join(a.Columns, ",") != "start,end,rows" {
		t.Errorf("columns = %v", a.Columns)
	}
	// The span is 7 seconds: 7 intervals of one second; the last is closed, so
	// that it holds the second 6 and the greatest time, the second 7.
	var want []string
	for i := 0; i < 7; i++ {
		n := 1
		if i == 6 {
			n = 2
		}
		want = append(want, fmt.Sprintf("%s|%s|%d", stampAt(i), stampAt(i+1), n))
	}
	if cells(a) != strings.Join(want, "\n") || a.Total != 7 || a.Cut != "" {
		t.Errorf("rows\n%s\ntotal %d cut %q; want\n%s\ntotal 7", cells(a), a.Total, a.Cut, strings.Join(want, "\n"))
	}
	f := a.Facts
	if f.Rows != 11 || f.NoTime != 3 || !f.HasTimes || f.FirstNanos != t0.UnixNano() || f.LastNanos != t0.Add(7e9).UnixNano() {
		t.Errorf("facts = %+v, want 11 records, 3 with no time, from %d to %d", f, t0.UnixNano(), t0.Add(7e9).UnixNano())
	}

	// 0 buckets is 24; whatever the number, each timed record is counted once,
	// the greatest time in the last interval.
	for _, n := range []int{0, 1, 2, 3, 5, 24, 200} {
		b := run(t, d, query.Question{Verb: query.Buckets, Buckets: n}, query.Limits{})
		wantN := n
		if n == 0 {
			wantN = 24
		}
		if len(b.Rows) != wantN || b.Total != int64(wantN) {
			t.Errorf("buckets %d: %d rows, total %d; want %d", n, len(b.Rows), b.Total, wantN)
			continue
		}
		sum := 0
		for _, r := range b.Rows {
			c, err := strconv.Atoi(r[2])
			if err != nil {
				t.Fatalf("buckets %d: row %v", n, r)
			}
			sum += c
		}
		if sum != 8 {
			t.Errorf("buckets %d: the rows add up to %d, want the 8 timed records", n, sum)
		}
	}
	// With 3 intervals of ceil(7s/3) the greatest time is in the last.
	c := run(t, d, query.Question{Verb: query.Buckets, Buckets: 3}, query.Limits{})
	if got := c.Rows[2][2]; got == "0" {
		t.Errorf("3 buckets: the last holds %s records, want the greatest time in it", got)
	}
}

func TestRunBucketsOfASetWhoseTimesAreAllEqualPutsEveryRecordInTheFirst(t *testing.T) {
	d := mined(t, strings.Repeat(withStamp(0, "svc login accepted for alice port 22"), 3))
	a := run(t, d, query.Question{Verb: query.Buckets, Buckets: 3}, query.Limits{})
	if len(a.Rows) != 3 || a.Rows[0][2] != "3" || a.Rows[1][2] != "0" || a.Rows[2][2] != "0" {
		t.Errorf("rows\n%s\nwant 3 buckets holding 3, 0 and 0", cells(a))
	}
	if a.Rows[0][0] != stampAt(0) || a.Facts.NoTime != 0 || a.Facts.Rows != 3 {
		t.Errorf("first start %q, facts %+v", a.Rows[0][0], a.Facts)
	}
}

func TestRunBucketsOfASetWithNoTimedRecordHasNoRows(t *testing.T) {
	d := minedSources(t, "svc login accepted for zed port 22\nsvc login accepted for zed port 25\n")
	a := run(t, d, query.Question{Verb: query.Buckets}, query.Limits{})
	if a.Rows == nil || len(a.Rows) != 0 || a.Total != 0 || a.Facts.HasTimes || a.Facts.Rows != 2 || a.Facts.NoTime != 2 {
		t.Errorf("rows %#v total %d facts %+v; want [] 0 and 2 records with no time", a.Rows, a.Total, a.Facts)
	}
	// A set with no record at all is the same.
	e := run(t, d, query.Question{Verb: query.Buckets, Patterns: []int64{0}}, query.Limits{})
	if e.Rows == nil || len(e.Rows) != 0 || e.Facts.Rows != 0 || e.Facts.NoTime != 0 {
		t.Errorf("empty set: rows %#v facts %+v", e.Rows, e.Facts)
	}
}

func TestRunBucketsOverTheRecordsOfAPatternAndAFilterOnly(t *testing.T) {
	d := mined(t, authBody(authLogins))
	col, accID, _ := userColumn(t, d)
	a := run(t, d, query.Question{Verb: query.Buckets, Buckets: 1, Patterns: []int64{accID}, Columns: []query.Column{col},
		Filters: []query.Filter{{Column: "user", Value: "alice"}}}, query.Limits{})
	// alice in the accepted lines: lines 1 and 4, seconds 0 and 3.
	if want := stampAt(0) + "|" + stampAt(3) + "|2"; cells(a) != want || a.Facts.Rows != 2 {
		t.Errorf("rows %q facts %+v, want %q", cells(a), a.Facts, want)
	}
}

func TestRunRowsGivesTheFirstOrTheLastRecordsInOrderWithTheirLineNumbers(t *testing.T) {
	// The file has a first line with no time.
	body := "preamble with no time\n" + authBody(authLogins)
	d := mined(t, body)
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")

	first := run(t, d, query.Question{Verb: query.Rows, Limit: 3}, query.Limits{})
	if strings.Join(first.Columns, ",") != "id,source,line,time,text" {
		t.Errorf("columns = %v", first.Columns)
	}
	if len(first.Rows) != 3 || first.Total != 9 || first.Cut != "rows" || first.Facts.Rows != 9 || first.Facts.NoTime != 1 {
		t.Fatalf("rows %v total %d cut %q facts %+v; want 3 rows of 9", first.Rows, first.Total, first.Cut, first.Facts)
	}
	checkRow := func(r []string, line int) {
		t.Helper()
		wantTime := ""
		if line > 1 {
			wantTime = stampAt(line - 2)
		}
		if r[1] != "test.log" || r[2] != strconv.Itoa(line) || r[3] != wantTime || r[4] != lines[line-1] {
			t.Errorf("row %v, want source test.log, line %d, time %q and text %q", r, line, wantTime, lines[line-1])
		}
	}
	for i, r := range first.Rows {
		checkRow(r, i+1)
	}
	// The ids rise with the reading order.
	id0, _ := strconv.ParseInt(first.Rows[0][0], 10, 64)
	id1, _ := strconv.ParseInt(first.Rows[1][0], 10, 64)
	if id1 <= id0 {
		t.Errorf("ids %d and %d: want them to rise", id0, id1)
	}

	last := run(t, d, query.Question{Verb: query.Rows, Last: true, Limit: 3}, query.Limits{})
	if len(last.Rows) != 3 || last.Total != 9 || last.Cut != "rows" {
		t.Fatalf("last: rows %v total %d cut %q", last.Rows, last.Total, last.Cut)
	}
	// The last three, the last of them first.
	for i, r := range last.Rows {
		checkRow(r, 9-i)
	}
	if got, _ := strconv.ParseInt(last.Rows[0][0], 10, 64); got <= id1 {
		t.Errorf("the id of the last record is %d", got)
	}

	// All of them when nothing cuts, and the limits of the call cut as well.
	all := run(t, d, query.Question{Verb: query.Rows}, query.Limits{})
	if len(all.Rows) != 9 || all.Cut != "" {
		t.Errorf("all: %d rows, cut %q", len(all.Rows), all.Cut)
	}
	if c := run(t, d, query.Question{Verb: query.Rows}, query.Limits{Rows: 2}); len(c.Rows) != 2 || c.Cut != "rows" || c.Total != 9 {
		t.Errorf("Rows 2: %d rows, cut %q, total %d", len(c.Rows), c.Cut, c.Total)
	}
}

func TestRunRowsOfAPatternAndAFilterAndTheSourceNames(t *testing.T) {
	d := minedSources(t, authBody(authLogins), withStamp(60, acceptedLine("alice", 22)))
	col, accID, _ := userColumn(t, d)
	a := run(t, d, query.Question{Verb: query.Rows, Patterns: []int64{accID}, Columns: []query.Column{col},
		Filters: []query.Filter{{Column: "user", Value: "alice"}}}, query.Limits{})
	// alice in the accepted lines: lines 1 and 4 of s1.log and line 1 of s2.log.
	want := []string{
		"s1.log|1|" + stampAt(0) + "|" + strings.TrimSuffix(withStamp(0, acceptedLine("alice", 22)), "\n"),
		"s1.log|4|" + stampAt(3) + "|" + strings.TrimSuffix(withStamp(3, acceptedLine("alice", 25)), "\n"),
		"s2.log|1|" + stampAt(60) + "|" + strings.TrimSuffix(withStamp(60, acceptedLine("alice", 22)), "\n"),
	}
	var got []string
	for _, r := range a.Rows {
		got = append(got, strings.Join(r[1:], "|"))
	}
	if !reflect.DeepEqual(got, want) || a.Total != 3 || a.Facts.Rows != 3 {
		t.Errorf("rows\n%s\ntotal %d; want\n%s", strings.Join(got, "\n"), a.Total, strings.Join(want, "\n"))
	}
}

func TestRunRowsCutsTheTextOfARecordOnACharacterBoundary(t *testing.T) {
	long := strings.Repeat("€", 200) // 600 bytes
	d := mined(t, withStamp(0, acceptedLine(long, 22)))
	a := run(t, d, query.Question{Verb: query.Rows}, query.Limits{})
	if len(a.Rows) != 1 {
		t.Fatalf("rows = %v", a.Rows)
	}
	txt := a.Rows[0][4]
	if len(txt) > query.MaxCellBytes || !strings.HasPrefix(txt, stampAt(0)+" svc login accepted for €") || !strings.HasSuffix(txt, "€") {
		t.Errorf("the text is %d bytes: %q", len(txt), txt)
	}
}

func TestRunWhereFindsAValueInEverySlotThatHoldsIt(t *testing.T) {
	d := mined(t, authBody(authLogins))
	_, accID, rejID := userColumn(t, d)
	ps, err := d.PatternStats(bg, 0)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[int64]string{}
	for _, p := range ps {
		texts[p.ID] = p.Text
	}
	accSlot, rejSlot := userSlot(t, d, accID), userSlot(t, d, rejID)
	posOf := func(slot int64, pattern int64) int {
		ss, _ := d.SlotStats(bg, pattern, 0)
		for _, s := range ss {
			if s.Slot == slot {
				return s.Pos
			}
		}
		t.Fatalf("slot %d is not in pattern %d", slot, pattern)
		return 0
	}

	a := run(t, d, query.Question{Verb: query.Where, Value: "alice"}, query.Limits{})
	if strings.Join(a.Columns, ",") != "pattern,slot,pos,type,rows,text" {
		t.Errorf("columns = %v", a.Columns)
	}
	// alice is in the user slot of each pattern, twice in each: the two rows
	// are equal in rows, and the lower slot is first.
	rows := []struct {
		pattern, slot int64
	}{{accID, accSlot}, {rejID, rejSlot}}
	sort.Slice(rows, func(i, j int) bool { return rows[i].slot < rows[j].slot })
	var want []string
	for _, r := range rows {
		want = append(want, fmt.Sprintf("%d|%d|%d|word|2|%s", r.pattern, r.slot, posOf(r.slot, r.pattern), texts[r.pattern]))
	}
	if cells(a) != strings.Join(want, "\n") || a.Total != 2 || a.Cut != "" {
		t.Errorf("rows\n%s\ntotal %d cut %q; want\n%s\ntotal 2", cells(a), a.Total, a.Cut, strings.Join(want, "\n"))
	}
	if a.Facts != (query.Facts{}) {
		t.Errorf("facts = %+v, want none: where covers no record set", a.Facts)
	}

	// 22 is the port of lines 1, 2 and 6 (accepted: 1, 2; rejected: 6) and
	// the busiest slot is first.
	b := run(t, d, query.Question{Verb: query.Where, Value: "22"}, query.Limits{})
	if len(b.Rows) != 2 || b.Rows[0][4] != "2" || b.Rows[1][4] != "1" || b.Rows[0][3] != "int" {
		t.Errorf("22: rows\n%s\nwant two int slots holding it in 2 and in 1 records", cells(b))
	}
	// A value nobody has: no rows, not nil.
	n := run(t, d, query.Question{Verb: query.Where, Value: "nobody"}, query.Limits{})
	if n.Rows == nil || len(n.Rows) != 0 || n.Total != 0 {
		t.Errorf("nobody: rows %#v total %d", n.Rows, n.Total)
	}
	// The limit cuts and says so.
	c := run(t, d, query.Question{Verb: query.Where, Value: "alice", Limit: 1}, query.Limits{})
	if len(c.Rows) != 1 || c.Total != 2 || c.Cut != "rows" {
		t.Errorf("limit 1: %d rows, total %d, cut %q", len(c.Rows), c.Total, c.Cut)
	}
}

func TestAWhereValueThatIsSQLMatchesOnlyItself(t *testing.T) {
	d := mined(t, authBody([]login{{true, "alice", 22}, {true, "bob", 22}, {true, "'OR1=1--", 22}}))
	for _, v := range []string{"' OR 1=1 --", "%", "al%", "x'; DROP TABLE slot_values; --"} {
		if a := run(t, d, query.Question{Verb: query.Where, Value: v}, query.Limits{}); len(a.Rows) != 0 {
			t.Errorf("the value %q matched %v", v, a.Rows)
		}
	}
	if a := run(t, d, query.Question{Verb: query.Where, Value: "'OR1=1--"}, query.Limits{}); len(a.Rows) != 1 || a.Rows[0][4] != "1" {
		t.Errorf("the value that is SQL: %v, want one slot with one record", a.Rows)
	}
	if a := run(t, d, query.Question{Verb: query.Where, Value: "alice"}, query.Limits{}); len(a.Rows) != 1 {
		t.Errorf("alice after the attempts: %v", a.Rows)
	}
}

// fillTimeTest appends the records from..to-1 of the database of the time
// limit test: one source (n), one pattern with two slots (s1, s2), a value
// in each; the values of the first slot are 1000 and those of the second 997
// apart, so that the two columns have many values and no two records share
// all of them.
func fillTimeTest(t *testing.T, d *store.DB, src int, s1, s2 int64, from, to int) {
	t.Helper()
	const chunk = 50_000
	for lo := from; lo < to; lo += chunk {
		hi := lo + chunk
		if hi > to {
			hi = to
		}
		recs := make([]record.Record, 0, hi-lo)
		as := make([]store.Assignment, 0, hi-lo)
		for i := lo; i < hi; i++ {
			recs = append(recs, record.Record{
				Source: src, Line: int64(i + 1), Offset: int64(i) * 40, Length: 40,
				Stamp: record.Stamp{State: record.Resolved, Nanos: t0.UnixNano() + int64(i)*1e9},
				Raw:   fmt.Sprintf("x %d", i),
			})
		}
		if err := d.Append(bg, recs); err != nil {
			t.Fatal(err)
		}
		// The ids of the records are the numbers of the lines: one source, from 1.
		for i := lo; i < hi; i++ {
			as = append(as, store.Assignment{Record: int64(i + 1), Pattern: 1, Slots: []int64{s1, s2},
				Values: []string{"a" + strconv.Itoa(i%1000), "b" + strconv.Itoa(i%997)}})
		}
		if err := d.Assign(bg, as); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.FinishMining(bg); err != nil {
		t.Fatal(err)
	}
}

func TestAQuestionThatRunsOutOfTimeGivesNoFiguresAndErrTimeLimit(t *testing.T) {
	started := time.Now()
	d := newStore(t)
	src, err := d.AddSource(bg, store.Source{Name: "big.log", Format: "text", Encoding: "utf-8", TimeKind: store.TimeWall})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := d.WritePatterns(bg, []store.PatternRow{{ID: 1, Text: "x <*>", Slots: []string{"word", "word"}}})
	if err != nil {
		t.Fatal(err)
	}
	s1, s2 := ids[1][0], ids[1][1]
	q := ask(query.Question{Verb: query.Pairs,
		Columns: []query.Column{{Name: "a", Slots: []int64{s1}}, {Name: "b", Slots: []int64{s2}}}, By: "a", With: "b"})

	size := 200_000
	fillTimeTest(t, d, src, s1, s2, 0, size)

	// The question is a good one: with room it is answered.
	if full, err := d.Run(bg, q, query.Limits{Time: time.Minute}); err != nil || full.Total != 1000 || len(full.Rows) != 1000 {
		t.Fatalf("the question without a short limit: %d rows, total %d, %v; want 1000 values", len(full.Rows), full.Total, err)
	}

	for {
		begin := time.Now()
		a, err := d.Run(bg, q, query.Limits{Time: time.Millisecond})
		took := time.Since(begin)
		if err == nil {
			// The machine finished the question in a millisecond: a larger table.
			if size >= 2_000_000 {
				t.Skipf("the question over %d records finished in %v, within a millisecond: cannot test the time limit here", size, took)
			}
			next := size * 2
			if next > 2_000_000 {
				next = 2_000_000
			}
			fillTimeTest(t, d, src, s1, s2, size, next)
			size = next
			continue
		}
		if !errors.Is(err, query.ErrTimeLimit) {
			t.Fatalf("Run with Time 1ms over %d records = %v, want ErrTimeLimit", size, err)
		}
		if !reflect.DeepEqual(a, query.Answer{}) {
			t.Errorf("answer = %+v, want the zero value", a)
		}
		if took > time.Second {
			t.Errorf("the question returned after %v, want within a second", took)
		}
		break
	}
	// The database is whole after the interruption.
	if a, err := d.Run(bg, ask(query.Question{Verb: query.Count}), query.Limits{}); err != nil || a.Facts.Rows != int64(size) {
		t.Errorf("count after the interruption = %+v, %v; want %d", a.Facts, err, size)
	}
	t.Logf("the test took %v (a duration note, not a measurement of the program), over %d records", time.Since(started), size)
}
