// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package query

import (
	"fmt"
	"strings"
	"time"

	"github.com/ckir/aigolog/internal/textsafe"
)

const timeLayout = "2006-01-02 15:04:05"

// timesLine ends a text that printed a time (decision 8 of plan B2b).
const timesLine = "times: UTC for sources that state a zone, as written in the log for the others"

// RenderText writes a for a terminal. The first line is
// "answer ID: VERB" (or "answer: VERB" when id is "" or "-"); then the
// question as "question: " and its canonical text; then the table, tab
// separated, with a header row; then the facts, one to a line. Every part that
// can hold text from a log (each cell, each name of a column, the question) goes through
// textsafe.Clean, and the other lines hold only numbers and words fixed by the
// program, so that no line a log wrote can look like one of ours.
func RenderText(id string, a Answer) string {
	var b strings.Builder
	if id == "" || id == "-" {
		fmt.Fprintf(&b, "answer: %s\n", textsafe.Clean(string(a.Question.Verb)))
	} else {
		fmt.Fprintf(&b, "answer %s: %s\n", textsafe.Clean(id), textsafe.Clean(string(a.Question.Verb)))
	}
	fmt.Fprintf(&b, "question: %s\n", textsafe.Clean(a.Question.Canonical()))
	if len(a.Columns) > 0 {
		writeRow(&b, a.Columns)
	}
	for _, row := range a.Rows {
		writeRow(&b, row)
	}
	f := a.Facts
	printedTime := false
	if f.Rows > 0 {
		fmt.Fprintf(&b, "rows covered %d\n", f.Rows)
		if f.HasTimes {
			printedTime = true
			fmt.Fprintf(&b, "first time: %s\n", time.Unix(0, f.FirstNanos).UTC().Format(timeLayout))
			fmt.Fprintf(&b, "last time: %s\n", time.Unix(0, f.LastNanos).UTC().Format(timeLayout))
			fmt.Fprintf(&b, "span: %s\n", time.Duration(f.LastNanos-f.FirstNanos).Round(time.Second).String())
		}
	}
	if f.First != nil {
		fmt.Fprintf(&b, "first: source %d line %d\n", f.First.Source, f.First.Line)
	}
	if f.Last != nil {
		fmt.Fprintf(&b, "last: source %d line %d\n", f.Last.Source, f.Last.Line)
	}
	if f.NoTime > 0 {
		fmt.Fprintf(&b, "no time: %d\n", f.NoTime)
	}
	if a.Cut != "" {
		fmt.Fprintf(&b, "cut: %s (showing %d of %d)\n", textsafe.Clean(a.Cut), len(a.Rows), a.Total)
	}
	fmt.Fprintf(&b, "answered in %d ms\n", a.ElapsedMillis)
	if printedTime {
		b.WriteString(timesLine + "\n")
	}
	return b.String()
}

// writeRow writes cells, cleaned, separated by tabs. A tab inside a cell is
// written by Clean as \x09, so the columns stay where they are.
func writeRow(b *strings.Builder, cells []string) {
	for i, c := range cells {
		if i > 0 {
			b.WriteByte('\t')
		}
		b.WriteString(textsafe.Clean(c))
	}
	b.WriteByte('\n')
}
