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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/buildinfo"
	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/reader/text"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/summary"
	"github.com/ckir/aigolog/internal/testlog"
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
	if want := "aigolog: note: removed 1 database that was not finished\n"; !strings.Contains(errOut, want) {
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
	if code != exitcode.OK || !regexp.MustCompile(`^removed 1 database, [0-9]+ bytes; running 0\n$`).MatchString(out) {
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
	// The note ends the block of its source; the digest, then the summary,
	// follow it, and the database line is the last line of the report.
	if at < 0 || !strings.HasPrefix(lines[at-1], "  time: ") {
		t.Fatalf("the note %q is not in place in:\n%s", note, out)
	}
	heading := slices.Index(lines, "patterns")
	if heading <= at || !strings.HasPrefix(lines[len(lines)-1], "database: ") || heading >= len(lines)-1 {
		t.Errorf("the note, then the heading patterns, then the database line last, are not in place in:\n%s", out)
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
	p := h.file("a.log", "2026-01-05 10:00:01 a\n")
	// The limit is room for one database and not for two. The size of one is
	// measured, from a database that the same command makes in a cache of its
	// own, because it changes with the layout and with what analyze stores
	// (mining adds a page or so for each index); then the limit is one and a
	// half of it.
	_, probeOut, probeErr := newHarness(t).run("analyze", p)
	m := regexp.MustCompile(`(?m)^database: .+\.db \(([0-9]+) bytes\)`).FindStringSubmatch(probeOut)
	if m == nil {
		t.Fatalf("the probe run printed no database line; stderr %q, stdout:\n%s", probeErr, probeOut)
	}
	one, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || one <= 0 {
		t.Fatalf("the size of one database is %q", m[1])
	}
	h.config = h.file("config.json", fmt.Sprintf(`{"max_total_bytes": %d}`, one*3/2))
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

// reportLog is a log of n lines of one shape, with a time stamp each.
func reportLog(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "2026-01-05 10:00:%02d svc login ok for item %d\n", i%60, i)
	}
	return b.String()
}

// buildSummaryWas replaces the seam that builds the summary for one test.
func buildSummaryWas(t *testing.T, f func(context.Context, *store.DB, summary.Options) (summary.Summary, summary.Digest, error)) {
	t.Helper()
	old := buildSummary
	buildSummary = f
	t.Cleanup(func() { buildSummary = old })
}

func TestAnalyzePrintsTheDigestAfterTheSourcesAndTheDatabaseLineLast(t *testing.T) {
	h := newHarness(t)
	body, _ := testlog.SSH(1, 400)
	code, out, errOut := h.run("analyze", "--as-of", "2027-01-01", h.file("ssh.log", string(body)))
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	// The sources first, then the digest, then the summary, the database last.
	marks := []string{
		"aigolog " + buildinfo.Version + ": sources 1, records 400, lines 400\n",
		"\n\npatterns\ndigest: 400 records, ",
		"\n\nsummary\n[S1] sources\n",
		"\n[S2] miner\n",
		"\n[S3] patterns\n",
		"\ndatabase: ",
	}
	at := -1
	for _, m := range marks {
		i := strings.Index(out, m)
		if i < 0 || i <= at {
			t.Fatalf("%q is missing or out of order (after offset %d, found at %d) in:\n%s", m, at, i, out)
		}
		at = i
	}
	last := out[strings.LastIndex(strings.TrimRight(out, "\n"), "\n")+1:]
	if !strings.HasPrefix(last, "database: ") || strings.Count(out, "database: ") != 1 {
		t.Errorf("the database line must come last and once, got last line %q", last)
	}
}

func TestWithNoModelTheReportHoldsTheDigestAndTheFullSummaryWithBlockIdentifiers(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run("analyze", h.file("a.log", reportLog(20)))
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"\npatterns\ndigest: 20 records, ", "[S1] sources", "[S2] miner", "[S3] patterns"} {
		if !strings.Contains(out, want) {
			t.Errorf("no flag, and %q is missing:\n%s", want, out)
		}
	}
	// The texts of B1 stay, and no line offers a command that holds log text.
	if !strings.Contains(out, "source 1: ") || !strings.Contains(out, "  records 20, lines 20, bytes ") {
		t.Errorf("the source lines of the first report are missing:\n%s", out)
	}
	if strings.Contains(out, "\naigolog query") || strings.Contains(out, "\naigolog show") {
		t.Errorf("a command is shown:\n%s", out)
	}
}

