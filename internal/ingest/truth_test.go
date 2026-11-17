// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package ingest_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/ingest"
	"github.com/ckir/aigolog/internal/miner"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/testlog"
)

// These are the A2 cases of the generators, carried through the miner and the
// store: the truth that the generator counted from what it wrote, and the
// numbers that SQL written here finds in the database. B2b repeats them
// through the query tools.

var bg = context.Background()

// wallNanos is the wall-clock reading of a date, as Options.AsOf takes it.
func wallNanos(y int, mo time.Month, d, h, mi, s int) int64 {
	return time.Date(y, mo, d, h, mi, s, 0, time.UTC).UnixNano()
}

// notAFile hides the type of the reader under it, as standard input does.
type notAFile struct{ io.Reader }

// via is how a log reaches Run: the name of the way, and the input it makes.
type via struct {
	name string
	make func(t *testing.T, data []byte) (ingest.Input, func())
}

var ways = []via{
	{"file", func(t *testing.T, data []byte) (ingest.Input, func()) {
		path := filepath.Join(t.TempDir(), "generated.log")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		st, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		return ingest.Input{Name: path, R: f, ModTime: st.ModTime()}, func() { f.Close() }
	}},
	{"stdin", func(t *testing.T, data []byte) (ingest.Input, func()) {
		return ingest.Input{Name: "-", R: notAFile{bytes.NewReader(data)}}, func() {}
	}},
}

