// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package summary

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/textsafe"
)

const (
	// textBytes is how much of a string from a log is shown: a string is cleaned
	// and then cut so that at most textBytes bytes of it remain, and "..." is
	// appended when something was cut. So a shown string is at most
	// textBytes+len("...") = 123 bytes, whatever the log held.
	textBytes = 120
	// sourceNameBytes is the cut of a source name in the sources block, where a
	// line is at most sourceLineBytes bytes; the cut is made smaller when the
	// numbers of the line need the room.
	sourceNameBytes = 40
	sourceLineBytes = 100
	// maxSources is how many sources the sources block lists; the rest are
	// counted in one line.
	maxSources = 16
	// stampLayout is how a stored time is written. The zone is UTC: a time is
	// made from stored nanoseconds with time.Unix(0, n).UTC(), never the local
	// zone, so the same database gives the same text on every machine.
	stampLayout = "2006-01-02 15:04:05"
)

// shown returns s made safe to print and at most max bytes long, plus "..." when
// it was cut. s is cleaned first (textsafe.Clean, character by character, so
// that what is counted is what is printed), then cut on a character boundary:
// a character that would pass max, an escape such as \x1b included, is left out
// whole, never cut in the middle. The result is at most max+3 bytes.
func shown(s string, max int) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		_, size := utf8.DecodeRuneInString(s[i:])
		piece := textsafe.Clean(s[i : i+size])
		if b.Len()+len(piece) > max {
			b.WriteString("...")
			return b.String()
		}
		b.WriteString(piece)
		i += size
	}
	return b.String()
}

// pct writes a share (0 to 1) as a percentage with one decimal. A share that is
// not a finite number is written as 0.0%, so that nothing prints NaN. The verb is
// fixed, so a tie is rounded the same way every time (strconv rounds the exact
// tie 1.25 to 1.2).
func pct(share float64) string {
	if math.IsNaN(share) || math.IsInf(share, 0) {
		share = 0
	}
	return strconv.FormatFloat(share*100, 'f', 1, 64) + "%"
}

// ratio is n divided by of, and 0 when of is not positive.
func ratio(n, of int64) float64 {
	if of <= 0 {
		return 0
	}
	return float64(n) / float64(of)
}

// stamp writes stored nanoseconds as a time in UTC.
func stamp(nanos int64) string {
	return time.Unix(0, nanos).UTC().Format(stampLayout)
}

// plural returns the noun for a count: one for 1, many for any other number
// ("1 record", "2 records", "0 records"). A noun is written with its count, so
// a line says "1 record", never "1 records".
func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// zoneWord is what follows a time that is known to be in UTC.
const zoneWord = " UTC"

// span writes the first and the last time of something; utc says whether the
// times are known to be in UTC (see allExact), in which case the zone is named.
// A time with no stated zone is shown as it was written, with no zone word.
func span(first, last int64, utc bool) string {
	s := stamp(first) + " to " + stamp(last)
	if utc {
		s += zoneWord
	}
	return s
}

// allExact reports whether the summary may name the zone of the times of
// records that come from any source: it has sources with times, and every one
// of them states its zone (store.TimeExact, a time converted to UTC). A source
// with no time does not matter. A summary with no sources block has no source
// to say, so no zone is named.
func allExact(blocks []Block) bool {
	found := false
	for _, blk := range blocks {
		if blk.Kind != "sources" {
			continue
		}
		for _, s := range blk.Sources {
			if !s.HasTimes {
				continue
			}
			if s.TimeKind != store.TimeExact {
				return false
			}
			found = true
		}
	}
	return found
}

