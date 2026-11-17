// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/ckir/aigolog/internal/record"
)

// PatternRow is a pattern as the store keeps it. Slots are the types of its
// slots, in order. The catch-all has ID 0 and no slots.
type PatternRow struct {
	ID       int64
	Text     string
	Slots    []string
	CatchAll bool
}

// WritePatterns inserts the patterns and one row of slots for each slot (pos
// from 0), all in one transaction. It returns, for each pattern, the ids of
// its slots in order; a pattern with no slots has an empty list.
func (d *DB) WritePatterns(ctx context.Context, ps []PatternRow) (map[int64][]int64, error) {
	if d.conn == nil {
		return nil, ErrClosed
	}
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("writing the patterns: %w", err)
	}
	defer tx.Rollback()
	pat, err := tx.PrepareContext(ctx, `INSERT INTO patterns (id, text, slots, catch_all) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("writing the patterns: %w", err)
	}
	defer pat.Close()
	slot, err := tx.PrepareContext(ctx, `INSERT INTO slots (pattern, pos, type) VALUES (?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("writing the patterns: %w", err)
	}
	defer slot.Close()
	ids := make(map[int64][]int64, len(ps))
	for _, p := range ps {
		catchAll := 0
		if p.CatchAll {
			catchAll = 1
		}
		if _, err := pat.ExecContext(ctx, p.ID, p.Text, strings.Join(p.Slots, ","), catchAll); err != nil {
			return nil, fmt.Errorf("writing the patterns: %w", err)
		}
		slotIDs := make([]int64, 0, len(p.Slots))
		for pos, typ := range p.Slots {
			res, err := slot.ExecContext(ctx, p.ID, pos, typ)
			if err != nil {
				return nil, fmt.Errorf("writing the patterns: %w", err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return nil, fmt.Errorf("writing the patterns: %w", err)
			}
			slotIDs = append(slotIDs, id)
		}
		ids[p.ID] = slotIDs
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("writing the patterns: %w", err)
	}
	return ids, nil
}

// RecordText is what the second pass reads of a record: its id, its source, the
// length of its stamp and its text.
type RecordText struct {
	ID     int64
	Source int
	Len    int
	Raw    string
}

// RecordTexts returns the records with id > afterID, in id order, at most
// limit of them. It stops adding records once their Raw adds up to maxBytes,
// the record that reaches it included, so a chunk holds about maxBytes and at
// least one record is returned when there is one. The result set is closed
// before it returns.
func (d *DB) RecordTexts(ctx context.Context, afterID int64, limit, maxBytes int) ([]RecordText, error) {
	if d.conn == nil {
		return nil, ErrClosed
	}
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, source, t_len, raw FROM records WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("reading the texts of records: %w", err)
	}
	defer rows.Close()
	var out []RecordText
	total := 0
	for rows.Next() {
		var r RecordText
		if err := rows.Scan(&r.ID, &r.Source, &r.Len, &r.Raw); err != nil {
			return nil, fmt.Errorf("reading the texts of records: %w", err)
		}
		out = append(out, r)
		total += len(r.Raw)
		if total >= maxBytes {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the texts of records: %w", err)
	}
	return out, nil
}

// Assignment is the pattern of one record and its values: Values[i] is the
// value of the slot whose id is Slots[i].
type Assignment struct {
	Record, Pattern int64
	Slots           []int64
	Values          []string
}

// Assign sets the pattern of each record and stores its values, all in one
// transaction. Values are parameters, never part of a statement. An assignment
// whose Slots and Values differ in length is refused and nothing is written.
func (d *DB) Assign(ctx context.Context, as []Assignment) error {
	if d.conn == nil {
		return ErrClosed
	}
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("storing the patterns of records: %w", err)
	}
	defer tx.Rollback()
	set, err := tx.PrepareContext(ctx, `UPDATE records SET pattern = ? WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("storing the patterns of records: %w", err)
	}
	defer set.Close()
	ins, err := tx.PrepareContext(ctx, `INSERT INTO slot_values (record, slot, value) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("storing the patterns of records: %w", err)
	}
	defer ins.Close()
	for _, a := range as {
		if len(a.Slots) != len(a.Values) {
			return fmt.Errorf("storing the patterns of records: record %d has %d values for %d slots", a.Record, len(a.Values), len(a.Slots))
		}
		if _, err := set.ExecContext(ctx, a.Pattern, a.Record); err != nil {
			return fmt.Errorf("storing the patterns of records: %w", err)
		}
		for i, v := range a.Values {
			if _, err := ins.ExecContext(ctx, a.Record, a.Slots[i], v); err != nil {
				return fmt.Errorf("storing the values of records: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("storing the patterns of records: %w", err)
	}
	return nil
}

// FinishMining ends the second pass: it sets, for each pattern, the number of
// its records and the smallest and largest time of those records whose time is
// resolved (NULL when none is), then makes the indexes. The indexes are made
// after the load, as one build is faster than the upkeep of three indexes
// (measured on 2026-11-17: 24 s against 69 s for 2.6 million values). It is
// safe to call twice.
func (d *DB) FinishMining(ctx context.Context) error {
	if d.conn == nil {
		return ErrClosed
	}
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("finishing the patterns: %w", err)
	}
	defer tx.Rollback()
	// One pass over records: the index on records.pattern does not exist yet.
	resolved := int(record.Resolved)
	if _, err := tx.ExecContext(ctx, `UPDATE patterns SET records = s.n, first_nanos = s.lo, last_nanos = s.hi
FROM (SELECT pattern AS p, COUNT(*) AS n,
        MIN(CASE WHEN t_state = ? THEN t_nanos END) AS lo,
        MAX(CASE WHEN t_state = ? THEN t_nanos END) AS hi
      FROM records GROUP BY pattern) AS s
WHERE patterns.id = s.p`, resolved, resolved); err != nil {
		return fmt.Errorf("finishing the patterns: %w", err)
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS records_by_pattern ON records (pattern)`,
		`CREATE INDEX IF NOT EXISTS slot_values_by_slot ON slot_values (slot, value)`,
		`CREATE INDEX IF NOT EXISTS slot_values_by_record ON slot_values (record)`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("making the indexes: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("finishing the patterns: %w", err)
	}
	return nil
}
