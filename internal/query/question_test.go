// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package query

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// col is a column of one slot, for the questions below.
func col(name string, slots ...int64) Column { return Column{Name: name, Slots: slots} }

// valid is one question for each verb, with the fields the verb needs and some
// it takes.
func valid() map[Verb]Question {
	cols := []Column{col("user", 1), col("host", 2, 3)}
	flt := []Filter{{Column: "user", Value: "root"}}
	return map[Verb]Question{
		Patterns: {Format: 1, Verb: Patterns, Words: []string{"failed", "password"}},
		Slots:    {Format: 1, Verb: Slots, Patterns: []int64{4}},
		Count:    {Format: 1, Verb: Count, Patterns: []int64{4, 5}, Columns: cols, Filters: flt},
		Top:      {Format: 1, Verb: Top, Patterns: []int64{4}, Columns: cols, Filters: flt, By: "user", Limit: 10},
		Pairs:    {Format: 1, Verb: Pairs, Columns: cols, By: "user", With: "host", Limit: 10},
		Buckets:  {Format: 1, Verb: Buckets, Patterns: []int64{4}, Columns: cols, Filters: flt, Buckets: 24},
		Rows:     {Format: 1, Verb: Rows, Patterns: []int64{4}, Columns: cols, Filters: flt, Last: true, Limit: 20},
		Where:    {Format: 1, Verb: Where, Value: "<*>&", Limit: 50},
	}
}

func TestValidateAcceptsEveryVerbWithItsRequiredFields(t *testing.T) {
	all := []Verb{Patterns, Slots, Count, Top, Pairs, Buckets, Rows, Where}
	v := valid()
	if len(v) != len(all) {
		t.Fatalf("%d questions for %d verbs", len(v), len(all))
	}
	for _, verb := range all {
		if err := v[verb].Validate(); err != nil {
			t.Errorf("%s: %v", verb, err)
		}
	}
	// The edges of the ranges.
	edge := valid()[Buckets]
	edge.Buckets = 200
	edge.Columns = nil
	edge.Filters = nil
	for _, n := range []int{0, 1, 200} {
		edge.Buckets = n
		if err := edge.Validate(); err != nil {
			t.Errorf("buckets %d: %v", n, err)
		}
	}
	top := valid()[Top]
	top.Limit = 1000
	if err := top.Validate(); err != nil {
		t.Errorf("limit 1000: %v", err)
	}
	top.Limit = 0
	if err := top.Validate(); err != nil {
		t.Errorf("limit 0: %v", err)
	}
	many := valid()[Count]
	many.Columns = nil
	many.Filters = nil
	for i := 0; i < 8; i++ {
		many.Columns = append(many.Columns, col(string(rune('a'+i)), 1))
		many.Filters = append(many.Filters, Filter{Column: string(rune('a' + i)), Value: ""})
	}
	if err := many.Validate(); err != nil {
		t.Errorf("8 columns and 8 filters: %v", err)
	}
	many.Columns[0].Slots = make([]int64, 64)
	for i := range many.Columns[0].Slots {
		many.Columns[0].Slots[i] = int64(i + 1)
	}
	if err := many.Validate(); err != nil {
		t.Errorf("a column of 64 slots: %v", err)
	}
	hundred := valid()[Count]
	hundred.Patterns = make([]int64, 100)
	if err := hundred.Validate(); err != nil {
		t.Errorf("100 patterns: %v", err)
	}
}

