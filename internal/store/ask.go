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
	"time"
	"unicode/utf8"

	"github.com/ckir/aigolog/internal/query"
)

// result is what a verb finds, before the limits of the answer are applied.
type result struct {
	columns []string
	rows    [][]string
	total   int64 // the rows there are before any cut
	facts   query.Facts
}

// Run answers a question about the records of the database. It returns the
// *query.UsageError of a question that is wrong (one the model refuses, or one
// that names a slot or a pattern the database does not have), ErrClosed after
// Close, query.ErrTimeLimit with no figures when lim.Time ran out, and the
// error of ctx when the caller cancelled it. lim.Rows, lim.Bytes and lim.Time
// at 0 or less mean no cap.
//
// The rows are cut to lim.Rows (and to q.Limit, when set and smaller), and then
// by their bytes to lim.Bytes; Cut says which, "rows" when both applied, and
// Total is the number of rows before the cut. Each cell is held to
// query.MaxCellBytes.
func (d *DB) Run(ctx context.Context, q query.Question, lim query.Limits) (query.Answer, error) {
	if d.conn == nil {
		return query.Answer{}, ErrClosed
	}
	if err := q.Validate(); err != nil {
		return query.Answer{}, err
	}
	start := time.Now()
	rctx := ctx
	if lim.Time > 0 {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, lim.Time)
		defer cancel()
	}
	res, err := d.answer(rctx, q, rowCap(q, lim))
	if err != nil {
		var ue *query.UsageError
		switch {
		case errors.As(err, &ue):
			return query.Answer{}, err
		case ctx.Err() != nil:
			return query.Answer{}, ctx.Err()
		case errors.Is(rctx.Err(), context.DeadlineExceeded):
			return query.Answer{}, query.ErrTimeLimit
		}
		return query.Answer{}, err
	}
	a := query.Answer{Question: cloneQuestion(q), Columns: res.columns, Rows: res.rows, Total: res.total, Facts: res.facts}
	if a.Columns == nil {
		a.Columns = []string{}
	}
	if a.Rows == nil {
		a.Rows = [][]string{}
	}
	cutAnswer(&a, rowCap(q, lim), lim.Bytes)
	a.ElapsedMillis = time.Since(start).Milliseconds()
	return a, nil
}

// rowCap is the most rows the answer may hold: the smaller of lim.Rows and
// q.Limit that is above 0; 0 when neither is.
func rowCap(q query.Question, lim query.Limits) int {
	n := lim.Rows
	if n < 0 {
		n = 0
	}
	if q.Limit > 0 && (n == 0 || q.Limit < n) {
		n = q.Limit
	}
	return n
}

// answer checks the numbers of the question and runs its verb. A verb may
// return fewer rows than there are (rowCap of them, when it cuts in SQL), and
// says how many there are in total.
func (d *DB) answer(ctx context.Context, q query.Question, rowCap int) (result, error) {
	if err := d.checkNumbers(ctx, q); err != nil {
		return result{}, err
	}
	switch q.Verb {
	case query.Patterns:
		return d.runPatterns(ctx, q, rowCap)
	case query.Slots:
		return d.runSlots(ctx, q)
	case query.Count:
		return d.runCount(ctx, q)
	case query.Top:
		return d.runTop(ctx, q, rowCap)
	case query.Pairs, query.Buckets, query.Rows, query.Where:
		return result{}, &query.UsageError{Msg: fmt.Sprintf("verb: the verb %s is not built yet", q.Verb)}
	}
	return result{}, &query.UsageError{Msg: fmt.Sprintf("verb: %q is not a verb", string(q.Verb))}
}

// checkNumbers returns a UsageError for the first pattern, and then the first
// slot of a column, that the database has no row for. Pattern 0, the catch-all,
// is a row of patterns.
func (d *DB) checkNumbers(ctx context.Context, q query.Question) error {
	if err := d.missing(ctx, "patterns", "pattern", q.Patterns, ""); err != nil {
		return err
	}
	for _, c := range q.Columns {
		if err := d.missing(ctx, "slots", "slot", c.Slots, fmt.Sprintf(" (column %q)", c.Name)); err != nil {
			return err
		}
	}
	return nil
}

// missing returns a UsageError that names the first of ids that is not the id
// of a row of table (patterns or slots). field is the key of the question that
// holds the numbers: the table name.
func (d *DB) missing(ctx context.Context, table, noun string, ids []int64, note string) error {
	if len(ids) == 0 {
		return nil
	}
	field := table
	if table == "slots" {
		field = "columns"
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := d.conn.QueryContext(ctx, `SELECT id FROM `+table+` WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return fmt.Errorf("checking the %ss of the question: %w", noun, err)
	}
	have := make(map[int64]bool, len(ids))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("checking the %ss of the question: %w", noun, err)
		}
		have[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("checking the %ss of the question: %w", noun, err)
	}
	for _, id := range ids {
		if !have[id] {
			return &query.UsageError{Msg: fmt.Sprintf("%s: %s %d does not exist%s", field, noun, id, note)}
		}
	}
	return nil
}

// cutAnswer applies the caps to the rows of a: rowCap rows, then bytes of
// cells, both when above 0. Each cell is held to query.MaxCellBytes first, so
// that the cap counts what is shown.
func cutAnswer(a *query.Answer, rowCap, bytes int) {
	for _, r := range a.Rows {
		for i, c := range r {
			r[i] = cutCell(c)
		}
	}
	if rowCap > 0 && len(a.Rows) > rowCap {
		a.Rows = a.Rows[:rowCap]
	}
	if int64(len(a.Rows)) < a.Total {
		a.Cut = "rows"
	}
	if bytes <= 0 {
		return
	}
	used := 0
	keep := 0
	for _, r := range a.Rows {
		n := 0
		for _, c := range r {
			n += len(c)
		}
		if used+n > bytes {
			break
		}
		used += n
		keep++
	}
	if keep < len(a.Rows) {
		a.Rows = a.Rows[:keep]
		if a.Cut == "" {
			a.Cut = "bytes"
		}
	}
}

// cutCell holds a cell to query.MaxCellBytes bytes, cutting between two
// characters. The miner has a cut of its own; the store does not import it.
func cutCell(s string) string {
	if len(s) <= query.MaxCellBytes {
		return s
	}
	n := query.MaxCellBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// cloneQuestion copies q so that the answer shares no slice with the caller.
func cloneQuestion(q query.Question) query.Question {
	c := q
	c.Words = append([]string(nil), q.Words...)
	c.Patterns = append([]int64(nil), q.Patterns...)
	c.Filters = append([]query.Filter(nil), q.Filters...)
	c.Columns = make([]query.Column, len(q.Columns))
	for i, col := range q.Columns {
		c.Columns[i] = query.Column{Name: col.Name, Slots: append([]int64(nil), col.Slots...)}
	}
	if len(q.Words) == 0 {
		c.Words = nil
	}
	if len(q.Patterns) == 0 {
		c.Patterns = nil
	}
	if len(q.Filters) == 0 {
		c.Filters = nil
	}
	if len(q.Columns) == 0 {
		c.Columns = nil
	}
	return c
}
