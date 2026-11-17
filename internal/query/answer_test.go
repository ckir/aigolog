// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package query

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseAnswerReturnsWhatWasEncodedAndNeverNilRows(t *testing.T) {
	empty := Answer{
		Question:      Question{Format: 1, Verb: Count},
		Columns:       []string{"count"},
		Rows:          [][]string{},
		ElapsedMillis: 3,
	}
	full := Answer{
		Question: valid()[Top],
		Columns:  []string{"user", "count"},
		Rows:     [][]string{{"root", "5"}, {"admin <&>", "2"}},
		Total:    40,
		Cut:      "rows",
		Facts: Facts{
			Rows: 7, HasTimes: true, FirstNanos: 1_000_000_000, LastNanos: 90_000_000_000, NoTime: 2,
			First: &Ref{Source: 1, Line: 3}, Last: &Ref{Source: 2, Line: 9},
		},
		ElapsedMillis: 12,
	}
	for name, a := range map[string]Answer{"no rows": empty, "full": full} {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(a); err != nil {
			t.Fatal(err)
		}
		back, err := ParseAnswer(b.String())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(back, a) {
			t.Errorf("%s: ParseAnswer = %+v, want %+v", name, back, a)
		}
		if back.Rows == nil || back.Columns == nil {
			t.Errorf("%s: Rows or Columns is nil", name)
		}
	}
	// A document that leaves them out, or writes null, still gives non-nil slices.
	for _, s := range []string{`{}`, `{"rows":null,"columns":null}`} {
		a, err := ParseAnswer(s)
		if err != nil {
			t.Fatalf("ParseAnswer(%s): %v", s, err)
		}
		if a.Rows == nil || a.Columns == nil || len(a.Rows) != 0 || len(a.Columns) != 0 {
			t.Errorf("ParseAnswer(%s): Rows = %#v, Columns = %#v", s, a.Rows, a.Columns)
		}
	}
	for _, s := range []string{``, `{`, `[]`, `{"rows":"x"}`, `{} {}`, `{} x`} {
		if _, err := ParseAnswer(s); err == nil {
			t.Errorf("ParseAnswer(%q) succeeded", s)
		}
	}
}

func TestARefEncodesWithLowerCaseKeys(t *testing.T) {
	b, err := json.Marshal(Facts{Rows: 2, First: &Ref{Source: 1, Line: 7}, Last: &Ref{Source: 2, Line: 9}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"first":{"source":1,"line":7}`, `"last":{"source":2,"line":9}`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("Facts encodes as %s, want it to hold %s", b, want)
		}
	}
	if strings.Contains(string(b), `"Source"`) || strings.Contains(string(b), `"Line"`) {
		t.Errorf("Facts encodes as %s, with a capitalised key", b)
	}
}

func TestIDRoundTrips(t *testing.T) {
	if got := ID(3); got != "A3" {
		t.Errorf("ID(3) = %q", got)
	}
	if got := ID(120); got != "A120" {
		t.Errorf("ID(120) = %q", got)
	}
	for _, s := range []string{"A3", "3"} {
		if n, ok := ParseID(s); !ok || n != 3 {
			t.Errorf("ParseID(%q) = %d, %v", s, n, ok)
		}
	}
	if n, ok := ParseID(ID(9223372036854775807)); !ok || n != 9223372036854775807 {
		t.Errorf("the largest id does not round trip: %d, %v", n, ok)
	}
	for _, s := range []string{"A", "A0", "0", "-1", "A-1", "+3", "a3", "AA3", "A3x", "3 ", " 3", "", "A9223372036854775808", "A1.5"} {
		if n, ok := ParseID(s); ok {
			t.Errorf("ParseID(%q) = %d, true", s, n)
		}
	}
}