func TestValidateRefusesWhatTheTableForbids(t *testing.T) {
	with := func(verb Verb, f func(q *Question)) Question {
		q := valid()[verb]
		f(&q)
		return q
	}
	slots65 := make([]int64, 65)
	for i := range slots65 {
		slots65[i] = int64(i + 1)
	}
	nine := make([]Column, 9)
	for i := range nine {
		nine[i] = col(string(rune('a'+i)), 1)
	}
	nineFilters := make([]Filter, 9)
	for i := range nineFilters {
		nineFilters[i] = Filter{Column: "user", Value: "x"}
	}
	hundredOne := make([]int64, 101)
	cases := []struct {
		name  string
		q     Question
		field string
	}{
		{"unknown verb", with(Count, func(q *Question) { q.Verb = "frob" }), "verb"},
		{"empty verb", with(Count, func(q *Question) { q.Verb = "" }), "verb"},
		{"format 2", with(Count, func(q *Question) { q.Format = 2 }), "format"},
		{"format 0", with(Count, func(q *Question) { q.Format = 0 }), "format"},
		{"patterns takes no limit", with(Patterns, func(q *Question) { q.Limit = 5 }), "limit"},
		{"patterns takes no patterns", with(Patterns, func(q *Question) { q.Patterns = []int64{1} }), "patterns"},
		{"patterns takes no columns", with(Patterns, func(q *Question) { q.Columns = []Column{col("a", 1)} }), "columns"},
		{"patterns with no words", with(Patterns, func(q *Question) { q.Words = nil }), "words"},
		{"patterns with nine words", with(Patterns, func(q *Question) { q.Words = strings.Fields("a b c d e f g h i") }), "words"},
		{"patterns with an empty word", with(Patterns, func(q *Question) { q.Words = []string{"a", ""} }), "words"},
		{"slots with two patterns", with(Slots, func(q *Question) { q.Patterns = []int64{1, 2} }), "patterns"},
		{"slots with no pattern", with(Slots, func(q *Question) { q.Patterns = nil }), "patterns"},
		{"slots takes no words", with(Slots, func(q *Question) { q.Words = []string{"a"} }), "words"},
		{"count takes no by", with(Count, func(q *Question) { q.By = "user" }), "by"},
		{"count takes no limit", with(Count, func(q *Question) { q.Limit = 3 }), "limit"},
		{"count takes no value", with(Count, func(q *Question) { q.Value = "x" }), "value"},
		{"count takes no words", with(Count, func(q *Question) { q.Words = []string{"x"} }), "words"},
		{"top without by", with(Top, func(q *Question) { q.By = "" }), "by"},
		{"top takes no with", with(Top, func(q *Question) { q.With = "host" }), "with"},
		{"top takes no last", with(Top, func(q *Question) { q.Last = true }), "last"},
		{"top by an undefined column", with(Top, func(q *Question) { q.By = "nobody" }), "by"},
		{"pairs without with", with(Pairs, func(q *Question) { q.With = "" }), "with"},
		{"pairs with an undefined column", with(Pairs, func(q *Question) { q.With = "nobody" }), "with"},
		{"pairs without by", with(Pairs, func(q *Question) { q.By = "" }), "by"},
		{"buckets takes no limit", with(Buckets, func(q *Question) { q.Limit = 5 }), "limit"},
		{"buckets 201", with(Buckets, func(q *Question) { q.Buckets = 201 }), "buckets"},
		{"buckets -1", with(Buckets, func(q *Question) { q.Buckets = -1 }), "buckets"},
		{"rows takes no buckets", with(Rows, func(q *Question) { q.Buckets = 3 }), "buckets"},
		{"limit 1001", with(Rows, func(q *Question) { q.Limit = 1001 }), "limit"},
		{"limit -1", with(Rows, func(q *Question) { q.Limit = -1 }), "limit"},
		{"where with an empty value", with(Where, func(q *Question) { q.Value = "" }), "value"},
		{"where takes no filters", with(Where, func(q *Question) { q.Filters = []Filter{{Column: "a", Value: "b"}} }), "filters"},
		{"where takes no patterns", with(Where, func(q *Question) { q.Patterns = []int64{1} }), "patterns"},
		{"where takes no last", with(Where, func(q *Question) { q.Last = true }), "last"},
		{"two columns with one name", with(Count, func(q *Question) { q.Columns = []Column{col("a", 1), col("a", 2)} }), "columns"},
		{"a column with no name", with(Count, func(q *Question) { q.Columns = []Column{col("", 1)} }), "columns"},
		{"a column with no slot", with(Count, func(q *Question) { q.Columns = []Column{{Name: "a"}} }), "columns"},
		{"a column of 65 slots", with(Count, func(q *Question) { q.Columns = []Column{{Name: "a", Slots: slots65}} }), "columns"},
		{"a slot 0", with(Count, func(q *Question) { q.Columns = []Column{col("a", 0)} }), "columns"},
		{"a negative slot", with(Count, func(q *Question) { q.Columns = []Column{col("a", 2, -1)} }), "columns"},
		{"nine columns", with(Count, func(q *Question) { q.Columns = nine; q.Filters = nil }), "columns"},
		{"a filter that names no column", with(Count, func(q *Question) { q.Filters = []Filter{{Column: "nobody", Value: "x"}} }), "filters"},
		{"nine filters", with(Count, func(q *Question) { q.Filters = nineFilters }), "filters"},
		{"a negative pattern", with(Count, func(q *Question) { q.Patterns = []int64{1, -2} }), "patterns"},
		{"101 patterns", with(Count, func(q *Question) { q.Patterns = hundredOne }), "patterns"},
	}
	if len(cases) < 12 {
		t.Fatal("the table has fewer than twelve cases")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.q.Validate()
			if err == nil {
				t.Fatal("the question was accepted")
			}
			var ue *UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("the error is %T, want *UsageError", err)
			}
			if ue.Error() != ue.Msg {
				t.Errorf("Error() = %q, Msg = %q", ue.Error(), ue.Msg)
			}
			if !strings.HasPrefix(ue.Msg, c.field) {
				t.Errorf("the error %q does not start with the field %q", ue.Msg, c.field)
			}
		})
	}
}

