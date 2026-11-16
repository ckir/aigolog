// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/exitcode"
)

func TestA2TwoFilesFromDecemberIntoJanuaryKeepTheirOrder(t *testing.T) {
	h := newHarness(t)
	december := h.file("syslog.1", "Dec 28 23:00:00 h a\nDec 31 23:59:59 h b\n")
	january := h.file("syslog", "Jan  1 00:00:01 h c\nJan 14 09:00:00 h d\n")
	code, out, errOut := h.run("analyze", "--as-of", "2026-01-15", december, january)
	if code != exitcode.OK {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	for _, want := range []string{
		"  time: 2025-12-28 23:00:00 to 2025-12-31 23:59:59; as written, no zone stated",
		"  time: 2026-01-01 00:00:01 to 2026-01-14 09:00:00; as written, no zone stated",
		"  years: worked out from the anchor 2026-01-15 23:59:59, --as-of",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestA2LinesAcrossMidnightOnNewYearsEveALittleOutOfOrderKeepTheirYears(t *testing.T) {
	h := newHarness(t)
	f := h.file("nye.log", "Dec 31 23:59:58 h a\nJan  1 00:00:01 h b\nDec 31 23:59:59 h c\nJan  1 00:00:02 h d\n")
	code, out, _ := h.run("analyze", "--as-of", "2026-01-05", f)
	if code != exitcode.OK || !strings.Contains(out, "  time: 2025-12-31 23:59:58 to 2026-01-01 00:00:02; as written, no zone stated") {
		t.Errorf("code %d, output:\n%s", code, out)
	}
}

func TestA2AsOfTheDayOfTheLastRecordKeepsItInThatYearAndTheReportStatesTheAnchor(t *testing.T) {
	h := newHarness(t)
	f := h.file("last.log", "Dec 31 22:00:00 h a\nDec 31 23:00:00 h b\n")
	code, out, _ := h.run("analyze", "--as-of", "2025-12-31", f)
	if code != exitcode.OK ||
		!strings.Contains(out, "  time: 2025-12-31 22:00:00 to 2025-12-31 23:00:00; as written, no zone stated") ||
		!strings.Contains(out, "  years: worked out from the anchor 2025-12-31 23:59:59, --as-of") {
		t.Errorf("code %d, output:\n%s", code, out)
	}
}

func TestA2TwentyNinthOfFebruaryWithAnAnchorInAnOrdinaryYearIsPlacedInTheLeapYearBefore(t *testing.T) {
	h := newHarness(t)
	f := h.file("leap.log", "Feb 29 10:00:00 h a\nMar  1 01:00:00 h b\n")
	code, out, _ := h.run("analyze", "--as-of", "2026-03-05", f)
	if code != exitcode.OK || !strings.Contains(out, "  time: 2024-02-29 10:00:00 to 2026-03-01 01:00:00; as written, no zone stated") {
		t.Errorf("code %d, output:\n%s", code, out)
	}
}

func TestA2ALogWithNoTimeStampsIsAnalysedWithoutTimeAndTheReportSaysSo(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("analyze", h.file("plain.log", "one\ntwo\nthree\n"))
	if code != exitcode.OK || !strings.Contains(out, "layout none") ||
		!strings.Contains(out, "  time: none found; everything that depends on time is left out") ||
		strings.Contains(out, "years:") {
		t.Errorf("code %d, output:\n%s", code, out)
	}
}

func TestA2TwoAnalysesAtTheSameTimeFinishWithCorrectSeparateDatabases(t *testing.T) {
	h := newHarness(t)
	var big strings.Builder
	for i := 0; i < 20000; i++ {
		big.WriteString("2026-01-05 10:00:01 a line that is long enough to take a moment " + strings.Repeat("z", 40) + "\n")
	}
	a := h.file("a.log", big.String())
	b := h.file("b.log", big.String()+"one more\n")
	var wg sync.WaitGroup
	results := make([]struct {
		code int
		out  string
	}, 2)
	for i, f := range []string{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].code, results[i].out, _ = h.run("analyze", f)
		}()
	}
	wg.Wait()
	if results[0].code != exitcode.OK || results[1].code != exitcode.OK {
		t.Fatalf("codes %d and %d", results[0].code, results[1].code)
	}
	if !strings.Contains(results[0].out, "records 20000, lines 20000") || !strings.Contains(results[1].out, "records 20000, lines 20001") {
		t.Errorf("each run must report its own counts:\n%s\n%s", results[0].out, results[1].out)
	}
	if n := dbCount(t, h.cache); n != 2 {
		t.Fatalf("databases kept: %d, want 2", n)
	}
	_, list, _ := h.run("list")
	if strings.Count(list, "\tcomplete\t") != 2 || strings.Count(list, "\t20000\t") != 2 {
		t.Errorf("list:\n%s", list)
	}
}

func TestA2ACleanerThatRunsWhileAnAnalysisWorksRemovesNothingThatIsLive(t *testing.T) {
	h := newHarness(t)
	var big strings.Builder
	for i := 0; i < 60000; i++ {
		big.WriteString("plain line " + strings.Repeat("q", 80) + "\n")
	}
	f := h.file("big.log", big.String())
	done := make(chan struct{})
	var code int
	var out string
	go func() {
		code, out, _ = h.run("analyze", f)
		close(done)
	}()
	// wait until the run has made its lock file, then clean until it ends
	for {
		if m, _ := filepath.Glob(filepath.Join(h.cache, "*.lock")); len(m) > 0 {
			break
		}
		select {
		case <-done:
			t.Fatal("the analysis ended before the test could clean beside it")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	metLive := false
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			if _, cleaned, _ := h.run("clean"); strings.HasSuffix(strings.TrimSpace(cleaned), "running 1") {
				metLive = true
			}
		}
	}
	if code != exitcode.OK || !strings.Contains(out, "records 60000") {
		t.Errorf("the analysis was disturbed by a cleaner: code %d, output:\n%s", code, out)
	}
	if !metLive {
		t.Error("no cleaner ever met the live run, so this test proved nothing")
	}
}
