// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/record"
	"github.com/ckir/aigolog/internal/store"
)

// These tests run the real program, as a child process, because what they
// pin is not reachable from inside one process: a signal delivered by the
// kernel, two processes that hold locks of their own, a resident size.
//
// The test binary is its own program: when AIGOLOG_TEST_HELPER is set in its
// environment it runs the command line it was given and exits with the exit
// code of the command, as cmd/aigolog does.

const (
	helperEnv = "AIGOLOG_TEST_HELPER"
	// readyEnv names a file that the helper makes when it is about to wait for
	// more of its standard input than the first read gave it (see readyReader).
	readyEnv = "AIGOLOG_TEST_READY"
	// peakEnv names a file that the helper writes its own peak resident size
	// to, in KiB, when the command ends (see TestMemoryOfAProcessStaysBounded).
	peakEnv = "AIGOLOG_TEST_PEAK"
)

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) != "" {
		os.Exit(runHelper())
	}
	os.Exit(m.Run())
}

func runHelper() int {
	code := runHelperCommand()
	if path := os.Getenv(peakEnv); path != "" {
		if kib, ok := ownPeakKiB(); ok {
			os.WriteFile(path, []byte(fmt.Sprint(kib)), 0o600)
		}
	}
	return code
}

func runHelperCommand() int {
	ready := os.Getenv(readyEnv)
	if ready == "" {
		return Run(os.Args[1:], os.Stdout, os.Stderr)
	}
	// The same environment as Run makes, except that the standard input says
	// when the run is waiting for it.
	return RunEnv(Env{
		Stdin:     &readyReader{r: os.Stdin, marker: ready},
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		LookupEnv: os.LookupEnv,
		Now:       time.Now,
		Zone:      time.Local,
	}, os.Args[1:])
}

// readyReader makes the marker file when it is asked for the second time. The
// reader of the text asks for 64 KiB before it decides anything, so a standard
// input that holds less than that and stays open is, from the second call on,
// a read that returns only when more text or the end comes: a signal that
// arrives then is noticed after that read, not before. The test needs to know
// that the run is in that state; the folder holding a .db file shows only that
// it began.
type readyReader struct {
	r      io.Reader
	marker string
	calls  int
}

func (r *readyReader) Read(p []byte) (int, error) {
	r.calls++
	if r.calls == 2 {
		os.WriteFile(r.marker, nil, 0o600)
	}
	return r.r.Read(p)
}

// syncBuffer is a bytes buffer that the copying goroutine of exec writes and
// the test reads.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// stdinFeed writes a text to the standard input of a child, and keeps the
// input open until release is called: a standard input that stays open.
type stdinFeed struct {
	w       io.WriteCloser
	written chan struct{}
	once    sync.Once
}

func newStdinFeed(w io.WriteCloser, text string) *stdinFeed {
	f := &stdinFeed{w: w, written: make(chan struct{})}
	go func() {
		defer close(f.written)
		io.WriteString(w, text) // a child that has died makes this fail, which is fine
	}()
	return f
}

// release closes the input once all of the text is written.
func (f *stdinFeed) release() {
	f.once.Do(func() {
		<-f.written
		f.w.Close()
	})
}

// deadline is how long a test waits for a condition; steps are of 20 ms.
const (
	deadline = 20 * time.Second
	step     = 20 * time.Millisecond
)

// childDeadline is how long a test waits for a child to end. An analysis mines
// every record, and mining 300,000 lines took 33 to 46 s on a loaded machine;
// a slow runner must not fail on time, and a child that hangs is still caught.
const childDeadline = 180 * time.Second

// waitFor polls cond until it holds. It fails the test at the deadline.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	end := time.Now().Add(deadline)
	for !cond() {
		if time.Now().After(end) {
			t.Fatalf("gave up after %v waiting for %s", deadline, what)
		}
		time.Sleep(step)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasFile(dir, pattern string) bool {
	m, _ := filepath.Glob(filepath.Join(dir, pattern))
	return len(m) > 0
}

// child is a running helper process.
type child struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdout *syncBuffer
	stderr *syncBuffer
	gate   *stdinFeed // nil when the child has no standard input
	cache  string
	ready  string // the marker file of readyReader
	peak   string // the file the helper writes its peak resident size to
	done   chan struct{}
}

// childOpts says how startChild starts the helper.
type childOpts struct {
	cache string // AIGOLOG_CACHE_DIR
	// stdin is the text on the standard input of the child; with hold the
	// input then stays open until release is called.
	stdin string
	hold  bool
	// ignoreINT starts the child, through sh, with SIGINT ignored.
	ignoreINT bool
	args      []string
}

