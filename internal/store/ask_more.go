// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/ckir/aigolog/internal/query"
	"github.com/ckir/aigolog/internal/record"
)

// defaultBuckets is the number of intervals of a buckets question that sets
// none.
const defaultBuckets = 24

// stampLayout is how a time is written in a cell.
const stampLayout = "2006-01-02 15:04:05"

// cellTime writes a time of nanoseconds since 1970 as a cell.
func cellTime(nanos int64) string { return time.Unix(0, nanos).UTC().Format(stampLayout) }

// runPairs counts, for each value of the column By in the records of the set,
// the records that have it and the distinct values of the column With in those
// records, the busiest value first and equal counts by value in byte order. A
// record that holds a value in two slots of By, or two values in With, counts
// once. A value whose records hold no value of With is kept, with 0 distinct,
// so that total is the number of rows there are. rowCap > 0 cuts the list in
// SQL.
func (d *DB) runPairs(ctx context.Context, q query.Question, rowCap int) (result, error) {
	s := setOf(q)
	f, err := d.facts(ctx, s)
	if err != nil {
		return result{}, err
	}
	by, with := columnSlots(q, q.By), columnSlots(q, q.With)
	where := ` WHERE a.slot IN (` + placeholders(len(by)) + `)`
	byArgs := int64Args(by)
	if s.where != "" {
		where += ` AND a.record IN (` + s.ids() + `)`
		byArgs = append(byArgs, s.args...)
	}
	res := result{columns: []string{"value", "rows", "distinct"}, facts: f}
	if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(DISTINCT a.value) FROM slot_values a`+where, byArgs...).Scan(&res.total); err != nil {
		return result{}, fmt.Errorf("counting the values of a column: %w", err)
	}
	stmt := `SELECT a.value, COUNT(DISTINCT a.record) AS n, COUNT(DISTINCT b.value)
FROM slot_values a LEFT JOIN slot_values b ON b.record = a.record AND b.slot IN (` + placeholders(len(with)) + `)` +
		where + ` GROUP BY a.value ORDER BY n DESC, a.value`
	args := append(int64Args(with), byArgs...)
	if rowCap > 0 {
		stmt += ` LIMIT ?`
		args = append(args, rowCap)
	}
	rows, err := d.conn.QueryContext(ctx, stmt, args...)
	if err != nil {
		return result{}, fmt.Errorf("reading the pairs of columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		var n, distinct int64
		if err := rows.Scan(&v, &n, &distinct); err != nil {
			return result{}, fmt.Errorf("reading the pairs of columns: %w", err)
		}
		res.rows = append(res.rows, []string{v, strconv.FormatInt(n, 10), strconv.FormatInt(distinct, 10)})
	}
	if err := rows.Err(); err != nil {
		return result{}, fmt.Errorf("reading the pairs of columns: %w", err)
	}
	return res, nil
}

// mulSat returns a * b, held at the largest uint64 when it would overflow.
func mulSat(a, b uint64) uint64 {
	if a != 0 && b > math.MaxUint64/a {
		return math.MaxUint64
	}
	return a * b
}

// runBuckets counts the records of the set by time, over q.Buckets intervals
// (24 when it is 0) of one width between the least and the greatest resolved
// time of the set: the arithmetic of TimeBuckets. With lo and hi the least and
// the greatest time, diff = hi - lo and n the number of intervals, the width is
// w = ceil(diff / n), at least 1 ns; interval i holds the times t with
// lo + i*w <= t < lo + (i+1)*w, and the last is closed on its right edge so that
// it also holds hi when diff is a multiple of n. There are always n rows; an
// interval that no time falls in has 0. A record with no resolved time is in no
// interval (Facts.NoTime counts them). A set with no resolved time has no rows.
func (d *DB) runBuckets(ctx context.Context, q query.Question) (result, error) {
	s := setOf(q)
	f, err := d.facts(ctx, s)
	if err != nil {
		return result{}, err
	}
	res := result{columns: []string{"start", "end", "rows"}, facts: f}
	if !f.HasTimes {
		return res, nil
	}
	n := q.Buckets
	if n == 0 {
		n = defaultBuckets
	}
	lo := f.FirstNanos
	diff := uint64(f.LastNanos) - uint64(lo)
	nb := uint64(n)
	w := diff / nb
	if diff%nb != 0 {
		w++
	}
	if w == 0 {
		w = 1
	}
	counts := make([]int64, n)
	cond := ` WHERE r.t_state = ?`
	if s.where != "" {
		cond = s.where + ` AND r.t_state = ?`
	}
	rows, err := d.conn.QueryContext(ctx, `SELECT r.t_nanos FROM records r`+cond, append(append([]any(nil), s.args...), int(record.Resolved))...)
	if err != nil {
		return result{}, fmt.Errorf("reading the times of records: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t int64
		if err := rows.Scan(&t); err != nil {
			return result{}, fmt.Errorf("reading the times of records: %w", err)
		}
		i := (uint64(t) - uint64(lo)) / w
		if i >= nb {
			// Only the greatest time can be here: the last interval is closed.
			i = nb - 1
		}
		counts[i]++
	}
	if err := rows.Err(); err != nil {
		return result{}, fmt.Errorf("reading the times of records: %w", err)
	}
	res.total = int64(n)
	for i, c := range counts {
		start := satAdd(lo, mulSat(uint64(i), w))
		end := satAdd(lo, mulSat(uint64(i)+1, w))
		res.rows = append(res.rows, []string{cellTime(start), cellTime(end), strconv.FormatInt(c, 10)})
	}
	return res, nil
}

// runRows gives the first records of the set, or the last (q.Last) with the
// last of them first. rowCap > 0 cuts the list in SQL; total is the number of
// records of the set.
func (d *DB) runRows(ctx context.Context, q query.Question, rowCap int) (result, error) {
	s := setOf(q)
	f, err := d.facts(ctx, s)
	if err != nil {
		return result{}, err
	}
	res := result{columns: []string{"id", "source", "line", "time", "text"}, total: f.Rows, facts: f}
	stmt := `SELECT r.id, src.name, r.line, r.t_state, r.t_nanos, r.raw FROM records r JOIN sources src ON src.id = r.source` +
		s.where + ` ORDER BY r.id`
	if q.Last {
		stmt += ` DESC`
	}
	args := append([]any(nil), s.args...)
	if rowCap > 0 {
		stmt += ` LIMIT ?`
		args = append(args, rowCap)
	}
	rows, err := d.conn.QueryContext(ctx, stmt, args...)
	if err != nil {
		return result{}, fmt.Errorf("reading the records: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, line, nanos int64
		var state int
		var name, raw string
		if err := rows.Scan(&id, &name, &line, &state, &nanos, &raw); err != nil {
			return result{}, fmt.Errorf("reading the records: %w", err)
		}
		when := ""
		if state == int(record.Resolved) {
			when = cellTime(nanos)
		}
		res.rows = append(res.rows, []string{strconv.FormatInt(id, 10), name, strconv.FormatInt(line, 10), when, cutCell(raw)})
	}
	if err := rows.Err(); err != nil {
		return result{}, fmt.Errorf("reading the records: %w", err)
	}
	return res, nil
}

// runWhere lists every slot that holds the value, with the number of records
// that hold it there: the busiest first and equal counts by slot. CROSS JOIN
// fixes the order of the join: the slots first, and the index
// slot_values_by_slot (slot, value) searched once for each. With a plain JOIN
// the planner (which has no statistics) scans every value instead and looks the
// slot up for each; EXPLAIN QUERY PLAN shows the difference. It covers no set of
// records: the facts are empty.
func (d *DB) runWhere(ctx context.Context, q query.Question) (result, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT p.id, s.id, s.pos, s.type, COUNT(DISTINCT sv.record) AS n, p.text
FROM slots s CROSS JOIN slot_values sv ON sv.slot = s.id AND sv.value = ? JOIN patterns p ON p.id = s.pattern
GROUP BY s.id, p.id, s.pos, s.type, p.text ORDER BY n DESC, s.id`, q.Value)
	if err != nil {
		return result{}, fmt.Errorf("looking for a value: %w", err)
	}
	defer rows.Close()
	res := result{columns: []string{"pattern", "slot", "pos", "type", "rows", "text"}}
	for rows.Next() {
		var pattern, slot, pos, n int64
		var typ, text string
		if err := rows.Scan(&pattern, &slot, &pos, &typ, &n, &text); err != nil {
			return result{}, fmt.Errorf("looking for a value: %w", err)
		}
		res.rows = append(res.rows, []string{
			strconv.FormatInt(pattern, 10), strconv.FormatInt(slot, 10), strconv.FormatInt(pos, 10), typ, strconv.FormatInt(n, 10), text,
		})
	}
	if err := rows.Err(); err != nil {
		return result{}, fmt.Errorf("looking for a value: %w", err)
	}
	res.total = int64(len(res.rows))
	return res, nil
}
