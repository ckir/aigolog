// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ckir/aigolog/internal/query"
)

// runPatterns lists the patterns whose text holds every word, the busiest
// first. A word is matched byte for byte, in any place of the text.
func (d *DB) runPatterns(ctx context.Context, q query.Question, rowCap int) (result, error) {
	conds := make([]string, len(q.Words))
	args := make([]any, len(q.Words))
	for i, w := range q.Words {
		conds[i] = "instr(text, ?) > 0"
		args[i] = w
	}
	where := strings.Join(conds, " AND ")
	res := result{columns: []string{"pattern", "records", "text"}}
	if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM patterns WHERE `+where, args...).Scan(&res.total); err != nil {
		return result{}, fmt.Errorf("counting the patterns: %w", err)
	}
	stmt := `SELECT id, records, text FROM patterns WHERE ` + where + ` ORDER BY records DESC, id`
	if rowCap > 0 {
		stmt += ` LIMIT ?`
		args = append(args, rowCap)
	}
	rows, err := d.conn.QueryContext(ctx, stmt, args...)
	if err != nil {
		return result{}, fmt.Errorf("reading the patterns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, n int64
		var text string
		if err := rows.Scan(&id, &n, &text); err != nil {
			return result{}, fmt.Errorf("reading the patterns: %w", err)
		}
		res.rows = append(res.rows, []string{strconv.FormatInt(id, 10), strconv.FormatInt(n, 10), text})
	}
	if err := rows.Err(); err != nil {
		return result{}, fmt.Errorf("reading the patterns: %w", err)
	}
	return res, nil
}

// runSlots gives the statistics of each slot of the one pattern, with its five
// most frequent values.
func (d *DB) runSlots(ctx context.Context, q query.Question) (result, error) {
	stats, err := d.SlotStats(ctx, q.Patterns[0], 5)
	if err != nil {
		return result{}, err
	}
	res := result{columns: []string{"slot", "pos", "type", "values", "distinct", "min", "max", "top"}, total: int64(len(stats))}
	for _, s := range stats {
		lo, hi := "", ""
		if s.Numeric && s.Total > 0 {
			lo = strconv.FormatFloat(s.Min, 'g', -1, 64)
			hi = strconv.FormatFloat(s.Max, 'g', -1, 64)
		}
		top := make([]string, len(s.Top))
		for i, v := range s.Top {
			top[i] = fmt.Sprintf("%s (%d)", v.Value, v.Count)
		}
		res.rows = append(res.rows, []string{
			strconv.FormatInt(s.Slot, 10), strconv.Itoa(s.Pos), s.Type,
			strconv.FormatInt(s.Total, 10), strconv.FormatInt(s.Distinct, 10), lo, hi, strings.Join(top, ", "),
		})
	}
	return res, nil
}

// runCount counts the records of the set: one row.
func (d *DB) runCount(ctx context.Context, q query.Question) (result, error) {
	f, err := d.facts(ctx, setOf(q))
	if err != nil {
		return result{}, err
	}
	return result{columns: []string{"rows"}, rows: [][]string{{strconv.FormatInt(f.Rows, 10)}}, total: 1, facts: f}, nil
}

// runTop counts, for each value of the column By in the records of the set, the
// records that have it, the most frequent first and equal counts by value in
// byte order. A record that has the value in two slots of the column counts
// once. rowCap > 0 cuts the list in SQL; total is the number of values.
func (d *DB) runTop(ctx context.Context, q query.Question, rowCap int) (result, error) {
	s := setOf(q)
	f, err := d.facts(ctx, s)
	if err != nil {
		return result{}, err
	}
	slots := columnSlots(q, q.By)
	where := ` FROM slot_values sv WHERE sv.slot IN (` + placeholders(len(slots)) + `)`
	args := int64Args(slots)
	if s.where != "" {
		where += ` AND sv.record IN (` + s.ids() + `)`
		args = append(args, s.args...)
	}
	res := result{columns: []string{"value", "rows", "share"}, facts: f}
	if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(DISTINCT sv.value)`+where, args...).Scan(&res.total); err != nil {
		return result{}, fmt.Errorf("counting the values of a column: %w", err)
	}
	stmt := `SELECT sv.value, COUNT(DISTINCT sv.record) AS n` + where + ` GROUP BY sv.value ORDER BY n DESC, sv.value`
	if rowCap > 0 {
		stmt += ` LIMIT ?`
		args = append(args, rowCap)
	}
	rows, err := d.conn.QueryContext(ctx, stmt, args...)
	if err != nil {
		return result{}, fmt.Errorf("reading the values of a column: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		var n int64
		if err := rows.Scan(&v, &n); err != nil {
			return result{}, fmt.Errorf("reading the values of a column: %w", err)
		}
		sh := 0.0
		if f.Rows > 0 {
			sh = float64(n) / float64(f.Rows)
		}
		res.rows = append(res.rows, []string{v, strconv.FormatInt(n, 10), fmt.Sprintf("%.4f", sh)})
	}
	if err := rows.Err(); err != nil {
		return result{}, fmt.Errorf("reading the values of a column: %w", err)
	}
	return res, nil
}
