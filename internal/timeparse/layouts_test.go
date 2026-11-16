// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package timeparse

import (
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/record"
)

func wall(y int, mo time.Month, d, h, mi, s int) int64 {
	return time.Date(y, mo, d, h, mi, s, 0, time.UTC).UnixNano()
}

func layoutNamed(t *testing.T, name string) Layout {
	t.Helper()
	for _, l := range Layouts {
		if l.Name == name {
			return l
		}
	}
	t.Fatalf("no layout named %q", name)
	return Layout{}
}

func TestLayoutsAreTheNineOfTheDesignInOrder(t *testing.T) {
	var names []string
	for _, l := range Layouts {
		names = append(names, l.Name)
	}
	want := "rfc3339 iso-space-offset iso-space-offset-sp clf iso-t iso-space slash ctime syslog"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("layout names = %q, want %q", got, want)
	}
}

func TestParseReadsEveryLayout(t *testing.T) {
	cases := []struct {
		layout string
		line   string
		want   record.Stamp
	}{
		{"rfc3339", "2026-01-05T10:00:01+02:00 sshd: x", record.Stamp{State: record.Resolved, Nanos: wall(2026, 1, 5, 8, 0, 1), Exact: true, Zone: "+02:00"}},
		{"rfc3339", "2026-01-05T10:00:01.123Z sshd: x", record.Stamp{State: record.Resolved, Nanos: wall(2026, 1, 5, 10, 0, 1) + 123e6, Exact: true, Zone: "Z"}},
		{"iso-space-offset", "2026-01-05 10:00:01+02:00 x", record.Stamp{State: record.Resolved, Nanos: wall(2026, 1, 5, 8, 0, 1), Exact: true, Zone: "+02:00"}},
		{"iso-space-offset-sp", "2026-01-05 10:00:01 -0700 x", record.Stamp{State: record.Resolved, Nanos: wall(2026, 1, 5, 17, 0, 1), Exact: true, Zone: "-0700"}},
		{"clf", `05/Jan/2026:10:00:01 +0200 "GET /"`, record.Stamp{State: record.Resolved, Nanos: wall(2026, 1, 5, 8, 0, 1), Exact: true, Zone: "+0200"}},
		{"iso-t", "2026-01-05T10:00:01 x", record.Stamp{State: record.Resolved, Nanos: wall(2026, 1, 5, 10, 0, 1)}},
		{"iso-space", "2026-01-05 10:00:01,250 x", record.Stamp{State: record.Resolved, Nanos: wall(2026, 1, 5, 10, 0, 1) + 250e6}},
		{"slash", "2026/01/05 10:00:01 x", record.Stamp{State: record.Resolved, Nanos: wall(2026, 1, 5, 10, 0, 1)}},
		{"ctime", "Mon Jan  5 10:00:01 2026 x", record.Stamp{State: record.Resolved, Nanos: wall(2026, 1, 5, 10, 0, 1)}},
		{"syslog", "Jan  5 10:00:01 host sshd[1]: x", record.Stamp{State: record.YearPending, Month: 1, Day: 5, NanoOfDay: int64(10*time.Hour + time.Second)}},
		{"syslog", "Jan 15 10:00:01 host x", record.Stamp{State: record.YearPending, Month: 1, Day: 15, NanoOfDay: int64(10*time.Hour + time.Second)}},
		{"syslog", "Feb 29 10:00:00 host x", record.Stamp{State: record.YearPending, Month: 2, Day: 29, NanoOfDay: int64(10 * time.Hour)}},
	}
	for _, c := range cases {
		got, ok := layoutNamed(t, c.layout).Parse(c.line)
		if !ok || got != c.want {
			t.Errorf("%s: Parse(%q) = %+v, %v; want %+v, true", c.layout, c.line, got, ok, c.want)
		}
	}
}

