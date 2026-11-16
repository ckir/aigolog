// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package timeparse

import (
	"fmt"
	"math"
	"time"
)

// day is the slack that is allowed past the limit, and the span of a date.
const day = 24 * time.Hour

// maxBack is how many years before the year of the limit a date is tried in.
const maxBack = 9

// YearResolver gives a year to the stamps that have none. Records are
// resolved from the last to the first: each one gets the latest year that does
// not put it after the one that follows it.
type YearResolver struct {
	limit int64 // wall-clock nanoseconds that the next stamp may not pass
}

// NewYearResolver returns a resolver for the records of one source, whose last
// record is not after anchor (wall-clock nanoseconds). One day of slack is
// allowed past the anchor.
func NewYearResolver(anchor int64) *YearResolver {
	return &YearResolver{limit: addDay(anchor)}
}

// addDay returns n plus one day. It stops at math.MaxInt64 and does not wrap.
func addDay(n int64) int64 {
	if n > math.MaxInt64-int64(day) {
		return math.MaxInt64
	}
	return n + int64(day)
}

// Resolve returns the wall-clock nanoseconds of the stamp with this month, day
// and time of day, in the latest year that does not put it after the limit.
// 29 February is placed only in a leap year. The stamp then becomes the limit,
// with one day of slack, for the next call. It is false when no year fits.
func (r *YearResolver) Resolve(month, dayOfMonth int, nanoOfDay int64) (int64, bool) {
	top := time.Unix(0, r.limit).UTC().Year()
	for y := top; y >= top-maxBack; y-- {
		t := time.Date(y, time.Month(month), dayOfMonth, 0, 0, 0, 0, time.UTC)
		if int(t.Month()) != month || t.Day() != dayOfMonth {
			continue // the date does not exist in this year
		}
		// A time that does not fit in int64 nanoseconds is after the limit.
		n, ok := nanosOf(t.Add(time.Duration(nanoOfDay)))
		if !ok || n > r.limit {
			continue
		}
		r.limit = addDay(n)
		return n, true
	}
	return 0, false
}

// asOfLayouts are the forms ParseAsOf takes, with whether the form has no time
// of day (and so means the end of its day).
var asOfLayouts = []struct {
	layout   string
	dateOnly bool
}{
	{"2006-01-02", true},
	{"2006-01-02 15:04", false},
	{"2006-01-02 15:04:05", false},
	{"2006-01-02T15:04", false},
	{"2006-01-02T15:04:05", false},
}

// ParseAsOf reads the date that --as-of gives. 2006-01-02 means the last
// nanosecond of that day; 2006-01-02 15:04 and 2006-01-02 15:04:05, and the
// same with T in place of the space, mean that wall-clock time. The result is
// wall-clock nanoseconds.
func ParseAsOf(s string) (int64, error) {
	for _, l := range asOfLayouts {
		t, err := time.Parse(l.layout, s)
		if err != nil {
			continue
		}
		if l.dateOnly {
			t = t.Add(day - 1)
		}
		n, ok := nanosOf(wallTime(t))
		if !ok {
			return 0, fmt.Errorf("as-of %q: the time does not fit in nanoseconds since 1970 (1677-09-21 to 2262-04-11)", s)
		}
		return n, nil
	}
	return 0, fmt.Errorf("as-of %q: want 2006-01-02, 2006-01-02 15:04, 2006-01-02 15:04:05, or the same with T between date and time", s)
}