// humanBytes writes a size with a binary unit; it is at most 10 bytes for an
// int64 ("1023.9 KiB", or "1024.0 KiB" when the rounding reaches it).
func humanBytes(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	v := float64(n) / 1024
	u := 0
	for v >= 1024 && u < len(units)-1 {
		v /= 1024
		u++
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[u]
}

// number writes a real as plainly as it can: positional notation for the
// magnitudes in between, and the shortest exponent form for 0 beyond them, so
// that a huge or a tiny value (5e-324, 1e300) stays short and a line stays
// within its bound.
func number(f float64) string {
	if a := math.Abs(f); a == 0 || (a >= 1e-6 && a < 1e15) {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// Render writes the summary as plain text: for each block a heading such as
// [S1] sources and its lines, blocks separated by one blank line. No time zone
// is assumed or converted: a time is shown as the log wrote it, and the word UTC
// follows a time only where the log states its zone (the source is
// store.TimeExact): in the line of such a source, and in the lines of patterns,
// slots and time blocks only when every source with times is of that kind. A
// render that is empty has no blocks. No line is longer than 400 bytes and no
// byte below 0x20 other than the line feed is written.
func Render(s Summary) string {
	return render(s, allExact(s.Blocks))
}

// render is Render with the decision about the zone made: utc says that the
// times of patterns, slots and time blocks are named as UTC.
func render(s Summary, utc bool) string {
	var b strings.Builder
	for i, blk := range s.Blocks {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[%s] %s\n", blk.ID, blk.Kind)
		switch blk.Kind {
		case "sources":
			renderSources(&b, blk.Sources)
		case "miner":
			renderMiner(&b, blk.Miner)
		case "patterns":
			for _, p := range blk.Patterns {
				fmt.Fprintf(&b, "  %s\n", patternLine(p, utc))
			}
			if s.Omitted.Patterns > 0 {
				fmt.Fprintf(&b, "not listed: %d more %s (%d %s)\n", s.Omitted.Patterns, plural(int64(s.Omitted.Patterns), "pattern", "patterns"),
					s.Omitted.Records, plural(s.Omitted.Records, "record", "records"))
			}
		case "slots":
			renderSlots(&b, blk)
		case "time":
			renderTime(&b, blk, utc)
		}
	}
	return b.String()
}

// sourceLine is the line of one source, at most sourceLineBytes bytes: the name
// is cut at sourceNameBytes, or shorter when the numbers of the line (up to 19
// digits each) need the room.
func sourceLine(s store.SourceInfo) string {
	rest := fmt.Sprintf(": %d rec, %s, %s timed", s.Records, humanBytes(s.Bytes), pct(ratio(s.WithTime, s.Records)))
	if s.HasTimes {
		rest += ", " + stamp(s.FirstNanos) + ".." + stamp(s.LastNanos)
		if s.TimeKind == store.TimeExact {
			rest += zoneWord
		}
	}
	name := s.Name
	if name == "-" {
		// The stored name stays; the report of analyze says it the same way.
		name = "standard input"
	}
	room := sourceLineBytes - 2 - len(rest) - 3 // the indent, and the dots of a cut name
	if room < 0 {
		// The numbers alone take the line (only for counts of 19 digits): the
		// name is left out, because a cut name still takes its three dots.
		return "  " + rest[len(": "):]
	}
	return "  " + shown(name, min(sourceNameBytes, room)) + rest
}

func renderSources(b *strings.Builder, srcs []store.SourceInfo) {
	if len(srcs) == 0 {
		b.WriteString("  no sources\n")
		return
	}
	for i, s := range srcs {
		if i == maxSources {
			var records int64
			for _, r := range srcs[maxSources:] {
				records += r.Records
			}
			fmt.Fprintf(b, "  not listed: %d more %s (%d %s)\n", len(srcs)-maxSources, plural(int64(len(srcs)-maxSources), "source", "sources"), records, plural(records, "record", "records"))
			break
		}
		b.WriteString(sourceLine(s))
		b.WriteByte('\n')
	}
}

// renderMiner writes the report of the miner on itself. A database with no
// pattern at all (mined with no miner, or empty) says so and nothing about
// shares: its records all have pattern 0 by default, which is not a catch-all.
func renderMiner(b *strings.Builder, m *store.MinerReport) {
	if m == nil || m.Patterns == 0 {
		b.WriteString("  no patterns were mined\n")
		return
	}
	// The patterns hold the records that the catch-all does not: a record that
	// no pattern fits (past the cap, or with nothing but a time stamp) is
	// in the catch-all, which is not one of the m.Patterns.
	held := m.Records - m.CatchAll
	verb := "hold"
	if m.Patterns == 1 {
		verb = "holds"
	}
	fmt.Fprintf(b, "  %d %s %s %d %s\n", m.Patterns, plural(int64(m.Patterns), "pattern", "patterns"), verb, held, plural(held, "record", "records"))
	fmt.Fprintf(b, "  seen once: %d %s (%s of records)\n", m.SeenOnce, plural(m.SeenOnce, "pattern", "patterns"), pct(ratio(m.SeenOnce, m.Records)))
	fmt.Fprintf(b, "  catch-all: %d %s (%s of records)\n", m.CatchAll, plural(m.CatchAll, "record", "records"), pct(ratio(m.CatchAll, m.Records)))
}

// patternLine is one pattern: id, records, share, first and last time, text.
func patternLine(p store.PatternStat, utc bool) string {
	id := fmt.Sprintf("p%d", p.ID)
	if p.CatchAll {
		id += " (catch-all)"
	}
	when := "no time"
	if p.HasTimes {
		when = span(p.FirstNanos, p.LastNanos, utc)
	}
	return fmt.Sprintf("%s  %d %s (%s)  %s  %s", id, p.Records, plural(p.Records, "record", "records"), pct(p.Share), when, shown(p.Text, textBytes))
}

// patternHead is the line that names the pattern a block is about.
func patternHead(blk Block) string {
	if len(blk.Patterns) == 0 {
		return fmt.Sprintf("  pattern %d\n", blk.Pattern)
	}
	return fmt.Sprintf("  pattern %d: %s\n", blk.Pattern, shown(blk.Patterns[0].Text, textBytes))
}

func renderSlots(b *strings.Builder, blk Block) {
	b.WriteString(patternHead(blk))
	for _, s := range blk.Slots {
		fmt.Fprintf(b, "  slot %d (%s): %d %s, %d distinct", s.Pos, shown(s.Type, 16), s.Total, plural(s.Total, "value", "values"), s.Distinct)
		if s.Numeric && s.Total > 0 {
			fmt.Fprintf(b, ", range %s to %s", number(s.Min), number(s.Max))
		}
		b.WriteByte('\n')
		for _, v := range s.Top {
			fmt.Fprintf(b, "    %d (%s) %s\n", v.Count, pct(v.Share), shown(v.Value, textBytes))
		}
	}
}

func renderTime(b *strings.Builder, blk Block, utc bool) {
	zone := "times as written in the log (no zone is assumed)"
	if utc {
		zone = "times in UTC"
	}
	if blk.Pattern < 0 {
		b.WriteString("  all records, " + zone + "\n")
	} else {
		b.WriteString(strings.TrimSuffix(patternHead(blk), "\n") + ", " + zone + "\n")
	}
	for _, k := range blk.Buckets {
		fmt.Fprintf(b, "  %s to %s  %d\n", stamp(k.StartNanos), stamp(k.EndNanos), k.Count)
	}
}

// RenderDigest writes the digest as plain text: a line with the record and
// pattern counts, then one line for each group in order.
func RenderDigest(d Digest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "digest: %d %s, %d %s\n", d.Records, plural(d.Records, "record", "records"), d.Patterns, plural(int64(d.Patterns), "pattern", "patterns"))
	for i, g := range d.Groups {
		id := fmt.Sprintf("p%d", g.ID)
		if g.CatchAll {
			id += " (catch-all)"
		}
		fmt.Fprintf(&b, "  %d. %s  %d %s (%s)  %s\n", i+1, id, g.Records, plural(g.Records, "record", "records"), pct(g.Share), shown(g.Text, textBytes))
	}
	return b.String()
}
