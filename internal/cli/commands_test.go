// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/buildinfo"
	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/reader/text"
	"github.com/ckir/aigolog/internal/store"
)

// harness runs a command with a private cache folder and no configuration file.
type harness struct {
	t     *testing.T
	cache string
	stdin string
	now   time.Time
	// config, when it is not empty, is the file that AIGOLOG_CONFIG names.
	config string
	// ctx, when it is not nil, is the Context of the Env.
	ctx context.Context
	// stdinReader, when it is not nil, is the standard input, in place of stdin.
	stdinReader io.Reader
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return &harness{t: t, cache: filepath.Join(t.TempDir(), "cache"), now: time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)}
}

func (h *harness) run(args ...string) (code int, out, errOut string) {
	h.t.Helper()
	var o, e bytes.Buffer
	vars := map[string]string{"AIGOLOG_CACHE_DIR": h.cache, "AIGOLOG_CONFIG": filepath.Join(h.cache, "no-config.json")}
	if h.config != "" {
		vars["AIGOLOG_CONFIG"] = h.config
	}
	var stdin io.Reader = strings.NewReader(h.stdin)
	if h.stdinReader != nil {
		stdin = h.stdinReader
	}
	code = RunEnv(Env{
		Context:   h.ctx,
		Stdin:     stdin,
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

// removeRunWas replaces the seam of the --no-keep branch for one test.
func removeRunWas(t *testing.T, f func(db *store.DB) error) {
	t.Helper()
	old := removeRun
	removeRun = f
	t.Cleanup(func() { removeRun = old })
}

func TestNoKeepSaysRemovedWhenOnlyTheLockFileStays(t *testing.T) {
	h := newHarness(t)
	removeRunWas(t, func(db *store.DB) error {
		lock := strings.TrimSuffix(db.Path(), ".db") + ".lock"
		if err := db.Remove(); err != nil {
			return err
		}
		// What Windows does when it cannot delete the lock file: the database
		// is gone and the lock file is not.
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			return err
		}
		return &store.RemoveError{DBGone: true, Err: errors.New("lock in use")}
	})
	code, out, errOut := h.run("analyze", "--no-keep", h.file("a.log", "x\n"))
	if code != exitcode.OK {
		t.Errorf("exit code %d, want 0; stderr %q", code, errOut)
	}
	if !strings.Contains(out, "database: removed at the end of the run (--no-keep)") || strings.Contains(out, "kept ") {
		t.Errorf("the summary must say the database was removed:\n%s", out)
	}
	if want := "aigolog: warning: could not remove the database: lock in use\n"; errOut != want {
		t.Errorf("standard error %q, want %q", errOut, want)
	}
	if dbCount(t, h.cache) != 0 || !hasFile(h.cache, "*.lock") {
		t.Errorf("the folder must hold the lock file and no database; databases %d", dbCount(t, h.cache))
	}
}

func TestNoKeepSaysKeptWhenTheDatabaseFileStays(t *testing.T) {
	h := newHarness(t)
	removeRunWas(t, func(db *store.DB) error { return errors.New("access denied") })
	code, out, errOut := h.run("analyze", "--no-keep", h.file("a.log", "x\n"))
	if code != exitcode.OK || !strings.Contains(errOut, "warning: could not remove the database: access denied") {
		t.Errorf("exit code %d, stderr %q", code, errOut)
	}
	if strings.Contains(out, "removed at the end of the run") || !strings.Contains(out, "database: ") || !strings.Contains(out, "kept 7 days") {
		t.Errorf("a database that is still there must be shown as kept:\n%s", out)
	}
}

func TestASweepThatRemovesAnUnfinishedDatabaseSaysSo(t *testing.T) {
	h := newHarness(t)
	var body strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&body, "2026-01-05 10:00:01 line %d %s\n", i, strings.Repeat("x", 60))
	}
	p := h.file("a.log", body.String())
	code, out, errOut := h.run("analyze", p)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("first run: code %d, stderr %q", code, errOut)
	}
	first := regexp.MustCompile(`(?m)^database: (.+\.db) \(`).FindStringSubmatch(out)[1]
	// An unfinished run: the database is whole, and its complete mark is not set.
	db, err := sql.Open(store.DriverName, first)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE meta SET value = '0' WHERE key = 'complete'`)
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	code, _, errOut = h.run("analyze", p)
	if code != exitcode.OK {
		t.Fatalf("second run: code %d, stderr %q", code, errOut)
	}
	if want := "aigolog: note: removed 1 databases that were not finished\n"; !strings.Contains(errOut, want) {
		t.Errorf("standard error %q does not hold %q", errOut, want)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) || dbCount(t, h.cache) != 1 {
		t.Errorf("the unfinished database: %v; databases %d; want it gone and the new one left", err, dbCount(t, h.cache))
	}
}

func TestASweepThatRemovesOnlyAFinishedDatabaseSaysNothing(t *testing.T) {
	h := newHarness(t)
	h.now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	p := h.file("a.log", "x\n")
	if code, _, errOut := h.run("analyze", p); code != exitcode.OK {
		t.Fatalf("first run: code %d, stderr %q", code, errOut)
	}
	h.now = h.now.Add(8 * 24 * time.Hour)
	code, _, errOut := h.run("analyze", p)
	if code != exitcode.OK || errOut != "" {
		t.Errorf("second run: code %d, stderr %q; an old finished database goes without a note", code, errOut)
	}
	if dbCount(t, h.cache) != 1 {
		t.Errorf("databases %d, want the old one gone", dbCount(t, h.cache))
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

func TestAnEmptyAsOfIsRefused(t *testing.T) {
	h := newHarness(t)
	p := h.file("a.log", "Dec 31 23:00:00 h a\n")
	for _, args := range [][]string{
		{"analyze", "--as-of", "", p},
		{"analyze", p, "--as-of="},
		{"analyze", "--as-of", "2026-01-05", "--as-of", "", p},
	} {
		code, out, errOut := h.run(args...)
		if code != exitcode.Usage || out != "" || !strings.Contains(errOut, "as-of") || dbCount(t, h.cache) != 0 {
			t.Errorf("%q: code %d, stdout %q, stderr %q, databases %d", args, code, out, errOut, dbCount(t, h.cache))
		}
	}
	if _, err := os.Stat(h.cache); !os.IsNotExist(err) {
		t.Errorf("the cache folder was made: %v", err)
	}
	// the last occurrence is the one that counts
	code, _, errOut := h.run("analyze", "--as-of", "", "--as-of", "2026-01-05", p)
	if code != exitcode.OK {
		t.Errorf("a later valid --as-of must win: code %d, stderr %q", code, errOut)
	}
}

func TestHelpFlagOfAnalyzePrintsUsageAndSucceeds(t *testing.T) {
	h := newHarness(t)
	for _, flagName := range []string{"-h", "--help"} {
		code, out, errOut := h.run("analyze", flagName)
		if code != exitcode.OK || out != "" || !strings.Contains(errOut, "Usage: aigolog analyze") || !strings.Contains(errOut, "as-of") {
			t.Errorf("%s: code %d, stdout %q, stderr %q", flagName, code, out, errOut)
		}
		if !strings.Contains(errOut, "YYYY-MM-DD") || strings.Contains(errOut, "2006") {
			t.Errorf("%s: the help of --as-of must show the forms in words, not Go's reference date: %q", flagName, errOut)
		}
	}
	if dbCount(t, h.cache) != 0 {
		t.Error("a database was made")
	}
}

func TestListAndCleanRefuseABadConfiguration(t *testing.T) {
	h := newHarness(t)
	h.config = h.file("config.json", `{"keep_dayz": 3}`)
	for _, args := range [][]string{{"list"}, {"clean"}, {"analyze", "x"}} {
		code, out, errOut := h.run(args...)
		if code != exitcode.Usage || out != "" || !strings.Contains(errOut, "keep_dayz") {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	if _, err := os.Stat(h.cache); !os.IsNotExist(err) {
		t.Errorf("the cache folder was made: %v", err)
	}
}

func TestListShowsALiveRunAsRunning(t *testing.T) {
	h := newHarness(t)
	db, err := store.Create(context.Background(), store.Options{Dir: h.cache, Now: func() time.Time { return h.now }})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	code, out, _ := h.run("list")
	fields := strings.Split(strings.TrimRight(out, "\n"), "\t")
	if code != exitcode.OK || strings.Count(out, "\n") != 1 || len(fields) != 6 ||
		fields[0] != db.Path() || fields[3] != "running" || fields[4] != "-" || fields[5] != "-" {
		t.Errorf("list: code %d, output %q (fields %q)", code, out, fields)
	}
	code, out, _ = h.run("clean")
	if code != exitcode.OK || out != "removed 0 databases, 0 bytes; running 1\n" {
		t.Errorf("clean: code %d, output %q", code, out)
	}
	if _, err := os.Stat(db.Path()); err != nil {
		t.Errorf("the live database is gone: %v", err)
	}
}

// cancelOnSecondRead is a standard input of one line whose next read cancels
// the context, as a signal that arrives while the input is being read does.
type cancelOnSecondRead struct {
	cancel context.CancelFunc
	reads  int
}

func (c *cancelOnSecondRead) Read(p []byte) (int, error) {
	c.reads++
	if c.reads == 1 {
		return copy(p, "x\n"), nil
	}
	c.cancel()
	return 0, io.EOF
}

func noFilesIn(t *testing.T, dir string) {
	t.Helper()
	for _, pattern := range []string{"*.db", "*.lock", "*-journal"} {
		if m, _ := filepath.Glob(filepath.Join(dir, pattern)); len(m) != 0 {
			t.Errorf("%s left in %s: %v", pattern, dir, m)
		}
	}
}

func TestAnInterruptedAnalysisRemovesItsDatabaseWhateverTheFlags(t *testing.T) {
	for _, args := range [][]string{{"analyze", "-"}, {"analyze", "--no-keep", "-"}} {
		h := newHarness(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h.ctx = ctx
		h.stdinReader = &cancelOnSecondRead{cancel: cancel}
		code, out, errOut := h.run(args...)
		if code != exitcode.Failure || out != "" || !strings.Contains(errOut, "interrupted") {
			t.Errorf("%v: code %d, stdout %q, stderr %q", args, code, out, errOut)
		}
		noFilesIn(t, h.cache)
	}

	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.ctx = ctx
	code, out, errOut := h.run("analyze", h.file("a.log", "x\n"))
	if code != exitcode.Failure || out != "" || !strings.Contains(errOut, "interrupted") {
		t.Errorf("already cancelled: code %d, stdout %q, stderr %q", code, out, errOut)
	}
	if _, err := os.Stat(h.cache); !os.IsNotExist(err) {
		t.Errorf("the cache folder was made: %v", err)
	}
}

func TestADatabaseOverTheSizeLimitIsWarnedAboutAndRemovedAtTheNextRun(t *testing.T) {
	h := newHarness(t)
	h.config = h.file("config.json", `{"max_total_bytes": 1}`)
	p := h.file("a.log", "2026-01-05 10:00:01 a\n")
	code, out, errOut := h.run("analyze", p)
	if code != exitcode.OK || !strings.Contains(errOut, "over the limit of 1 bytes") || !strings.Contains(errOut, "max_total_bytes") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	m := regexp.MustCompile(`(?m)^database: (.+\.db) \(`).FindStringSubmatch(out)
	if m == nil || !strings.Contains(out, "kept 7 days") {
		t.Fatalf("the database line is not as before:\n%s", out)
	}
	first := m[1]
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("the first database is not there: %v", err)
	}
	if code, _, errOut = h.run("analyze", p); code != exitcode.OK {
		t.Fatalf("second run: code %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) || dbCount(t, h.cache) != 1 {
		t.Errorf("the first database: %v; databases %d; want it gone and one left", err, dbCount(t, h.cache))
	}
}

func TestNoWarningWithTheDefaultLimitOrWithNoKeep(t *testing.T) {
	h := newHarness(t)
	if code, _, errOut := h.run("analyze", h.file("a.log", "x\n")); code != exitcode.OK || errOut != "" {
		t.Errorf("default limit: code %d, stderr %q", code, errOut)
	}
	h = newHarness(t)
	h.config = h.file("config.json", `{"max_total_bytes": 1}`)
	if code, _, errOut := h.run("analyze", "--no-keep", h.file("a.log", "x\n")); code != exitcode.OK || errOut != "" {
		t.Errorf("--no-keep: code %d, stderr %q", code, errOut)
	}
}

func TestAnAnalysisRemovesDatabasesOlderThanKeepDays(t *testing.T) {
	for _, tc := range []struct {
		later time.Duration
		want  int
	}{{8 * 24 * time.Hour, 1}, {6 * 24 * time.Hour, 2}} {
		h := newHarness(t)
		h.now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		p := h.file("a.log", "x\n")
		code, out, errOut := h.run("analyze", p)
		if code != exitcode.OK {
			t.Fatalf("first run: code %d, stderr %q", code, errOut)
		}
		first := regexp.MustCompile(`(?m)^database: (.+\.db) \(`).FindStringSubmatch(out)[1]
		h.now = h.now.Add(tc.later)
		if code, _, errOut = h.run("analyze", p); code != exitcode.OK {
			t.Fatalf("second run: code %d, stderr %q", code, errOut)
		}
		if got := dbCount(t, h.cache); got != tc.want {
			t.Errorf("%v later: %d databases, want %d", tc.later, got, tc.want)
		}
		_, err := os.Stat(first)
		if gone := os.IsNotExist(err); gone != (tc.want == 1) {
			t.Errorf("%v later: first database gone %v", tc.later, gone)
		}
	}
}

func TestAFileThatCannotBeReadIsNamedOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the wording of the system errors differs on Windows")
	}
	h := newHarness(t)
	missing := filepath.Join(t.TempDir(), "absent.log")
	code, _, errOut := h.run("analyze", missing)
	if want := "aigolog: cannot read " + missing + ": no such file or directory\n"; code != exitcode.Failure || errOut != want {
		t.Errorf("missing: code %d, stderr %q, want %q", code, errOut, want)
	}
	dir := t.TempDir()
	code, _, errOut = h.run("analyze", dir)
	if want := "aigolog: cannot read " + dir + ": is a directory\n"; code != exitcode.Failure || errOut != want {
		t.Errorf("directory: code %d, stderr %q, want %q", code, errOut, want)
	}
}

func TestTheSummaryNotesRecordsCutAtOneMiBAndOnlyThen(t *testing.T) {
	line := "  " + strings.Repeat("z", 97) + "\n" // 100 bytes
	body := "2026-01-05 10:00:01 start\n" + strings.Repeat(line, 1536<<10/len(line))

	// The reader is the oracle for how many records were cut.
	rd, err := text.New(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := rd.Next(); err != nil {
			break
		}
	}
	want := rd.Cut()
	if want < 1 {
		t.Fatalf("the reader cut %d records of 1.5 MiB", want)
	}

	h := newHarness(t)
	code, out, errOut := h.run("analyze", h.file("big.log", body))
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	note := fmt.Sprintf("  note: joined text was cut at 1 MiB %d times; each cut started a new record", want)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	at := -1
	for i, l := range lines {
		if l == note {
			at = i
		}
	}
	// The note ends the block of its source: the database line follows.
	if at < 0 || !strings.HasPrefix(lines[at-1], "  time: ") || !strings.HasPrefix(lines[at+1], "database: ") {
		t.Errorf("the note %q is not in place in:\n%s", note, out)
	}

	h = newHarness(t)
	_, out, _ = h.run("analyze", h.file("small.log", "2026-01-05 10:00:01 a\n  b\n"))
	if strings.Contains(out, "note:") {
		t.Errorf("a normal file has a note:\n%s", out)
	}
}

func TestOnlyTheSignalsThatAreNotIgnoredAreCaught(t *testing.T) {
	// ignoring says that the given signals are ignored and no other.
	ignoring := func(ignored ...os.Signal) func(os.Signal) bool {
		return func(s os.Signal) bool { return slices.Contains(ignored, s) }
	}
	// The function has no branch for Windows: the same two signals everywhere.
	for _, tc := range []struct {
		name    string
		ignored func(os.Signal) bool
		want    []os.Signal
	}{
		{"none ignored", ignoring(), []os.Signal{os.Interrupt, syscall.SIGTERM}},
		{"interrupt ignored", ignoring(os.Interrupt), []os.Signal{syscall.SIGTERM}},
		{"terminate ignored", ignoring(syscall.SIGTERM), []os.Signal{os.Interrupt}},
		{"both ignored", ignoring(os.Interrupt, syscall.SIGTERM), []os.Signal{}},
	} {
		got := signalsToCatch(tc.ignored)
		if len(got) != len(tc.want) || !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	// The real predicate is what interruptContext passes; that it is honoured
	// by a real process is tested in process_test.go
	// (TestSIGINTIgnoredAtStartIsStillIgnoredAndSIGTERMStops).
}

func TestAnalyzeWarnsOnceAboutKeptDatabasesItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not deny access on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can open a file whatever its mode")
	}
	h := newHarness(t)
	// One database of layout 2 is 28672 bytes (seven pages); the limit is room
	// for one and not for two, as it was for layout 1 (20000 held one).
	h.config = h.file("config.json", `{"max_total_bytes": 40000}`)
	p := h.file("a.log", "2026-01-05 10:00:01 a\n")
	code, out, errOut := h.run("analyze", p)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("first run: code %d, stderr %q", code, errOut)
	}
	first := regexp.MustCompile(`(?m)^database: (.+\.db) \(`).FindStringSubmatch(out)[1]
	if err := os.Chmod(first, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(first, 0o600) })
	code, _, errOut = h.run("analyze", p)
	const want = `aigolog: warning: 1 kept databases could not be read and were left alone; "aigolog list" shows them and "aigolog clean" removes them`
	if code != exitcode.OK || strings.Count(errOut, want) != 1 {
		t.Errorf("second run: code %d, stderr %q; want the warning once", code, errOut)
	}
	if _, err := os.Stat(first); err != nil {
		t.Errorf("the unreadable database was removed: %v", err)
	}
}

// skipUnlessAFolderCanBeReadOnly skips the test where chmod cannot make a
// folder that nothing can be removed from or made in: on Windows, and for root.
func skipUnlessAFolderCanBeReadOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a folder is made read-only with chmod on Unix only")
	}
	if os.Geteuid() == 0 {
		t.Skip("root is not stopped by the permissions of a folder")
	}
}

// readOnlyCache makes the cache folder of h one that nothing can be removed
// from or made in, and gives it back its permissions when the test ends.
func readOnlyCache(t *testing.T, h *harness) {
	t.Helper()
	if err := os.Chmod(h.cache, 0o500); err != nil {
		t.Fatal(err)
	}
	// This runs before the removal of the temporary folder, which needs it.
	t.Cleanup(func() { os.Chmod(h.cache, 0o700) })
}

func TestCleanWithErrorsStillPrintsItsLineAndExitsWithOne(t *testing.T) {
	skipUnlessAFolderCanBeReadOnly(t)
	h := newHarness(t)
	if code, _, errOut := h.run("analyze", h.file("a.log", "x\n")); code != exitcode.OK {
		t.Fatalf("analyze: code %d, stderr %q", code, errOut)
	}
	readOnlyCache(t, h)
	code, out, errOut := h.run("clean")
	if code != exitcode.Failure {
		t.Errorf("code = %d, want %d (Failure): a database could not be removed", code, exitcode.Failure)
	}
	if want := "removed 0 databases, 0 bytes; running 0\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if !strings.Contains(errOut, "aigolog: ") || !strings.Contains(errOut, "permission denied") {
		t.Errorf("stderr = %q, want an aigolog: line that says permission denied", errOut)
	}
	if got := dbCount(t, h.cache); got != 1 {
		t.Errorf("databases left = %d, want the 1 that could not be removed", got)
	}
}

func TestSweepErrorsDuringAnalyzeArePrintedAsWarnings(t *testing.T) {
	skipUnlessAFolderCanBeReadOnly(t)
	h := newHarness(t)
	present := h.now
	h.now = present.Add(-8 * 24 * time.Hour) // older than the 7 days that are kept
	p := h.file("a.log", "x\n")
	if code, _, errOut := h.run("analyze", p); code != exitcode.OK {
		t.Fatalf("first analyze: code %d, stderr %q", code, errOut)
	}
	h.now = present
	readOnlyCache(t, h)
	code, out, errOut := h.run("analyze", p)
	warning := ""
	for _, line := range strings.Split(errOut, "\n") {
		if strings.HasPrefix(line, "aigolog: warning: ") {
			warning = line
		}
	}
	if !strings.Contains(warning, "removing the database ") || !strings.Contains(warning, "permission denied") {
		t.Errorf("stderr = %q, want an %q line that says permission denied for the database that could not be removed", errOut, "aigolog: warning: removing the database ...")
	}
	// The folder takes no new file, so the run fails where it makes its database.
	if code != exitcode.StorageUnavailable {
		t.Errorf("code = %d, want %d (StorageUnavailable); stdout %q, stderr %q", code, exitcode.StorageUnavailable, out, errOut)
	}
	if got := dbCount(t, h.cache); got != 1 {
		t.Errorf("databases = %d, want only the old one that could not be removed", got)
	}
}

func TestListShowsOtherForADatabaseOfAnotherLayout(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(h.cache, 0o700); err != nil {
		t.Fatal(err)
	}
	// A database as B1 made it: a meta table with schema 1 and no header.
	path := filepath.Join(h.cache, store.DatabaseName(h.now.Add(-time.Hour), "0badf00d"))
	db, err := sql.Open(store.DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID`,
		`INSERT INTO meta VALUES ('version', 'b1')`,
		`INSERT INTO meta VALUES ('schema', '1')`,
		`INSERT INTO meta VALUES ('complete', '1')`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	code, out, _ := h.run("list")
	fields := strings.Split(strings.TrimRight(out, "\n"), "\t")
	if code != exitcode.OK || !strings.Contains(out, "\tother\t") || len(fields) != 6 || fields[3] != "other" || fields[5] != "b1" {
		t.Errorf("list: code %d, output %q (fields %q)", code, out, fields)
	}
	if code, _, _ := h.run("clean"); code != exitcode.OK {
		t.Fatalf("clean: code %d", code)
	}
	if dbCount(t, h.cache) != 0 {
		t.Error("clean left the database of another layout")
	}
}