func TestParseQuestionRefusesUnknownFieldsAndOtherFormats(t *testing.T) {
	good := `{"format":1,"verb":"count"}`
	if q, err := ParseQuestion(good); err != nil || q.Verb != Count || q.Format != 1 {
		t.Fatalf("ParseQuestion(%s) = %+v, %v", good, q, err)
	}
	for _, s := range []string{
		`{"format":1,"verb":"count","sort":"asc"}`,
		`{"format":2,"verb":"count"}`,
		`{"verb":"count"}`,
		`{"format":1}`,
		`{"format":1,"verb":"top"}`,
		`{"format":1,"verb":"count"} {"format":1,"verb":"count"}`,
		`{"format":1,"verb":"count"} x`,
		`{"format":"1","verb":"count"}`,
		`[1]`,
		`null`,
		``,
		`{"format":1,"verb":"count"`,
	} {
		_, err := ParseQuestion(s)
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("ParseQuestion(%q) = %v, want a *UsageError", s, err)
		}
	}
}

func TestCanonicalIsStableAndDoesNotEscapeHTML(t *testing.T) {
	q := Question{
		Format: 1, Verb: Where, Value: "<*>&",
		Limit: 7,
	}
	c := q.Canonical()
	if want := `{"format":1,"verb":"where","value":"<*>&","limit":7}`; c != want {
		t.Errorf("Canonical = %s, want %s", c, want)
	}
	if c2 := q.Canonical(); c2 != c {
		t.Errorf("two encodings differ: %s and %s", c, c2)
	}
	for verb, q := range valid() {
		c := q.Canonical()
		if strings.Contains(c, "\n") {
			t.Errorf("%s: the canonical text has a line feed: %q", verb, c)
		}
		back, err := ParseQuestion(c)
		if err != nil {
			t.Errorf("%s: ParseQuestion(%s): %v", verb, c, err)
			continue
		}
		if !reflect.DeepEqual(back, q) {
			t.Errorf("%s: ParseQuestion(Canonical) = %+v, want %+v", verb, back, q)
		}
		if back.Canonical() != c {
			t.Errorf("%s: the canonical text changed on a round trip", verb)
		}
	}
	// The order of the fields is the order of the struct.
	full := valid()[Rows].Canonical()
	want := `{"format":1,"verb":"rows","patterns":[4],"columns":[{"name":"user","slots":[1]},{"name":"host","slots":[2,3]}],"filters":[{"column":"user","value":"root"}],"last":true,"limit":20}`
	if full != want {
		t.Errorf("Canonical = %s, want %s", full, want)
	}
}

func TestDefaultLimitsAndConstants(t *testing.T) {
	l := DefaultLimits()
	if l.Rows != 100 || l.Bytes != 64<<10 || l.Time.Seconds() != 5 {
		t.Errorf("DefaultLimits = %+v", l)
	}
	if MaxCellBytes != 512 {
		t.Errorf("MaxCellBytes = %d", MaxCellBytes)
	}
	if ErrTimeLimit == nil || ErrTimeLimit.Error() != "the question ran out of time" {
		t.Errorf("ErrTimeLimit = %v", ErrTimeLimit)
	}
}
