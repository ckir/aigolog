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
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/ingest"
	"github.com/ckir/aigolog/internal/miner"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/testlog"
)

var bg = context.Background()

// t0 is the time of the first line of the bodies below, as the log writes it.
var t0 = time.Date(2026, time.January, 5, 10, 0, 0, 0, time.UTC)

// stampAt writes the time stamp of the line that is off seconds after t0.
func stampAt(off int) string {
	return t0.Add(time.Duration(off) * time.Second).Format("2006-01-02 15:04:05")
}

// asOf is the anchor for the logs that have no year.
func asOf() *int64 {
	n := time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC).UnixNano()
	return &n
}

func newStore(t *testing.T) *store.DB {
	t.Helper()
	d, err := store.Create(bg, store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// minedCfg runs the whole of ingest over one input made of body, with a miner
// of configuration cfg, and returns the database.
func minedCfg(t *testing.T, body string, cfg miner.Config) *store.DB {
	t.Helper()
	d := newStore(t)
	_, err := ingest.Run(bg, d, []ingest.Input{{Name: "test.log", R: strings.NewReader(body)}},
		ingest.Options{AsOf: asOf(), Now: time.Now(), Zone: time.UTC, Miner: miner.New(cfg)})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// mined is minedCfg with the configuration the program uses.
func mined(t *testing.T, body string) *store.DB {
	t.Helper()
	return minedCfg(t, body, miner.DefaultConfig())
}

// shapeLine is a message of the shape named by word: "svc <word> done for item
// <n>". Shapes with different words are different patterns (the word is the
// second token); the item number is the one slot.
func shapeLine(word string, n int) string {
	return fmt.Sprintf("svc %s done for item %d", word, n)
}

// withStamp puts the time stamp of off seconds in front of a message.
func withStamp(off int, msg string) string { return stampAt(off) + " " + msg + "\n" }

// patternWith returns the stat of the pattern whose text holds word.
func patternWith(t *testing.T, ps []store.PatternStat, word string) store.PatternStat {
	t.Helper()
	for _, p := range ps {
		if strings.Contains(p.Text, " "+word+" ") {
			return p
		}
	}
	t.Fatalf("no pattern holds %q in %+v", word, ps)
	return store.PatternStat{}
}

func TestSourceInfosCountsRecordsAndRecordsWithATime(t *testing.T) {
	// One source with a first line that has no time and then ten that have;
	// one source with no time at all.
	var stamped strings.Builder
	stamped.WriteString("preamble without a time\n")
	for i := 0; i < 10; i++ {
		stamped.WriteString(withStamp(i*3, shapeLine("login", i)))
	}
	var bare strings.Builder
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&bare, "%s\n", shapeLine("login", i))
	}
	d := newStore(t)
	res, err := ingest.Run(bg, d, []ingest.Input{
		{Name: "stamped.log", R: strings.NewReader(stamped.String())},
		{Name: "bare.log", R: strings.NewReader(bare.String())},
	}, ingest.Options{AsOf: asOf(), Now: time.Now(), Zone: time.UTC, Miner: miner.New(miner.DefaultConfig())})
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.SourceInfos(bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("SourceInfos = %+v, want 2 sources", got)
	}
	a, b := got[0], got[1]
	if a.ID != res.Sources[0].ID || b.ID != res.Sources[1].ID || a.ID >= b.ID {
		t.Errorf("ids %d and %d, want those of the run in source order (%d, %d)", a.ID, b.ID, res.Sources[0].ID, res.Sources[1].ID)
	}
	if a.Name != "stamped.log" || b.Name != "bare.log" {
		t.Errorf("names %q and %q", a.Name, b.Name)
	}
	if a.TimeKind != store.TimeWall || b.TimeKind != store.TimeNone {
		t.Errorf("time kinds %q and %q, want %q and %q", a.TimeKind, b.TimeKind, store.TimeWall, store.TimeNone)
	}
	if a.Layout == "" || a.Layout != res.Sources[0].Source.Layout || b.Layout != res.Sources[1].Source.Layout {
		t.Errorf("layouts %q and %q, want those the run stored (%q, %q)", a.Layout, b.Layout, res.Sources[0].Source.Layout, res.Sources[1].Source.Layout)
	}
	if a.Records != 11 || a.WithTime != 10 {
		t.Errorf("stamped source: records %d, with time %d; want 11 and 10", a.Records, a.WithTime)
	}
	if b.Records != 5 || b.WithTime != 0 {
		t.Errorf("bare source: records %d, with time %d; want 5 and 0", b.Records, b.WithTime)
	}
	if a.Lines != 11 || b.Lines != 5 {
		t.Errorf("lines %d and %d, want 11 and 5", a.Lines, b.Lines)
	}
	if a.Bytes != int64(stamped.Len()) || b.Bytes != int64(bare.Len()) {
		t.Errorf("bytes %d and %d, want %d and %d", a.Bytes, b.Bytes, stamped.Len(), bare.Len())
	}
	if !a.HasTimes || a.FirstNanos != t0.UnixNano() || a.LastNanos != t0.Add(27*time.Second).UnixNano() {
		t.Errorf("stamped source: has times %v, first %d, last %d; want true, %d, %d", a.HasTimes, a.FirstNanos, a.LastNanos, t0.UnixNano(), t0.Add(27*time.Second).UnixNano())
	}
	if b.HasTimes || b.FirstNanos != 0 || b.LastNanos != 0 {
		t.Errorf("bare source: has times %v, first %d, last %d; want false, 0, 0", b.HasTimes, b.FirstNanos, b.LastNanos)
	}
}

func TestMinerReportCountsPatternsSeenOnceAndTheCatchAll(t *testing.T) {
	// Five records of one shape and one of another, and room for one pattern:
	// the second shape falls into the catch-all.
	var body strings.Builder
	for i := 0; i < 5; i++ {
		body.WriteString(withStamp(i, shapeLine("login", i)))
	}
	body.WriteString(withStamp(5, "svc other shape with more words than the first one"))
	cfg := miner.DefaultConfig()
	cfg.MaxPatterns = 1
	got, err := minedCfg(t, body.String(), cfg).MinerReport(bg)
	if err != nil {
		t.Fatal(err)
	}
	if want := (store.MinerReport{Patterns: 1, Records: 6, SeenOnce: 0, CatchAll: 1}); got != want {
		t.Errorf("with a cap of one pattern: %+v, want %+v", got, want)
	}

	// Two shapes with four and one records and the default cap: the shape of one
	// record is seen once, nothing is in the catch-all.
	body.Reset()
	for i := 0; i < 4; i++ {
		body.WriteString(withStamp(i, shapeLine("login", i)))
	}
	body.WriteString(withStamp(4, shapeLine("cache", 4)))
	got, err = mined(t, body.String()).MinerReport(bg)
	if err != nil {
		t.Fatal(err)
	}
	if want := (store.MinerReport{Patterns: 2, Records: 5, SeenOnce: 1, CatchAll: 0}); got != want {
		t.Errorf("with the default cap: %+v, want %+v", got, want)
	}
}

// orderedBody has four shapes, in the order of their first line: login (3
// records), cache (5), queue (3) and timer (1). login and queue tie.
func orderedBody() string {
	counts := []struct {
		word string
		n    int
	}{{"login", 3}, {"cache", 5}, {"queue", 3}, {"timer", 1}}
	var b strings.Builder
	off := 0
	// The first line of each shape, in order, then the rest of each.
	for _, c := range counts {
		b.WriteString(withStamp(off, shapeLine(c.word, off)))
		off++
	}
	for _, c := range counts {
		for i := 1; i < c.n; i++ {
			b.WriteString(withStamp(off, shapeLine(c.word, off)))
			off++
		}
	}
	return b.String()
}

func TestPatternStatsOrdersByRecordsThenID(t *testing.T) {
	d := mined(t, orderedBody())
	got, err := d.PatternStats(bg, 0)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, p := range got {
		for _, w := range []string{"login", "cache", "queue", "timer"} {
			if strings.Contains(p.Text, " "+w+" ") {
				order = append(order, fmt.Sprintf("%s:%d", w, p.Records))
			}
		}
	}
	// login and queue tie at 3 records; login was seen first, so its id is smaller.
	if want := "cache:5 login:3 queue:3 timer:1"; strings.Join(order, " ") != want {
		t.Errorf("order = %q, want %q", strings.Join(order, " "), want)
	}
	if len(got) != 4 {
		t.Fatalf("%d patterns, want 4: %+v", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		a, b := got[i-1], got[i]
		if a.Records < b.Records || (a.Records == b.Records && a.ID >= b.ID) {
			t.Errorf("rows %d and %d are out of order: (%d records, id %d) then (%d records, id %d)", i-1, i, a.Records, a.ID, b.Records, b.ID)
		}
	}
	login, queue := patternWith(t, got, "login"), patternWith(t, got, "queue")
	if login.ID >= queue.ID {
		t.Errorf("login has id %d and queue %d; the tie in this test needs login first", login.ID, queue.ID)
	}
}

func TestPatternStatsLimit(t *testing.T) {
	d := mined(t, orderedBody())
	all, err := d.PatternStats(bg, 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("all = %+v, %v", all, err)
	}
	for _, limit := range []int{1, 2, 3} {
		got, err := d.PatternStats(bg, limit)
		if err != nil || len(got) != limit {
			t.Fatalf("limit %d: %d rows, %v", limit, len(got), err)
		}
		for i := range got {
			if got[i].ID != all[i].ID {
				t.Errorf("limit %d: row %d is pattern %d, want %d (the first rows of the full list)", limit, i, got[i].ID, all[i].ID)
			}
		}
	}
	for _, limit := range []int{0, -1, 4, 100} {
		got, err := d.PatternStats(bg, limit)
		if err != nil || len(got) != 4 {
			t.Errorf("limit %d: %d rows, %v; want all 4", limit, len(got), err)
		}
	}
}

// portBody has one shape, "svc login accepted for <name> port <n>", for the
// given names and ports, in order.
func portBody(names []string, ports []int) string {
	var b strings.Builder
	for i := range names {
		b.WriteString(withStamp(i, fmt.Sprintf("svc login accepted for %s port %d", names[i], ports[i])))
	}
	return b.String()
}

func TestSlotStatsGivesDistinctTopValuesAndNumericRange(t *testing.T) {
	d := mined(t, portBody([]string{"alice", "bob", "alice", "Zed"}, []int{22, 22, 80, 25}))
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) != 1 {
		t.Fatalf("patterns = %+v, %v", ps, err)
	}
	got, err := d.SlotStats(bg, ps[0].ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d slots, want 2: %+v", len(got), got)
	}
	name, port := got[0], got[1]
	if name.Pos != 0 || port.Pos != 1 || name.Type != "word" || port.Type != "int" {
		t.Errorf("slots (pos, type) = (%d, %s) and (%d, %s), want (0, word) and (1, int)", name.Pos, name.Type, port.Pos, port.Type)
	}
	if name.Slot == port.Slot {
		t.Errorf("both slots have id %d", name.Slot)
	}

	if port.Total != 4 || port.Distinct != 3 {
		t.Errorf("port: total %d, distinct %d; want 4 and 3", port.Total, port.Distinct)
	}
	wantPorts := []store.ValueCount{{Value: "22", Count: 2, Share: 0.5}, {Value: "25", Count: 1, Share: 0.25}, {Value: "80", Count: 1, Share: 0.25}}
	if fmt.Sprint(port.Top) != fmt.Sprint(wantPorts) {
		t.Errorf("port top = %v, want %v (the tie of 25 and 80 goes by value)", port.Top, wantPorts)
	}
	if !port.Numeric || port.Min != 22 || port.Max != 80 {
		t.Errorf("port: numeric %v, min %v, max %v; want true, 22, 80", port.Numeric, port.Min, port.Max)
	}

	// The names: alice twice, Zed and bob once. The tie goes by byte order, in
	// which "Zed" (Z is 0x5A) comes before "bob" (b is 0x62).
	wantNames := []store.ValueCount{{Value: "alice", Count: 2, Share: 0.5}, {Value: "Zed", Count: 1, Share: 0.25}, {Value: "bob", Count: 1, Share: 0.25}}
	if fmt.Sprint(name.Top) != fmt.Sprint(wantNames) {
		t.Errorf("name top = %v, want %v", name.Top, wantNames)
	}
	if name.Numeric || name.Min != 0 || name.Max != 0 {
		t.Errorf("name: numeric %v, min %v, max %v; want false, 0, 0", name.Numeric, name.Min, name.Max)
	}
	if name.Total != 4 || name.Distinct != 3 {
		t.Errorf("name: total %d, distinct %d; want 4 and 3", name.Total, name.Distinct)
	}

	// top cuts the list; top <= 0 gives none, and the counts are the same.
	cut, err := d.SlotStats(bg, ps[0].ID, 2)
	if err != nil || len(cut) != 2 || len(cut[1].Top) != 2 || cut[1].Top[1].Value != "25" || cut[1].Total != 4 || cut[1].Distinct != 3 {
		t.Errorf("top 2 = %+v, %v", cut, err)
	}
	none, err := d.SlotStats(bg, ps[0].ID, 0)
	if err != nil || len(none) != 2 || none[1].Top == nil || len(none[1].Top) != 0 || none[1].Distinct != 3 {
		t.Errorf("top 0 = %+v, %v", none, err)
	}
	if other, err := d.SlotStats(bg, ps[0].ID+1000, 5); err != nil || len(other) != 0 {
		t.Errorf("a pattern that does not exist: %+v, %v; want none", other, err)
	}
}

func TestSlotStatsRangeIsNumericNotTextual(t *testing.T) {
	// As text "10" < "100" < "9"; as numbers 9 < 10 < 100.
	d := mined(t, portBody([]string{"a", "b", "c", "d"}, []int{10, 100, 9, 10}))
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) != 1 {
		t.Fatalf("patterns = %+v, %v", ps, err)
	}
	got, err := d.SlotStats(bg, ps[0].ID, 5)
	if err != nil || len(got) != 2 {
		t.Fatalf("slots = %+v, %v", got, err)
	}
	if p := got[1]; !p.Numeric || p.Min != 9 || p.Max != 100 {
		t.Errorf("port: numeric %v, min %v, max %v; want true, 9, 100", p.Numeric, p.Min, p.Max)
	}
}

