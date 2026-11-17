// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/ckir/aigolog/internal/record"
)

// The queries of this file are read-only and take every value as a parameter.
// Every one has a total order (the last key is an id or the value itself), so
// that the same database gives the same answer on every run.

// SourceInfo is what the database knows about one input of the run.
type SourceInfo struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Layout   string `json:"layout"`
	TimeKind string `json:"time_kind"`
	Records  int64  `json:"records"`
	Lines    int64  `json:"lines"`
	Bytes    int64  `json:"bytes"`
	// WithTime is the number of records whose time is resolved.
	WithTime int64 `json:"with_time"`
	// HasTimes is true when WithTime is not 0; FirstNanos and LastNanos are then
	// the smallest and the largest time, and 0 otherwise.
	HasTimes   bool  `json:"has_times"`
	FirstNanos int64 `json:"first_nanos"`
	LastNanos  int64 `json:"last_nanos"`
}

// SourceInfos returns the sources of the run in source order (by id).
func (d *DB) SourceInfos(ctx context.Context) ([]SourceInfo, error) {
	if d.conn == nil {
		return nil, ErrClosed
	}
	rows, err := d.conn.QueryContext(ctx, `SELECT s.id, s.name, s.layout, s.time_kind, s.records, s.lines, s.bytes,
  COALESCE(w.n, 0), s.first_nanos, s.last_nanos
FROM sources s
LEFT JOIN (SELECT source, COUNT(*) AS n FROM records WHERE t_state = ? GROUP BY source) w ON w.source = s.id
ORDER BY s.id`, int(record.Resolved))
	if err != nil {
		return nil, fmt.Errorf("reading the sources: %w", err)
	}
	defer rows.Close()
	out := []SourceInfo{}
	for rows.Next() {
		var s SourceInfo
		var first, last sql.NullInt64
		if err := rows.Scan(&s.ID, &s.Name, &s.Layout, &s.TimeKind, &s.Records, &s.Lines, &s.Bytes, &s.WithTime, &first, &last); err != nil {
			return nil, fmt.Errorf("reading the sources: %w", err)
		}
		if first.Valid && last.Valid {
			s.HasTimes, s.FirstNanos, s.LastNanos = true, first.Int64, last.Int64
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the sources: %w", err)
	}
	return out, nil
}

// MinerReport says how well the patterns hold the records. The records that the
// mined patterns hold are Records less CatchAll. A record goes to the catch-all
// when no pattern fits it: past the cap on the number of patterns, and a record
// with no token after its time stamp (it has no tokens to fit to a pattern). On
// a database made with no miner, records.pattern is 0 for every record (its
// default), so CatchAll equals Records and Patterns is 0.
type MinerReport struct {
	// Patterns is the number of mined patterns; the catch-all is not one.
	Patterns int `json:"patterns"`
	// Records is the number of all records.
	Records int64 `json:"records"`
	// SeenOnce is the number of records in mined patterns that hold exactly one
	// record (the catch-all is not a mined pattern).
	SeenOnce int64 `json:"seen_once"`
	// CatchAll is the number of records of pattern 0, the catch-all.
	CatchAll int64 `json:"catch_all"`
}

// MinerReport counts the patterns and the records they hold.
func (d *DB) MinerReport(ctx context.Context) (MinerReport, error) {
	if d.conn == nil {
		return MinerReport{}, ErrClosed
	}
	var r MinerReport
	if err := d.conn.QueryRowContext(ctx, `SELECT
  (SELECT COUNT(*) FROM patterns WHERE catch_all = 0),
  (SELECT COUNT(*) FROM patterns WHERE catch_all = 0 AND records = 1),
  (SELECT COUNT(*) FROM records),
  (SELECT COUNT(*) FROM records WHERE pattern = 0)`).Scan(&r.Patterns, &r.SeenOnce, &r.Records, &r.CatchAll); err != nil {
		return MinerReport{}, fmt.Errorf("reading the miner report: %w", err)
	}
	return r, nil
}

// PatternStat is one pattern and how many records it holds.
type PatternStat struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
	// Slots are the types of the slots in order; the empty slice, never nil and
	// never [""], for a pattern with no slot.
	Slots    []string `json:"slots"`
	CatchAll bool     `json:"catch_all"`
	Records  int64    `json:"records"`
	// Share is Records divided by the number of all records, 0 when there is none.
	Share    float64 `json:"share"`
	HasTimes bool    `json:"has_times"`
	// FirstNanos and LastNanos are the smallest and the largest resolved time of
	// the records of the pattern; 0 when HasTimes is false.
	FirstNanos int64 `json:"first_nanos"`
	LastNanos  int64 `json:"last_nanos"`
}

// share returns n divided by of, and 0 when of is 0 (never NaN, which
// encoding/json refuses).
func share(n, of int64) float64 {
	if of <= 0 {
		return 0
	}
	return float64(n) / float64(of)
}

