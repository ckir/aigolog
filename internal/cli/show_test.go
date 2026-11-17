// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/query"
	"github.com/ckir/aigolog/internal/store"
)

func TestShowPrintsTheRunTheSummaryTheAnswersAndTheNoExchangeLine(t *testing.T) {
	h := newHarness(t)
	path, analysis := analysed(t, h, authLog)
	if code, _, errOut := h.run("query", "--db", path, "count"); code != exitcode.OK || errOut != "" {
		t.Fatalf("count: code %d, stderr %q", code, errOut)
	}
	if code, _, errOut := h.run("query", "--db", path, "patterns", "rejected"); code != exitcode.OK || errOut != "" {
		t.Fatalf("patterns: code %d, stderr %q", code, errOut)
	}
	code, out, errOut := h.run("show", "--db", path)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !regexp.MustCompile(`^database: ` + regexp.QuoteMeta(path) + ` \([0-9]+ bytes\)$`).MatchString(lines[0]) {
		t.Errorf("first line = %q", lines[0])
	}
	if last := lines[len(lines)-1]; last != "model exchange: none recorded" {
		t.Errorf("last line = %q", last)
	}
	// The digest and the summary are what analyze printed: the text from the
	// blank line before "patterns" to the line before "database:".
	start := strings.Index(analysis, "\npatterns\n")
	end := strings.Index(analysis, "database: ")
	if start < 0 || end < start {
		t.Fatalf("analyze printed no patterns or no database line:\n%s", analysis)
	}
	if body := analysis[start:end]; !strings.Contains(out, body) {
		t.Errorf("show did not print the digest and the summary of analyze:\n%s\nwant it to hold:\n%s", out, body)
	}
	// The order: the run, the summary, the answers, the exchange.
	at := -1
	for _, want := range []string{"database: ", "\npatterns\n", "\nsummary\n", "\nanswers: 2 stored\n", "\nanswer A1: count\n", "\nanswer A2: patterns\n", "\nmodel exchange: none recorded\n"} {
		i := strings.Index(out, want)
		if i < 0 || i < at {
			t.Errorf("%q is at %d, after %d, in:\n%s", want, i, at, out)
		}
		at = i
	}
	// Two answers are separated by a blank line.
	if !strings.Contains(out, "\n\nanswer A2: patterns\n") {
		t.Errorf("no blank line before the second answer:\n%s", out)
	}
	// The stored answer is shown as it was stored, with its question.
	if !strings.Contains(out, `question: {"format":1,"verb":"count"}`) {
		t.Errorf("the question of A1 is not shown:\n%s", out)
	}
}

func TestShowWithNoAnswersSaysSo(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	code, out, _ := h.run("show", "--db", path)
	if code != exitcode.OK || !strings.Contains(out, "\nanswers: 0 stored\n") || !strings.HasSuffix(out, "model exchange: none recorded\n") {
		t.Errorf("code %d, output:\n%s", code, out)
	}
}

func TestShowSaysWhenAStoredAnswerCannotBeRead(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	q := query.Question{Format: 1, Verb: query.Count}
	if _, err := store.SaveAnswer(context.Background(), path, q.Canonical(), "this is not an answer", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := h.run("query", "--db", path, "count"); code != exitcode.OK || errOut != "" {
		t.Fatalf("count: code %d, stderr %q", code, errOut)
	}
	code, out, errOut := h.run("show", "--db", path)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"\nanswers: 2 stored\n", "\nanswer A1: stored text could not be read\n", "\nanswer A2: count\n", "model exchange: none recorded\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestShowRefusesJSON(t *testing.T) {
	h := newHarness(t)
	path, _ := analysed(t, h, authLog)
	for _, args := range [][]string{
		{"show", "--db", path, "--json"},
		{"show", "--json", "--db", path},
		{"show", "--db", path, "extra"},
		{"show", "--db", path, "--", "extra"},
		{"show", "--db", path, "--bogus"},
		{"show"},
		{"show", "--db"},
		{"show", "extra"},
	} {
		code, out, errOut := h.run(args...)
		if code != exitcode.Usage || out != "" || !strings.HasPrefix(errOut, "aigolog: ") {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

func TestShowOnADatabaseThatCannotBeOpenedIsExitOne(t *testing.T) {
	h := newHarness(t)
	db, err := store.Create(context.Background(), store.Options{Dir: h.cache, Now: func() time.Time { return h.now }})
	if err != nil {
		t.Fatal(err)
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + ".missing"} {
		code, out, errOut := h.run("show", "--db", p)
		if code != exitcode.Failure || out != "" || !strings.HasPrefix(errOut, "aigolog: ") {
			t.Errorf("%s: code %d, stdout %q, stderr %q", p, code, out, errOut)
		}
	}
}