func TestTheSummaryFlagIsAcceptedAndChangesNothingWithNoModel(t *testing.T) {
	h := newHarness(t)
	p := h.file("a.log", reportLog(30))
	// --no-keep: the line of the database is then the same for both runs.
	codeA, plain, errA := h.run("analyze", "--as-of", "2026-06-01", "--no-keep", p)
	codeB, flagged, errB := h.run("analyze", "--as-of", "2026-06-01", "--no-keep", "--summary", p)
	codeC, single, errC := h.run("analyze", "-summary", "--as-of", "2026-06-01", "--no-keep", p)
	if codeA != exitcode.OK || codeB != exitcode.OK || codeC != exitcode.OK || errA+errB+errC != "" {
		t.Fatalf("codes %d %d %d, stderr %q %q %q", codeA, codeB, codeC, errA, errB, errC)
	}
	if !strings.Contains(plain, "[S3] patterns") {
		t.Fatalf("the output to compare has no summary:\n%s", plain)
	}
	if flagged != plain || single != plain {
		t.Errorf("--summary changed the output:\n--- without\n%s--- with\n%s", plain, flagged)
	}
}

func TestJSONFlagPrintsOneDocumentAndNothingElseOnStandardOutput(t *testing.T) {
	h := newHarness(t)
	// A limit of one byte makes the warning about the size of the database.
	h.config = h.file("config.json", `{"max_total_bytes": 1}`)
	code, out, errOut := h.run("analyze", "--json", "--as-of", "2026-06-01", h.file("a.log", reportLog(20)))
	if code != exitcode.OK {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	if !strings.Contains(errOut, "over the limit of 1 bytes") {
		t.Errorf("the warning must be on standard error, got %q", errOut)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("the whole of standard output must be one document: %v\n%s", err, out)
	}
	if !strings.HasSuffix(out, "}\n") || strings.HasSuffix(out, "\n\n") || !strings.HasPrefix(out, "{\n  \"format\": 1,") {
		t.Errorf("the document must be indented and end with one newline: %q ... %q", out[:min(30, len(out))], out[max(0, len(out)-5):])
	}
	for _, text := range []string{"\npatterns\n", "\nsummary\n", "\ndatabase: ", "warning"} {
		if strings.Contains(out, text) {
			t.Errorf("standard output holds %q from the text report or a warning", text)
		}
	}
	for _, key := range []string{"format", "program", "sources", "database", "digest", "summary"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("key %q is missing", key)
		}
	}
	srcs, _ := doc["sources"].([]any)
	if len(srcs) != 1 {
		t.Fatalf("sources = %v", doc["sources"])
	}
	s0, _ := srcs[0].(map[string]any)
	if s0["records"] != float64(20) || s0["lines"] != float64(20) || s0["layout"] != "iso-space" || s0["has_times"] != true {
		t.Errorf("source = %v", s0)
	}
	if p, _ := doc["program"].(map[string]any); p["name"] != "aigolog" || p["version"] != buildinfo.Version {
		t.Errorf("program = %v", doc["program"])
	}
	if dg, _ := doc["digest"].(map[string]any); dg["records"] != float64(20) {
		t.Errorf("digest = %v", doc["digest"])
	}
}

func TestNoKeepStillPrintsTheDigestAndSaysTheDatabaseWasRemoved(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run("analyze", "--no-keep", h.file("a.log", reportLog(20)))
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	for _, want := range []string{"\npatterns\ndigest: 20 records, 1 pattern\n", "[S3] patterns", "database: removed at the end of the run (--no-keep)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if dbCount(t, h.cache) != 0 {
		t.Error("the database was kept")
	}
}

func TestJSONSaysTheDatabaseWasNotKeptWithNoKeep(t *testing.T) {
	type doc struct {
		Database struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
			Kept bool   `json:"kept"`
		} `json:"database"`
	}
	for _, noKeep := range []bool{true, false} {
		h := newHarness(t)
		args := []string{"analyze", "--json", h.file("a.log", reportLog(5))}
		if noKeep {
			args = append(args, "--no-keep")
		}
		code, out, errOut := h.run(args...)
		var d doc
		if err := json.Unmarshal([]byte(out), &d); code != exitcode.OK || err != nil {
			t.Fatalf("no-keep %v: code %d, stderr %q, error %v", noKeep, code, errOut, err)
		}
		if d.Database.Kept == noKeep {
			t.Errorf("no-keep %v: database.kept = %v", noKeep, d.Database.Kept)
		}
		if !strings.HasPrefix(d.Database.Path, h.cache) || !strings.HasSuffix(d.Database.Path, ".db") {
			t.Errorf("no-keep %v: database.path = %q must still name the file in %s", noKeep, d.Database.Path, h.cache)
		}
		if d.Database.Size <= 0 {
			t.Errorf("no-keep %v: database.size = %d, want the size before the removal", noKeep, d.Database.Size)
		}
		if got := dbCount(t, h.cache); got != map[bool]int{true: 0, false: 1}[noKeep] {
			t.Errorf("no-keep %v: databases left %d", noKeep, got)
		}
	}
}

