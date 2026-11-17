// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package query

import (
	"strings"
	"testing"
)

// wantTimesLine is the line of decision 8.
const wantTimesLine = "times: UTC for sources that state a zone, as written in the log for the others"

func plainAnswer() Answer {
	return Answer{
		Question: Question{Format: 1, Verb: Count},
		Columns:  []string{"count"},
		Rows:     [][]string{{"12"}},
		Total:    1,
		Facts: Facts{
			Rows: 12, HasTimes: true,
			FirstNanos: 0, LastNanos: 90_400_000_000, // 1970-01-01 00:00:00 and 00:01:30.4
			NoTime: 2, First: &Ref{Source: 1, Line: 1}, Last: &Ref{Source: 2, Line: 30},
		},
		ElapsedMillis: 4,
	}
}

func TestRenderTextIsTheFixedShape(t *testing.T) {
	got := RenderText("A3", plainAnswer())
	want := "answer A3: count\n" +
		"question: {\"format\":1,\"verb\":\"count\"}\n" +
		"count\n" +
		"12\n" +
		"rows covered 12\n" +
		"first time: 1970-01-01 00:00:00\n" +
		"last time: 1970-01-01 00:01:30\n" +
		"span: 1m30s\n" +
		"first: source 1 line 1\n" +
		"last: source 2 line 30\n" +
		"no time: 2\n" +
		"answered in 4 ms\n" +
		wantTimesLine + "\n"
	if got != want {
		t.Errorf("RenderText =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderTextHeaderNamesTheAnswerOrNone(t *testing.T) {
	for _, id := range []string{"", "-"} {
		if first, _, _ := strings.Cut(RenderText(id, plainAnswer()), "\n"); first != "answer: count" {
			t.Errorf("id %q: first line %q", id, first)
		}
	}
	if first, _, _ := strings.Cut(RenderText("A12", plainAnswer()), "\n"); first != "answer A12: count" {
		t.Errorf("first line %q", first)
	}
}

func TestRenderTextLeavesOutWhatTheFactsDoNotHold(t *testing.T) {
	a := plainAnswer()
	a.Facts = Facts{}
	got := RenderText("A1", a)
	for _, s := range []string{"rows covered", "first time", "last time", "span", "first:", "last:", "no time", "cut:", "times:"} {
		if strings.Contains(got, s) {
			t.Errorf("a answer with no facts says %q:\n%s", s, got)
		}
	}
	if !strings.HasSuffix(got, "answered in 4 ms\n") {
		t.Errorf("the last line is not the elapsed time:\n%s", got)
	}
	// Rows but no times: no time lines and no times line.
	a.Facts = Facts{Rows: 5, NoTime: 5}
	got = RenderText("A1", a)
	if !strings.Contains(got, "rows covered 5\n") || !strings.Contains(got, "no time: 5\n") {
		t.Errorf("rows covered or no time is missing:\n%s", got)
	}
	for _, s := range []string{"first time", "last time", "span", "times:"} {
		if strings.Contains(got, s) {
			t.Errorf("an answer with no times says %q:\n%s", s, got)
		}
	}
}

func TestRenderTextCleansEveryCellAndTheQuestion(t *testing.T) {
	hostile := "\x1b[31mred\u202eevil\x7f\nline\ttab\u009b"
	a := Answer{
		Question: Question{Format: 1, Verb: Where, Value: hostile},
		Columns:  []string{"na" + hostile + "me", "second"},
		Rows:     [][]string{{hostile, "ok"}, {"a", hostile}},
		Total:    2,
		Facts:    Facts{Rows: 2},
	}
	got := RenderText("A1", a)
	for _, raw := range []string{"\x1b", "\u202e", "\x7f", "\u009b"} {
		if strings.Contains(got, raw) {
			t.Errorf("the text holds a raw %q:\n%q", raw, got)
		}
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	// header, question, column names, two rows, rows covered, answered in
	if len(lines) != 7 {
		t.Fatalf("%d lines, want 7:\n%q", len(lines), got)
	}
	for i := 2; i <= 4; i++ {
		if n := strings.Count(lines[i], "\t"); n != 1 {
			t.Errorf("line %d has %d tabs, want 1: %q", i, n, lines[i])
		}
	}
	if !strings.HasPrefix(lines[1], "question: ") || !strings.Contains(lines[1], `\u001b[31mred\u202eevil\x7f\n`) {
		t.Errorf("the question line is %q", lines[1])
	}
	if lines[0] != "answer A1: where" {
		t.Errorf("the header carries more than the verb: %q", lines[0])
	}
}

func TestRenderTextSaysWhenAnswerWasCut(t *testing.T) {
	a := plainAnswer()
	a.Rows = [][]string{{"1"}, {"2"}, {"3"}}
	a.Total = 250
	a.Cut = "rows"
	if got := RenderText("A1", a); !strings.Contains(got, "\ncut: rows (showing 3 of 250)\n") {
		t.Errorf("no cut line for rows:\n%s", got)
	}
	a.Cut = "bytes"
	if got := RenderText("A1", a); !strings.Contains(got, "\ncut: bytes (showing 3 of 250)\n") {
		t.Errorf("no cut line for bytes:\n%s", got)
	}
	a.Cut = ""
	if got := RenderText("A1", a); strings.Contains(got, "cut:") {
		t.Errorf("a cut line for an answer that was not cut:\n%s", got)
	}
	// A cut value from a stored answer is cleaned too.
	a.Cut = "\x1b[2J"
	if got := RenderText("A1", a); strings.Contains(got, "\x1b") {
		t.Errorf("a raw ESC in the cut line:\n%q", got)
	}
}

func TestRenderTextTimesAreUTCAndDoNotDependOnTheZone(t *testing.T) {
	a := plainAnswer()
	a.Facts.FirstNanos = 1_700_000_000_000_000_000 // 2023-11-14 22:13:20 UTC
	a.Facts.LastNanos = a.Facts.FirstNanos + 3*3600*1_000_000_000 + 1_499_000_000
	got := RenderText("A1", a)
	for _, want := range []string{"first time: 2023-11-14 22:13:20\n", "last time: 2023-11-15 01:13:21\n", "span: 3h0m1s\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, wantTimesLine+"\n") {
		t.Errorf("the last line is not the times line:\n%s", got)
	}
}
