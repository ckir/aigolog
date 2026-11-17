// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package report is the report of a run of analyze: the sources, the database,
// the digest and the summary, as text for a person and as one JSON document for
// a program.
//
// The text forms print what package summary renders, and nothing that comes
// from a log by another route: package summary escapes and cuts every string
// of a log. The JSON form is a machine document, outside the text budget, and
// carries the strings of the log as the encoding of JSON writes them.
package report

import (
	"fmt"
	"io"

	"github.com/ckir/aigolog/internal/ingest"
	"github.com/ckir/aigolog/internal/summary"
)

// FormatVersion is the number of the layout of the JSON document.
const FormatVersion = 1

// Program says which program made the report.
type Program struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// SourceLine is what the run did with one input.
type SourceLine struct {
	Name        string `json:"name"`
	Format      string `json:"format"`
	Encoding    string `json:"encoding"`
	Layout      string `json:"layout"`
	TimeKind    string `json:"time_kind"`
	AnchorKind  string `json:"anchor_kind"`
	AnchorNanos int64  `json:"anchor_nanos"`
	Records     int64  `json:"records"`
	Lines       int64  `json:"lines"`
	Bytes       int64  `json:"bytes"`
	// CutRecords is how many records were started because the record before
	// reached the size limit of a record.
	CutRecords int64 `json:"cut_records"`
	// HasTimes is true when a record of the input has a time; FirstNanos and
	// LastNanos are then the smallest and the largest.
	FirstNanos int64 `json:"first_nanos"`
	LastNanos  int64 `json:"last_nanos"`
	HasTimes   bool  `json:"has_times"`
}

// DatabaseLine says where the database of the run is. Size is the size of the
// file when the run ended, and Kept is false when the run removed it.
type DatabaseLine struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Kept bool   `json:"kept"`
}

// Report is the JSON document of a run.
type Report struct {
	Format   int             `json:"format"`
	Program  Program         `json:"program"`
	Sources  []SourceLine    `json:"sources"`
	Database DatabaseLine    `json:"database"`
	Digest   summary.Digest  `json:"digest"`
	Summary  summary.Summary `json:"summary"`
}

// SourceLines returns one line for each input of the run, in the order given:
// an input that had no record is there with its counts at zero. The result is
// never nil, so that the document holds [] and not null.
func SourceLines(res ingest.Result) []SourceLine {
	out := make([]SourceLine, 0, len(res.Sources))
	for _, s := range res.Sources {
		out = append(out, SourceLine{
			Name:        s.Source.Name,
			Format:      s.Source.Format,
			Encoding:    s.Source.Encoding,
			Layout:      s.Source.Layout,
			TimeKind:    s.Source.TimeKind,
			AnchorKind:  s.Source.AnchorKind,
			AnchorNanos: s.Source.AnchorNanos,
			Records:     s.Totals.Records,
			Lines:       s.Totals.Lines,
			Bytes:       s.Totals.Bytes,
			CutRecords:  s.CutRecords,
			FirstNanos:  s.Totals.FirstNanos,
			LastNanos:   s.Totals.LastNanos,
			HasTimes:    s.Totals.HasTimes,
		})
	}
	return out
}

// WriteDigest writes the digest as text: a blank line, the heading patterns,
// and the digest as package summary renders it.
func WriteDigest(w io.Writer, d summary.Digest) {
	fmt.Fprintf(w, "\npatterns\n%s", summary.RenderDigest(d))
}

// WriteSummary writes the full summary as text: a blank line, the heading
// summary, and the summary as package summary renders it.
func WriteSummary(w io.Writer, s summary.Summary) {
	fmt.Fprintf(w, "\nsummary\n%s", summary.Render(s))
}