func TestAnalyzeHelpListsTheNewFlags(t *testing.T) {
	h := newHarness(t)
	code, out, errOut := h.run("analyze", "--help")
	if code != exitcode.OK || out != "" {
		t.Fatalf("code %d, stdout %q", code, out)
	}
	for _, want := range []string{
		"[--summary] [--json]",
		"  -summary\n",
		"print the full summary (with no model it is always printed)",
		"  -json\n",
		"print the report as one JSON document on standard output",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the help does not hold %q:\n%s", want, errOut)
		}
	}
	// Wrong usage is still 2, and the new flags take no value.
	for _, args := range [][]string{{"analyze", "--json=maybe", "x"}, {"analyze", "--summary=2", "x"}} {
		if code, out, _ := h.run(args...); code != exitcode.Usage || out != "" {
			t.Errorf("%v: code %d, stdout %q", args, code, out)
		}
	}
}

func TestAFailureToBuildTheSummaryExitsWithFiveAndRemovesTheDatabase(t *testing.T) {
	for _, args := range [][]string{{"analyze"}, {"analyze", "--json"}, {"analyze", "--no-keep"}} {
		h := newHarness(t)
		buildSummaryWas(t, func(context.Context, *store.DB, summary.Options) (summary.Summary, summary.Digest, error) {
			return summary.Summary{}, summary.Digest{}, errors.New("boom")
		})
		code, out, errOut := h.run(append(args, h.file("a.log", reportLog(5)))...)
		if code != exitcode.StorageUnavailable {
			t.Errorf("%v: exit code %d, want 5", args, code)
		}
		if want := "aigolog: the report could not be built: boom\n"; errOut != want {
			t.Errorf("%v: standard error %q, want %q", args, errOut, want)
		}
		if out != "" {
			t.Errorf("%v: no half report on standard output, got %q", args, out)
		}
		if dbCount(t, h.cache) != 0 || hasFile(h.cache, "*.lock") {
			t.Errorf("%v: the database or its lock file is left", args)
		}
	}
}

func TestAFailureToBuildTheSummaryReportsAFailureToRemoveTheDatabaseAsNoKeepDoes(t *testing.T) {
	h := newHarness(t)
	buildSummaryWas(t, func(context.Context, *store.DB, summary.Options) (summary.Summary, summary.Digest, error) {
		return summary.Summary{}, summary.Digest{}, errors.New("boom")
	})
	removeRunWas(t, func(db *store.DB) error { return errors.New("access denied") })
	code, out, errOut := h.run("analyze", h.file("a.log", reportLog(5)))
	want := "aigolog: the report could not be built: boom\naigolog: warning: could not remove the database: access denied\n"
	if code != exitcode.StorageUnavailable || out != "" || errOut != want {
		t.Errorf("code %d, stdout %q, stderr %q, want %q", code, out, errOut, want)
	}
}

func TestASignalWhileTheSummaryIsBuiltIsAnInterruptionAndRemovesTheDatabase(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.ctx = ctx
	buildSummaryWas(t, func(c context.Context, _ *store.DB, _ summary.Options) (summary.Summary, summary.Digest, error) {
		cancel()
		return summary.Summary{}, summary.Digest{}, c.Err()
	})
	code, out, errOut := h.run("analyze", h.file("a.log", reportLog(5)))
	if code != exitcode.Failure || out != "" || errOut != "aigolog: interrupted\n" || dbCount(t, h.cache) != 0 {
		t.Errorf("code %d, stdout %q, stderr %q, databases %d", code, out, errOut, dbCount(t, h.cache))
	}
}

func TestTheSummaryIsBuiltWithTheDefaultOptionsWhileTheDatabaseIsStillThere(t *testing.T) {
	h := newHarness(t)
	var got summary.Options
	var existed bool
	buildSummaryWas(t, func(ctx context.Context, db *store.DB, o summary.Options) (summary.Summary, summary.Digest, error) {
		got = o
		_, err := os.Stat(db.Path())
		existed = err == nil
		return summary.Build(ctx, db, o)
	})
	code, _, errOut := h.run("analyze", "--no-keep", h.file("a.log", reportLog(5)))
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	if got != summary.DefaultOptions() || !existed {
		t.Errorf("options %+v (want the defaults), database there at the build: %v", got, existed)
	}
}

