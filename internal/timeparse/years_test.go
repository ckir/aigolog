// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package timeparse

import (
	"strings"
	"testing"
	"time"
)

type pending struct {
	month, day int
	tod        time.Duration
}

// resolveBackwards resolves the records last to first, the way the store does,
// and returns the results in the records' own order, as "2006-01-02 15:04:05".
func resolveBackwards(anchor int64, recs []pending) []string {
	r := NewYearResolver(anchor)
	out := make([]string, len(recs))
	for i := len(recs) - 1; i >= 0; i-- {
		n, ok := r.Resolve(recs[i].month, recs[i].day, int64(recs[i].tod))
		if !ok {
			out[i] = "unplaced"
			continue
		}
		out[i] = time.Unix(0, n).UTC().Format("2006-01-02 15:04:05")
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTwoFilesFromDecemberIntoJanuaryKeepTheirOrder(t *testing.T) {
	anchor, _ := ParseAsOf("2026-01-15") // the same anchor for both sources
	first := resolveBackwards(anchor, []pending{{12, 28, 23 * time.Hour}, {12, 31, 23*time.Hour + 59*time.Minute + 59*time.Second}})
	second := resolveBackwards(anchor, []pending{{1, 1, time.Second}, {1, 14, 9 * time.Hour}})
	if want := []string{"2025-12-28 23:00:00", "2025-12-31 23:59:59"}; !equal(first, want) {
		t.Errorf("first file = %v, want %v", first, want)
	}
	if want := []string{"2026-01-01 00:00:01", "2026-01-14 09:00:00"}; !equal(second, want) {
		t.Errorf("second file = %v, want %v", second, want)
	}
}

func TestLinesAcrossNewYearsEveALittleOutOfOrderKeepTheirYears(t *testing.T) {
	anchor, _ := ParseAsOf("2026-01-05")
	got := resolveBackwards(anchor, []pending{
		{12, 31, 23*time.Hour + 59*time.Minute + 58*time.Second},
		{1, 1, time.Second},
		{12, 31, 23*time.Hour + 59*time.Minute + 59*time.Second},
		{1, 1, 2 * time.Second},
	})
	want := []string{"2025-12-31 23:59:58", "2026-01-01 00:00:01", "2025-12-31 23:59:59", "2026-01-01 00:00:02"}
	if !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAsOfTheDayOfTheLastRecordKeepsItInThatYear(t *testing.T) {
	anchor, err := ParseAsOf("2025-12-31") // a date means the end of that day
	if err != nil {
		t.Fatal(err)
	}
	got := resolveBackwards(anchor, []pending{{12, 31, 22 * time.Hour}, {12, 31, 23 * time.Hour}})
	want := []string{"2025-12-31 22:00:00", "2025-12-31 23:00:00"}
	if !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestOneDayOfSlackLetsAStampSitJustPastTheAnchor(t *testing.T) {
	anchor, _ := ParseAsOf("2025-12-31")
	got := resolveBackwards(anchor, []pending{{1, 1, 30 * time.Minute}})
	if want := []string{"2026-01-01 00:30:00"}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTwentyNinthOfFebruaryGoesToTheLeapYearBefore(t *testing.T) {
	anchor, _ := ParseAsOf("2026-03-05")
	got := resolveBackwards(anchor, []pending{{2, 29, 10 * time.Hour}, {3, 1, time.Hour}})
	want := []string{"2024-02-29 10:00:00", "2026-03-01 01:00:00"}
	if !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if got := resolveBackwards(mustAsOf(t, "2028-03-01"), []pending{{2, 29, 10 * time.Hour}}); !equal(got, []string{"2028-02-29 10:00:00"}) {
		t.Errorf("anchor in a leap year: got %v", got)
	}
}

func TestAStampExactlyAtTheLimitStaysInItsYearAndOneNanosecondLaterGoesBack(t *testing.T) {
	anchor := mustAsOf(t, "2026-01-05 10:00:00") // the limit is 2026-01-06 10:00:00
	at, ok := NewYearResolver(anchor).Resolve(1, 6, int64(10*time.Hour))
	if got := time.Unix(0, at).UTC().Format("2006-01-02 15:04:05"); !ok || got != "2026-01-06 10:00:00" {
		t.Errorf("a stamp at the limit = %q, %v; want 2026-01-06 10:00:00, true", got, ok)
	}
	later, ok := NewYearResolver(anchor).Resolve(1, 6, int64(10*time.Hour)+1)
	if got := time.Unix(0, later).UTC().Format("2006-01-02 15:04:05"); !ok || got != "2025-01-06 10:00:00" || later != wall(2025, 1, 6, 10, 0, 0)+1 {
		t.Errorf("a stamp one nanosecond past the limit = %q (%d), %v; want 2025-01-06 10:00:00 plus 1ns, true", got, later, ok)
	}
}

func TestTwentyNinthOfFebruaryFindsALeapYearAcrossTheCenturyGap(t *testing.T) {
	// 2100 is not a leap year, so the nearest one before 2103 is 2096, seven
	// years back.
	n, ok := NewYearResolver(mustAsOf(t, "2103-03-05")).Resolve(2, 29, int64(10*time.Hour))
	if got := time.Unix(0, n).UTC().Format("2006-01-02 15:04:05"); !ok || got != "2096-02-29 10:00:00" {
		t.Errorf("29 February from 2103 = %q, %v; want 2096-02-29 10:00:00, true", got, ok)
	}
	n, ok = NewYearResolver(mustAsOf(t, "2100-06-01")).Resolve(2, 29, 0)
	if got := time.Unix(0, n).UTC().Format("2006-01-02 15:04:05"); !ok || got != "2096-02-29 00:00:00" {
		t.Errorf("29 February from 2100 = %q, %v; want 2096-02-29 00:00:00, true", got, ok)
	}
}

func mustAsOf(t *testing.T, s string) int64 {
	t.Helper()
	n, err := ParseAsOf(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestParseAsOf(t *testing.T) {
	good := map[string]int64{
		"2026-01-05":          wall(2026, 1, 5, 23, 59, 59) + 999999999,
		"2026-01-05 10:30":    wall(2026, 1, 5, 10, 30, 0),
		"2026-01-05 10:30:15": wall(2026, 1, 5, 10, 30, 15),
		"2026-01-05T10:30:15": wall(2026, 1, 5, 10, 30, 15),
	}
	for in, want := range good {
		if got, err := ParseAsOf(in); err != nil || got != want {
			t.Errorf("ParseAsOf(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "yesterday", "2026-13-01", "2026-01-05 25:00", "05/01/2026", "2026-01-05Z"} {
		if _, err := ParseAsOf(in); err == nil {
			t.Errorf("ParseAsOf(%q) was accepted", in)
		}
	}
}

func TestAnAnchorAtTheEdgeOfTheRangeDoesNotWrap(t *testing.T) {
	if n, err := ParseAsOf("2262-04-11"); err == nil {
		t.Errorf("ParseAsOf(2262-04-11) = %d, want an error: the end of that day does not fit", n)
	}
	anchor, err := ParseAsOf("2262-04-10")
	if err != nil {
		t.Fatalf("ParseAsOf(2262-04-10): %v", err)
	}
	if got, want := resolveBackwards(anchor, []pending{{4, 11, 0}}), []string{"2262-04-11 00:00:00"}; !equal(got, want) {
		t.Errorf("11 April at the edge: got %v, want %v", got, want)
	}
	if got, want := resolveBackwards(anchor, []pending{{12, 31, 0}}), []string{"2261-12-31 00:00:00"}; !equal(got, want) {
		t.Errorf("31 December at the edge: got %v, want %v", got, want)
	}
}

func TestTheAsOfMessageDoesNotUseGoReferenceDates(t *testing.T) {
	_, err := ParseAsOf("2026-02-30")
	if err == nil {
		t.Fatal("2026-02-30 was accepted")
	}
	if msg := err.Error(); !strings.Contains(msg, "YYYY-MM-DD") || strings.Contains(msg, "2006") || !strings.HasPrefix(msg, `as-of "2026-02-30":`) {
		t.Errorf("message = %q", msg)
	}
}

func TestParseAsOfAcceptsOnlyTheFiveFormsAtTheirFullLength(t *testing.T) {
	for _, in := range []string{"2026-11-16 7:05", "2026-11-16 07:05:09.5", "2026-11-16T07:05:09.123", "2026-11-16 07:05:09 "} {
		if n, err := ParseAsOf(in); err == nil {
			t.Errorf("ParseAsOf(%q) = %d, want an error", in, n)
		}
	}
	for _, in := range []string{"2026-11-16", "2026-11-16 07:05", "2026-11-16 07:05:09", "2026-11-16T07:05", "2026-11-16T07:05:09"} {
		if _, err := ParseAsOf(in); err != nil {
			t.Errorf("ParseAsOf(%q): %v", in, err)
		}
	}
}

func TestResolveRefusesATimeOfDayOutsideTheDay(t *testing.T) {
	anchor := mustAsOf(t, "2026-11-16")
	for _, n := range []int64{-1, 86400e9} {
		if got, ok := NewYearResolver(anchor).Resolve(11, 15, n); ok {
			t.Errorf("Resolve(11, 15, %d) = %d, true; want false", n, got)
		}
	}
	for _, n := range []int64{0, 86399999999999} {
		if _, ok := NewYearResolver(anchor).Resolve(11, 15, n); !ok {
			t.Errorf("Resolve(11, 15, %d) = false; want true", n)
		}
	}
}

func TestParseAsOfNamesNonexistentDates(t *testing.T) {
	_, err := ParseAsOf("2026-02-30")
	if err == nil {
		t.Fatal("2026-02-30 was accepted")
	}
	for _, form := range []string{"YYYY-MM-DD", "YYYY-MM-DD HH:MM", "YYYY-MM-DD HH:MM:SS", "the same with T"} {
		if !strings.Contains(err.Error(), form) {
			t.Errorf("message %q does not name the form %q", err.Error(), form)
		}
	}
}