// startChild starts the test binary as the program with the given arguments
// and its own cache folder. It kills the child when the test ends.
func startChild(t *testing.T, o childOpts) *child {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := &child{t: t, stdout: &syncBuffer{}, stderr: &syncBuffer{}, cache: o.cache, done: make(chan struct{})}
	tmp := t.TempDir()
	c.ready, c.peak = filepath.Join(tmp, "ready"), filepath.Join(tmp, "peak")
	if o.ignoreINT {
		// sh ignores SIGINT, and an ignored signal stays ignored through exec.
		// The program is $0 so that its path is not part of the script.
		c.cmd = exec.Command("sh", append([]string{"-c", `trap "" INT; exec "$0" "$@"`, exe}, o.args...)...)
	} else {
		c.cmd = exec.Command(exe, o.args...)
	}
	// The environment of the developer must not leak in: every AIGOLOG_
	// variable is replaced by the ones this test sets.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "AIGOLOG_") {
			env = append(env, kv)
		}
	}
	c.cmd.Env = append(env,
		helperEnv+"=1",
		readyEnv+"="+c.ready,
		peakEnv+"="+c.peak,
		"AIGOLOG_CACHE_DIR="+o.cache,
		"AIGOLOG_CONFIG="+filepath.Join(o.cache, "no-config.json"),
	)
	c.cmd.Stdout, c.cmd.Stderr = c.stdout, c.stderr
	var in io.WriteCloser
	if o.stdin != "" || o.hold {
		var err error
		if in, err = c.cmd.StdinPipe(); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if in != nil {
		c.gate = newStdinFeed(in, o.stdin)
		if !o.hold {
			c.gate.release()
		}
	}
	go func() {
		c.cmd.Wait()
		close(c.done)
	}()
	t.Cleanup(func() {
		c.cmd.Process.Kill()
		if c.gate != nil {
			c.gate.release()
		}
		select {
		case <-c.done:
		case <-time.After(deadline):
			t.Errorf("a child process did not end after it was killed")
		}
	})
	return c
}

// release ends the standard input of the child.
func (c *child) release() { c.gate.release() }

// waitReady waits until the child holds its database and waits for more input.
func (c *child) waitReady() {
	c.t.Helper()
	waitFor(c.t, "the child to wait for its input", func() bool {
		select {
		case <-c.done:
			c.t.Fatalf("the child ended before it waited for input; stderr %q", c.stderr.String())
		default:
		}
		return exists(c.ready) && hasFile(c.cache, "*.db")
	})
}

// waitStderr waits until the standard error of the child holds s.
func (c *child) waitStderr(s string) {
	c.t.Helper()
	waitFor(c.t, fmt.Sprintf("%q on the standard error of the child", s), func() bool {
		if strings.Contains(c.stderr.String(), s) {
			return true
		}
		select {
		case <-c.done:
			c.t.Fatalf("the child ended (%v) without writing %q; stderr %q", c.cmd.ProcessState, s, c.stderr.String())
		default:
		}
		return false
	})
}

// wait waits for the child to end, and returns its state.
func (c *child) wait() *os.ProcessState {
	c.t.Helper()
	select {
	case <-c.done:
	case <-time.After(childDeadline):
		c.t.Fatalf("the child did not end within %v; stderr %q", childDeadline, c.stderr.String())
	}
	return c.cmd.ProcessState
}

func (c *child) isRunning() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

// signalNotice is what the program says when it has caught the first signal.
const signalNotice = "aigolog: signal received; send it again to stop at once\n"

// A running analysis reads its input in blocking reads, and a signal is
// noticed when the read that is waiting returns (interruptContext says so).
// The tests therefore wait for the notice, which shows that the signal was
// caught, and then end the standard input; the program must then stop, say
// "interrupted", exit with 1 and remove its files.
func requireStopsOn(t *testing.T, sig os.Signal, o childOpts) {
	t.Helper()
	o.stdin, o.hold = "hello\n", true
	c := startChild(t, o)
	c.waitReady()
	if err := c.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	c.waitStderr(signalNotice)
	if !c.isRunning() {
		t.Fatalf("the program ended at the first signal, before its input was read; stderr %q", c.stderr.String())
	}
	c.release()
	st := c.wait()
	if st.ExitCode() != exitcode.Failure {
		t.Errorf("exit code %d (state %v), want %d; stderr %q", st.ExitCode(), st, exitcode.Failure, c.stderr.String())
	}
	if got := strings.TrimRight(c.stderr.String(), "\n"); !strings.HasSuffix(got, "aigolog: interrupted") {
		t.Errorf("standard error does not end with the interrupted message: %q", c.stderr.String())
	}
	if out := c.stdout.String(); out != "" {
		t.Errorf("standard output %q, want nothing", out)
	}
	noFilesIn(t, o.cache)
}