func TestSlotStatsOfAnAbsurdNumberStaysFiniteSoThatJSONSucceeds(t *testing.T) {
	// The miner never types a token of more than 256 bytes as a number, but the
	// table holds any text: a value of 400 digits casts to +Inf as a real, and
	// json.Marshal refuses Inf.
	d := newStore(t)
	ids, err := d.WritePatterns(bg, []store.PatternRow{{ID: 1, Text: "port <*>", Slots: []string{"int"}}})
	if err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("9", 400)
	var as []store.Assignment
	for i, v := range []string{"7", huge, "-" + huge} {
		as = append(as, store.Assignment{Record: int64(i + 1), Pattern: 1, Slots: ids[1], Values: []string{v}})
	}
	if err := d.Assign(bg, as); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishMining(bg); err != nil {
		t.Fatal(err)
	}
	got, err := d.SlotStats(bg, 1, 5)
	if err != nil || len(got) != 1 {
		t.Fatalf("slots = %+v, %v", got, err)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Errorf("json.Marshal of %+v: %v", got, err)
	}
	if s := got[0]; s.Total != 3 || s.Min != -math.MaxFloat64 || s.Max != math.MaxFloat64 {
		t.Errorf("slot: total %d, min %v, max %v; want 3 and the largest finite reals of each sign", s.Total, s.Min, s.Max)
	}
}

