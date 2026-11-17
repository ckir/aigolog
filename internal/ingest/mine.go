// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package ingest

import (
	"context"

	"github.com/ckir/aigolog/internal/miner"
	"github.com/ckir/aigolog/internal/store"
)

const (
	// mineChunkRecords is the most records the second pass reads at once.
	mineChunkRecords = 5000
	// mineChunkBytes is how much record text the second pass reads at once,
	// whatever the number of records (the memory of a run must not grow with
	// its input, design 7.4).
	mineChunkBytes = 8 << 20
)

// afterChunk, when not nil, is called by mine after each chunk of records has
// been assigned. It is nil in production: a test sets it to stop the run, or
// to measure the heap, between two chunks.
var afterChunk func()

// messageOf returns the message of a record: its text without the time stamp
// that opens it. Both passes take the message through this function, so that
// Match finds the pattern that Learn made. A stamp length that is negative or
// longer than the text gives the whole text or an empty message.
func messageOf(raw string, stampLen int) string {
	if stampLen < 0 {
		stampLen = 0
	}
	return raw[min(stampLen, len(raw)):]
}

// mine is the second pass: it ends the learning of m, writes the patterns, and
// assigns a pattern and its values to every record of the database, in chunks,
// in the order of their ids (so all the sources of a run share one set of
// patterns). It checks ctx between chunks and returns its error as it is.
func mine(ctx context.Context, db *store.DB, m *miner.Miner) error {
	pats := m.Finish()
	rows := make([]store.PatternRow, len(pats))
	for i, p := range pats {
		slots := make([]string, len(p.Slots))
		for j, s := range p.Slots {
			slots[j] = string(s)
		}
		rows[i] = store.PatternRow{ID: int64(p.ID), Text: p.Text, Slots: slots, CatchAll: p.CatchAll}
	}
	slotIDs, err := db.WritePatterns(ctx, rows)
	if err != nil {
		return err
	}
	var after int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		texts, err := db.RecordTexts(ctx, after, mineChunkRecords, mineChunkBytes)
		if err != nil {
			return err
		}
		if len(texts) == 0 {
			break
		}
		// The values Match returns may share memory with the message; Assign
		// writes them before this chunk is let go.
		as := make([]store.Assignment, len(texts))
		for i, t := range texts {
			id, values := m.Match(messageOf(t.Raw, t.Len))
			as[i] = store.Assignment{Record: t.ID, Pattern: int64(id), Slots: slotIDs[int64(id)], Values: values}
		}
		if err := db.Assign(ctx, as); err != nil {
			return err
		}
		after = texts[len(texts)-1].ID
		if afterChunk != nil {
			afterChunk()
		}
	}
	return db.FinishMining(ctx)
}