func TestAJSONReportThatCannotBeWrittenExitsWithOneAndSaysSo(t *testing.T) {
	h := newHarness(t)
	var e bytes.Buffer
	vars := map[string]string{"AIGOLOG_CACHE_DIR": h.cache, "AIGOLOG_CONFIG": filepath.Join(h.cache, "no-config.json")}
	code := RunEnv(Env{
		Stdout:    failingWriter{},
		Stderr:    &e,
		LookupEnv: func(k string) (string, bool) { v, ok := vars[k]; return v, ok },
		Now:       func() time.Time { return h.now },
		Zone:      time.UTC,
	}, []string{"analyze", "--json", h.file("a.log", reportLog(5))})
	if code != exitcode.Failure || !strings.Contains(e.String(), "aigolog: the report could not be written: ") {
		t.Errorf("code %d, stderr %q", code, e.String())
	}
}

// markOf changes the database at path with the statements, as a test of the
// sweep needs a database that is not what a run leaves.
func markOf(t *testing.T, path string, statements ...string) {
	t.Helper()
	db, err := sql.Open(store.DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range statements {
		if _, err = db.Exec(q); err != nil {
			break
		}
	}
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestASweepThatRemovesAnAgedOutDatabaseOfAnotherLayoutSaysNothingOfUnfinished(t *testing.T) {
	h := newHarness(t)
	h.now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	p := h.file("a.log", "x\n")
	code, out, errOut := h.run("analyze", p)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("first run: code %d, stderr %q", code, errOut)
	}
	first := regexp.MustCompile(`(?m)^database: (.+\.db) \(`).FindStringSubmatch(out)[1]
	// A finished database of layout 1, the one of B1: the sweep calls it other.
	markOf(t, first, `PRAGMA user_version = 1`)
	h.now = h.now.Add(8 * 24 * time.Hour)
	code, _, errOut = h.run("analyze", p)
	if code != exitcode.OK || errOut != "" {
		t.Errorf("second run: code %d, stderr %q; an aged-out database of another layout goes with no note", code, errOut)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) || dbCount(t, h.cache) != 1 {
		t.Errorf("the old database: %v; databases %d; want it gone and the new one left", err, dbCount(t, h.cache))
	}
}

func TestTheNoteOfASweepSaysDatabaseOrDatabasesByTheNumberRemoved(t *testing.T) {
	h := newHarness(t)
	p := h.file("a.log", "x\n")
	var paths []string
	for i := 0; i < 2; i++ {
		h.now = h.now.Add(time.Minute) // another name for each database
		code, out, errOut := h.run("analyze", p)
		if code != exitcode.OK || errOut != "" {
			t.Fatalf("run %d: code %d, stderr %q", i, code, errOut)
		}
		paths = append(paths, regexp.MustCompile(`(?m)^database: (.+\.db) \(`).FindStringSubmatch(out)[1])
	}
	for _, path := range paths {
		markOf(t, path, `UPDATE meta SET value = '0' WHERE key = 'complete'`)
	}
	h.now = h.now.Add(time.Minute)
	code, _, errOut := h.run("analyze", p)
	if code != exitcode.OK {
		t.Fatalf("third run: code %d, stderr %q", code, errOut)
	}
	if want := "aigolog: note: removed 2 databases that were not finished\n"; !strings.Contains(errOut, want) {
		t.Errorf("standard error %q does not hold %q", errOut, want)
	}
}

func TestTheNoteOfASweepCountsAFileThatIsNotADatabaseAsNotFinished(t *testing.T) {
	h := newHarness(t)
	p := h.file("a.log", "x\n")
	code, out, errOut := h.run("analyze", p)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("first run: code %d, stderr %q", code, errOut)
	}
	first := regexp.MustCompile(`(?m)^database: (.+\.db) \(`).FindStringSubmatch(out)[1]
	if err := os.WriteFile(first, []byte(strings.Repeat("this is text and not a database\n", 20)), 0o600); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(time.Minute)
	code, _, errOut = h.run("analyze", p)
	if code != exitcode.OK {
		t.Fatalf("second run: code %d, stderr %q", code, errOut)
	}
	if want := "aigolog: note: removed 1 database that was not finished\n"; !strings.Contains(errOut, want) {
		t.Errorf("standard error %q does not hold %q", errOut, want)
	}
}

