// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ckir/aigolog/internal/record"
)

// withRecords adds one source and appends the records to a new database.
func withRecords(t *testing.T, recs []record.Record) *DB {
	t.Helper()
	d := newDB(t)
	src, err := d.AddSource(ctx, Source{Name: "m.log", Format: "text", Encoding: "utf-8", TimeKind: TimeWall})
	if err != nil {
		t.Fatal(err)
	}
	for i := range recs {
		recs[i].Source = src
		recs[i].Line = int64(i + 1)
	}
	if err := d.Append(ctx, recs); err != nil {
		t.Fatal(err)
	}
	return d
}

func rawsOfLength(n, size int) []record.Record {
	recs := make([]record.Record, n)
	for i := range recs {
		recs[i].Raw = fmt.Sprintf("%02d", i) + strings.Repeat("x", size-2)
	}
	return recs
}

func TestWritePatternsGivesEachSlotAnIDInOrder(t *testing.T) {
	d := newDB(t)
	ids, err := d.WritePatterns(ctx, []PatternRow{
		{ID: 1, Text: "user <*> from <*>", Slots: []string{"word", "ip4"}},
		{ID: 2, Text: "started", Slots: nil},
		{ID: 3, Text: "took <*> ms", Slots: []string{"int"}},
		{ID: 0, Text: "<other>", CatchAll: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64][]int64{1: {1, 2}, 2: {}, 3: {3}, 0: {}}
	if len(ids) != len(want) {
		t.Fatalf("slot ids = %v, want %v", ids, want)
	}
	for p, w := range want {
		got, ok := ids[p]
		if !ok || len(got) != len(w) || (len(w) > 0 && !reflect.DeepEqual(got, w)) {
			t.Errorf("slot ids of pattern %d = %v (present %v), want %v", p, got, ok, w)
		}
	}
	rows, err := d.conn.QueryContext(ctx, `SELECT id, pattern, pos, type FROM slots ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id, pattern, pos int64
		var typ string
		if err := rows.Scan(&id, &pattern, &pos, &typ); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%d:%d:%d:%s", id, pattern, pos, typ))
	}
	if w := "1:1:0:word,2:1:1:ip4,3:3:0:int"; strings.Join(got, ",") != w {
		t.Errorf("slots = %v, want %s", got, w)
	}
	var text, slots string
	var catchAll int
	if err := d.conn.QueryRowContext(ctx, `SELECT text, slots, catch_all FROM patterns WHERE id = 1`).Scan(&text, &slots, &catchAll); err != nil ||
		text != "user <*> from <*>" || slots != "word,ip4" || catchAll != 0 {
		t.Errorf("pattern 1 = %q %q %d, %v", text, slots, catchAll, err)
	}
	if err := d.conn.QueryRowContext(ctx, `SELECT text, slots, catch_all FROM patterns WHERE id = 2`).Scan(&text, &slots, &catchAll); err != nil || slots != "" {
		t.Errorf("a pattern with no slots has slots %q, %v; want the empty string", slots, err)
	}
	if err := d.conn.QueryRowContext(ctx, `SELECT text, slots, catch_all FROM patterns WHERE id = 0`).Scan(&text, &slots, &catchAll); err != nil ||
		text != "<other>" || catchAll != 1 {
		t.Errorf("catch-all = %q %q %d, %v", text, slots, catchAll, err)
	}
}

func TestWritePatternsIsAllOrNothing(t *testing.T) {
	d := newDB(t)
	_, err := d.WritePatterns(ctx, []PatternRow{
		{ID: 1, Text: "a", Slots: []string{"int"}},
		{ID: 1, Text: "same id again", Slots: []string{"int"}},
	})
	if err == nil {
		t.Fatal("two patterns with one id were accepted")
	}
	var n int
	if err := d.conn.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM patterns) + (SELECT COUNT(*) FROM slots)`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d rows were left by a failed WritePatterns, %v; want none", n, err)
	}
}

func TestRecordTextsReadsInIDOrderAndStopsAtTheByteCap(t *testing.T) {
	recs := rawsOfLength(5, 100)
	recs[1].Stamp.Len = 7
	d := withRecords(t, recs)
	first, err := d.RecordTexts(ctx, 0, 100, 250)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 {
		t.Fatalf("first call returned %d records, want 3 (100+100+100 reaches 250)", len(first))
	}
	for i, r := range first {
		if r.ID != int64(i+1) || r.Raw != recs[i].Raw || r.Source != 1 {
			t.Errorf("record %d = %+v", i, r)
		}
	}
	if first[1].Len != 7 || first[0].Len != 0 {
		t.Errorf("lengths = %d, %d; want the stored t_len, 0 and 7", first[0].Len, first[1].Len)
	}
	second, err := d.RecordTexts(ctx, first[len(first)-1].ID, 100, 250)
	if err != nil || len(second) != 2 || second[0].ID != 4 || second[1].ID != 5 {
		t.Fatalf("second call = %+v, %v; want records 4 and 5", second, err)
	}
	none, err := d.RecordTexts(ctx, 5, 100, 250)
	if err != nil || len(none) != 0 {
		t.Errorf("after the last record: %+v, %v; want none", none, err)
	}
	limited, err := d.RecordTexts(ctx, 0, 2, 1<<20)
	if err != nil || len(limited) != 2 {
		t.Errorf("limit 2 returned %d records, %v", len(limited), err)
	}
}

func TestRecordTextsReturnsOneRecordLargerThanTheCap(t *testing.T) {
	recs := rawsOfLength(3, 100)
	recs[0].Raw = strings.Repeat("y", 5000)
	d := withRecords(t, recs)
	got, err := d.RecordTexts(ctx, 0, 100, 10)
	if err != nil || len(got) != 1 || len(got[0].Raw) != 5000 {
		t.Fatalf("got %d records, %v; want the one large record", len(got), err)
	}
}

func TestAssignSetsThePatternAndStoresTheValuesInOneTransaction(t *testing.T) {
	d := withRecords(t, rawsOfLength(3, 20))
	slotIDs, err := d.WritePatterns(ctx, []PatternRow{
		{ID: 1, Text: "p", Slots: []string{"word", "int"}},
		{ID: 0, Text: "<other>", CatchAll: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	as := []Assignment{
		{Record: 1, Pattern: 1, Slots: slotIDs[1], Values: []string{"alice", "42"}},
		{Record: 2, Pattern: 1, Slots: slotIDs[1], Values: []string{"bob", "7"}},
		{Record: 3, Pattern: 0},
	}
	if err := d.Assign(ctx, as); err != nil {
		t.Fatal(err)
	}
	var p1, p2, p3 int64
	if err := d.conn.QueryRowContext(ctx, `SELECT (SELECT pattern FROM records WHERE id = 1), (SELECT pattern FROM records WHERE id = 2), (SELECT pattern FROM records WHERE id = 3)`).Scan(&p1, &p2, &p3); err != nil || p1 != 1 || p2 != 1 || p3 != 0 {
		t.Errorf("patterns = %d %d %d, %v", p1, p2, p3, err)
	}
	var got string
	if err := d.conn.QueryRowContext(ctx, `SELECT group_concat(record || ':' || slot || '=' || value, ' ') FROM (SELECT * FROM slot_values ORDER BY record, slot)`).Scan(&got); err != nil ||
		got != "1:1=alice 1:2=42 2:1=bob 2:2=7" {
		t.Errorf("values = %q, %v", got, err)
	}
}

func TestAssignWithAnAlreadyCancelledContextWritesNothing(t *testing.T) {
	d := withRecords(t, rawsOfLength(2, 20))
	slotIDs, err := d.WritePatterns(ctx, []PatternRow{{ID: 1, Text: "p", Slots: []string{"word"}}})
	if err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	err = d.Assign(cctx, []Assignment{{Record: 1, Pattern: 1, Slots: slotIDs[1], Values: []string{"a"}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context's error", err)
	}
	var n int
	if err := d.conn.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM slot_values) + (SELECT COUNT(*) FROM records WHERE pattern <> 0)`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d rows were written under a cancelled context, %v", n, err)
	}
}

func TestAssignRefusesValuesThatDoNotMatchTheSlotsAndWritesNothing(t *testing.T) {
	d := withRecords(t, rawsOfLength(2, 20))
	slotIDs, _ := d.WritePatterns(ctx, []PatternRow{{ID: 1, Text: "p", Slots: []string{"word", "int"}}})
	err := d.Assign(ctx, []Assignment{
		{Record: 1, Pattern: 1, Slots: slotIDs[1], Values: []string{"a", "1"}},
		{Record: 2, Pattern: 1, Slots: slotIDs[1], Values: []string{"only one"}},
	})
	if err == nil {
		t.Fatal("an assignment with one value for two slots was accepted")
	}
	var n int
	if err := d.conn.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM slot_values) + (SELECT COUNT(*) FROM records WHERE pattern <> 0)`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d rows were left by a failed Assign, %v; want none (one transaction)", n, err)
	}
}

func TestAValueWithSQLInItIsStoredAndTheTablesSurvive(t *testing.T) {
	d := withRecords(t, rawsOfLength(2, 20))
	slotIDs, err := d.WritePatterns(ctx, []PatternRow{{ID: 1, Text: "p", Slots: []string{"word"}}})
	if err != nil {
		t.Fatal(err)
	}
	evil := `'); DROP TABLE records;--`
	if err := d.Assign(ctx, []Assignment{{Record: 1, Pattern: 1, Slots: slotIDs[1], Values: []string{evil}}}); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := d.conn.QueryRowContext(ctx, `SELECT value FROM slot_values WHERE record = 1`).Scan(&got); err != nil || got != evil {
		t.Errorf("value = %q, %v; want %q", got, err, evil)
	}
	var n int
	if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM records`).Scan(&n); err != nil || n != 2 {
		t.Errorf("records has %d rows, %v; want 2", n, err)
	}
}

func TestFinishMiningCountsRecordsAndTimesAndMakesTheIndexes(t *testing.T) {
	recs := make([]record.Record, 6)
	for i := range recs {
		recs[i].Raw = fmt.Sprintf("r%d", i)
	}
	// Pattern 1: resolved times 300 and 100, and one record whose time is not
	// resolved (its t_nanos must not count). Pattern 2: no resolved time.
	// Pattern 0 (the catch-all): one resolved time.
	recs[0].Stamp = record.Stamp{State: record.Resolved, Nanos: 300}
	recs[1].Stamp = record.Stamp{State: record.Resolved, Nanos: 100}
	recs[2].Stamp = record.Stamp{State: record.NoStamp, Nanos: 5}
	recs[3].Stamp = record.Stamp{State: record.NoStamp}
	recs[4].Stamp = record.Stamp{State: record.Resolved, Nanos: 900}
	recs[5].Stamp = record.Stamp{State: record.NoStamp, Nanos: 1}
	d := withRecords(t, recs)
	slotIDs, err := d.WritePatterns(ctx, []PatternRow{
		{ID: 1, Text: "a", Slots: []string{"int"}},
		{ID: 2, Text: "b"},
		{ID: 3, Text: "never used"},
		{ID: 0, Text: "<other>", CatchAll: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Assign(ctx, []Assignment{
		{Record: 1, Pattern: 1, Slots: slotIDs[1], Values: []string{"1"}},
		{Record: 2, Pattern: 1, Slots: slotIDs[1], Values: []string{"2"}},
		{Record: 3, Pattern: 1, Slots: slotIDs[1], Values: []string{"3"}},
		{Record: 4, Pattern: 2},
		{Record: 5, Pattern: 0},
		{Record: 6, Pattern: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishMining(ctx); err != nil {
		t.Fatal(err)
	}
	type row struct {
		records     int64
		first, last *int64
	}
	read := func(id int64) row {
		var r row
		var f, l *int64
		if err := d.conn.QueryRowContext(ctx, `SELECT records, first_nanos, last_nanos FROM patterns WHERE id = ?`, id).Scan(&r.records, &f, &l); err != nil {
			t.Fatal(err)
		}
		r.first, r.last = f, l
		return r
	}
	if r := read(1); r.records != 3 || r.first == nil || *r.first != 100 || r.last == nil || *r.last != 300 {
		t.Errorf("pattern 1 = %+v, want 3 records from 100 to 300 (an unresolved time does not count)", r)
	}
	if r := read(2); r.records != 2 || r.first != nil || r.last != nil {
		t.Errorf("pattern 2 = %+v, want 2 records and no times (NULL)", r)
	}
	if r := read(3); r.records != 0 || r.first != nil || r.last != nil {
		t.Errorf("pattern 3 = %+v, want 0 records and no times", r)
	}
	if r := read(0); r.records != 1 || r.first == nil || *r.first != 900 || *r.last != 900 {
		t.Errorf("the catch-all = %+v, want 1 record at 900", r)
	}
	rows, err := d.conn.QueryContext(ctx, `SELECT name, tbl_name, sql FROM sqlite_master WHERE type = 'index' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name, tbl, sqlText string
		if err := rows.Scan(&name, &tbl, &sqlText); err != nil {
			t.Fatal(err)
		}
		got = append(got, name+" "+tbl+" "+strings.Join(strings.Fields(sqlText), " "))
	}
	want := []string{
		"records_by_pattern records CREATE INDEX records_by_pattern ON records (pattern)",
		"slot_values_by_record slot_values CREATE INDEX slot_values_by_record ON slot_values (record)",
		"slot_values_by_slot slot_values CREATE INDEX slot_values_by_slot ON slot_values (slot, value)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("indexes = %q, want %q", got, want)
	}
	// A second call changes nothing and does not fail on the indexes.
	if err := d.FinishMining(ctx); err != nil {
		t.Errorf("second FinishMining: %v", err)
	}
}

func TestTheMiningMethodsReturnErrClosedAfterClose(t *testing.T) {
	d := withRecords(t, rawsOfLength(1, 10))
	d.Close()
	if _, err := d.WritePatterns(ctx, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("WritePatterns: %v", err)
	}
	if _, err := d.RecordTexts(ctx, 0, 1, 1); !errors.Is(err, ErrClosed) {
		t.Errorf("RecordTexts: %v", err)
	}
	if err := d.Assign(ctx, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("Assign: %v", err)
	}
	if err := d.FinishMining(ctx); !errors.Is(err, ErrClosed) {
		t.Errorf("FinishMining: %v", err)
	}
}
