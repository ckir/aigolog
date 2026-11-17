// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package timeparse reads the time stamp that a log line begins with. Times
// are taken as written: no zone is assumed or converted. A stamp with no zone
// is kept as a wall-clock reading, carried as nanoseconds since 1970 as if the
// reading were UTC.
package timeparse

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/ckir/aigolog/internal/record"
)

// The two ways a layout writes its zone, which decide how much of the matched
// text is the zone text.
const (
	zoneNone  = iota // the layout has no zone
	zoneColon        // Z07:00: "Z", or a sign and hh:mm
	zonePlain        // -0700: a sign and hhmm
)

// Layout is one way a log writes the time stamp at the start of a line.
type Layout struct {
	// Name is the short name of the layout.
	Name string
	// Exact is true when the layout states a zone, so the stamp is a real
	// instant.
	Exact bool
	// YearPending is true when the layout has no year.
	YearPending bool

	goLayout string // the layout in the form time.Parse takes
	zone     int    // how the layout writes its zone
}

// Layouts are the layouts that are known, in the order Detect prefers them
// when two fit equally well.
var Layouts = []Layout{
	{Name: "rfc3339", Exact: true, goLayout: "2006-01-02T15:04:05Z07:00", zone: zoneColon},
	{Name: "iso-space-offset", Exact: true, goLayout: "2006-01-02 15:04:05Z07:00", zone: zoneColon},
	{Name: "iso-space-offset-sp", Exact: true, goLayout: "2006-01-02 15:04:05 -0700", zone: zonePlain},
	{Name: "iso-t-offset-plain", Exact: true, goLayout: "2006-01-02T15:04:05-0700", zone: zonePlain},
	{Name: "iso-space-offset-plain", Exact: true, goLayout: "2006-01-02 15:04:05-0700", zone: zonePlain},
	{Name: "clf", Exact: true, goLayout: "02/Jan/2006:15:04:05 -0700", zone: zonePlain},
	{Name: "iso-t", goLayout: "2006-01-02T15:04:05"},
	{Name: "iso-space", goLayout: "2006-01-02 15:04:05"},
	{Name: "slash", goLayout: "2006/01/02 15:04:05"},
	{Name: "ctime", goLayout: "Mon Jan _2 15:04:05 2006"},
	{Name: "syslog", YearPending: true, goLayout: "Jan _2 15:04:05"},
}

// maxSlack is how many bytes past the length of the layout a stamp is looked
// for. It leaves room for a priority tag, a bracket and fractional seconds,
// and it bounds the work for a line of any length.
const maxSlack = 40

// detectLines is how many lines Detect looks at.
const detectLines = 100

// WallNanos returns the nanoseconds since 1970 of the year, month, day, hour,
// minute, second and nanosecond of t, read as UTC. The zone of t is dropped.
// It is false when that time is too far from 1970 to be counted in an int64
// (see nanosOf).
func WallNanos(t time.Time) (int64, bool) {
	return nanosOf(wallTime(t))
}

// wallTime returns the year, month, day, hour, minute, second and nanosecond
// of t, read as UTC.
func wallTime(t time.Time) time.Time {
	y, mo, d := t.Date()
	h, mi, s := t.Clock()
	return time.Date(y, mo, d, h, mi, s, t.Nanosecond(), time.UTC)
}

// The whole seconds since 1970 that a count of nanoseconds in an int64 can
// reach, about 1677-09-21 to 2262-04-11.
const (
	minSecond = math.MinInt64 / 1_000_000_000
	maxSecond = math.MaxInt64 / 1_000_000_000
)

// nanosOf returns the nanoseconds since 1970 of t. It is false when t is too
// far from 1970 to be counted in an int64 (time.Time.UnixNano gives a wrapped
// number then, so it is checked and never returned).
func nanosOf(t time.Time) (int64, bool) {
	if s := t.Unix(); s < minSecond || s > maxSecond {
		return 0, false
	}
	n := t.UnixNano()
	// Within the last whole second at either end a fraction can still wrap,
	// and then the count does not give t back.
	if !time.Unix(0, n).Equal(t) {
		return 0, false
	}
	return n, true
}