func TestARealSIGINTStopsAnalyzeRemovesTheDatabaseAndExitsWithOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a child cannot be sent an interrupt on Windows")
	}
	if signal.Ignored(os.Interrupt) {
		t.Skip("this process ignores SIGINT, and a child would inherit that")
	}
	requireStopsOn(t, os.Interrupt, childOpts{cache: filepath.Join(t.TempDir(), "cache"), args: []string{"analyze", "-"}})
}

func TestARealSIGTERMDoesTheSame(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a child cannot be sent a terminate signal on Windows")
	}
	if signal.Ignored(syscall.SIGTERM) {
		t.Skip("this process ignores SIGTERM, and a child would inherit that")
	}
	requireStopsOn(t, syscall.SIGTERM, childOpts{cache: filepath.Join(t.TempDir(), "cache"), args: []string{"analyze", "-"}})
}

func TestASecondSIGINTKillsAtOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a child cannot be sent an interrupt on Windows")
	}
	if signal.Ignored(os.Interrupt) {
		t.Skip("this process ignores SIGINT, and a child would inherit that")
	}
	cache := filepath.Join(t.TempDir(), "cache")
	c := startChild(t, childOpts{cache: cache, stdin: "hello\n", hold: true, args: []string{"analyze", "-"}})
	c.waitReady()
	c.cmd.Process.Signal(os.Interrupt)
	// The second signal has its default effect only once the notice is out:
	// until then the first handler is still there and swallows it.
	c.waitStderr(signalNotice)
	c.cmd.Process.Signal(os.Interrupt)
	// The input stays open: only the signal can have ended the process.
	st := c.wait()
	ws, ok := st.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no wait status in %T", st.Sys())
	}
	// Measured on Linux (2026-11-17): with -count=20 every
	// run ended with the wait status "signal: interrupt", ExitCode -1: the
	// second SIGINT kills the process, it does not make it exit with 1. (With
	// a script, the same: Python's returncode -2.) The database and its lock
	// file stay, as they do for any kill; the next sweep removes them.
	t.Logf("process state %q, exit code %d", st, st.ExitCode())
	if !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("wait status %v (exit code %d), want death by SIGINT; stderr %q", ws, st.ExitCode(), c.stderr.String())
	}
	if strings.Contains(c.stderr.String(), "interrupted") {
		t.Errorf("a killed program cannot have said it was interrupted: %q", c.stderr.String())
	}
}

func TestSIGINTIgnoredAtStartIsStillIgnoredAndSIGTERMStops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh and no signals to send on Windows")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh to start the program with SIGINT ignored")
	}
	if signal.Ignored(syscall.SIGTERM) {
		t.Skip("this process ignores SIGTERM, and a child would inherit that")
	}
	cache := filepath.Join(t.TempDir(), "cache")
	c := startChild(t, childOpts{cache: cache, stdin: "hello\n", hold: true, ignoreINT: true, args: []string{"analyze", "-"}})
	c.waitReady()
	if err := c.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	// That a signal does nothing can be seen only by waiting: this is the one
	// fixed wait of these tests. If the program caught the signal (it must
	// not), the notice comes within a few milliseconds, and a program that
	// died of it is seen by isRunning; the wait cannot make a failure pass.
	time.Sleep(300 * time.Millisecond)
	if !c.isRunning() {
		t.Fatalf("SIGINT ended a program that was started ignoring it; stderr %q", c.stderr.String())
	}
	if got := c.stderr.String(); got != "" {
		t.Errorf("the ignored SIGINT was caught: stderr %q", got)
	}
	if !hasFile(cache, "*.db") {
		t.Error("the database went at an ignored SIGINT")
	}
	// SIGTERM is not ignored, so it is caught and stops the run.
	if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	c.waitStderr(signalNotice)
	c.release()
	st := c.wait()
	if st.ExitCode() != exitcode.Failure || !strings.HasSuffix(strings.TrimRight(c.stderr.String(), "\n"), "aigolog: interrupted") {
		t.Errorf("exit code %d, stderr %q; want 1 and the interrupted message", st.ExitCode(), c.stderr.String())
	}
	noFilesIn(t, cache)
}