// PatternStats returns the mined patterns, and the catch-all when it has
// records, by records descending and then id ascending. limit <= 0 means all.
// Patterns with no record are listed (a pattern the second pass never chose).
func (d *DB) PatternStats(ctx context.Context, limit int) ([]PatternStat, error) {
	if d.conn == nil {
		return nil, ErrClosed
	}
	var total int64
	if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM records`).Scan(&total); err != nil {
		return nil, fmt.Errorf("reading the patterns: %w", err)
	}
	// A negative LIMIT means no limit to SQLite; the guard is spelled out so
	// that limit <= 0 is not left to that.
	lim := int64(-1)
	if limit > 0 {
		lim = int64(limit)
	}
	rows, err := d.conn.QueryContext(ctx, `SELECT id, text, slots, catch_all, records, first_nanos, last_nanos
FROM patterns
WHERE catch_all = 0 OR records > 0
ORDER BY records DESC, id
LIMIT ?`, lim)
	if err != nil {
		return nil, fmt.Errorf("reading the patterns: %w", err)
	}
	defer rows.Close()
	out := []PatternStat{}
	for rows.Next() {
		var p PatternStat
		var slots string
		var catchAll int
		var first, last sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Text, &slots, &catchAll, &p.Records, &first, &last); err != nil {
			return nil, fmt.Errorf("reading the patterns: %w", err)
		}
		p.Slots = []string{}
		if slots != "" {
			p.Slots = strings.Split(slots, ",")
		}
		p.CatchAll = catchAll != 0
		p.Share = share(p.Records, total)
		if first.Valid && last.Valid {
			p.HasTimes, p.FirstNanos, p.LastNanos = true, first.Int64, last.Int64
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the patterns: %w", err)
	}
	return out, nil
}

// ValueCount is a value of a slot and how many records have it.
type ValueCount struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
	// Share is Count divided by the Total of the slot, 0 when Total is 0.
	Share float64 `json:"share"`
}

// SlotStat is what the records of a pattern hold in one of its slots.
type SlotStat struct {
	Slot int64  `json:"slot"`
	Pos  int    `json:"pos"`
	Type string `json:"type"`
	// Total is the number of values of the slot; Distinct the number of
	// different ones.
	Total    int64 `json:"total"`
	Distinct int64 `json:"distinct"`
	// Top are the most frequent values, by count descending and then value
	// ascending in byte order; never nil.
	Top []ValueCount `json:"top"`
	// Numeric is true for the types int and dec; Min and Max are then the
	// smallest and the largest value as a real number (a value too large for
	// one is held at the largest finite real, so that the result can be encoded).
	Numeric bool    `json:"numeric"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
}

// finite holds a real that SQLite made infinite (a number of hundreds of
// digits) at the largest finite one of the same sign.
func finite(f float64) float64 {
	switch {
	case math.IsNaN(f):
		return 0
	case math.IsInf(f, 1):
		return math.MaxFloat64
	case math.IsInf(f, -1):
		return -math.MaxFloat64
	}
	return f
}

