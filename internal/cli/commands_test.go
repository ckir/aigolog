// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/buildinfo"
	"github.com/ckir/aigolog/internal/exitcode"
)

// harness runs a command with a private cache folder and no configuration file.
type harness struct {
	t     *testing.T
	cache string
	stdin string
	now   time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{t: t, cache: filepath.Join(t.TempDir(), "cache"), now: time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)}
}

func (h *harness) run(args ...string) (code int, out, errOut string) {
	h.t.Helper()
	var o, e bytes.Buffer
	vars := map[string]string{"AIGOLOG_CACHE_DIR": h.cache, "AIGOLOG_CONFIG": filepath.Join(h.cache, "no-config.json")}
	code = RunEnv(Env{
		Stdin:     strings.NewReader(h.stdin),
		Stdout:    &o,
		Stderr:    &e,
		LookupEnv: func(k string) (string, bool) { v, ok := vars[k]; return v, ok },
		Now:       func() time.Time { return h.now },
		Zone:      time.UTC,
	}, args)
	return code, o.String(), e.String()
}

func (h *harness) file(name, body string) string {
	h.t.Helper()
	p := filepath.Join(h.t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		h.t.Fatal(err)
	}
	return p
}

func dbCount(t *testing.T, dir string) int {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "*.db"))
	return len(m)
}

func TestAnalyzeReadsATextFileAndPrintsTheSummary(t *testing.T) {
	h := newHarness(t)
	p := h.file("auth.log", "2026-01-05 10:00:01 a\n2026-01-05 10:00:09 b\n  continued\n")
	code, out, errOut := h.run("analyze", p)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	want := []string{
		"aigolog " + buildinfo.Version + ": sources 1, records 2, lines 3",
		"source 1: " + p + " (utf-8, layout iso-space)",
		"  records 2, lines 3, bytes 56",
		"  time: 2026-01-05 10:00:01 to 2026-01-05 10:00:09; as written, no zone stated",
	}
	for i, w := range want {
		if i >= len(lines) || lines[i] != w {
			t.Errorf("line %d = %q, want %q\nfull output:\n%s", i+1, lines[min(i, len(lines)-1)], w, out)
		}
	}
	last := lines[len(lines)-1]
	if !regexp.MustCompile(`^database: .+\.db \([0-9]+ bytes\), kept 7 days$`).MatchString(last) {
		t.Errorf("last line = %q", last)
	}
	if dbCount(t, h.cache) != 1 {
		t.Errorf("databases kept: %d, want 1", dbCount(t, h.cache))
	}
}

