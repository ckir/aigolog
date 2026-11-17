// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/miner"
	"github.com/ckir/aigolog/internal/record"
	"github.com/ckir/aigolog/internal/store"
)

// peek opens a second connection on the database of d, to read what a run
// stored. The run holds no transaction when the test reads.
func peek(t *testing.T, d *store.DB) *sql.DB {
	t.Helper()
	db, err := sql.Open(store.DriverName, d.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func count(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// valuesOf returns the values stored for a record, in slot order, joined by |.
func valuesOf(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	rows, err := db.Query(`SELECT value FROM slot_values WHERE record = ? ORDER BY slot`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var vs []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		vs = append(vs, v)
	}
	return strings.Join(vs, "|")
}

// valuesMatchSlots counts the records whose number of stored values differs
// from the number of slots of their pattern.
func valuesMatchSlots(t *testing.T, db *sql.DB) int64 {
	return count(t, db, `SELECT COUNT(*) FROM records r
WHERE (SELECT COUNT(*) FROM slot_values v WHERE v.record = r.id) <> (SELECT COUNT(*) FROM slots s WHERE s.pattern = r.pattern)`)
}

func miningOptions() Options {
	return Options{Now: time.Now(), Zone: time.UTC, Miner: miner.New(miner.DefaultConfig())}
}

// sshdMessage is the message of line i of the sshd-like body: three shapes.
func sshdMessage(i int) string {
	names := []string{"root", "alice", "bob", "carol"}
	switch i % 3 {
	case 0:
		return fmt.Sprintf("sshd[123]: Failed password for %s from 10.0.0.%d port %d ssh2", names[(i/3)%4], i%250+1, 4000+i)
	case 1:
		return fmt.Sprintf("sshd[123]: Accepted password for %s from 10.0.1.%d port %d ssh2", names[(i/3)%4], i%250+1, 4000+i)
	default:
		return fmt.Sprintf("sshd[124]: session opened for user %s by (uid=%d)", names[(i/3)%4], i)
	}
}

func sshdBody(n int, stamped bool) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if stamped {
			fmt.Fprintf(&b, "2026-01-05 10:%02d:%02d ", i/60%60, i%60)
		}
		b.WriteString(sshdMessage(i))
		b.WriteByte('\n')
	}
	return b.String()
}

func TestEveryRecordGetsAPatternAndItsValues(t *testing.T) {
	d := newDB(t)
	res, err := Run(ctx, d, []Input{in("sshd.log", sshdBody(300, true))}, miningOptions())
	if err != nil || res.Records != 300 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	db := peek(t, d)
	if n := count(t, db, `SELECT COUNT(*) FROM records WHERE pattern = 0`); n != 0 {
		t.Errorf("%d records are in the catch-all; the cap was not reached, so every record must have a pattern", n)
	}
	if n := valuesMatchSlots(t, db); n != 0 {
		t.Errorf("%d records have a number of values that differs from the slots of their pattern", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM patterns WHERE records > 0 AND id <> 0`); n != 3 {
		t.Errorf("%d patterns have records, want the 3 shapes", n)
	}
	if n := count(t, db, `SELECT SUM(records) FROM patterns`); n != 300 {
		t.Errorf("the pattern counts add up to %d, want 300", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM slot_values`); n == 0 {
		t.Error("no value was stored")
	}
	// The time stamp is not part of the message: no value comes from it.
	if n := count(t, db, `SELECT COUNT(*) FROM slot_values WHERE value LIKE '2026-%' OR value LIKE '10:%'`); n != 0 {
		t.Errorf("%d values come from the time stamp", n)
	}
	// Record 1 is the first shape: Failed password for root from 10.0.0.1 port 4000.
	got := valuesOf(t, db, 1)
	if !strings.Contains(got, "10.0.0.1") || !strings.Contains(got, "4000") {
		t.Errorf("values of record 1 = %q, want the address and the port of its line", got)
	}
	// The stored pattern of record 1 carries the words of the message and none
	// of the time stamp.
	var text string
	if err := db.QueryRow(`SELECT p.text FROM records r JOIN patterns p ON p.id = r.pattern WHERE r.id = 1`).Scan(&text); err != nil ||
		!strings.Contains(text, "Failed password") || strings.Contains(text, "2026") {
		t.Errorf("pattern of record 1 = %q, %v", text, err)
	}
	// Every pattern's slots column lists its slot types, comma-joined.
	if n := count(t, db, `SELECT COUNT(*) FROM patterns p WHERE p.slots <> COALESCE((SELECT group_concat(type, ',') FROM (SELECT type FROM slots WHERE pattern = p.id ORDER BY pos)), '')`); n != 0 {
		t.Errorf("%d patterns have a slots column that differs from their slots rows", n)
	}
}

func TestTwoSourcesShareOnePatternSet(t *testing.T) {
	d := newDB(t)
	res, err := Run(ctx, d, []Input{in("stamped.log", sshdBody(99, true)), in("bare.log", sshdBody(99, false))}, miningOptions())
	if err != nil || res.Records != 198 || len(res.Sources) != 2 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	db := peek(t, d)
	if res.Sources[0].Source.TimeKind != store.TimeWall || res.Sources[1].Source.TimeKind != store.TimeNone {
		t.Fatalf("the sources are %+v and %+v; want one with stamps and one without", res.Sources[0].Source, res.Sources[1].Source)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM records WHERE pattern = 0`); n != 0 {
		t.Errorf("%d records are in the catch-all", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM patterns WHERE records > 0 AND id <> 0`); n != 3 {
		t.Errorf("%d patterns have records, want 3 shared by both sources", n)
	}
	for _, src := range []int{1, 2} {
		if n := count(t, db, `SELECT COUNT(DISTINCT pattern) FROM records WHERE source = ?`, src); n != 3 {
			t.Errorf("source %d uses %d patterns, want 3", src, n)
		}
	}
	// Line i of one source and line i of the other have the same message, so
	// the same pattern and the same values.
	for i := int64(1); i <= 99; i++ {
		var a, b int64
		if err := db.QueryRow(`SELECT (SELECT pattern FROM records WHERE id = ?), (SELECT pattern FROM records WHERE id = ?)`, i, i+99).Scan(&a, &b); err != nil || a != b {
			t.Fatalf("line %d: patterns %d and %d, %v", i, a, b, err)
		}
		if va, vb := valuesOf(t, db, i), valuesOf(t, db, i+99); va != vb || va == "" {
			t.Fatalf("line %d: values %q and %q", i, va, vb)
		}
	}
	if n := valuesMatchSlots(t, db); n != 0 {
		t.Errorf("%d records have a number of values that differs from the slots of their pattern", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM slot_values v JOIN records r ON r.id = v.record WHERE r.source = 2`); n == 0 {
		t.Error("the source without stamps has no stored values")
	}
}

func TestASourceWithNoStampsMinesTheWholeText(t *testing.T) {
	d := newDB(t)
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "alpha %d beta gamma delta\n", i)
	}
	if _, err := Run(ctx, d, []Input{in("bare.log", b.String())}, miningOptions()); err != nil {
		t.Fatal(err)
	}
	db := peek(t, d)
	if n := count(t, db, `SELECT COUNT(*) FROM records WHERE t_len <> 0`); n != 0 {
		t.Fatalf("%d records have a stamp length, want 0 for a source with no stamps", n)
	}
	var text string
	if err := db.QueryRow(`SELECT p.text FROM records r JOIN patterns p ON p.id = r.pattern WHERE r.id = 1`).Scan(&text); err != nil || !strings.HasPrefix(text, "alpha ") {
		t.Errorf("pattern of record 1 = %q, %v; want one that starts with the first word of the line", text, err)
	}
	if got := valuesOf(t, db, 1); got != "1" {
		t.Errorf("values of record 1 = %q, want %q", got, "1")
	}
	if got := valuesOf(t, db, 20); got != "20" {
		t.Errorf("values of record 20 = %q, want %q", got, "20")
	}
}

func TestARecordThatIsOnlyAStampHasNoValues(t *testing.T) {
	d := newDB(t)
	body := "2026-01-05 10:00:01\n2026-01-05 10:00:02 job 1 done\n2026-01-05 10:00:03 job 2 done\n2026-01-05 10:00:04 job 3 done\n"
	if _, err := Run(ctx, d, []Input{in("s.log", body)}, miningOptions()); err != nil {
		t.Fatal(err)
	}
	db := peek(t, d)
	if p := count(t, db, `SELECT pattern FROM records WHERE id = 1`); p != 0 {
		t.Errorf("the record that is only a stamp has pattern %d, want the catch-all", p)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM slot_values WHERE record = 1`); n != 0 {
		t.Errorf("%d values for a record with an empty message", n)
	}
	if p := count(t, db, `SELECT pattern FROM records WHERE id = 2`); p == 0 {
		t.Error("a record with a message is in the catch-all")
	}
	if n := valuesMatchSlots(t, db); n != 0 {
		t.Errorf("%d records have a number of values that differs from the slots of their pattern", n)
	}
}

func TestMessageOfCutsTheStampAndNeverPanics(t *testing.T) {
	for _, c := range []struct {
		raw  string
		n    int
		want string
	}{
		{"abc", 0, "abc"},
		{"abc", 1, "bc"},
		{"abc", 3, ""},
		{"abc", 10, ""},
		{"", 5, ""},
		{"abc", -1, "abc"},
	} {
		if got := messageOf(c.raw, c.n); got != c.want {
			t.Errorf("messageOf(%q, %d) = %q, want %q", c.raw, c.n, got, c.want)
		}
	}
}

// TestBothPassesSeeTheSameMessage learns through flush (pass 1, from the
// records in memory) and matches through mine (pass 2, from the stored raw
// text and the stored t_len): a record that was learned must find its pattern.
// The lengths cover none, part of the text, all of it and more than the text.
func TestBothPassesSeeTheSameMessage(t *testing.T) {
	d := newDB(t)
	src, err := d.AddSource(ctx, store.Source{Name: "x", Format: "text", Encoding: "utf-8", TimeKind: store.TimeNone})
	if err != nil {
		t.Fatal(err)
	}
	m := miner.New(miner.DefaultConfig())
	var recs []record.Record
	add := func(raw string, n int) {
		recs = append(recs, record.Record{Source: src, Line: int64(len(recs) + 1), Raw: raw, Stamp: record.Stamp{Len: n}})
	}
	for i := 0; i < 5; i++ {
		add(fmt.Sprintf("alpha beta %d gamma", i), 0)
		add(fmt.Sprintf("STAMP alpha beta %d gamma", i), 6) // the stamp is cut
		add(fmt.Sprintf("alpha beta %d gamma", i), 99)      // longer than the text: an empty message
		add("tiny", 4)                                      // all of the text: an empty message
		add("héllo wörld again", 0)
	}
	if err := flush(ctx, d, recs, m); err != nil {
		t.Fatal(err)
	}
	if err := mine(ctx, d, m); err != nil {
		t.Fatal(err)
	}
	db := peek(t, d)
	for i, r := range recs {
		empty := messageOf(r.Raw, r.Stamp.Len) == ""
		p := count(t, db, `SELECT pattern FROM records WHERE id = ?`, i+1)
		if empty && p != 0 {
			t.Errorf("record %d (%q, len %d) has an empty message and pattern %d", i+1, r.Raw, r.Stamp.Len, p)
		}
		if !empty && p == 0 {
			t.Errorf("record %d (%q, len %d) was learned and did not find its pattern", i+1, r.Raw, r.Stamp.Len)
		}
		if empty && valuesOf(t, db, int64(i+1)) != "" {
			t.Errorf("record %d has values for an empty message", i+1)
		}
	}
	// The records of one shape with a cut stamp and with none share a pattern.
	if a, b := count(t, db, `SELECT pattern FROM records WHERE id = 1`), count(t, db, `SELECT pattern FROM records WHERE id = 2`); a != b {
		t.Errorf("the same message has patterns %d and %d with and without a stamp", a, b)
	}
	if n := valuesMatchSlots(t, db); n != 0 {
		t.Errorf("%d records have a number of values that differs from the slots of their pattern", n)
	}
}

func TestHardRecordsDoNotBreakMining(t *testing.T) {
	var b strings.Builder
	many := strings.Repeat("w ", 300) // more than the cap of tokens
	huge := strings.Repeat("Z", 200<<10)
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&b, "x\n2026-01-05 10:00:0%d\n%s%d\nκόσμος %d ωραίο\n2026-01-05 10:00:0%d start %s\none two\n", i, many, i, i, i, huge)
	}
	d := newDB(t)
	if _, err := Run(ctx, d, []Input{in("hard.log", b.String())}, miningOptions()); err != nil {
		t.Fatal(err)
	}
	db := peek(t, d)
	if n := valuesMatchSlots(t, db); n != 0 {
		t.Errorf("%d records have a number of values that differs from the slots of their pattern", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM slot_values WHERE LENGTH(CAST(value AS BLOB)) > 512`); n != 0 {
		t.Errorf("%d stored values are longer than 512 bytes", n)
	}
}

// chunkedBody has enough short records for several chunks of pass 2.
func chunkedBody(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "2026-01-05 10:00:01 job %d done\n", i)
	}
	return b.String()
}

func TestTheDatabaseIsNotCompleteWhenMiningIsStopped(t *testing.T) {
	d := newDB(t)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	chunks := 0
	afterChunk = func() {
		chunks++
		if chunks == 1 {
			cancel()
		}
	}
	t.Cleanup(func() { afterChunk = nil })
	// 12,000 records are three chunks of 5,000 records at most.
	_, err := Run(cctx, d, []Input{in("many.log", chunkedBody(12000))}, miningOptions())
	if err != context.Canceled {
		t.Fatalf("err = %v, want the error of the context itself", err)
	}
	if chunks != 1 {
		t.Errorf("%d chunks were assigned, want 1: the context is checked between chunks", chunks)
	}
	path := d.Path()
	db := peek(t, d)
	if n := count(t, db, `SELECT COUNT(*) FROM records WHERE pattern <> 0`); n == 0 || n >= 12000 {
		t.Errorf("%d records have a pattern, want some but not all", n)
	}
	d.Close()
	if _, err := store.Open(ctx, path); !errors.Is(err, store.ErrIncomplete) {
		t.Errorf("Open of the database of a stopped run: %v, want store.ErrIncomplete", err)
	}
}

func TestRunWithoutAMinerStoresNoPatterns(t *testing.T) {
	d := newDB(t)
	if _, err := Run(ctx, d, []Input{in("a.log", sshdBody(30, true))}, Options{Now: time.Now(), Zone: time.UTC}); err != nil {
		t.Fatal(err)
	}
	db := peek(t, d)
	if n := count(t, db, `SELECT (SELECT COUNT(*) FROM patterns) + (SELECT COUNT(*) FROM slots) + (SELECT COUNT(*) FROM slot_values)`); n != 0 {
		t.Errorf("%d rows of patterns, slots and values without a miner", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index'`); n != 0 {
		t.Errorf("%d indexes without a miner", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM records WHERE pattern <> 0`); n != 0 {
		t.Errorf("%d records have a pattern without a miner", n)
	}
	path := d.Path()
	d.Close()
	o, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("the run is not complete: %v", err)
	}
	o.Close()
}

func TestAMinedRunIsCompleteAndHasItsIndexes(t *testing.T) {
	d := newDB(t)
	if _, err := Run(ctx, d, []Input{in("a.log", sshdBody(30, true))}, miningOptions()); err != nil {
		t.Fatal(err)
	}
	db := peek(t, d)
	for _, name := range []string{"records_by_pattern", "slot_values_by_slot", "slot_values_by_record"} {
		if n := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name); n != 1 {
			t.Errorf("index %s is absent", name)
		}
	}
	path := d.Path()
	d.Close()
	o, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("the run is not complete: %v", err)
	}
	o.Close()
}

// shapeLines produces n lines of about 100 bytes in 20 shapes without keeping
// them. At a line boundary, every 50,000 lines, it collects garbage and keeps
// the largest growth of the heap over the baseline; so does sample, which the
// second pass calls after each chunk.
type shapeLines struct {
	n, i     int
	buf      []byte
	baseline uint64
	peak     int64
	readN    int // samples taken while the input was read (pass 1)
	mineN    int // samples taken while the records were assigned (pass 2)
}

func (s *shapeLines) sample(counter *int) {
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	if grown := int64(m.HeapAlloc) - int64(s.baseline); grown > s.peak {
		s.peak = grown
	}
	*counter++
}

func (s *shapeLines) Read(p []byte) (int, error) {
	if len(s.buf) == 0 {
		if s.i >= s.n {
			return 0, io.EOF
		}
		if s.i > 0 && s.i%50000 == 0 {
			s.sample(&s.readN)
		}
		k := s.i % 20
		s.buf = []byte(fmt.Sprintf("2026-01-05 10:00:01 worker%c handled request %d from 10.0.%d.%d in %d ms %s\n",
			'a'+k, s.i, s.i%250, s.i%199, s.i%977, strings.Repeat("y", 30)))
		s.i++
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

// miningPeak runs n lines of 20 shapes through Run with a miner and returns
// the largest growth of the heap over the baseline, taken in both passes.
func miningPeak(t *testing.T, n int) (peak int64, readSamples, mineSamples int) {
	t.Helper()
	d := newDB(t)
	var m runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m)
	s := &shapeLines{n: n, baseline: m.HeapAlloc}
	afterChunk = func() { s.sample(&s.mineN) }
	defer func() { afterChunk = nil }()
	res, err := Run(ctx, d, []Input{{Name: "big.log", R: s}}, miningOptions())
	if err != nil || res.Records != int64(n) {
		t.Fatalf("res = %+v, err = %v", res.Records, err)
	}
	return s.peak, s.readN, s.mineN
}

func TestMemoryOfMiningDoesNotGrowWithTheInput(t *testing.T) {
	const lines = 400000
	peak, readN, mineN := miningPeak(t, lines)
	if readN == 0 || mineN == 0 {
		t.Fatalf("the heap was sampled %d times while reading and %d times while mining; both must be above 0", readN, mineN)
	}
	t.Logf("peak growth %d bytes over %d samples of the first pass and %d of the second", peak, readN, mineN)
	// Measured with this test's generator (20 shapes, about 100 bytes a line),
	// two runs of each: the peak growth of the heap was 1,282,192 and
	// 1,271,504 bytes at 100,000 lines, and 1,290,032 and 1,265,536 bytes at
	// 400,000 lines, so four times the input did not move it. The bound is
	// the largest 400,000-line peak plus 50%: 1,290,032 * 1.5 = 1,935,048.
	// Keeping the 400,000 records, or the values of one pass, would show as
	// 40 MB or more.
	if peak > memoryBound {
		t.Errorf("the heap grew by %d MiB during a run of %d lines with mining (bound %d MiB)", peak>>20, lines, memoryBound>>20)
	}
}

// memoryBound is the bound of TestMemoryOfMiningDoesNotGrowWithTheInput, in
// bytes (see the comment there).
const memoryBound = 1935048
