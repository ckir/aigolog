// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package query

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Verb is what a question asks for.
type Verb string

// The eight verbs of design 7.6.
const (
	Patterns Verb = "patterns" // the patterns whose text holds every word
	Slots    Verb = "slots"    // the slots of one pattern, with a few values each
	Count    Verb = "count"    // how many records
	Top      Verb = "top"      // the most frequent values of a column
	Pairs    Verb = "pairs"    // for each value of a column, how many distinct values of another
	Buckets  Verb = "buckets"  // the records over time
	Rows     Verb = "rows"     // some records, the first or the last
	Where    Verb = "where"    // where a value occurs
)

// Limits of a question.
const (
	maxWords    = 8
	maxColumns  = 8
	maxSlots    = 64 // slots of a column
	maxFilters  = 8
	maxPatterns = 100
	maxLimit    = 1000
	maxBuckets  = 200
)

// Column names the values of one or more slots; a question defines the columns
// it uses (decision 2 of plan B2b).
type Column struct {
	Name  string  `json:"name"`
	Slots []int64 `json:"slots"`
}

// Filter keeps the records whose column has exactly Value.
type Filter struct {
	Column string `json:"column"`
	Value  string `json:"value"`
}

// Question is what the user asked. It is stored with its answer, in the form of
// Canonical, so that the question can be asked again.
type Question struct {
	Format   int      `json:"format"` // always 1
	Verb     Verb     `json:"verb"`
	Words    []string `json:"words,omitempty"`    // patterns
	Patterns []int64  `json:"patterns,omitempty"` // restrict the records (slots: exactly one)
	Columns  []Column `json:"columns,omitempty"`
	Filters  []Filter `json:"filters,omitempty"`
	By       string   `json:"by,omitempty"`    // top, pairs: the column counted
	With     string   `json:"with,omitempty"`  // pairs: the column whose distinct values are counted
	Value    string   `json:"value,omitempty"` // where
	Buckets  int      `json:"buckets,omitempty"`
	Last     bool     `json:"last,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// UsageError is a question that is wrong, not a failure of the program. Msg
// starts with the name of the field (the key of its JSON) that is wrong.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

func usagef(format string, args ...any) error {
	return &UsageError{Msg: fmt.Sprintf(format, args...)}
}

// The fields a question can set, one bit each, in the order of the struct.
type fieldSet uint

const (
	fWords fieldSet = 1 << iota
	fPatterns
	fColumns
	fFilters
	fBy
	fWith
	fValue
	fBuckets
	fLast
	fLimit
)

// takes is, for each verb, the fields it uses. A field that is set and not in
// the verb's set is an error. A verb that reads columns (count, top, pairs,
// buckets, rows) takes the Columns that define them; patterns, slots and where
// have no use for a column.
var takes = map[Verb]fieldSet{
	Patterns: fWords,
	Slots:    fPatterns,
	Count:    fPatterns | fColumns | fFilters,
	Top:      fPatterns | fColumns | fFilters | fBy | fLimit,
	Pairs:    fPatterns | fColumns | fFilters | fBy | fWith | fLimit,
	Buckets:  fPatterns | fColumns | fFilters | fBuckets,
	Rows:     fPatterns | fColumns | fFilters | fLast | fLimit,
	Where:    fValue | fLimit,
}

// set returns the fields of q that are set (not the zero value), with their
// JSON names.
func (q Question) set() []struct {
	bit  fieldSet
	name string
} {
	var out []struct {
		bit  fieldSet
		name string
	}
	add := func(on bool, bit fieldSet, name string) {
		if on {
			out = append(out, struct {
				bit  fieldSet
				name string
			}{bit, name})
		}
	}
	add(len(q.Words) > 0, fWords, "words")
	add(len(q.Patterns) > 0, fPatterns, "patterns")
	add(len(q.Columns) > 0, fColumns, "columns")
	add(len(q.Filters) > 0, fFilters, "filters")
	add(q.By != "", fBy, "by")
	add(q.With != "", fWith, "with")
	add(q.Value != "", fValue, "value")
	add(q.Buckets != 0, fBuckets, "buckets")
	add(q.Last, fLast, "last")
	add(q.Limit != 0, fLimit, "limit")
	return out
}

// Validate returns nil when q is a question the program can answer, and a
// *UsageError that names the field otherwise. It does not change q: a Buckets
// of 0 is the default of 24, which the one who runs the question applies.
func (q Question) Validate() error {
	if q.Format != 1 {
		return usagef("format: is %d, want 1", q.Format)
	}
	allowed, ok := takes[q.Verb]
	if !ok {
		return usagef("verb: %q is not one of patterns, slots, count, top, pairs, buckets, rows, where", string(q.Verb))
	}
	for _, f := range q.set() {
		if f.bit&allowed == 0 {
			return usagef("%s: the verb %s does not take it", f.name, q.Verb)
		}
	}
	if q.Limit < 0 || q.Limit > maxLimit {
		return usagef("limit: %d is not from 0 to %d", q.Limit, maxLimit)
	}
	if q.Buckets < 0 || q.Buckets > maxBuckets {
		return usagef("buckets: %d is not from 1 to %d (0 is the default)", q.Buckets, maxBuckets)
	}
	if len(q.Patterns) > maxPatterns {
		return usagef("patterns: %d numbers, at most %d", len(q.Patterns), maxPatterns)
	}
	for _, p := range q.Patterns {
		if p < 0 {
			return usagef("patterns: %d is negative", p)
		}
	}
	names, err := q.checkColumns()
	if err != nil {
		return err
	}
	if len(q.Filters) > maxFilters {
		return usagef("filters: %d filters, at most %d", len(q.Filters), maxFilters)
	}
	for i, f := range q.Filters {
		if !names[f.Column] {
			return usagef("filters: filter %d names the column %q, which the question does not define", i+1, f.Column)
		}
	}
	for _, c := range []struct{ field, name string }{{"by", q.By}, {"with", q.With}} {
		if c.name != "" && !names[c.name] {
			return usagef("%s: the column %q is not defined by the question", c.field, c.name)
		}
	}
	switch q.Verb {
	case Patterns:
		if len(q.Words) < 1 || len(q.Words) > maxWords {
			return usagef("words: %d words, want 1 to %d", len(q.Words), maxWords)
		}
		for i, w := range q.Words {
			if w == "" {
				return usagef("words: word %d is empty", i+1)
			}
		}
	case Slots:
		if len(q.Patterns) != 1 {
			return usagef("patterns: %d numbers, want exactly one", len(q.Patterns))
		}
	case Top:
		if q.By == "" {
			return usagef("by: the verb top needs the column to count")
		}
	case Pairs:
		if q.By == "" {
			return usagef("by: the verb pairs needs the column to count")
		}
		if q.With == "" {
			return usagef("with: the verb pairs needs the column whose distinct values are counted")
		}
	case Where:
		if q.Value == "" {
			return usagef("value: the verb where needs a value")
		}
	}
	return nil
}

// checkColumns checks the definitions and returns the set of their names.
func (q Question) checkColumns() (map[string]bool, error) {
	if len(q.Columns) > maxColumns {
		return nil, usagef("columns: %d columns, at most %d", len(q.Columns), maxColumns)
	}
	names := make(map[string]bool, len(q.Columns))
	for i, c := range q.Columns {
		if c.Name == "" {
			return nil, usagef("columns: column %d has no name", i+1)
		}
		if names[c.Name] {
			return nil, usagef("columns: the name %q is used twice", c.Name)
		}
		names[c.Name] = true
		if len(c.Slots) < 1 || len(c.Slots) > maxSlots {
			return nil, usagef("columns: the column %q has %d slots, want 1 to %d", c.Name, len(c.Slots), maxSlots)
		}
		for _, s := range c.Slots {
			if s < 1 {
				return nil, usagef("columns: the column %q names the slot %d, which is not above 0", c.Name, s)
			}
		}
	}
	return names, nil
}

// Canonical is the JSON of q on one line, with the fields in the order of the
// struct and no HTML escaping (a value of <*> stays as it is). It is the form
// that is stored and that ParseQuestion reads.
func (q Question) Canonical() string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(q); err != nil {
		// A Question holds only strings, numbers, booleans and slices of them.
		panic("query: a question cannot be encoded: " + err.Error())
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// ParseQuestion reads the form of Canonical. It refuses a field it does not
// know, a document that is not one object, a format other than 1, and a
// question that does not Validate; each is a *UsageError.
func ParseQuestion(s string) (Question, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	var q Question
	if err := dec.Decode(&q); err != nil {
		return Question{}, usagef("question: %v", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Question{}, usagef("question: there is more after the first document")
	}
	if err := q.Validate(); err != nil {
		return Question{}, err
	}
	return q, nil
}

// Limits are the caps of one answer: the rows, the bytes of the cells, and the
// time the question may take.
type Limits struct {
	Rows, Bytes int
	Time        time.Duration
}

// DefaultLimits are provisional (decision 4 of plan B2b).
func DefaultLimits() Limits { return Limits{Rows: 100, Bytes: 64 << 10, Time: 5 * time.Second} }

// MaxCellBytes is the most bytes of one cell of an answer.
const MaxCellBytes = 512

// ErrTimeLimit is the error of a question that ran out of its time; it gives no
// figures.
var ErrTimeLimit = errors.New("the question ran out of time")
