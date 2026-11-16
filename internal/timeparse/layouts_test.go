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