// runWithStdout is run with the standard output given.
func (h *harness) runWithStdout(stdout io.Writer, args ...string) (code int, errOut string) {
	h.t.Helper()
	var e bytes.Buffer
	vars := map[string]string{"AIGOLOG_CACHE_DIR": h.cache, "AIGOLOG_CONFIG": filepath.Join(h.cache, "no-config.json")}
	code = RunEnv(Env{
		Stdout:    stdout,
		Stderr:    &e,
		LookupEnv: func(k string) (string, bool) { v, ok := vars[k]; return v, ok },
		Now:       func() time.Time { return h.now },
		Zone:      time.UTC,
	}, args)
	return code, e.String()
}

func TestATextReportThatCannotBeWrittenExitsWithOneAndSaysSoAndTheDatabaseIsHandledAsBefore(t *testing.T) {
	h := newHarness(t)
	code, errOut := h.runWithStdout(failingWriter{}, "analyze", h.file("a.log", reportLog(5)))
	if code != exitcode.Failure || !strings.Contains(errOut, "aigolog: the report could not be written: write failed\n") {
		t.Errorf("code %d, stderr %q", code, errOut)
	}
	if dbCount(t, h.cache) != 1 {
		t.Errorf("databases %d; the database of a run is kept when the report could not be written", dbCount(t, h.cache))
	}
	h2 := newHarness(t)
	code, errOut = h2.runWithStdout(failingWriter{}, "analyze", "--no-keep", h2.file("a.log", reportLog(5)))
	if code != exitcode.Failure || !strings.Contains(errOut, "aigolog: the report could not be written: write failed\n") {
		t.Errorf("--no-keep: code %d, stderr %q", code, errOut)
	}
	if dbCount(t, h2.cache) != 0 {
		t.Errorf("--no-keep: databases %d; want it removed as always", dbCount(t, h2.cache))
	}
	// A write that works still gives exit 0.
	h3 := newHarness(t)
	if code, errOut = h3.runWithStdout(&bytes.Buffer{}, "analyze", h3.file("a.log", reportLog(5))); code != exitcode.OK || errOut != "" {
		t.Errorf("a working writer: code %d, stderr %q", code, errOut)
	}
}

func TestAnalyzeNamesTheFilesModificationTimeAsTheAnchorWhenNoAsOfIsGiven(t *testing.T) {
	h := newHarness(t) // the current time of the harness is 2026-02-01 12:00:00
	p := h.file("syslog", "Jan 20 10:00:00 h a\nJan 21 11:00:00 h b\n")
	mod := time.Date(2025, time.January, 25, 8, 30, 0, 0, time.UTC)
	if err := os.Chtimes(p, mod, mod); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := h.run("analyze", p)
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	// The years follow the modification time (2025), not the current time
	// (which would give 2026).
	for _, want := range []string{
		"  time: 2025-01-20 10:00:00 to 2025-01-21 11:00:00; as written, no zone stated",
		"  years: worked out from the anchor 2025-01-25 08:30:00, the file's modification time",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestAnalyzeSaysAnEmptyInputWasNotStoredWhenOtherInputsAre(t *testing.T) {
	// A populated file with an empty file before it, and an empty standard
	// input after it: the empty ones are named and not counted.
	h := newHarness(t)
	empty := h.file("empty.log", "")
	full := h.file("full.log", "2026-01-05 10:00:01 a\n2026-01-05 10:00:09 b\n")
	h.stdin = ""
	code, out, errOut := h.run("analyze", empty, full, "-")
	if code != exitcode.OK || errOut != "" {
		t.Fatalf("code %d, stderr %q, output:\n%s", code, errOut, out)
	}
	for _, want := range []string{
		"sources 1, records 2, lines 2",
		"source -: " + empty + " is empty and was not stored\n",
		"source -: standard input is empty and was not stored\n",
		"source 1: " + full + " (utf-8, layout iso-space)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "source 2:") {
		t.Errorf("an empty input was numbered as a source:\n%s", out)
	}
	if dbCount(t, h.cache) != 1 {
		t.Fatalf("databases kept: %d, want 1", dbCount(t, h.cache))
	}
	paths, _ := filepath.Glob(filepath.Join(h.cache, "*.db"))
	db, err := store.Open(context.Background(), paths[0])
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	infos, err := db.SourceInfos(context.Background())
	if err != nil || len(infos) != 1 || infos[0].Name != full || infos[0].Records != 2 {
		t.Errorf("the database holds %+v, %v; want only the populated source with 2 records", infos, err)
	}
}