// mineLog stores and mines a log, and returns the database for reading, and
// what Run said. The anchor of the logs that have no year is as.
func mineLog(t *testing.T, w via, data []byte, as int64) (*sql.DB, ingest.Result) {
	t.Helper()
	d, err := store.Create(bg, store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	in, closeIn := w.make(t, data)
	defer closeIn()
	res, err := ingest.Run(bg, d, []ingest.Input{in}, ingest.Options{
		AsOf:  &as,
		Now:   time.Date(2027, 1, 20, 0, 0, 0, 0, time.UTC),
		Zone:  time.UTC,
		Miner: miner.New(miner.DefaultConfig()),
	})
	if err != nil {
		t.Fatalf("%s: %v", w.name, err)
	}
	db, err := sql.Open(store.DriverName, d.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, res
}

// one returns the first column of the only row of a query, as an int.
func one(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// firstWord names the first slot of type word of the pattern of slot s.
const firstWord = `(SELECT MIN(w.pos) FROM slots w WHERE w.pattern = s.pattern AND w.type = 'word')`

func TestTheSSHLogKeepsItsTruthThroughTheMiner(t *testing.T) {
	data, truth := testlog.SSH(7, 3000)
	if truth.FailedLines < 1000 || truth.TopAttempts < 100 || truth.AcceptedLine != 1500 {
		t.Fatalf("the truth is too small to prove anything: %+v", truth)
	}
	// The comparisons below tell something only when the numbers differ.
	if truth.Names <= truth.TopNames || truth.TopOnRoot >= truth.TopAttempts {
		t.Fatalf("the truth is degenerate (all the names are the top address's, or all its attempts are on root): %+v", truth)
	}
	const failed = "%Failed password%"
	for _, w := range ways {
		t.Run(w.name, func(t *testing.T) {
			db, res := mineLog(t, w, data, wallNanos(2026, time.December, 31, 0, 0, 0))
			if res.Records != 3000 {
				t.Fatalf("%d records", res.Records)
			}
			if n := one(t, db, `SELECT COUNT(*) FROM patterns WHERE text LIKE ?`, failed); n != 2 {
				t.Errorf("%d patterns hold Failed password, want the 2 shapes", n)
			}
			if n := one(t, db, `SELECT COUNT(*) FROM records r JOIN patterns p ON p.id = r.pattern WHERE p.text LIKE ?`, failed); n != truth.FailedLines {
				t.Errorf("the records of the Failed password patterns are %d, truth %d", n, truth.FailedLines)
			}
			if n := one(t, db, `SELECT SUM(records) FROM patterns WHERE text LIKE ?`, failed); n != truth.FailedLines {
				t.Errorf("the counts of the Failed password patterns add up to %d, truth %d", n, truth.FailedLines)
			}
			if n := one(t, db, `SELECT COUNT(DISTINCT v.value) FROM slot_values v JOIN slots s ON s.id = v.slot JOIN patterns p ON p.id = s.pattern
WHERE p.text LIKE ? AND s.type = 'ip4'`, failed); n != truth.Addresses {
				t.Errorf("%d distinct addresses, truth %d", n, truth.Addresses)
			}
			if n := one(t, db, `SELECT COUNT(DISTINCT v.value) FROM slot_values v JOIN slots s ON s.id = v.slot JOIN patterns p ON p.id = s.pattern
WHERE p.text LIKE ? AND s.type = 'word' AND s.pos = `+firstWord, failed); n != truth.Names {
				t.Errorf("%d distinct names, truth %d", n, truth.Names)
			}

			if n := one(t, db, `SELECT COUNT(*) FROM patterns WHERE text LIKE '%Accepted%'`); n != 1 {
				t.Fatalf("%d patterns hold Accepted, want 1", n)
			}
			var recs, line int
			if err := db.QueryRow(`SELECT COUNT(*), MIN(r.line) FROM records r JOIN patterns p ON p.id = r.pattern WHERE p.text LIKE '%Accepted%'`).Scan(&recs, &line); err != nil {
				t.Fatal(err)
			}
			if recs != 1 || line != truth.AcceptedLine {
				t.Errorf("the Accepted pattern has %d records, at line %d; truth: 1, at line %d", recs, line, truth.AcceptedLine)
			}

			// The rows of the top address, in the slots of the Failed password
			// patterns: the attempts, the names it tried, those on root, and the
			// first and the last line.
			const ofTop = `FROM slot_values a JOIN slots s ON s.id = a.slot JOIN patterns p ON p.id = s.pattern
JOIN records r ON r.id = a.record
WHERE p.text LIKE ? AND s.type = 'ip4' AND a.value = ?`
			if n := one(t, db, `SELECT COUNT(*) `+ofTop, failed, truth.TopAddress); n != truth.TopAttempts {
				t.Errorf("%s has %d rows in slot_values, truth %d attempts", truth.TopAddress, n, truth.TopAttempts)
			}
			var first, last int
			if err := db.QueryRow(`SELECT MIN(r.line), MAX(r.line) `+ofTop, failed, truth.TopAddress).Scan(&first, &last); err != nil {
				t.Fatal(err)
			}
			if first != truth.TopFirstLine || last != truth.TopLastLine {
				t.Errorf("first and last line %d and %d, truth %d and %d", first, last, truth.TopFirstLine, truth.TopLastLine)
			}
			const namesOfTop = `FROM slot_values a JOIN slots s ON s.id = a.slot JOIN patterns p ON p.id = s.pattern
JOIN slots ns ON ns.pattern = s.pattern AND ns.type = 'word'
JOIN slot_values nv ON nv.slot = ns.id AND nv.record = a.record
WHERE p.text LIKE ? AND s.type = 'ip4' AND a.value = ?
AND ns.pos = (SELECT MIN(w.pos) FROM slots w WHERE w.pattern = s.pattern AND w.type = 'word')`
			if n := one(t, db, `SELECT COUNT(DISTINCT nv.value) `+namesOfTop, failed, truth.TopAddress); n != truth.TopNames {
				t.Errorf("%s tried %d names, truth %d", truth.TopAddress, n, truth.TopNames)
			}
			if n := one(t, db, `SELECT COUNT(*) `+namesOfTop+` AND nv.value = ?`, failed, truth.TopAddress, "root"); n != truth.TopOnRoot {
				t.Errorf("%s tried root %d times, truth %d", truth.TopAddress, n, truth.TopOnRoot)
			}
		})
	}
}

func TestTheRestartLogKeepsItsTruthThroughTheMiner(t *testing.T) {
	var gaveUp, looping int
	for _, seed := range []int64{1, 2, 3, 4, 5, 6} {
		data, truth := testlog.Restart(seed, 2000)
		if truth.Unit != "app-worker.service" || truth.Restarts < 100 {
			t.Fatalf("seed %d: the truth is too small to prove anything: %+v", seed, truth)
		}
		if truth.GaveUp {
			gaveUp++
		} else {
			looping++
		}
		for _, w := range ways {
			t.Run(fmt.Sprintf("seed%d/%s", seed, w.name), func(t *testing.T) {
				db, res := mineLog(t, w, data, wallNanos(2026, time.December, 31, 0, 0, 0))
				if res.Records != 2000 {
					t.Fatalf("%d records", res.Records)
				}
				const counter = "%restart counter is at <int>%"
				if n := one(t, db, `SELECT COUNT(*) FROM patterns WHERE text LIKE ?`, counter); n != 1 {
					t.Fatalf("%d patterns hold the restart counter, want 1", n)
				}
				if n := one(t, db, `SELECT COUNT(*) FROM records r JOIN patterns p ON p.id = r.pattern WHERE p.text LIKE ?`, counter); n != truth.Restarts {
					t.Errorf("the restart pattern has %d records, truth %d restarts", n, truth.Restarts)
				}
				// The counter is the last int slot of the pattern (the first is the
				// process number of systemd).
				var n, lo, hi int
				if err := db.QueryRow(`SELECT COUNT(DISTINCT CAST(v.value AS INTEGER)), MIN(CAST(v.value AS INTEGER)), MAX(CAST(v.value AS INTEGER))
FROM slot_values v JOIN slots s ON s.id = v.slot JOIN patterns p ON p.id = s.pattern
WHERE p.text LIKE ? AND s.type = 'int' AND s.pos = (SELECT MAX(i.pos) FROM slots i WHERE i.pattern = s.pattern AND i.type = 'int')`, counter).Scan(&n, &lo, &hi); err != nil {
					t.Fatal(err)
				}
				if n != truth.Restarts || lo != 1 || hi != truth.Restarts {
					t.Errorf("the counter has %d distinct values from %d to %d, truth 1 to %d", n, lo, hi, truth.Restarts)
				}
				gave := one(t, db, `SELECT COUNT(*) FROM records r JOIN patterns p ON p.id = r.pattern WHERE p.text LIKE '%Failed to start%'`)
				repeated := one(t, db, `SELECT COUNT(*) FROM records r JOIN patterns p ON p.id = r.pattern WHERE p.text LIKE '%Start request repeated too quickly%'`)
				if (gave > 0) != truth.GaveUp || (repeated > 0) != truth.GaveUp {
					t.Errorf("records of Failed to start: %d, of Start request repeated: %d; truth GaveUp = %v", gave, repeated, truth.GaveUp)
				}
				// Each event of the unit is a pattern of its own.
				for _, event := range []string{"%Started app-worker.service.%", "%Main process exited, code=exited, status=%", "%Failed with result%"} {
					if one(t, db, `SELECT COUNT(*) FROM patterns WHERE records > 0 AND text LIKE ?`, event) != 1 {
						t.Errorf("the event %q is not in exactly one pattern", event)
					}
				}
			})
		}
	}
	if gaveUp == 0 || looping == 0 {
		t.Errorf("%d logs gave up and %d were still looping: want both", gaveUp, looping)
	}
}

func TestTheWebLogKeepsItsTruthThroughTheMiner(t *testing.T) {
	data, truth := testlog.Web(11, 3000)
	if truth.DistinctPaths < 500 || truth.ClientRequests < 1000 {
		t.Fatalf("the truth is too small to prove anything: %+v", truth)
	}
	if truth.ClientPaths >= truth.ClientRequests || truth.DistinctPaths <= truth.ClientPaths {
		t.Fatalf("the truth is degenerate (requests, distinct paths of the client and of the log must differ): %+v", truth)
	}
	for _, w := range ways {
		t.Run(w.name, func(t *testing.T) {
			db, res := mineLog(t, w, data, wallNanos(2026, time.December, 31, 0, 0, 0))
			if res.Records != int64(truth.Requests) {
				t.Fatalf("%d records, truth %d requests", res.Records, truth.Requests)
			}
			if n := one(t, db, `SELECT COUNT(DISTINCT v.value) FROM slot_values v JOIN slots s ON s.id = v.slot WHERE s.type = 'word'`); n != truth.DistinctPaths {
				t.Errorf("the path slot has %d distinct values, truth %d", n, truth.DistinctPaths)
			}
			if n := one(t, db, `SELECT COUNT(*) FROM records WHERE pattern = 0`); n != 0 {
				t.Errorf("%d requests are in the catch-all", n)
			}
			var client string
			var n int
			if err := db.QueryRow(`SELECT v.value, COUNT(*) AS c FROM slot_values v JOIN slots s ON s.id = v.slot
WHERE s.type = 'ip4' GROUP BY v.value ORDER BY c DESC, v.value ASC LIMIT 1`).Scan(&client, &n); err != nil {
				t.Fatal(err)
			}
			if client != truth.Client || n != truth.ClientRequests {
				t.Errorf("the most frequent client is %s with %d requests, truth %s with %d", client, n, truth.Client, truth.ClientRequests)
			}
			if n := one(t, db, `SELECT COUNT(DISTINCT pv.value) FROM slot_values cv
JOIN slots cs ON cs.id = cv.slot AND cs.type = 'ip4'
JOIN slot_values pv ON pv.record = cv.record
JOIN slots ps ON ps.id = pv.slot AND ps.type = 'word'
WHERE cv.value = ?`, truth.Client); n != truth.ClientPaths {
				t.Errorf("%s asked for %d distinct paths, truth %d", truth.Client, n, truth.ClientPaths)
			}
		})
	}
}

func TestTheGeneratedLogsHaveTheTimeRulesOfTheirLayouts(t *testing.T) {
	t.Run("web is exact", func(t *testing.T) {
		data, _ := testlog.Web(3, 500)
		for _, w := range ways {
			// The anchor does not matter to an exact log: give a far one.
			_, res := mineLog(t, w, data, wallNanos(2001, time.January, 1, 0, 0, 0))
			src := res.Sources[0]
			if src.Source.TimeKind != store.TimeExact || src.Source.Layout != "clf" {
				t.Fatalf("%s: time kind %q, layout %q; want exact, clf", w.name, src.Source.TimeKind, src.Source.Layout)
			}
			if !src.Totals.HasTimes || src.Totals.FirstNanos != wallNanos(2026, time.November, 16, 8, 15, 22) {
				t.Errorf("%s: the first time is %v, has times %v; want 2026-11-16 08:15:22 UTC", w.name, time.Unix(0, src.Totals.FirstNanos).UTC(), src.Totals.HasTimes)
			}
			if src.Totals.LastNanos <= src.Totals.FirstNanos || src.Totals.LastNanos > wallNanos(2026, time.November, 16, 8, 15, 22)+int64(time.Hour) {
				t.Errorf("%s: the last time is %v", w.name, time.Unix(0, src.Totals.LastNanos).UTC())
			}
		}
	})
	t.Run("ssh has its year from the anchor", func(t *testing.T) {
		data, _ := testlog.SSH(3, 500)
		for _, tc := range []struct {
			name string
			as   int64
			year int
		}{
			{"the anchor is the end of the year", wallNanos(2026, time.December, 31, 0, 0, 0), 2026},
			{"the anchor is in the next year", wallNanos(2027, time.January, 15, 12, 0, 0), 2026},
			{"the anchor is before the date", wallNanos(2026, time.December, 1, 0, 0, 0), 2025},
		} {
			for _, w := range ways {
				_, res := mineLog(t, w, data, tc.as)
				src := res.Sources[0]
				if src.Source.TimeKind != store.TimeWallYearInferred || src.Source.Layout != "syslog" || src.Source.AnchorKind != store.AnchorAsOf {
					t.Fatalf("%s/%s: time kind %q, layout %q, anchor %q", tc.name, w.name, src.Source.TimeKind, src.Source.Layout, src.Source.AnchorKind)
				}
				if want := wallNanos(tc.year, time.December, 10, 7, 7, 38); !src.Totals.HasTimes || src.Totals.FirstNanos != want {
					t.Errorf("%s/%s: the first time is %v, want 7:07:38 on 10 December %d", tc.name, w.name, time.Unix(0, src.Totals.FirstNanos).UTC(), tc.year)
				}
				if src.Totals.LastNanos <= src.Totals.FirstNanos {
					t.Errorf("%s/%s: the last time %v is not after the first", tc.name, w.name, time.Unix(0, src.Totals.LastNanos).UTC())
				}
			}
		}
	})
}
