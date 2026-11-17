// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/ckir/aigolog/internal/testlog"
)

// These are the A2 cases of the generators, asked through the command query:
// what the generator counted from what it wrote (the truth structs of
// internal/testlog), and what the query tools answer for the same log, given
// as a file and on standard input. internal/ingest/truth_test.go makes the same
// checks with SQL. The slots are found with the verbs patterns and slots, as in
// a3_test.go; none is written here.

func itoa(n int) string { return strconv.Itoa(n) }

// topRow returns the first row of "top --by by", and its rows as a number.
func topRow(t *testing.T, f failedLogins, by string, extra ...string) (value string, rows int64) {
	t.Helper()
	a := f.ask(t, append(extra, "top", "--by", by, "--limit", "1")...)
	if len(a.Rows) != 1 {
		t.Fatalf("top by %s has no row: %v", by, a.Rows)
	}
	return a.Rows[0][0], whole(t, cell(t, a, a.Rows[0], "rows"))
}

func TestGeneratedLogsGiveTheirTruthThroughTheQueryTools(t *testing.T) {
	t.Run("ssh", func(t *testing.T) {
		data, truth := testlog.SSH(1, 3000)
		// The comparisons below tell something only when the numbers differ.
		if truth.FailedLines < 1000 || truth.TopAttempts < 100 || truth.AcceptedLine != 1500 ||
			truth.Names <= truth.TopNames || truth.TopOnRoot >= truth.TopAttempts || truth.TopOnRoot == 0 {
			t.Fatalf("the truth is too small or degenerate to prove anything: %+v", truth)
		}
		for _, w := range logWays {
			t.Run(w.name, func(t *testing.T) {
				h, db := analyzeVia(t, w, data)
				f := findFailedLogins(t, h, db)

				if c := ask(t, h, db, "--pattern", ids(f.patterns), "count"); len(c.Rows) != 1 || c.Rows[0][0] != itoa(truth.FailedLines) {
					t.Errorf("count of the Failed password patterns is %v, truth %d", c.Rows, truth.FailedLines)
				}
				if a := f.ask(t, "top", "--by", "addr", "--limit", "1"); a.Total != int64(truth.Addresses) {
					t.Errorf("%d distinct addresses, truth %d", a.Total, truth.Addresses)
				}
				if a := f.ask(t, "top", "--by", "names", "--limit", "1"); a.Total != int64(truth.Names) {
					t.Errorf("%d distinct names, truth %d", a.Total, truth.Names)
				}

				acc := patternsWith(t, h, db, "Accepted")
				if len(acc) != 1 || acc[0].Records != 1 {
					t.Fatalf("the patterns of Accepted are %+v, want one, with one record", acc)
				}
				if r := ask(t, h, db, "--pattern", ids(acc), "rows", "--limit", "1"); len(r.Rows) != 1 || cell(t, r, r.Rows[0], "line") != itoa(truth.AcceptedLine) {
					t.Errorf("the Accepted record is %v, truth: one, at line %d", r.Rows, truth.AcceptedLine)
				}

				top, n := topRow(t, f, "addr")
				if top != truth.TopAddress || n != int64(truth.TopAttempts) {
					t.Errorf("the top address is %s with %d rows, truth %s with %d", top, n, truth.TopAddress, truth.TopAttempts)
				}
				who := "addr=" + truth.TopAddress
				c := f.ask(t, "--where", who, "count")
				if len(c.Rows) != 1 || c.Rows[0][0] != itoa(truth.TopAttempts) {
					t.Errorf("%s: count is %v, truth %d", truth.TopAddress, c.Rows, truth.TopAttempts)
				}
				if first, last := lines(t, c); first != int64(truth.TopFirstLine) || last != int64(truth.TopLastLine) {
					t.Errorf("%s: first and last line %d and %d, truth %d and %d", truth.TopAddress, first, last, truth.TopFirstLine, truth.TopLastLine)
				}
				p := f.ask(t, "--where", who, "pairs", "--by", "addr", "--with", "names")
				if pr := row(p, truth.TopAddress); pr == nil || cell(t, p, pr, "rows") != itoa(truth.TopAttempts) || cell(t, p, pr, "distinct") != itoa(truth.TopNames) {
					t.Errorf("%s: pairs says %v, truth %d rows on %d names", truth.TopAddress, p.Rows, truth.TopAttempts, truth.TopNames)
				}
				names := f.ask(t, "--where", who, "top", "--by", "names", "--limit", "100")
				if root := row(names, "root"); names.Total != int64(truth.TopNames) || root == nil || cell(t, names, root, "rows") != itoa(truth.TopOnRoot) {
					t.Errorf("%s: top by names has %d values and root is %v, truth %d values and root on %d", truth.TopAddress, names.Total, root, truth.TopNames, truth.TopOnRoot)
				}
			})
		}
	})

	t.Run("web", func(t *testing.T) {
		data, truth := testlog.Web(1, 3000)
		if truth.DistinctPaths < 500 || truth.ClientRequests < 1000 ||
			truth.ClientPaths >= truth.ClientRequests || truth.DistinctPaths <= truth.ClientPaths {
			t.Fatalf("the truth is too small or degenerate to prove anything: %+v", truth)
		}
		for _, w := range logWays {
			t.Run(w.name, func(t *testing.T) {
				h, db := analyzeVia(t, w, data)
				pats := patternsWith(t, h, db, "GET")
				if len(pats) == 0 {
					t.Fatal("no pattern holds GET")
				}
				var client, path []int64
				for _, p := range pats {
					client = append(client, columnOf(t, h, db, p.ID, "ip4")...)
					path = append(path, columnOf(t, h, db, p.ID, "word")...)
				}
				if len(client) != len(pats) || len(path) != len(pats) {
					t.Fatalf("the patterns %+v have %d ip4 slots and %d word slots, want one of each each", pats, len(client), len(path))
				}
				web := func(args ...string) []string {
					return append([]string{"--col", colArg("client", client), "--col", colArg("path", path)}, args...)
				}

				if c := ask(t, h, db, web("count")...); len(c.Rows) != 1 || c.Rows[0][0] != itoa(truth.Requests) || c.Facts.Rows != int64(truth.Requests) {
					t.Errorf("count is %v covering %d rows, truth %d requests", c.Rows, c.Facts.Rows, truth.Requests)
				}
				if a := ask(t, h, db, web("top", "--by", "path", "--limit", "1")...); a.Total != int64(truth.DistinctPaths) {
					t.Errorf("%d distinct paths, truth %d", a.Total, truth.DistinctPaths)
				}
				a := ask(t, h, db, web("top", "--by", "client", "--limit", "1")...)
				if len(a.Rows) != 1 || a.Rows[0][0] != truth.Client || cell(t, a, a.Rows[0], "rows") != itoa(truth.ClientRequests) {
					t.Errorf("the top client is %v, truth %s with %d requests", a.Rows, truth.Client, truth.ClientRequests)
				}
				p := ask(t, h, db, web("--where", "client="+truth.Client, "pairs", "--by", "client", "--with", "path")...)
				if pr := row(p, truth.Client); pr == nil || cell(t, p, pr, "rows") != itoa(truth.ClientRequests) || cell(t, p, pr, "distinct") != itoa(truth.ClientPaths) {
					t.Errorf("%s: pairs says %v, truth %d requests on %d paths", truth.Client, p.Rows, truth.ClientRequests, truth.ClientPaths)
				}
			})
		}
	})

	t.Run("restart", func(t *testing.T) {
		// Seed 1 is a log that keeps looping; seed 2 ends with the unit giving up.
		var gaveUp, looping int
		for _, seed := range []int64{1, 2} {
			data, truth := testlog.Restart(seed, 300)
			if truth.Unit != "app-worker.service" || truth.Restarts < 20 {
				t.Fatalf("seed %d: the truth is too small to prove anything: %+v", seed, truth)
			}
			if truth.GaveUp {
				gaveUp++
			} else {
				looping++
			}
			for _, w := range logWays {
				t.Run(fmt.Sprintf("seed%d/%s", seed, w.name), func(t *testing.T) {
					h, db := analyzeVia(t, w, data)

					// The counter is the last int slot of the pattern that says
					// "restart counter is at <int>": the first is the process
					// number of systemd. slots states its greatest value.
					counter := patternsWith(t, h, db, "restart", "counter")
					if len(counter) != 1 || counter[0].Records != int64(truth.Restarts) {
						t.Fatalf("the patterns of restart counter are %+v, want one, with %d records", counter, truth.Restarts)
					}
					var last *slotRow
					for _, s := range patternSlots(t, h, db, counter[0].ID) {
						if s.Type == "int" {
							s := s
							last = &s
						}
					}
					if last == nil {
						t.Fatalf("pattern %d has no int slot", counter[0].ID)
					}
					if last.Max != itoa(truth.Restarts) || last.Min != "1" || last.Values != int64(truth.Restarts) || last.Distinct != int64(truth.Restarts) {
						t.Errorf("the counter has %d values, %d distinct, from %s to %s; truth 1 to %d, each once", last.Values, last.Distinct, last.Min, last.Max, truth.Restarts)
					}
					if c := ask(t, h, db, "--pattern", ids(counter), "count"); len(c.Rows) != 1 || c.Rows[0][0] != itoa(truth.Restarts) {
						t.Errorf("count of the restart pattern is %v, truth %d", c.Rows, truth.Restarts)
					}

					// The unit is a word of the text of the patterns of its events,
					// not a value of a slot, so patterns finds it (where, which looks
					// in the values of the slots, does not).
					unit := patternsWith(t, h, db, truth.Unit)
					found := false
					for _, p := range unit {
						found = found || p.ID == counter[0].ID
					}
					if len(unit) < 4 || !found {
						t.Errorf("the patterns that hold %s are %+v, want its events, among them the restart counter (pattern %d)", truth.Unit, unit, counter[0].ID)
					}

					// The unit gave up exactly when a pattern holds "Failed to start"
					// and has records; "Start request repeated too quickly" goes with it.
					for _, words := range [][]string{{"Failed", "to", "start"}, {"Start", "request", "repeated", "too", "quickly"}} {
						var records int64
						for _, p := range patternsWith(t, h, db, words...) {
							records += p.Records
						}
						if (records > 0) != truth.GaveUp {
							t.Errorf("the patterns of %v have %d records; truth GaveUp = %v", words, records, truth.GaveUp)
						}
					}
				})
			}
		}
		if gaveUp == 0 || looping == 0 {
			t.Errorf("%d logs gave up and %d were still looping: want both", gaveUp, looping)
		}
	})
}