// timedBody has one record for each offset (seconds after t0), in the order
// given, of the shape the word names; the offsets and words are parallel.
func timedBody(offs []int, words []string) string {
	var b strings.Builder
	for i, off := range offs {
		b.WriteString(withStamp(off, shapeLine(words[i], i)))
	}
	return b.String()
}

// spread are the offsets of 13 records over 120 seconds: with 4 buckets the
// width is 30 s and the largest offset falls on the end of the last bucket.
var spread = []int{0, 5, 5, 29, 30, 31, 59, 60, 61, 89, 90, 119, 120}

func counts(bs []store.Bucket) []int64 {
	out := make([]int64, len(bs))
	for i, b := range bs {
		out[i] = b.Count
	}
	return out
}

func TestTimeBucketsCoverTheSpanAndSumToTheRecordCount(t *testing.T) {
	words := make([]string, len(spread))
	for i := range words {
		words[i] = "login"
	}
	// A first line with no time: it is a record but is in no bucket.
	d := mined(t, "preamble without a time\n"+timedBody(spread, words))

	// Divisible: 120 s in 4 buckets of 30 s. [0,30): 0 5 5 29; [30,60): 30 31
	// 59; [60,90): 60 61 89; the last, closed: 90 119 120.
	got, err := d.TimeBuckets(bg, -1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(counts(got)) != "[4 3 3 3]" {
		t.Fatalf("4 buckets: counts %v, want [4 3 3 3]; buckets %+v", counts(got), got)
	}
	if got[0].StartNanos != t0.UnixNano() {
		t.Errorf("the first bucket starts at %d, want the smallest time %d", got[0].StartNanos, t0.UnixNano())
	}
	if want := t0.Add(120 * time.Second).UnixNano(); got[3].EndNanos != want {
		t.Errorf("the last bucket ends at %d, want the largest time %d, which it includes", got[3].EndNanos, want)
	}
	for i := 1; i < len(got); i++ {
		if got[i].StartNanos != got[i-1].EndNanos {
			t.Errorf("bucket %d starts at %d but bucket %d ends at %d", i, got[i].StartNanos, i-1, got[i-1].EndNanos)
		}
		if w, w0 := got[i].EndNanos-got[i].StartNanos, got[0].EndNanos-got[0].StartNanos; w != w0 {
			t.Errorf("bucket %d is %d ns wide, bucket 0 %d ns", i, w, w0)
		}
	}

	// Not divisible: 7 buckets of 120e9/7 ns rounded up, 17,142,857,143 ns, so
	// the edges are at 17.14 s, 34.29 s, 51.43 s, 68.57 s, 85.71 s and 102.86 s.
	// [0,17.14): 0 5 5; [17.14,34.29): 29 30 31; [34.29,51.43): none;
	// [51.43,68.57): 59 60 61; [68.57,85.71): none; [85.71,102.86): 89 90; the
	// last: 119 120.
	got, err = d.TimeBuckets(bg, -1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(counts(got)) != "[3 3 0 3 0 2 2]" {
		t.Errorf("7 buckets: counts %v, want [3 3 0 3 0 2 2]", counts(got))
	}
	if w := got[0].EndNanos - got[0].StartNanos; w != 17142857143 {
		t.Errorf("7 buckets: width %d ns, want 17142857143", w)
	}

	// One bucket holds every record that has a time, and none that has not.
	one, err := d.TimeBuckets(bg, -1, 1)
	if err != nil || len(one) != 1 || one[0].Count != int64(len(spread)) {
		t.Errorf("1 bucket = %+v, %v; want one holding %d", one, err, len(spread))
	}
}

func TestTimeBucketsOfOnePatternUseTheTimesOfThatPattern(t *testing.T) {
	// login has the offsets 5, 29, 31, 60 and 90 of the 13 below, cache the rest.
	login := map[int]bool{5: true, 29: true, 31: true, 60: true, 90: true}
	words := make([]string, len(spread))
	for i, off := range spread {
		words[i] = "cache"
		if login[off] {
			words[i] = "login"
		}
	}
	// spread has 5 twice; the second 5 is login too, so login has 6 records.
	d := mined(t, timedBody(spread, words))
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) != 2 {
		t.Fatalf("patterns = %+v, %v", ps, err)
	}
	p := patternWith(t, ps, "login")
	// login: 5 5 29 31 60 90 over [5, 90]: 85 s in 5 buckets of 17 s.
	// [5,22): 5 5; [22,39): 29 31; [39,56): none; [56,73): 60; [73,90]: 90.
	got, err := d.TimeBuckets(bg, p.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(counts(got)) != "[2 2 0 1 1]" {
		t.Errorf("counts %v, want [2 2 0 1 1]", counts(got))
	}
	if got[0].StartNanos != t0.Add(5*time.Second).UnixNano() {
		t.Errorf("starts at %d, want the smallest time of the pattern", got[0].StartNanos)
	}
	var sum int64
	for _, b := range got {
		sum += b.Count
	}
	if sum != p.Records {
		t.Errorf("the buckets hold %d records, the pattern has %d", sum, p.Records)
	}
}

func TestTimeBucketsIsEmptyWithoutTimes(t *testing.T) {
	d := mined(t, shapeLine("login", 1)+"\n"+shapeLine("login", 2)+"\n")
	for _, pattern := range []int64{-1, 1} {
		got, err := d.TimeBuckets(bg, pattern, 5)
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("pattern %d: %#v, %v; want an empty slice and no error", pattern, got, err)
		}
	}
}

func TestTimeBucketsWithOneInstantGivesOneBucket(t *testing.T) {
	d := mined(t, withStamp(7, shapeLine("login", 1))+withStamp(7, shapeLine("login", 2))+withStamp(7, shapeLine("login", 3)))
	got, err := d.TimeBuckets(bg, -1, 5)
	if err != nil || len(got) != 1 {
		t.Fatalf("buckets = %+v, %v; want one", got, err)
	}
	at := t0.Add(7 * time.Second).UnixNano()
	if got[0].Count != 3 || got[0].StartNanos != at || got[0].EndNanos < at {
		t.Errorf("bucket = %+v, want 3 records, starting at %d", got[0], at)
	}
}

func TestTimeBucketsAreFewerWhenTheSpanIsShorterThanN(t *testing.T) {
	// Three records one nanosecond apart: a span of 3 ns, so 10 buckets become 3.
	var b strings.Builder
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&b, "2026-01-05 10:00:00.%09d %s\n", i, shapeLine("login", i))
	}
	d := mined(t, b.String())
	got, err := d.TimeBuckets(bg, -1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(counts(got)) != "[1 1 1]" {
		t.Fatalf("counts %v, want [1 1 1]; buckets %+v", counts(got), got)
	}
	for i, bk := range got {
		if w := bk.EndNanos - bk.StartNanos; w < 1 {
			t.Errorf("bucket %d is %d ns wide, want at least 1", i, w)
		}
	}
	// Two records 10 ns apart and n = 7: the width is ceil(10/7) = 2 ns, and the
	// 7th bucket would start at 12 ns, past the largest time, so there are 6:
	// [0,2) [2,4) [4,6) [6,8) [8,10) and the last, closed, [10,12].
	var c strings.Builder
	for _, ns := range []int{0, 10} {
		fmt.Fprintf(&c, "2026-01-05 10:00:00.%09d %s\n", ns, shapeLine("login", ns))
	}
	got, err = mined(t, c.String()).TimeBuckets(bg, -1, 7)
	if err != nil || fmt.Sprint(counts(got)) != "[1 0 0 0 0 1]" {
		t.Errorf("a span of 10 ns in 7 buckets: counts %v, %v; want [1 0 0 0 0 1]", counts(got), err)
	}
	if zero, err := d.TimeBuckets(bg, -1, 0); err != nil || len(zero) != 0 {
		t.Errorf("n = 0: %+v, %v; want none", zero, err)
	}
}