func TestParseFindsTheStampOnlyAtTheStart(t *testing.T) {
	l := layoutNamed(t, "iso-space")
	for _, line := range []string{"[2026-01-05 10:00:01] x", "(2026-01-05 10:00:01) x", "<34>2026-01-05 10:00:01 x"} {
		if _, ok := l.Parse(line); !ok {
			t.Errorf("Parse(%q) refused a stamp after a bracket or a priority tag", line)
		}
	}
	for _, line := range []string{" 2026-01-05 10:00:01 x", "\t2026-01-05 10:00:01 x", "at 2026-01-05 10:00:01", "x [2026-01-05 10:00:01]", "", "hello"} {
		if _, ok := l.Parse(line); ok {
			t.Errorf("Parse(%q) accepted a stamp that is not at the start", line)
		}
	}
}

func TestParseRefusesDatesThatDoNotExist(t *testing.T) {
	if _, ok := layoutNamed(t, "iso-space").Parse("2026-13-45 10:00:01 x"); ok {
		t.Error("month 13 was accepted")
	}
	if _, ok := layoutNamed(t, "syslog").Parse("Feb 30 10:00:01 x"); ok {
		t.Error("30 February was accepted")
	}
}

func TestParseDoesNotScanAHugeLine(t *testing.T) {
	line := strings.Repeat("x", 4<<20)
	l := layoutNamed(t, "iso-space")
	start := time.Now()
	for i := 0; i < 200; i++ {
		if _, ok := l.Parse(line); ok {
			t.Fatal("junk was accepted")
		}
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("200 parses of a 4 MiB line took %v: the stamp must be looked for in the first bytes only", d)
	}
}

