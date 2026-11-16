// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package ingest reads the inputs of a run with the text reader and stores
// them in the database of the run.
package ingest

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/ckir/aigolog/internal/reader/text"
	"github.com/ckir/aigolog/internal/record"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/timeparse"
)

// defaultBatch is the number of records appended to the database at once when
// Options.BatchSize does not say.
const defaultBatch = 5000

// Input is one thing to read.
type Input struct {
	// Name is how the input is called in reports: a path, or - for standard
	// input.
	Name string
	// R is where the input is read from.
	R io.Reader
	// ModTime is the modification time of the file; it is zero for standard
	// input and for anything that is not a regular file.
	ModTime time.Time
}

// Options say how Run reads.
type Options struct {
	// AsOf is the anchor for the logs that have no year, in wall-clock
	// nanoseconds since 1970; nil means that it is not given.
	AsOf *int64
	// Now is the current time; the zero value means time.Now().
	Now time.Time
	// Zone is the zone in which ModTime and Now are read as wall-clock time;
	// nil means time.Local.
	Zone *time.Location
	// BatchSize is how many records are appended to the database at once; zero
	// or less means 5,000.
	BatchSize int
}

// SourceReport tells what became of one input.
type SourceReport struct {
	// ID is the number of the source in the database; it is 0 for an input
	// that has no records, which takes no row.
	ID     int
	Source store.Source
	Totals store.SourceTotals
}

// Result is what Run did. When Run returns an error, Result holds what had
// been done up to then.
type Result struct {
	// Sources has one report for each input that was finished, in the order
	// given.
	Sources []SourceReport
	// Records is the number of records stored.
	Records int64
	// Lines is the number of lines read, over all inputs.
	Lines int64
}

// InputError is an error that belongs to one input: it is not text, or it
// cannot be read.
type InputError struct {
	Name string
	Err  error
}

// Error implements error.
func (e *InputError) Error() string { return e.Name + ": " + e.Err.Error() }

// Unwrap returns the error that is wrapped.
func (e *InputError) Unwrap() error { return e.Err }

// ErrNothingToAnalyse is returned by Run when no input has a record.
var ErrNothingToAnalyse = errors.New("nothing to analyse: every input is empty")

// Run reads each input, in the order given, and stores its records in db.
//
// An error of the reader (text.New or Next, including a *text.NotTextError) is
// returned as an *InputError. An error of the store, and the error of ctx, are
// returned as they are. When Run fails, the database is not marked complete.
// When no input has a record, Run returns the Result and ErrNothingToAnalyse.
func Run(ctx context.Context, db *store.DB, inputs []Input, o Options) (Result, error) {
	var res Result
	zone := o.Zone
	if zone == nil {
		zone = time.Local
	}
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	size := o.BatchSize
	if size <= 0 {
		size = defaultBatch
	}
	batch := make([]record.Record, 0, size)
	stored := false
	for _, in := range inputs {
		rep, lines, err := runInput(ctx, db, in, o, zone, now, batch)
		res.Lines += lines
		if err != nil {
			return res, err
		}
		res.Sources = append(res.Sources, rep)
		res.Records += rep.Totals.Records
		if rep.ID != 0 {
			stored = true
		}
	}
	if !stored {
		return res, ErrNothingToAnalyse
	}
	if err := db.MarkComplete(ctx); err != nil {
		return res, err
	}
	return res, nil
}

// runInput stores one input. batch is an empty slice whose capacity is the
// batch size. It returns the report and the number of lines read.
func runInput(ctx context.Context, db *store.DB, in Input, o Options, zone *time.Location, now time.Time, batch []record.Record) (SourceReport, int64, error) {
	rd, err := text.New(in.R)
	if err != nil {
		return SourceReport{}, 0, &InputError{Name: in.Name, Err: err}
	}
	first, err := rd.Next()
	if err == io.EOF {
		return SourceReport{Source: store.Source{Name: in.Name, Format: "text", Encoding: rd.Encoding()}}, rd.Lines(), nil
	}
	if err != nil {
		return SourceReport{}, rd.Lines(), &InputError{Name: in.Name, Err: err}
	}

	src := store.Source{Name: in.Name, Format: "text", Encoding: rd.Encoding(), TimeKind: store.TimeNone}
	pending := false
	if l, found := rd.Layout(); found {
		src.Layout = l.Name
		switch {
		case l.YearPending:
			pending = true
			src.TimeKind = store.TimeWallYearInferred
			src.AnchorKind, src.AnchorNanos = anchor(in, o, zone, now)
		case l.Exact:
			src.TimeKind = store.TimeExact
		default:
			src.TimeKind = store.TimeWall
		}
	}
	id, err := db.AddSource(ctx, src)
	if err != nil {
		return SourceReport{}, rd.Lines(), err
	}

	rec := first
	for {
		rec.Source = id
		batch = append(batch, rec)
		if len(batch) == cap(batch) {
			if err := flush(ctx, db, batch); err != nil {
				return SourceReport{}, rd.Lines(), err
			}
			clear(batch)
			batch = batch[:0]
		}
		rec, err = rd.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return SourceReport{}, rd.Lines(), &InputError{Name: in.Name, Err: err}
		}
	}
	if len(batch) > 0 {
		if err := flush(ctx, db, batch); err != nil {
			return SourceReport{}, rd.Lines(), err
		}
		clear(batch)
	}

	if pending {
		if err := db.ResolveYears(ctx, id, timeparse.NewYearResolver(src.AnchorNanos)); err != nil {
			return SourceReport{}, rd.Lines(), err
		}
	}
	totals, err := db.FinishSource(ctx, id, rd.Lines(), rd.Bytes())
	if err != nil {
		return SourceReport{}, rd.Lines(), err
	}
	return SourceReport{ID: id, Source: src, Totals: totals}, rd.Lines(), nil
}

// flush appends a batch, unless ctx is done.
func flush(ctx context.Context, db *store.DB, batch []record.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return db.Append(ctx, batch)
}

// anchor returns the kind and the wall-clock nanoseconds of the anchor of a
// source that has no year.
func anchor(in Input, o Options, zone *time.Location, now time.Time) (string, int64) {
	switch {
	case o.AsOf != nil:
		return store.AnchorAsOf, *o.AsOf
	case !in.ModTime.IsZero():
		return store.AnchorModTime, timeparse.WallNanos(in.ModTime.In(zone))
	default:
		return store.AnchorNow, timeparse.WallNanos(now.In(zone))
	}
}
