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

	"github.com/ckir/aigolog/internal/miner"
	"github.com/ckir/aigolog/internal/reader/text"
	"github.com/ckir/aigolog/internal/record"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/timeparse"
)

// defaultBatch is the number of records appended to the database at once when
// Options.BatchSize does not say.
const defaultBatch = 5000

// maxBatchBytes is how much record text a batch may hold before it is
// appended, whatever the number of records: records can be as long as
// text.MaxRecordBytes, and a batch of defaultBatch of them would hold gigabytes.
const maxBatchBytes = 8 << 20

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
	// Miner, when not nil, learns the message of every record as the record is
	// stored (pass 1); Run then reads the records back from the database and
	// gives each one its pattern and values (pass 2) before it marks the
	// database complete. Nil means no mining: no pattern is stored. The miner
	// must be new: it is finished by Run.
	Miner *miner.Miner
}

// SourceReport tells what became of one input.
type SourceReport struct {
	// ID is the number of the source in the database; it is 0 for an input
	// that has no records, which takes no row.
	ID     int
	Source store.Source
	Totals store.SourceTotals
	// CutRecords is the number of records of the input that were started
	// because the record before them reached text.MaxRecordBytes.
	CutRecords int64
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
	// CutRecords is the sum of the CutRecords of the sources.
	CutRecords int64
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
// When o.Miner is not nil, Run mines every record (see Options.Miner) after
// the last input is stored and before it marks the database complete.
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
		res.CutRecords += rep.CutRecords
		if rep.ID != 0 {
			stored = true
		}
	}
	if !stored {
		return res, ErrNothingToAnalyse
	}
	if o.Miner != nil {
		if err := mine(ctx, db, o.Miner); err != nil {
			return res, err
		}
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
	batchBytes := 0
	for {
		rec.Source = id
		batch = append(batch, rec)
		batchBytes += len(rec.Raw)
		if len(batch) == cap(batch) || batchBytes >= maxBatchBytes {
			if err := flush(ctx, db, batch, o.Miner); err != nil {
				return SourceReport{}, rd.Lines(), err
			}
			clear(batch)
			batch = batch[:0]
			batchBytes = 0
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
		if err := flush(ctx, db, batch, o.Miner); err != nil {
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
	return SourceReport{ID: id, Source: src, Totals: totals, CutRecords: rd.Cut()}, rd.Lines(), nil
}

// flush appends a batch, unless ctx is done. When m is not nil it first
// learns the message of each record of the batch, as the record is appended.
func flush(ctx context.Context, db *store.DB, batch []record.Record, m *miner.Miner) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m != nil {
		for i := range batch {
			m.Learn(messageOf(batch[i].Raw, batch[i].Stamp.Len))
		}
	}
	return db.Append(ctx, batch)
}

// anchor returns the kind and the wall-clock nanoseconds of the anchor of a
// source that has no year. A modification time that does not fit in int64
// nanoseconds is not used: the current time is taken instead.
func anchor(in Input, o Options, zone *time.Location, now time.Time) (string, int64) {
	if o.AsOf != nil {
		return store.AnchorAsOf, *o.AsOf
	}
	if !in.ModTime.IsZero() {
		if n, ok := timeparse.WallNanos(in.ModTime.In(zone)); ok {
			return store.AnchorModTime, n
		}
	}
	n, ok := timeparse.WallNanos(now.In(zone))
	if !ok {
		n, _ = timeparse.WallNanos(time.Now().In(zone))
	}
	return store.AnchorNow, n
}