func TestAnalyzeOneFileAndStandardInputMakeOneDatabase(t *testing.T) {
	h := newHarness(t)
	h.stdin = "Jan  5 10:00:01 h a\n"
	a := h.file("a.log", "plain\nlines\n")
	code, out, _ := h.run("analyze", a, "-")
	if code != exitcode.OK || !strings.Contains(out, "sources 2, records 3, lines 3") || !strings.Contains(out, "source 2: standard input (utf-8, layout syslog)") {
		t.Errorf("code %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "  time: none found; everything that depends on time is left out") {
		t.Errorf("a source with no stamps must say so:\n%s", out)
	}
	if !strings.Contains(out, "  years: worked out from the anchor 2026-02-01 12:00:00, the current time") {
		t.Errorf("standard input must state its anchor:\n%s", out)
	}
	if dbCount(t, h.cache) != 1 {
		t.Errorf("databases kept: %d", dbCount(t, h.cache))
	}
}

func TestNoKeepRemovesTheDatabaseAndSaysSo(t *testing.T) {
	h := newHarness(t)
	code, out, _ := h.run("analyze", "--no-keep", h.file("a.log", "x\n"))
	if code != exitcode.OK || !strings.Contains(out, "database: removed at the end of the run (--no-keep)") {
		t.Errorf("code %d, output:\n%s", code, out)
	}
	if dbCount(t, h.cache) != 0 {
		t.Error("the database was kept")
	}
	if m, _ := filepath.Glob(filepath.Join(h.cache, "*.lock")); len(m) != 0 {
		t.Errorf("lock files left: %v", m)
	}
}

func TestFlagsMayComeAfterTheFiles(t *testing.T) {
	h := newHarness(t)
	p := h.file("a.log", "Dec 31 23:00:00 h a\n")
	code, out, errOut := h.run("analyze", p, "--as-of", "2026-01-05", "--no-keep")
	if code != exitcode.OK || !strings.Contains(out, "database: removed at the end of the run (--no-keep)") ||
		!strings.Contains(out, "  time: 2025-12-31 23:00:00 to 2025-12-31 23:00:00;") {
		t.Errorf("code %d, stderr %q, output:\n%s", code, errOut, out)
	}
	if dbCount(t, h.cache) != 0 {
		t.Error("--no-keep after the file was ignored")
	}
}

func TestUsageErrorsExitWithTwo(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"analyze"},
		{"analyze", "--bogus", "x"},
		{"analyze", "--format", "xml", "x"},
		{"analyze", "--as-of", "yesterday", "x"},
		{"list", "extra"},
		{"clean", "extra"},
	} {
		code, out, errOut := h.run(args...)
		if code != exitcode.Usage || out != "" || errOut == "" {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

func TestOtherReadersAreNotBuiltYet(t *testing.T) {
	h := newHarness(t)
	for _, f := range []string{"jsonl", "eventxml"} {
		code, _, errOut := h.run("analyze", "--format", f, h.file("a", "x\n"))
		if code != exitcode.Usage || !strings.Contains(errOut, `the format "`+f+`" is not built yet`) {
			t.Errorf("%s: code %d, stderr %q", f, code, errOut)
		}
	}
}

func TestAMissingFileFailsWithOneAndLeavesNoDatabase(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run("analyze", filepath.Join(t.TempDir(), "absent.log"))
	if code != exitcode.Failure || !strings.Contains(errOut, "cannot read") || dbCount(t, h.cache) != 0 {
		t.Errorf("code %d, stderr %q, databases %d", code, errOut, dbCount(t, h.cache))
	}
}

func TestInputWithANulByteIsRefusedAndLeavesNoDatabase(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run("analyze", h.file("bin.dat", "ab\x00cd\n"))
	if code != exitcode.Failure || !strings.Contains(errOut, "not text") || !strings.Contains(errOut, "byte offset 2") || dbCount(t, h.cache) != 0 {
		t.Errorf("code %d, stderr %q, databases %d", code, errOut, dbCount(t, h.cache))
	}
}

func TestEmptyInputSaysSoAndStoresNothing(t *testing.T) {
	h := newHarness(t)
	code, _, errOut := h.run("analyze", h.file("empty.log", ""))
	if code != exitcode.Failure || !strings.Contains(errOut, "nothing to analyse") || dbCount(t, h.cache) != 0 {
		t.Errorf("code %d, stderr %q, databases %d", code, errOut, dbCount(t, h.cache))
	}
}

func TestAnUnwritableCacheFolderExitsWithFive(t *testing.T) {
	h := newHarness(t)
	blocker := h.file("blocker", "i am a file, not a folder")
	h.cache = filepath.Join(blocker, "cache") // a folder cannot be made under a file
	code, _, errOut := h.run("analyze", h.file("a.log", "x\n"))
	if code != exitcode.StorageUnavailable || !strings.Contains(errOut, "cannot write the database") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
}

func TestNamesWithEscapeCharactersNeverReachTheTerminalRaw(t *testing.T) {
	h := newHarness(t)
	if runtime.GOOS == "windows" {
		t.Skip("Windows file names cannot hold these characters")
	}
	p := h.file("evil\x1b[31m\x07name.log", "x\n")
	_, out, errOut := h.run("analyze", p)
	_, listOut, _ := h.run("list")
	_, _, missing := h.run("analyze", p+"\x1b[2J.absent")
	for name, s := range map[string]string{"analyze": out, "list": listOut, "error": missing + errOut} {
		if strings.ContainsAny(s, "\x1b\x07") {
			t.Errorf("%s output holds a raw control character: %q", name, s)
		}
	}
	if !strings.Contains(out, `evil\x1b[31m\x07name.log`) {
		t.Errorf("the escaped name is missing:\n%s", out)
	}
}

func TestListShowsTheKeptDatabaseAndCleanRemovesIt(t *testing.T) {
	h := newHarness(t)
	if code, _, _ := h.run("list"); code != exitcode.OK {
		t.Fatalf("list of nothing: code %d", code)
	}
	if _, out, _ := h.run("list"); !strings.HasPrefix(out, "no databases are kept in ") {
		t.Errorf("empty list = %q", out)
	}
	h.run("analyze", h.file("a.log", "x\ny\n"))
	_, out, _ := h.run("list")
	fields := strings.Split(strings.TrimRight(out, "\n"), "\t")
	if len(fields) != 6 || !strings.HasSuffix(fields[0], ".db") || fields[3] != "complete" || fields[4] != "2" || fields[5] != buildinfo.Version {
		t.Errorf("list line = %q (fields %q)", out, fields)
	}
	code, out, _ := h.run("clean")
	if code != exitcode.OK || !regexp.MustCompile(`^removed 1 databases, [0-9]+ bytes; running 0\n$`).MatchString(out) {
		t.Errorf("clean: code %d, output %q", code, out)
	}
	if dbCount(t, h.cache) != 0 {
		t.Error("clean left a database")
	}
}

func TestAnAnalysisSweepsAnInterruptedRunOfEarlier(t *testing.T) {
	h := newHarness(t)
	h.run("analyze", h.file("a.log", "x\n"))
	// an interrupted run: a database that was never marked complete and that nobody holds
	dead := filepath.Join(h.cache, "20260101T000000Z-deadbeef.db")
	os.WriteFile(dead, []byte("not even a database"), 0o600)
	if code, _, errOut := h.run("analyze", h.file("b.log", "y\n")); code != exitcode.OK {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Errorf("the interrupted run's file was not removed: %v", err)
	}
	if dbCount(t, h.cache) != 2 {
		t.Errorf("databases kept: %d, want 2", dbCount(t, h.cache))
	}
}

func TestTheOldCommandsStillWork(t *testing.T) {
	var o, e bytes.Buffer
	if code := Run([]string{"help"}, &o, &e); code != exitcode.OK || !strings.Contains(o.String(), "  analyze   read logs into a database") {
		t.Errorf("help: code %d, output %q", code, o.String())
	}
}

func TestDoubleDashEndsTheFlags(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run("analyze", "--", "--no-keep-looking-name")
	if code != exitcode.Failure || out != "" || !strings.Contains(errOut, "cannot read --no-keep-looking-name") || dbCount(t, h.cache) != 0 {
		t.Errorf("code %d, stdout %q, stderr %q, databases %d", code, out, errOut, dbCount(t, h.cache))
	}
}

func TestStandardInputMayBeNamedOnlyOnce(t *testing.T) {
	h := newHarness(t)
	h.stdin = "x\n"
	code, out, errOut := h.run("analyze", "-", "-")
	if code != exitcode.Usage || out != "" || !strings.Contains(errOut, `standard input ("-") may be named only once`) {
		t.Errorf("code %d, stdout %q, stderr %q", code, out, errOut)
	}
	if _, err := os.Stat(h.cache); !os.IsNotExist(err) {
		t.Errorf("the cache folder was made before the usage error: %v", err)
	}
}