// isSpace reports whether b is white space.
func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// skipPrefix returns how many bytes at the start of s are a priority tag
// (< digits >, one to three digits) and then one [ or (, each optional and in
// that order.
func skipPrefix(s string) int {
	n := 0
	if n < len(s) && s[n] == '<' {
		i := n + 1
		for i < len(s) && i-n-1 < 3 && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i > n+1 && i < len(s) && s[i] == '>' {
			n = i + 1
		}
	}
	if n < len(s) && (s[n] == '[' || s[n] == '(') {
		n++
	}
	return n
}

// Parse reads the stamp that line begins with, in this layout. A line that
// begins with white space has none. A priority tag and a bracket may come
// before the stamp. Only the first bytes of the line are looked at. The
// second result is false when the line has no stamp in this layout.
func (l Layout) Parse(line string) (record.Stamp, bool) {
	if line == "" || isSpace(line[0]) {
		return record.Stamp{}, false
	}
	if limit := len(l.goLayout) + maxSlack; len(line) > limit {
		line = line[:limit]
	}
	text := line[skipPrefix(line):]

	// time.Parse takes the whole text, so it reports what is left over after
	// the longest stamp it can read, and that gives the longest prefix.
	_, err := time.Parse(l.goLayout, text)
	if err != nil {
		var pe *time.ParseError
		if !errors.As(err, &pe) || !strings.Contains(pe.Message, "extra text") {
			return record.Stamp{}, false
		}
		text = text[:len(text)-len(pe.ValueElem)]
	}
	t, err := time.Parse(l.goLayout, text)
	if err != nil {
		return record.Stamp{}, false
	}

	switch {
	case l.YearPending:
		return record.Stamp{
			State:     record.YearPending,
			Month:     int(t.Month()),
			Day:       t.Day(),
			NanoOfDay: int64(time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute + time.Duration(t.Second())*time.Second + time.Duration(t.Nanosecond())),
		}, true
	case l.zone != zoneNone:
		n, ok := nanosOf(t)
		if !ok {
			return record.Stamp{}, false
		}
		return record.Stamp{State: record.Resolved, Nanos: n, Exact: true, Zone: zoneText(text, l.zone)}, true
	}
	n, ok := nanosOf(wallTime(t))
	if !ok {
		return record.Stamp{}, false
	}
	return record.Stamp{State: record.Resolved, Nanos: n}, true
}

// zoneText returns the zone as written at the end of the matched text.
func zoneText(text string, kind int) string {
	if kind == zoneColon {
		if strings.HasSuffix(text, "Z") {
			return "Z"
		}
		return text[len(text)-6:]
	}
	return text[len(text)-5:]
}

// Detect picks the layout that the lines are written in. It looks at the
// first 100 lines that are not blank and do not begin with a space or a tab
// (such lines are the continuation of a record). A layout fits when it parses
// at least half of those lines, and at least one. Of the layouts that fit, the
// one that parses most lines is returned, the earlier one in Layouts on a tie.
// The second result is false when none fits.
func Detect(lines []string) (Layout, bool) {
	counts := make([]int, len(Layouts))
	considered := 0
	for _, line := range lines {
		if considered == detectLines {
			break
		}
		if line == "" || line[0] == ' ' || line[0] == '\t' || strings.TrimSpace(line) == "" {
			continue
		}
		considered++
		for i, l := range Layouts {
			if _, ok := l.Parse(line); ok {
				counts[i]++
			}
		}
	}
	best := -1
	for i, c := range counts {
		if c < 1 || c*2 < considered {
			continue
		}
		if best < 0 || c > counts[best] {
			best = i
		}
	}
	if best < 0 {
		return Layout{}, false
	}
	return Layouts[best], true
}