func TestParseOnAHugeLineWithAValidStampCopiesOnlyTheWindow(t *testing.T) {
	const stamp = "2026-01-05 10:00:01"
	line := stamp + strings.Repeat("x", 8<<20)
	l := layoutNamed(t, "iso-space")
	want := wall(2026, 1, 5, 10, 0, 1)
	const calls = 20

	// A stamp that is followed by junk makes time.Parse report an error that
	// quotes the text it was given, so the bytes allocated per call show how
	// much of the line was handed to it.
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < calls; i++ {
		got, ok := l.Parse(line)
		if !ok || got.Nanos != want {
			t.Fatalf("Parse(stamp + 8 MiB of x) = %+v, %v; want nanos %d, true", got, ok, want)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if perCall := (after.TotalAlloc - before.TotalAlloc) / calls; perCall > 64<<10 {
		t.Errorf("Parse allocated %d bytes per call on an 8 MiB line, want under 64 KiB: only the first bytes may be handed to time.Parse", perCall)
	}
}

func TestDetectPicksTheLayoutThatFits(t *testing.T) {
	syslog := []string{"Jan  5 10:00:01 host a", "Jan  5 10:00:02 host b", "Jan  5 10:00:03 host c"}
	if l, ok := Detect(syslog); !ok || l.Name != "syslog" {
		t.Errorf("Detect(syslog lines) = %q, %v", l.Name, ok)
	}
	zoned := []string{"2026-01-05T10:00:01+02:00 a", "2026-01-05T10:00:02+02:00 b"}
	if l, ok := Detect(zoned); !ok || l.Name != "rfc3339" {
		t.Errorf("a zoned line also parses as iso-t, and the earlier layout must win: got %q, %v", l.Name, ok)
	}
}

func TestDetectIgnoresIndentedLines(t *testing.T) {
	lines := []string{
		"2026-01-05 10:00:01 ERROR boom",
		"\tat main.run(main.go:10)",
		"\tat main.main(main.go:5)",
		"    caused by: x",
		"2026-01-05 10:00:02 INFO ok",
	}
	if l, ok := Detect(lines); !ok || l.Name != "iso-space" {
		t.Errorf("a stack trace must not count against the layout: got %q, %v", l.Name, ok)
	}
}

func TestDetectNeedsHalfOfTheLinesToFit(t *testing.T) {
	lines := []string{"2026-01-05 10:00:01 a", "plain one", "plain two", "plain three"}
	if l, ok := Detect(lines); ok {
		t.Errorf("one stamped line in four fitted %q", l.Name)
	}
	if _, ok := Detect([]string{"plain", "text", "only"}); ok {
		t.Error("a layout was found in text with no stamps")
	}
	if _, ok := Detect(nil); ok {
		t.Error("a layout was found in no lines")
	}
}

func TestParseRefusesTimesThatDoNotFitInInt64Nanoseconds(t *testing.T) {
	iso := layoutNamed(t, "iso-space")
	for _, line := range []string{"0001-01-01 00:00:00 x", "9999-12-31 23:59:59", "2262-04-11 23:47:17", "1677-09-21 00:12:43"} {
		if got, ok := iso.Parse(line); ok {
			t.Errorf("iso-space: Parse(%q) = %+v, true; want a refusal", line, got)
		}
	}
	for _, line := range []string{"9999-12-31T23:59:59Z", "0001-01-01T00:00:00Z x", "9999-12-31T23:59:59-07:00"} {
		if got, ok := layoutNamed(t, "rfc3339").Parse(line); ok {
			t.Errorf("rfc3339: Parse(%q) = %+v, true; want a refusal", line, got)
		}
	}
}

func TestParseAcceptsTheEdgesOfTheInt64NanosecondRange(t *testing.T) {
	const maxSec = math.MaxInt64 / 1_000_000_000
	const minSec = math.MinInt64 / 1_000_000_000
	const f = "2006-01-02 15:04:05"
	iso := layoutNamed(t, "iso-space")
	cases := []struct {
		sec  int64
		want int64
		ok   bool
	}{
		{maxSec, time.Date(2262, 4, 11, 23, 47, 16, 0, time.UTC).UnixNano(), true},
		{maxSec + 1, 0, false},
		{minSec, time.Date(1677, 9, 21, 0, 12, 44, 0, time.UTC).UnixNano(), true},
		{minSec - 1, 0, false},
	}
	for _, c := range cases {
		line := time.Unix(c.sec, 0).UTC().Format(f)
		got, ok := iso.Parse(line)
		want := record.Stamp{}
		if c.ok {
			want = record.Stamp{State: record.Resolved, Nanos: c.want}
		}
		if ok != c.ok || got != want {
			t.Errorf("Parse(%q) (second %d) = %+v, %v; want %+v, %v", line, c.sec, got, ok, want, c.ok)
		}
		zoned := strings.Replace(line, " ", "T", 1) + "Z"
		got, ok = layoutNamed(t, "rfc3339").Parse(zoned)
		want = record.Stamp{}
		if c.ok {
			want = record.Stamp{State: record.Resolved, Nanos: c.want, Exact: true, Zone: "Z"}
		}
		if ok != c.ok || got != want {
			t.Errorf("rfc3339: Parse(%q) (second %d) = %+v, %v; want %+v, %v", zoned, c.sec, got, ok, want, c.ok)
		}
	}
	// A fraction after the last whole second can still wrap: the last
	// nanosecond that fits is .854775807.
	if got, ok := iso.Parse("2262-04-11 23:47:16.854775807"); !ok || got.Nanos != math.MaxInt64 {
		t.Errorf("the last nanosecond that fits: got %+v, %v", got, ok)
	}
	if got, ok := iso.Parse("2262-04-11 23:47:16.9"); ok {
		t.Errorf("a fraction past the last nanosecond that fits was accepted: %+v", got)
	}
}

func TestParseStillAcceptsAStampWithNoYear(t *testing.T) {
	want := record.Stamp{State: record.YearPending, Month: 12, Day: 31, NanoOfDay: int64(23*time.Hour + 59*time.Minute + 59*time.Second)}
	if got, ok := layoutNamed(t, "syslog").Parse("Dec 31 23:59:59 host x"); !ok || got != want {
		t.Errorf("Parse = %+v, %v; want %+v, true", got, ok, want)
	}
}
