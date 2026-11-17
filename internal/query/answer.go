// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package query

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

// Ref is a position in the logs: the number of a source and a line in it.
type Ref struct{ Source, Line int64 }

// Facts are what an answer states about the records it covers, besides its
// rows (design 7.6).
type Facts struct {
	Rows       int64 `json:"rows"`
	HasTimes   bool  `json:"has_times"`
	FirstNanos int64 `json:"first_nanos"`
	LastNanos  int64 `json:"last_nanos"`
	NoTime     int64 `json:"no_time"`         // records of the set with no resolved time
	First      *Ref  `json:"first,omitempty"` // first and last record of the set by reading order
	Last       *Ref  `json:"last,omitempty"`
}

// Answer is a small table with named columns, and what is known about the
// records it covers. Rows is never nil, so that it is written as [].
type Answer struct {
	Question      Question   `json:"question"`
	Columns       []string   `json:"columns"`
	Rows          [][]string `json:"rows"`  // never nil
	Total         int64      `json:"total"` // rows before the cut
	Cut           string     `json:"cut"`   // "", "rows" or "bytes"
	Facts         Facts      `json:"facts"`
	ElapsedMillis int64      `json:"elapsed_ms"`
}

// ParseAnswer reads what the JSON encoding of an Answer wrote. Rows and
// Columns of the result are never nil.
func ParseAnswer(s string) (Answer, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	var a Answer
	if err := dec.Decode(&a); err != nil {
		return Answer{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Answer{}, errors.New("there is more after the first document")
	}
	if a.Rows == nil {
		a.Rows = [][]string{}
	}
	if a.Columns == nil {
		a.Columns = []string{}
	}
	return a, nil
}

// ID is the id of the stored answer number n: A3.
func ID(n int64) string { return "A" + strconv.FormatInt(n, 10) }

// ParseID reads A3 or 3 and returns 3. The number is above 0.
func ParseID(s string) (int64, bool) {
	digits := strings.TrimPrefix(s, "A")
	if digits == "" {
		return 0, false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}