func TestStatsOfAnEmptyMinedDatabaseAreEmptyNotErrors(t *testing.T) {
	check := func(name string, d *store.DB, records int64) {
		t.Helper()
		if got, err := d.PatternStats(bg, 5); err != nil || len(got) != 0 {
			t.Errorf("%s: PatternStats = %+v, %v", name, got, err)
		}
		if got, err := d.SlotStats(bg, 1, 5); err != nil || len(got) != 0 {
			t.Errorf("%s: SlotStats = %+v, %v", name, got, err)
		}
		if got, err := d.TimeBuckets(bg, -1, 5); err != nil || len(got) != 0 {
			t.Errorf("%s: TimeBuckets = %+v, %v", name, got, err)
		}
		got, err := d.MinerReport(bg)
		if err != nil || got.Patterns != 0 || got.SeenOnce != 0 || got.Records != records {
			t.Errorf("%s: MinerReport = %+v, %v; want no patterns and %d records", name, got, err, records)
		}
	}
	empty := newStore(t)
	check("a database with no source", empty, 0)
	if got, err := empty.SourceInfos(bg); err != nil || len(got) != 0 {
		t.Errorf("SourceInfos of a database with no source = %+v, %v", got, err)
	}
	if got, err := empty.MinerReport(bg); err != nil || got != (store.MinerReport{}) {
		t.Errorf("MinerReport of a database with no source = %+v, %v", got, err)
	}

	// Records, but a run with no miner: no pattern table row exists.
	d := newStore(t)
	_, err := ingest.Run(bg, d, []ingest.Input{{Name: "n.log", R: strings.NewReader(shapeLine("login", 1) + "\n" + shapeLine("login", 2) + "\n")}},
		ingest.Options{Now: time.Now(), Zone: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	check("a run with no miner", d, 2)
	if got, err := d.SourceInfos(bg); err != nil || len(got) != 1 || got[0].Records != 2 {
		t.Errorf("SourceInfos of a run with no miner = %+v, %v", got, err)
	}
}

func TestAPatternWithNoSlotsHasNoSlotTypes(t *testing.T) {
	// "LabSZ ok" is a fixed text: a pattern with no slot. With room for one
	// pattern, the other shape goes to the catch-all, which has no slot either.
	var b strings.Builder
	for i := 0; i < 3; i++ {
		b.WriteString(withStamp(i, "LabSZ ok"))
	}
	b.WriteString(withStamp(3, "something else entirely with several more words"))
	cfg := miner.DefaultConfig()
	cfg.MaxPatterns = 1
	d := minedCfg(t, b.String(), cfg)
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) != 2 {
		t.Fatalf("patterns = %+v, %v; want the fixed text and the catch-all", ps, err)
	}
	var fixed, other *store.PatternStat
	for i := range ps {
		if ps[i].CatchAll {
			other = &ps[i]
		} else {
			fixed = &ps[i]
		}
	}
	if fixed == nil || other == nil || fixed.Text != "LabSZ ok" || other.ID != 0 || fixed.Records != 3 || other.Records != 1 {
		t.Fatalf("patterns = %+v", ps)
	}
	for _, p := range ps {
		if p.Slots == nil || len(p.Slots) != 0 {
			t.Errorf("pattern %d (%q): Slots = %#v, want an empty slice, not nil and not [\"\"]", p.ID, p.Text, p.Slots)
		}
		if got, err := d.SlotStats(bg, p.ID, 5); err != nil || len(got) != 0 {
			t.Errorf("SlotStats of pattern %d = %+v, %v; want none", p.ID, got, err)
		}
	}
	out, err := json.Marshal(ps)
	if err != nil || strings.Contains(string(out), `"slots":null`) || !strings.Contains(string(out), `"slots":[]`) {
		t.Errorf("json = %s, %v; want \"slots\":[] for a pattern with no slot", out, err)
	}
	// A pattern with slots lists their types in order.
	d2 := mined(t, portBody([]string{"a", "b"}, []int{1, 2}))
	ps2, err := d2.PatternStats(bg, 0)
	if err != nil || len(ps2) != 1 || fmt.Sprint(ps2[0].Slots) != "[word int]" {
		t.Errorf("patterns = %+v, %v; want slots [word int]", ps2, err)
	}
}

func TestSharesAreZeroAndNeverNaNWhenThereIsNothingToDivideBy(t *testing.T) {
	// A pattern with two slots, no record and no value; the catch-all with no
	// record is left out.
	d := newStore(t)
	if _, err := d.WritePatterns(bg, []store.PatternRow{
		{ID: 0, Text: "<other>", CatchAll: true},
		{ID: 1, Text: "a <*> b <*>", Slots: []string{"int", "word"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishMining(bg); err != nil {
		t.Fatal(err)
	}
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) != 1 {
		t.Fatalf("patterns = %+v, %v; want the one mined pattern", ps, err)
	}
	if ps[0].Records != 0 || ps[0].Share != 0 || math.IsNaN(ps[0].Share) {
		t.Errorf("pattern share = %v for %d records, want 0", ps[0].Share, ps[0].Records)
	}
	ss, err := d.SlotStats(bg, 1, 5)
	if err != nil || len(ss) != 2 {
		t.Fatalf("slots = %+v, %v", ss, err)
	}
	for _, s := range ss {
		if s.Total != 0 || s.Distinct != 0 || s.Top == nil || len(s.Top) != 0 || s.Min != 0 || s.Max != 0 {
			t.Errorf("slot %+v, want no values and an empty, non-nil Top", s)
		}
	}
	rep, err := d.MinerReport(bg)
	if err != nil || rep != (store.MinerReport{Patterns: 1}) {
		t.Errorf("MinerReport = %+v, %v; want one pattern", rep, err)
	}
	for name, v := range map[string]any{"patterns": ps, "slots": ss, "report": rep} {
		if _, err := json.Marshal(v); err != nil {
			t.Errorf("json.Marshal of the %s: %v", name, err)
		}
	}
}

func TestSharesAddUpAndMatchTheCounts(t *testing.T) {
	data, _ := testlog.SSH(7, 600)
	body := string(data)
	lines := int64(strings.Count(body, "\n"))
	d := mined(t, body)
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) < 3 {
		t.Fatalf("patterns = %d, %v", len(ps), err)
	}
	var sum float64
	var records int64
	for _, p := range ps {
		sum += p.Share
		records += p.Records
		if want := float64(p.Records) / float64(lines); p.Share != want {
			t.Errorf("pattern %d: share %v, want %d/%d", p.ID, p.Share, p.Records, lines)
		}
	}
	if math.Abs(sum-1) > 1e-9 || records != lines {
		t.Errorf("the shares add up to %v and the records to %d; want 1 and %d", sum, records, lines)
	}
	rep, err := d.MinerReport(bg)
	if err != nil || rep.Records != lines {
		t.Errorf("MinerReport = %+v, %v; want %d records", rep, err, lines)
	}
	checked := 0
	for _, p := range ps {
		slots, err := d.SlotStats(bg, p.ID, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(slots) != len(p.Slots) {
			t.Errorf("pattern %d has %d slot types and %d slot stats", p.ID, len(p.Slots), len(slots))
		}
		for i, s := range slots {
			if s.Pos != i || s.Type != p.Slots[i] {
				t.Errorf("pattern %d slot %d: pos %d, type %s; want %d, %s", p.ID, i, s.Pos, s.Type, i, p.Slots[i])
			}
			// Every record of the pattern has one value for each of its slots.
			if s.Total != p.Records {
				t.Errorf("pattern %d slot %d: total %d, want its %d records", p.ID, i, s.Total, p.Records)
			}
			var inTop int64
			for j, v := range s.Top {
				if want := float64(v.Count) / float64(s.Total); v.Share != want {
					t.Errorf("pattern %d slot %d value %q: share %v, want %d/%d", p.ID, i, v.Value, v.Share, v.Count, s.Total)
				}
				if j > 0 && (v.Count > s.Top[j-1].Count || (v.Count == s.Top[j-1].Count && v.Value <= s.Top[j-1].Value)) {
					t.Errorf("pattern %d slot %d: top values out of order at %d: %v", p.ID, i, j, s.Top)
				}
				inTop += v.Count
				checked++
			}
			if inTop > s.Total || int64(len(s.Top)) > s.Distinct {
				t.Errorf("pattern %d slot %d: the top values hold %d of %d", p.ID, i, inTop, s.Total)
			}
		}
	}
	if checked == 0 {
		t.Error("no value was checked")
	}
	if _, err := json.Marshal(ps); err != nil {
		t.Errorf("json.Marshal: %v", err)
	}
}

func TestStatsMethodsReturnErrClosedAfterClose(t *testing.T) {
	d := mined(t, shapeLine("login", 1)+"\n")
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	calls := map[string]func() error{
		"SourceInfos":  func() error { _, err := d.SourceInfos(bg); return err },
		"MinerReport":  func() error { _, err := d.MinerReport(bg); return err },
		"PatternStats": func() error { _, err := d.PatternStats(bg, 3); return err },
		"SlotStats":    func() error { _, err := d.SlotStats(bg, 1, 3); return err },
		"TimeBuckets":  func() error { _, err := d.TimeBuckets(bg, -1, 3); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, store.ErrClosed) {
			t.Errorf("%s after Close = %v, want ErrClosed", name, err)
		}
	}
}

func TestStatsMethodsAreReadOnly(t *testing.T) {
	d := mined(t, orderedBody())
	before, err := d.Size()
	if err != nil {
		t.Fatal(err)
	}
	ps, _ := d.PatternStats(bg, 0)
	for _, p := range ps {
		if _, err := d.SlotStats(bg, p.ID, 3); err != nil {
			t.Fatal(err)
		}
		if _, err := d.TimeBuckets(bg, p.ID, 4); err != nil {
			t.Fatal(err)
		}
	}
	d.SourceInfos(bg)
	d.MinerReport(bg)
	after, err := d.Size()
	if err != nil || after != before {
		t.Errorf("the database was %d bytes and is %d after the queries (%v)", before, after, err)
	}
	again, _ := d.PatternStats(bg, 0)
	if fmt.Sprint(again) != fmt.Sprint(ps) {
		t.Errorf("a second call gave %v, the first %v", again, ps)
	}
}

func TestPatternStatsGivesEachPatternTheSpanOfItsResolvedTimes(t *testing.T) {
	// The stamps state a zone (Z), so the nanoseconds of each are those of the
	// instant the test wrote. The lines of two shapes are interleaved and out of
	// order in the file: the span of a pattern is its smallest and its largest
	// time, not its first and last line, and not another pattern's time. A third
	// shape has no time at all (its one record comes before the first stamp).
	at := func(off int) time.Time { return t0.Add(time.Duration(off) * time.Second) }
	stamped := func(off int, word string, n int) string {
		return at(off).Format("2006-01-02T15:04:05Z") + " " + shapeLine(word, n) + "\n"
	}
	var b strings.Builder
	b.WriteString(shapeLine("gamma", 0) + "\n")
	b.WriteString(stamped(40, "alpha", 1))
	b.WriteString(stamped(60, "beta", 2))
	b.WriteString(stamped(10, "alpha", 3))
	b.WriteString(stamped(20, "beta", 4))
	b.WriteString(stamped(90, "alpha", 5))
	d := mined(t, b.String())
	ps, err := d.PatternStats(bg, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 {
		t.Fatalf("%d patterns, want 3: %+v", len(ps), ps)
	}
	for _, c := range []struct {
		word        string
		records     int64
		hasTimes    bool
		first, last int64
	}{
		{"alpha", 3, true, at(10).UnixNano(), at(90).UnixNano()},
		{"beta", 2, true, at(20).UnixNano(), at(60).UnixNano()},
		{"gamma", 1, false, 0, 0},
	} {
		p := patternWith(t, ps, c.word)
		if p.Records != c.records || p.HasTimes != c.hasTimes || p.FirstNanos != c.first || p.LastNanos != c.last {
			t.Errorf("%s: records %d, has times %v, first %d, last %d; want %d, %v, %d, %d",
				c.word, p.Records, p.HasTimes, p.FirstNanos, p.LastNanos, c.records, c.hasTimes, c.first, c.last)
		}
	}
}

func TestSlotStatsComputesTheNumericRangeOfDecimalSlots(t *testing.T) {
	var b strings.Builder
	for i, v := range []string{"0.5", "12.25", "3.75"} {
		b.WriteString(withStamp(i, "svc load is "+v))
	}
	b.WriteString(portBody([]string{"alice", "bob", "carol"}, []int{22, 80, 25}))
	d := mined(t, b.String())
	ps, err := d.PatternStats(bg, 0)
	if err != nil || len(ps) != 2 {
		t.Fatalf("patterns = %+v, %v", ps, err)
	}
	load, err := d.SlotStats(bg, patternWith(t, ps, "load").ID, 5)
	if err != nil || len(load) != 1 {
		t.Fatalf("slots of load = %+v, %v", load, err)
	}
	if s := load[0]; s.Type != "dec" || !s.Numeric || s.Min != 0.5 || s.Max != 12.25 || s.Distinct != 3 || s.Total != 3 {
		t.Errorf("load slot: type %s, numeric %v, min %v, max %v, distinct %d, total %d; want dec, true, 0.5, 12.25, 3, 3",
			s.Type, s.Numeric, s.Min, s.Max, s.Distinct, s.Total)
	}
	name, err := d.SlotStats(bg, patternWith(t, ps, "login").ID, 5)
	if err != nil || len(name) != 2 {
		t.Fatalf("slots of login = %+v, %v", name, err)
	}
	if s := name[0]; s.Type != "word" || s.Numeric || s.Min != 0 || s.Max != 0 || s.Distinct != 3 {
		t.Errorf("name slot: type %s, numeric %v, min %v, max %v, distinct %d; want word, false, 0, 0, 3", s.Type, s.Numeric, s.Min, s.Max, s.Distinct)
	}
}