// SlotStats returns, for each slot of the pattern in pos order, its counts and
// its top most frequent values (none when top <= 0). A pattern with no slot, or
// one that does not exist, gives none.
func (d *DB) SlotStats(ctx context.Context, pattern int64, top int) ([]SlotStat, error) {
	if d.conn == nil {
		return nil, ErrClosed
	}
	// The slots are read to the end first: one connection serves one open
	// result set at a time.
	rows, err := d.conn.QueryContext(ctx, `SELECT id, pos, type FROM slots WHERE pattern = ? ORDER BY pos, id`, pattern)
	if err != nil {
		return nil, fmt.Errorf("reading the slots: %w", err)
	}
	out := []SlotStat{}
	for rows.Next() {
		var s SlotStat
		if err := rows.Scan(&s.Slot, &s.Pos, &s.Type); err != nil {
			rows.Close()
			return nil, fmt.Errorf("reading the slots: %w", err)
		}
		s.Numeric = s.Type == "int" || s.Type == "dec"
		out = append(out, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("reading the slots: %w", err)
	}
	for i := range out {
		s := &out[i]
		// Both aggregates read the index slot_values_by_slot (slot, value) and
		// nothing else.
		if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(DISTINCT value) FROM slot_values WHERE slot = ?`, s.Slot).
			Scan(&s.Total, &s.Distinct); err != nil {
			return nil, fmt.Errorf("reading the values of a slot: %w", err)
		}
		if s.Numeric && s.Total > 0 {
			// Over the distinct values: the cast is made once for each.
			var lo, hi sql.NullFloat64
			if err := d.conn.QueryRowContext(ctx, `SELECT MIN(v), MAX(v) FROM
  (SELECT CAST(value AS REAL) AS v FROM slot_values WHERE slot = ? GROUP BY value)`, s.Slot).Scan(&lo, &hi); err != nil {
				return nil, fmt.Errorf("reading the range of a slot: %w", err)
			}
			s.Min, s.Max = finite(lo.Float64), finite(hi.Float64)
		}
		s.Top = []ValueCount{}
		if top <= 0 || s.Total == 0 {
			continue
		}
		vrows, err := d.conn.QueryContext(ctx, `SELECT value, COUNT(*) AS n FROM slot_values WHERE slot = ?
GROUP BY value ORDER BY n DESC, value LIMIT ?`, s.Slot, top)
		if err != nil {
			return nil, fmt.Errorf("reading the top values of a slot: %w", err)
		}
		for vrows.Next() {
			var v ValueCount
			if err := vrows.Scan(&v.Value, &v.Count); err != nil {
				vrows.Close()
				return nil, fmt.Errorf("reading the top values of a slot: %w", err)
			}
			v.Share = share(v.Count, s.Total)
			s.Top = append(s.Top, v)
		}
		err = vrows.Err()
		vrows.Close()
		if err != nil {
			return nil, fmt.Errorf("reading the top values of a slot: %w", err)
		}
	}
	return out, nil
}

// Bucket is one interval of time and the number of records in it. See
// TimeBuckets for which instants it holds.
type Bucket struct {
	StartNanos int64 `json:"start_nanos"`
	EndNanos   int64 `json:"end_nanos"`
	Count      int64 `json:"count"`
}

// satAdd returns a + b, held at the largest int64 when it would overflow (a
// and b are not negative here: b is a width).
func satAdd(a int64, b uint64) int64 {
	if b > uint64(math.MaxInt64-a) {
		return math.MaxInt64
	}
	return a + int64(b)
}

// TimeBuckets counts the records by time, for the records of the pattern
// (pattern < 0 means all records). Only records whose time is resolved count.
//
// Let lo and hi be the smallest and the largest resolved time, and diff =
// hi - lo. At most n buckets of one width w = ceil(diff / n) (at least 1 ns)
// are made, all in integer arithmetic: bucket i holds the times t with
// lo + i*w <= t < lo + (i+1)*w, and starts at lo + i*w and ends at
// lo + (i+1)*w; the last bucket is closed, so that it also holds hi when hi
// falls on its end (when diff is a multiple of n). The first bucket starts at
// lo. Fewer than n buckets are made when diff + 1 < n (the span has fewer
// nanoseconds than n) and when the last ones would lie past hi, so that no
// bucket is empty only for lack of room; a bucket inside the span may be empty.
// The result is empty (not nil, and no error) when n <= 0 or no record has a
// resolved time.
func (d *DB) TimeBuckets(ctx context.Context, pattern int64, n int) ([]Bucket, error) {
	if d.conn == nil {
		return nil, ErrClosed
	}
	out := []Bucket{}
	if n <= 0 {
		return out, nil
	}
	resolved := int(record.Resolved)
	// pattern < 0 means all records; the second form of the query is the one that
	// uses the index records_by_pattern.
	where, args := ` WHERE t_state = ?`, []any{resolved}
	if pattern >= 0 {
		where, args = ` WHERE t_state = ? AND pattern = ?`, []any{resolved, pattern}
	}
	var lo, hi sql.NullInt64
	if err := d.conn.QueryRowContext(ctx, `SELECT MIN(t_nanos), MAX(t_nanos) FROM records`+where, args...).Scan(&lo, &hi); err != nil {
		return nil, fmt.Errorf("reading the times of records: %w", err)
	}
	if !lo.Valid || !hi.Valid {
		return out, nil
	}
	// The difference is taken as unsigned: it is exact even when it does not
	// fit an int64.
	diff := uint64(hi.Int64) - uint64(lo.Int64)
	nb := uint64(n)
	if diff < nb-1 {
		nb = diff + 1
	}
	w := diff / nb
	if diff%nb != 0 {
		w++
	}
	if w == 0 {
		w = 1
	}
	if used := diff/w + 1; used < nb {
		nb = used
	}
	out = make([]Bucket, nb)
	for i := range out {
		start := satAdd(lo.Int64, uint64(i)*w)
		out[i] = Bucket{StartNanos: start, EndNanos: satAdd(start, w)}
	}
	rows, err := d.conn.QueryContext(ctx, `SELECT t_nanos FROM records`+where, args...)
	if err != nil {
		return nil, fmt.Errorf("reading the times of records: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t int64
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("reading the times of records: %w", err)
		}
		i := (uint64(t) - uint64(lo.Int64)) / w
		if i >= nb {
			// Only the largest time can be here: the last bucket is closed.
			i = nb - 1
		}
		out[i].Count++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the times of records: %w", err)
	}
	return out, nil
}
