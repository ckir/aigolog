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
	"strings"

	"github.com/ckir/aigolog/internal/query"
	"github.com/ckir/aigolog/internal/record"
)

// placeholders returns n question marks joined by commas: "?,?,?". n is always
// at least 1, because Question.Validate bounds every list from below.
func placeholders(n int) string {
	if n <= 1 {
		return "?"
	}
	return strings.Repeat("?,", n-1) + "?"
}

// int64Args returns ids as the arguments of a statement.
func int64Args(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// columnSlots returns the slots of the column of q that has the name. A name
// the question does not define has none; Validate has refused such a question.
func columnSlots(q query.Question, name string) []int64 {
	for _, c := range q.Columns {
		if c.Name == name {
			return c.Slots
		}
	}
	return nil
}

// recordSet is the part after FROM records r of the statements that cover the
// records a question names: the patterns and the filters. where is empty or
// starts with " WHERE "; every number and value is in args, never in the text.
type recordSet struct {
	where string
	args  []any
}

func setOf(q query.Question) recordSet {
	var conds []string
	var args []any
	if len(q.Patterns) > 0 {
		conds = append(conds, "r.pattern IN ("+placeholders(len(q.Patterns))+")")
		args = append(args, int64Args(q.Patterns)...)
	}
	for _, f := range q.Filters {
		slots := columnSlots(q, f.Column)
		conds = append(conds, "EXISTS (SELECT 1 FROM slot_values f WHERE f.record = r.id AND f.slot IN ("+
			placeholders(len(slots))+") AND f.value = ?)")
		args = append(args, int64Args(slots)...)
		args = append(args, f.Value)
	}
	if len(conds) == 0 {
		return recordSet{}
	}
	return recordSet{where: " WHERE " + strings.Join(conds, " AND "), args: args}
}

// ids is the statement that selects the ids of the records of the set.
func (s recordSet) ids() string { return "SELECT r.id FROM records r" + s.where }

// facts states what the records of the set are: how many, the least and the
// greatest resolved time, how many have no resolved time, and the first and the
// last by id. The set is empty
// when no pattern or filter names any; then it is every record.
func (d *DB) facts(ctx context.Context, s recordSet) (query.Facts, error) {
	resolved := int(record.Resolved)
	var n, noTime int64
	var lo, hi, first, last sql.NullInt64
	err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*),
  MIN(CASE WHEN r.t_state = ? THEN r.t_nanos END), MAX(CASE WHEN r.t_state = ? THEN r.t_nanos END),
  COUNT(CASE WHEN r.t_state <> ? THEN 1 END),
  MIN(r.id), MAX(r.id) FROM records r`+s.where, append([]any{resolved, resolved, resolved}, s.args...)...).
		Scan(&n, &lo, &hi, &noTime, &first, &last)
	if err != nil {
		return query.Facts{}, fmt.Errorf("counting the records of the question: %w", err)
	}
	f := query.Facts{Rows: n, NoTime: noTime}
	if n == 0 {
		return f, nil
	}
	if lo.Valid && hi.Valid {
		f.HasTimes, f.FirstNanos, f.LastNanos = true, lo.Int64, hi.Int64
	}
	if f.First, err = d.refOf(ctx, first.Int64); err != nil {
		return query.Facts{}, err
	}
	if f.Last, err = d.refOf(ctx, last.Int64); err != nil {
		return query.Facts{}, err
	}
	return f, nil
}

// refOf returns the position of the record with the id.
func (d *DB) refOf(ctx context.Context, id int64) (*query.Ref, error) {
	var r query.Ref
	if err := d.conn.QueryRowContext(ctx, `SELECT source, line FROM records WHERE id = ?`, id).Scan(&r.Source, &r.Line); err != nil {
		return nil, fmt.Errorf("reading the position of a record: %w", err)
	}
	return &r, nil
}