// writeLog writes a log of n lines, each of at least 200 bytes and each
// different, a line at a time, so that the test itself holds none of it.
func writeLog(t *testing.T, path string, n int, tag string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	pad := strings.Repeat("abcdefghij", 20)
	for i := 0; i < n; i++ {
		fmt.Fprintf(w, "2026-01-05 10:00:%02d host %s[%d]: line %d %s\n", i%60, tag, i%1000, i, pad)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// completeRecords opens the only database of dir, which must be complete, and
// returns the number of records of its first source and the text of the first.
func completeRecords(t *testing.T, path string) (n int, first string) {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("a finished database does not open: %v", err)
	}
	defer db.Close()
	err = db.Records(context.Background(), 1, func(_ int64, r record.Record) error {
		if n == 0 {
			first = string(r.Raw)
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n, first
}

func TestTwoProcessesAnalyseAtTheSameTimeAndFinishWithSeparateDatabases(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cache")
	var logs [2]string
	for i, tc := range []struct {
		lines int
		tag   string
	}{{20000, "alpha"}, {20001, "beta"}} {
		p := filepath.Join(t.TempDir(), tc.tag+".log")
		writeLog(t, p, tc.lines, tc.tag)
		logs[i] = p
	}
	// Each process reads its log from standard input, which stays open at the
	// end of the text until both are known to be running: then both hold a
	// database and a lock of their own at the same moment, in two processes.
	var cs [2]*child
	for i, p := range logs {
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		cs[i] = startChild(t, childOpts{cache: cache, stdin: string(body), hold: true, args: []string{"analyze", "-"}})
	}
	waitFor(t, "both processes to wait for their input", func() bool {
		return exists(cs[0].ready) && exists(cs[1].ready)
	})
	m, _ := filepath.Glob(filepath.Join(cache, "*.lock"))
	if len(m) != 2 {
		t.Fatalf("lock files while both run: %v, want 2", m)
	}
	for _, c := range cs {
		c.release()
	}
	for i, c := range cs {
		if st := c.wait(); st.ExitCode() != exitcode.OK {
			t.Fatalf("process %d: exit code %d, stderr %q", i, st.ExitCode(), c.stderr.String())
		}
	}
	dbs, _ := filepath.Glob(filepath.Join(cache, "*.db"))
	if len(dbs) != 2 {
		t.Fatalf("databases: %v, want 2", dbs)
	}
	counts := map[string]int{}
	for _, p := range dbs {
		n, first := completeRecords(t, p)
		switch {
		case strings.Contains(first, " alpha["):
			counts["alpha"] = n
		case strings.Contains(first, " beta["):
			counts["beta"] = n
		default:
			t.Fatalf("the first record of %s is neither log's: %q", p, first)
		}
	}
	if counts["alpha"] != 20000 || counts["beta"] != 20001 {
		t.Errorf("records per database %v, want alpha 20000 and beta 20001", counts)
	}
	// Both finished: no lock is held and none is left.
	if hasFile(cache, "*-journal") {
		t.Error("a journal was left")
	}
	h := &harness{t: t, cache: cache, now: time.Now()}
	_, out, _ := h.run("list")
	if strings.Count(out, "\tcomplete\t") != 2 {
		t.Errorf("list does not show two complete databases:\n%s", out)
	}
}

func TestACleanerProcessBesideALiveAnalysisRemovesNothingLive(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cache")
	live := startChild(t, childOpts{cache: cache, stdin: "2026-01-05 10:00:01 a\n", hold: true, args: []string{"analyze", "-"}})
	live.waitReady()
	dbs, _ := filepath.Glob(filepath.Join(cache, "*.db"))
	if len(dbs) != 1 {
		t.Fatalf("databases before the cleaner: %v", dbs)
	}

	cleaner := startChild(t, childOpts{cache: cache, args: []string{"clean"}})
	st := cleaner.wait()
	if want := "removed 0 databases, 0 bytes; running 1\n"; st.ExitCode() != exitcode.OK || cleaner.stdout.String() != want {
		t.Fatalf("the cleaner: exit code %d, stdout %q, stderr %q; want %q", st.ExitCode(), cleaner.stdout.String(), cleaner.stderr.String(), want)
	}
	if !exists(dbs[0]) || !hasFile(cache, "*.lock") {
		t.Fatalf("the cleaner removed a file of the live run")
	}
	if !live.isRunning() {
		t.Fatalf("the live analysis ended: stderr %q", live.stderr.String())
	}

	live.release()
	if st := live.wait(); st.ExitCode() != exitcode.OK {
		t.Fatalf("the analysis, after the cleaner: exit code %d, stderr %q", st.ExitCode(), live.stderr.String())
	}
	if n, _ := completeRecords(t, dbs[0]); n != 1 {
		t.Errorf("the finished database holds %d records, want 1", n)
	}
}

// maxrss returns the largest resident size, in bytes, that the kernel
// reported for the process of st in its rusage (Maxrss).
func maxrss(t *testing.T, st *os.ProcessState) int64 {
	t.Helper()
	v := reflect.ValueOf(st.SysUsage())
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct || !v.FieldByName("Maxrss").IsValid() {
		t.Skipf("no peak resident size in %T on %s", st.SysUsage(), runtime.GOOS)
	}
	n := v.FieldByName("Maxrss").Int()
	if runtime.GOOS != "darwin" && runtime.GOOS != "ios" {
		n *= 1024 // kilobytes
	}
	return n
}

// ownPeakKiB returns the peak resident size of this process, in KiB, from the
// kernel's own count of the program that is running now (Linux).
func ownPeakKiB() (int64, bool) {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(l, "VmHWM:"); ok {
			var kib int64
			if _, err := fmt.Sscanf(strings.TrimSpace(rest), "%d kB", &kib); err == nil {
				return kib, true
			}
		}
	}
	return 0, false
}

// peakOf returns the peak resident size of the ended child c, in bytes, and
// how it was found. The rusage of a child (Maxrss) cannot be used alone: the
// kernel starts a child's figure at the peak of the process that started it,
// because the child shares that process's memory until it runs the program
// (exec), so a child of a test binary that once held 41 MiB never reports less
// than that. Measured on Linux, in a run of the whole package: both children
// of this test reported 41188 KiB of Maxrss, the VmHWM of the test process
// (alone, the test process is smaller and Maxrss is the child's own, 24420 and
// 25920 KiB). Where /proc exists the child
// therefore writes the VmHWM of its own, new memory when it ends (see
// runHelper), and that is the figure; elsewhere Maxrss is the figure.
func (c *child) peakOf(st *os.ProcessState) (bytes int64, how string) {
	c.t.Helper()
	if b, err := os.ReadFile(c.peak); err == nil {
		var kib int64
		if _, err := fmt.Sscan(string(b), &kib); err == nil {
			return kib << 10, "VmHWM written by the child"
		}
	}
	return maxrss(c.t, st), "Maxrss"
}

func TestMemoryOfAProcessStaysBounded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the peak resident size of a child is read from its rusage, which Windows has not")
	}
	if testing.Short() {
		t.Skip("analyses 330,000 lines")
	}
	dir := t.TempDir()
	peak := func(lines int) int64 {
		p := filepath.Join(dir, fmt.Sprintf("log-%d", lines))
		writeLog(t, p, lines, "mem")
		c := startChild(t, childOpts{cache: filepath.Join(dir, fmt.Sprintf("cache-%d", lines)), args: []string{"analyze", p}})
		st := c.wait()
		if st.ExitCode() != exitcode.OK {
			t.Fatalf("%d lines: exit code %d, stderr %q", lines, st.ExitCode(), c.stderr.String())
		}
		if !strings.Contains(c.stdout.String(), fmt.Sprintf("records %d, lines %d", lines, lines)) {
			t.Fatalf("%d lines: the summary does not say so:\n%s", lines, c.stdout.String())
		}
		os.Remove(p) // 75 MB for the big one
		n, how := c.peakOf(st)
		own, _ := ownPeakKiB()
		t.Logf("%d lines: peak resident size %d KiB (%s); Maxrss of the rusage %d KiB; VmHWM of this test process %d KiB",
			lines, n>>10, how, maxrss(t, st)>>10, own)
		return n
	}
	small := peak(30000)
	big := peak(300000)
	// A program that kept the text of the records would show a tenfold input
	// as a larger peak: 300,000 lines of 250 bytes are 75 MB. The size of the
	// large input is kept: the ratio of the two inputs is 10 (at least 4), the
	// lines are 250 bytes (at least 200), and the volume of 75 MB is less than
	// twice the peak of the child (31 to 42 MiB), so fewer lines would be less than
	// the peak and prove less; the limit is 1.5 times the small peak and 16 MiB,
	// which a log kept whole (the peak plus 75 MB) exceeds.
	if limit := small*3/2 + 16<<20; big >= limit {
		t.Errorf("the peak resident size grew with the input: %d bytes for 300,000 lines, %d for 30,000; limit %d", big, small, limit)
	}
}
