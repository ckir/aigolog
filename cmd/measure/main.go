// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Command measure shows the speed and the memory of an "aigolog analyze" run
// on the machine it is run on: it writes a generated log to a temporary
// folder, runs the program on it as a child process, and prints what the run
// took. Anyone who has the repository can repeat it; see docs/measuring.md.
package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/testlog"
	"github.com/ckir/aigolog/internal/textsafe"
)

const usage = "Usage: measure [-kind ssh|restart|web|dense] [-lines N] [-seed N] [-runs N] [-bin PATH] [-keep] [-json] [-bars] [-small-lines N]"

// The four bars of the acceptance record (A6). They are the owner's
// provisional bars; they are variables so that a test can set them.
var (
	// memoryFactor and memorySlackBytes: the peak memory at -lines is at most
	// memoryFactor times that at -small-lines, plus memorySlackBytes.
	memoryFactor     = 1.5
	memorySlackBytes = float64(16 << 20)
	// wallLimit: the wall time at -lines is at most this.
	wallLimit = 300 * time.Second
	// databaseFactor: the database is at most this many times the log.
	databaseFactor = 6.0
)

// peakReader returns the peak memory of the finished program, in bytes, or
// false when the system gives no figure. It is called once, after Wait, with
// the state of the process (which may be nil when Wait failed), and it always
// releases what openPeak took.
type peakReader func(state *os.ProcessState) (peak uint64, ok bool)

// peakMemory is called right after the program has started and returns the
// reader of its peak memory. A test replaces it with a system that gives none.
var peakMemory = openPeak

// resetPeak is called just before the program starts and reports whether its
// peak memory will be its own and not floored by the memory this command held
// before (see resetPeakMemory in peak_unix.go). When it says no, the peak is
// reported as not measured. A test replaces it.
var resetPeak = resetPeakMemory

// loadavgPath is where Linux gives the load average.
const loadavgPath = "/proc/loadavg"

// asOf is the date the program places logs without a year before.
const asOf = "2026-12-31"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// options are the flags.
type options struct {
	kind       string
	lines      int
	seed       int64
	runs       int
	bin        string
	keep       bool
	json       bool
	bars       bool
	smallLines int
}

// run is the command: it returns 0 when it measured (whether or not the bars
// are met), 1 when a run failed or the program could not be made, and 2 when
// the command line was wrong.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("measure", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, usage)
		fs.PrintDefaults()
	}
	var o options
	fs.StringVar(&o.kind, "kind", "ssh", "the kind of log: ssh, restart, web or dense")
	fs.IntVar(&o.lines, "lines", 655146, "the number of lines of the log (the default is the size of the full OpenSSH log)")
	fs.Int64Var(&o.seed, "seed", 1, "the seed: the same seed gives the same log")
	fs.IntVar(&o.runs, "runs", 2, "how many times each log is analyzed, at least 1")
	fs.StringVar(&o.bin, "bin", "", "the aigolog program to measure; by default ./cmd/aigolog is built with go build")
	fs.BoolVar(&o.keep, "keep", false, "keep the temporary folder and print its path")
	fs.BoolVar(&o.json, "json", false, "print the figures as one JSON document")
	fs.BoolVar(&o.bars, "bars", false, "also run the log at -small-lines lines and say whether the four bars of the acceptance record are met")
	fs.IntVar(&o.smallLines, "small-lines", 100000, "the smaller size for the memory bar, used with -bars")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitcode.OK
		}
		return exitcode.Usage
	}
	wrong := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "measure: "+format+"\n%s\n", append(a, usage)...)
		return exitcode.Usage
	}
	switch {
	case fs.NArg() > 0:
		return wrong("unexpected argument %q", fs.Arg(0))
	case o.kind != "ssh" && o.kind != "restart" && o.kind != "web" && o.kind != "dense":
		return wrong("unknown kind %q: want ssh, restart, web or dense", o.kind)
	case o.runs < 1:
		return wrong("-runs must be at least 1, not %d", o.runs)
	case o.lines < 1:
		return wrong("-lines must be at least 1, not %d", o.lines)
	case o.smallLines < 1:
		return wrong("-small-lines must be at least 1, not %d", o.smallLines)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The load at the start is read before the program is built: the build is
	// work of this command, not background load.
	loadStart := loadAverage()
	e, err := newExperiment(o)
	if err != nil {
		fmt.Fprintf(stderr, "measure: %v\n", err)
		return exitcode.Failure
	}
	e.loadStart = loadStart
	if !o.keep {
		defer os.RemoveAll(e.tmp)
	}
	return e.measure(ctx, stdout, stderr)
}

// experiment is one invocation: the folder it works in and the program.
type experiment struct {
	o         options
	tmp       string // the temporary folder: logs/, cache/, home/, scratch/, bin/
	bin       string // the program to measure
	loadStart string // the load average before the program was built, or ""
}

// newExperiment makes the temporary folder and, without -bin, builds the
// program into it.
func newExperiment(o options) (*experiment, error) {
	goTool := ""
	if o.bin == "" {
		var err error
		goTool, err = exec.LookPath("go")
		if err != nil {
			return nil, errors.New("the go command is not on the PATH: install Go to build the program, or give the program with -bin")
		}
	}
	tmp, err := os.MkdirTemp("", "aigolog-measure-")
	if err != nil {
		return nil, fmt.Errorf("making the temporary folder: %w", unwrapPath(err))
	}
	e := &experiment{o: o, tmp: tmp}
	fail := func(err error) (*experiment, error) {
		if !o.keep {
			os.RemoveAll(tmp)
		}
		return nil, err
	}
	for _, d := range []string{"logs", "cache", "home", "scratch", "bin"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o700); err != nil {
			return fail(fmt.Errorf("making the temporary folder: %w", unwrapPath(err)))
		}
	}
	if o.bin != "" {
		abs, err := filepath.Abs(o.bin)
		if err != nil {
			return fail(fmt.Errorf("-bin: %w", unwrapPath(err)))
		}
		e.bin = abs
		return e, nil
	}
	e.bin = filepath.Join(tmp, "bin", "aigolog")
	if runtime.GOOS == "windows" {
		e.bin += ".exe"
	}
	build := exec.Command(goTool, "build", "-o", e.bin, "github.com/ckir/aigolog/cmd/aigolog")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if err := build.Run(); err != nil {
		return fail(errors.New("building the program failed: run \"go build ./cmd/aigolog\" in the repository to see why, or give the program with -bin"))
	}
	return e, nil
}

// unwrapPath returns the cause of a *fs.PathError, so that its path (which
// may name the user) is not printed.
func unwrapPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// runFigures are the figures of one run.
type runFigures struct {
	WallSeconds     float64 `json:"wall_seconds"`
	CPUSeconds      float64 `json:"cpu_seconds"`
	PeakMemoryBytes *uint64 `json:"peak_memory_bytes"` // null when the system gives none
	LogBytes        int64   `json:"log_bytes"`
	DatabaseBytes   int64   `json:"database_bytes"`
	Records         int64   `json:"records"`
	Patterns        int     `json:"patterns"`
	SeenOnceRecords int64   `json:"seen_once_records"`
	CatchAllRecords int64   `json:"catch_all_records"`
}

// span is the minimum and the maximum of a figure over the runs.
type span[T any] struct {
	Min T `json:"min"`
	Max T `json:"max"`
}

// summaryFigures holds the span of each figure.
type summaryFigures struct {
	WallSeconds     span[float64] `json:"wall_seconds"`
	CPUSeconds      span[float64] `json:"cpu_seconds"`
	PeakMemoryBytes span[*uint64] `json:"peak_memory_bytes"`
	LogBytes        span[int64]   `json:"log_bytes"`
	DatabaseBytes   span[int64]   `json:"database_bytes"`
	Records         span[int64]   `json:"records"`
	Patterns        span[int]     `json:"patterns"`
	SeenOnceRecords span[int64]   `json:"seen_once_records"`
	CatchAllRecords span[int64]   `json:"catch_all_records"`
}

// spanOf returns the span of one figure.
func spanOf[T cmp.Ordered](runs []runFigures, get func(runFigures) T) span[T] {
	s := span[T]{Min: get(runs[0]), Max: get(runs[0])}
	for _, r := range runs[1:] {
		s.Min = min(s.Min, get(r))
		s.Max = max(s.Max, get(r))
	}
	return s
}

// peakSpan is the span of the peak memory; it is null and null when any run
// has no figure, because a figure that is not measured is never invented.
func peakSpan(runs []runFigures) span[*uint64] {
	var s span[*uint64]
	for i, r := range runs {
		if r.PeakMemoryBytes == nil {
			return span[*uint64]{}
		}
		v := *r.PeakMemoryBytes
		if i == 0 {
			lo, hi := v, v
			s = span[*uint64]{Min: &lo, Max: &hi}
			continue
		}
		if v < *s.Min {
			lo := v
			s.Min = &lo
		}
		if v > *s.Max {
			hi := v
			s.Max = &hi
		}
	}
	return s
}

// summarize returns the spans of the runs, or nil when there are none.
func summarize(runs []runFigures) *summaryFigures {
	if len(runs) == 0 {
		return nil
	}
	return &summaryFigures{
		WallSeconds:     spanOf(runs, func(r runFigures) float64 { return r.WallSeconds }),
		CPUSeconds:      spanOf(runs, func(r runFigures) float64 { return r.CPUSeconds }),
		PeakMemoryBytes: peakSpan(runs),
		LogBytes:        spanOf(runs, func(r runFigures) int64 { return r.LogBytes }),
		DatabaseBytes:   spanOf(runs, func(r runFigures) int64 { return r.DatabaseBytes }),
		Records:         spanOf(runs, func(r runFigures) int64 { return r.Records }),
		Patterns:        spanOf(runs, func(r runFigures) int { return r.Patterns }),
		SeenOnceRecords: spanOf(runs, func(r runFigures) int64 { return r.SeenOnceRecords }),
		CatchAllRecords: spanOf(runs, func(r runFigures) int64 { return r.CatchAllRecords }),
	}
}

// sizeReport is what one size of log gave.
type sizeReport struct {
	Lines   int             `json:"lines"`
	Runs    []runFigures    `json:"runs"`
	Summary *summaryFigures `json:"summary"`
}

// machine states the machine the figures come from. It holds no user name,
// host name or path.
type machine struct {
	OS               string `json:"os"`
	Arch             string `json:"arch"`
	GoVersion        string `json:"go_version"`
	CPUs             int    `json:"cpus"`
	Program          string `json:"program"`
	LoadAverageStart string `json:"load_average_start,omitempty"`
	LoadAverageEnd   string `json:"load_average_end,omitempty"`
}

// notes are said in every report.
var notes = []string{
	"The background load of the machine was not controlled.",
	"The log is written just before the first run, so it is probably in the file cache for every run, and the first run can still be slower because the program, the database folder and the processor caches are not warm yet. The command does not drop the file cache (that needs administrator rights), so a figure says nothing about a cold-cache run.",
	"A figure is quoted as the range of two or more runs, never as a point.",
}

// document is the whole report.
type document struct {
	Format  int             `json:"format"`
	Kind    string          `json:"kind"`
	Seed    int64           `json:"seed"`
	Lines   int             `json:"lines"`
	Machine machine         `json:"machine"`
	Runs    []runFigures    `json:"runs"`
	Summary *summaryFigures `json:"summary"`
	Small   *sizeReport     `json:"small,omitempty"`
	Bars    []bar           `json:"bars,omitempty"`
	Notes   []string        `json:"notes"`
	Kept    string          `json:"kept_folder,omitempty"`
	Failure string          `json:"failure,omitempty"`
}

// measure runs every size and prints the report.
func (e *experiment) measure(ctx context.Context, stdout, stderr io.Writer) int {
	o := e.o
	doc := document{
		Format: 1, Kind: o.kind, Seed: o.seed, Lines: o.lines,
		Machine: machine{
			OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(),
			CPUs: runtime.NumCPU(), Program: e.programVersion(),
			LoadAverageStart: e.loadStart,
		},
		Runs:  []runFigures{},
		Notes: notes,
	}
	withSmall := o.bars && o.lines > o.smallLines
	var small *sizeReport
	if withSmall {
		small = &sizeReport{Lines: o.smallLines, Runs: []runFigures{}}
	}

	var failure error
	if small != nil {
		failure = e.runSize(ctx, "small", small.Lines, &small.Runs)
	}
	if failure == nil {
		failure = e.runSize(ctx, "", o.lines, &doc.Runs)
	}
	doc.Machine.LoadAverageEnd = loadAverage()
	doc.Summary = summarize(doc.Runs)
	if small != nil {
		small.Summary = summarize(small.Runs)
		doc.Small = small
	}
	if o.keep {
		doc.Kept = e.tmp
	}

	if o.bars {
		wanted := o.runs
		completed := len(doc.Runs)
		in := barInput{Lines: o.lines, SmallLines: o.smallLines, Big: sizeOf(o.lines, doc.Runs)}
		if small != nil {
			wanted += o.runs
			completed += len(small.Runs)
			in.Small = sizeOf(small.Lines, small.Runs)
		}
		in.Wanted, in.Completed = wanted, completed
		doc.Bars = decideBars(in)
	}

	if failure != nil {
		doc.Failure = failure.Error()
		fmt.Fprintf(stderr, "measure: %v\n", failure)
		if !o.bars {
			return exitcode.Failure
		}
	}
	var werr error
	if o.json {
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		werr = enc.Encode(doc)
	} else {
		_, werr = io.WriteString(stdout, renderText(doc))
	}
	if werr != nil {
		fmt.Fprintf(stderr, "measure: writing the report: %v\n", werr)
		return exitcode.Failure
	}
	if failure != nil {
		return exitcode.Failure
	}
	return exitcode.OK
}

// runSize writes the log of one size and runs the program on it o.runs times,
// appending the figures of each run that finished. It stops at the first run
// that fails.
func (e *experiment) runSize(ctx context.Context, prefix string, lines int, into *[]runFigures) error {
	logPath := filepath.Join(e.tmp, "logs", fmt.Sprintf("%s-%d.log", e.o.kind, lines))
	data := generate(e.o.kind, e.o.seed, lines)
	logBytes := int64(len(data))
	err := os.WriteFile(logPath, data, 0o600)
	// The log is not kept in memory while the program runs: the program is
	// started from this process, and what this process holds must not be
	// counted as the program's.
	data = nil
	if err != nil {
		return fmt.Errorf("writing the log: %w", unwrapPath(err))
	}
	for n := 1; n <= e.o.runs; n++ {
		name := fmt.Sprintf("run%d", n)
		if prefix != "" {
			name = prefix + "-" + name
		}
		fig, err := e.runOnce(ctx, logPath, logBytes, filepath.Join(e.tmp, "cache", name))
		if err != nil {
			return fmt.Errorf("run %d (%d lines) failed: %s", n, lines, e.scrub(err.Error()))
		}
		*into = append(*into, fig)
	}
	return nil
}

// generate makes the log of a kind.
func generate(kind string, seed int64, lines int) []byte {
	var data []byte
	switch kind {
	case "restart":
		data, _ = testlog.Restart(seed, lines)
	case "web":
		data, _ = testlog.Web(seed, lines)
	case "dense":
		data = testlog.Dense(seed, lines)
	default:
		data, _ = testlog.SSH(seed, lines)
	}
	return data
}

// tail keeps the end of what a program writes, so that a program that says a
// great deal cannot fill the memory that is being measured next to it.
type tail struct{ buf []byte }

const tailMax = 4096

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailMax {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-tailMax:]...)
	}
	return len(p), nil
}

// lastLine is the last line that is not empty, cut to 200 bytes.
func (t *tail) lastLine() string {
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}

// scrub removes what must not be printed from a message: the temporary
// folder's path, and any control character.
func (e *experiment) scrub(s string) string {
	return textsafe.Clean(strings.ReplaceAll(s, e.tmp, "<tmp>"))
}

// childEnv is the whole environment of the program: nothing is inherited, so
// that nothing can point it to the cache or the configuration of a user. The
// cache folder is its own for each run; the configuration file is one that
// does not exist, so the defaults apply; every folder the program or the
// system library may look in for a home, a cache, a configuration or a
// temporary file is a folder inside the temporary folder.
func (e *experiment) childEnv(cacheDir string) []string {
	home := filepath.Join(e.tmp, "home")
	scratch := filepath.Join(e.tmp, "scratch")
	env := []string{
		"AIGOLOG_CACHE_DIR=" + cacheDir,
		"AIGOLOG_CONFIG=" + filepath.Join(e.tmp, "no-such-config.json"),
		"HOME=" + home,
		"USERPROFILE=" + home,
		"XDG_CACHE_HOME=" + filepath.Join(home, "cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		"XDG_DATA_HOME=" + filepath.Join(home, "data"),
		"APPDATA=" + filepath.Join(home, "appdata"),
		"LOCALAPPDATA=" + filepath.Join(home, "localappdata"),
		"TMPDIR=" + scratch,
		"TMP=" + scratch,
		"TEMP=" + scratch,
	}
	if runtime.GOOS == "windows" {
		// The system folder is the same for every user and holds no user name.
		for _, k := range []string{"SystemRoot", "windir"} {
			if v := os.Getenv(k); v != "" {
				env = append(env, k+"="+v)
			}
		}
	}
	return env
}

// programVersion asks the program for its version: the first line of
// "aigolog version", or "unknown".
func (e *experiment) programVersion() string {
	cmd := exec.Command(e.bin, "version")
	cmd.Env = e.childEnv(filepath.Join(e.tmp, "cache", "version"))
	cmd.Dir = filepath.Join(e.tmp, "home")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	first, _, _ := strings.Cut(string(out), "\n")
	first = strings.TrimSpace(first)
	if first == "" {
		return "unknown"
	}
	return textsafe.Clean(first)
}

// runOnce runs "aigolog analyze" once on the log and returns its figures.
func (e *experiment) runOnce(ctx context.Context, logPath string, logBytes int64, cacheDir string) (runFigures, error) {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return runFigures{}, fmt.Errorf("making the cache folder: %w", unwrapPath(err))
	}
	cmd := exec.CommandContext(ctx, e.bin, "analyze", "--as-of", asOf, logPath)
	cmd.Env = e.childEnv(cacheDir)
	cmd.Dir = filepath.Join(e.tmp, "home")
	errTail := &tail{}
	cmd.Stderr = errTail

	// The time is taken around Start and Wait, not Run: the handle of the peak
	// memory is opened between the two.
	debug.FreeOSMemory()
	canMeasurePeak := resetPeak()
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return runFigures{}, fmt.Errorf("the program could not be started: %w", unwrapPath(err))
	}
	readPeak := peakMemory(cmd)
	waitErr := cmd.Wait()
	wall := time.Since(started)
	peak, havePeak := readPeak(cmd.ProcessState)
	havePeak = havePeak && canMeasurePeak

	if waitErr != nil {
		if ctx.Err() != nil {
			return runFigures{}, errors.New("interrupted")
		}
		msg := waitErr.Error()
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			if code := ee.ExitCode(); code >= 0 {
				msg = fmt.Sprintf("the program exited with code %d", code)
			} else {
				msg = "the program was ended: " + ee.String()
			}
		}
		if line := errTail.lastLine(); line != "" {
			msg += ": " + line
		}
		return runFigures{}, errors.New(msg)
	}

	fig := runFigures{
		WallSeconds: roundMillis(wall.Seconds()),
		CPUSeconds:  roundMillis((cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime()).Seconds()),
		LogBytes:    logBytes,
	}
	if havePeak {
		p := peak
		fig.PeakMemoryBytes = &p
	}
	if err := readDatabase(ctx, cacheDir, &fig); err != nil {
		return runFigures{}, err
	}
	return fig, nil
}

// roundMillis rounds seconds to a thousandth.
func roundMillis(s float64) float64 { return math.Round(s*1000) / 1000 }

// readDatabase opens the one database the run made in its own cache folder
// and takes the figures from it.
func readDatabase(ctx context.Context, cacheDir string, fig *runFigures) error {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return fmt.Errorf("reading the cache folder of the run: %w", unwrapPath(err))
	}
	var dbs []string
	for _, ent := range entries {
		if ent.Type().IsRegular() && strings.HasSuffix(ent.Name(), ".db") {
			dbs = append(dbs, filepath.Join(cacheDir, ent.Name()))
		}
	}
	if len(dbs) != 1 {
		return fmt.Errorf("the run's cache folder holds %d database files, want 1", len(dbs))
	}
	db, err := store.Open(ctx, dbs[0])
	if err != nil {
		return fmt.Errorf("opening the database of the run: %w", unwrapPath(err))
	}
	defer db.Close()
	rep, err := db.MinerReport(ctx)
	if err != nil {
		return err
	}
	size, err := db.Size()
	if err != nil {
		return fmt.Errorf("reading the size of the database: %w", unwrapPath(err))
	}
	fig.DatabaseBytes = size
	fig.Records = rep.Records
	fig.Patterns = rep.Patterns
	fig.SeenOnceRecords = rep.SeenOnce
	fig.CatchAllRecords = rep.CatchAll
	return nil
}

// rusageBytes turns the Maxrss of a resource usage into bytes: it is in
// kibibytes everywhere but on darwin, where it is in bytes. A figure of zero
// or less is "not measured", never a figure.
func rusageBytes(maxrss int64, goos string) (uint64, bool) {
	if maxrss <= 0 {
		return 0, false
	}
	if goos == "darwin" {
		return uint64(maxrss), true
	}
	return uint64(maxrss) * 1024, true
}

// loadAverage returns the three load averages of Linux, or "" on a system
// that does not give them.
func loadAverage() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	data, err := os.ReadFile(loadavgPath)
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(bytes.TrimSpace(data)))
	if len(fields) < 3 {
		return ""
	}
	return strings.Join(fields[:3], " ")
}

// sizeFigures are the figures of the runs of one size that the bars use. Each
// is the worst of the runs: the longest wall time, the highest peak, the
// largest database.
type sizeFigures struct {
	Lines       int
	MaxWall     float64
	MaxPeak     *uint64 // nil when any run has no peak memory
	LogBytes    int64
	MaxDatabase int64
}

// sizeOf returns the figures of the runs of a size, or nil when none finished.
func sizeOf(lines int, runs []runFigures) *sizeFigures {
	if len(runs) == 0 {
		return nil
	}
	s := summarize(runs)
	return &sizeFigures{
		Lines:       lines,
		MaxWall:     s.WallSeconds.Max,
		MaxPeak:     s.PeakMemoryBytes.Max,
		LogBytes:    s.LogBytes.Max,
		MaxDatabase: s.DatabaseBytes.Max,
	}
}

// The results of a bar.
const (
	resultMet        = "met"
	resultNotMet     = "not met"
	resultNotDecided = "not decided"
)

// bar is one of the four bars of the acceptance record.
type bar struct {
	Number  int            `json:"number"`
	Name    string         `json:"name"`
	Result  string         `json:"result"`
	Detail  string         `json:"detail"`
	Figures map[string]any `json:"figures"`
}

// barInput is what the bars are decided from.
type barInput struct {
	Lines, SmallLines int
	// Wanted and Completed count the runs of every size.
	Wanted, Completed int
	// Big is the size of -lines and Small the smaller size; each is nil when
	// no run of it finished (or, for Small, it was not run).
	Big, Small *sizeFigures
}

// decideBars decides the four bars. A bar whose figure is not measured is
// "not decided", never "met".
func decideBars(in barInput) []bar {
	b1 := bar{Number: 1, Name: "all runs completed", Figures: map[string]any{
		"runs_completed": in.Completed, "runs_wanted": in.Wanted,
	}}
	if in.Wanted > 0 && in.Completed == in.Wanted {
		b1.Result = resultMet
	} else {
		b1.Result = resultNotMet
	}
	b1.Detail = fmt.Sprintf("%d of %d runs", in.Completed, in.Wanted)

	b2 := bar{Number: 2, Figures: map[string]any{
		"peak_bytes": nil, "small_peak_bytes": nil, "limit_bytes": nil,
	}}
	b2.Name = fmt.Sprintf("peak memory at %d lines is at most %g times that at %d lines plus %s", in.Lines, memoryFactor, in.SmallLines, fmtBytes(uint64(memorySlackBytes)))
	switch {
	case in.Small == nil:
		b2.Result, b2.Detail = resultNotDecided, "there is no second size: -lines is not larger than -small-lines, or the smaller size did not finish"
	case in.Big == nil:
		b2.Result, b2.Detail = resultNotDecided, "no run at "+fmt.Sprint(in.Lines)+" lines finished"
	case in.Big.MaxPeak == nil || in.Small.MaxPeak == nil:
		b2.Result, b2.Detail = resultNotDecided, "peak memory: not measured on this system"
	default:
		limit := memoryFactor*float64(*in.Small.MaxPeak) + memorySlackBytes
		b2.Figures["peak_bytes"] = *in.Big.MaxPeak
		b2.Figures["small_peak_bytes"] = *in.Small.MaxPeak
		b2.Figures["limit_bytes"] = limit
		if float64(*in.Big.MaxPeak) <= limit {
			b2.Result = resultMet
		} else {
			b2.Result = resultNotMet
		}
		b2.Detail = fmt.Sprintf("%s at %d lines, %s at %d lines, limit %s", fmtBytes(*in.Big.MaxPeak), in.Lines, fmtBytes(*in.Small.MaxPeak), in.SmallLines, fmtBytes(uint64(limit)))
	}

	b3 := bar{Number: 3, Figures: map[string]any{
		"wall_seconds": nil, "limit_seconds": wallLimit.Seconds(),
	}}
	b3.Name = fmt.Sprintf("wall time at %d lines is at most %s", in.Lines, fmtSeconds(wallLimit.Seconds()))
	if in.Big == nil {
		b3.Result, b3.Detail = resultNotDecided, "no run finished"
	} else {
		b3.Figures["wall_seconds"] = in.Big.MaxWall
		if in.Big.MaxWall <= wallLimit.Seconds() {
			b3.Result = resultMet
		} else {
			b3.Result = resultNotMet
		}
		b3.Detail = fmt.Sprintf("%s for the slowest run, limit %s", fmtSeconds(in.Big.MaxWall), fmtSeconds(wallLimit.Seconds()))
	}

	b4 := bar{Number: 4, Figures: map[string]any{
		"database_bytes": nil, "log_bytes": nil, "limit_bytes": nil,
	}}
	b4.Name = fmt.Sprintf("the database is at most %g times the size of the log", databaseFactor)
	if in.Big == nil || in.Big.LogBytes <= 0 {
		b4.Result, b4.Detail = resultNotDecided, "no run finished"
	} else {
		limit := databaseFactor * float64(in.Big.LogBytes)
		b4.Figures["database_bytes"] = in.Big.MaxDatabase
		b4.Figures["log_bytes"] = in.Big.LogBytes
		b4.Figures["limit_bytes"] = limit
		if float64(in.Big.MaxDatabase) <= limit {
			b4.Result = resultMet
		} else {
			b4.Result = resultNotMet
		}
		b4.Detail = fmt.Sprintf("database %s for a log of %s, %.2f times, limit %g times", fmtBytes(uint64(in.Big.MaxDatabase)), fmtBytes(uint64(in.Big.LogBytes)), float64(in.Big.MaxDatabase)/float64(in.Big.LogBytes), databaseFactor)
	}
	return []bar{b1, b2, b3, b4}
}

// fmtSeconds writes a time in seconds.
func fmtSeconds(s float64) string { return fmt.Sprintf("%.2f s", s) }

// fmtBytes writes a size with a binary unit.
func fmtBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// rangeText writes a figure as "low to high" when there are two runs or more,
// and as the one value when there is one run.
func rangeText(runs int, lo, hi string) string {
	if runs < 2 {
		return lo
	}
	return lo + " to " + hi
}

// renderText is the report for a person.
func renderText(d document) string {
	var b strings.Builder
	runWord := "runs"
	if len(d.Runs) == 1 {
		runWord = "run"
	}
	fmt.Fprintf(&b, "measure: %s log of %d lines, seed %d, %d %s of aigolog analyze\n\n", d.Kind, d.Lines, d.Seed, len(d.Runs), runWord)

	m := d.Machine
	b.WriteString("Machine\n")
	fmt.Fprintf(&b, "  system: %s/%s\n", m.OS, m.Arch)
	fmt.Fprintf(&b, "  go: %s\n", m.GoVersion)
	fmt.Fprintf(&b, "  cpus: %d\n", m.CPUs)
	fmt.Fprintf(&b, "  program: %s\n", m.Program)
	if m.LoadAverageStart != "" {
		fmt.Fprintf(&b, "  load average at start: %s\n", m.LoadAverageStart)
	}
	if m.LoadAverageEnd != "" {
		fmt.Fprintf(&b, "  load average at end: %s\n", m.LoadAverageEnd)
	}
	b.WriteString("\n")

	writeFigures(&b, d.Lines, d.Runs, d.Summary)
	if d.Small != nil {
		writeFigures(&b, d.Small.Lines, d.Small.Runs, d.Small.Summary)
	}

	if len(d.Bars) > 0 {
		b.WriteString("Bars (the owner's provisional bars of A6; the exit code does not depend on them)\n")
		for _, br := range d.Bars {
			fmt.Fprintf(&b, "  %d. %s: %s (%s)\n", br.Number, br.Name, br.Result, br.Detail)
		}
		b.WriteString("\n")
	}

	b.WriteString("Notes\n")
	for _, n := range d.Notes {
		fmt.Fprintf(&b, "  %s\n", n)
	}
	if d.Kept != "" {
		fmt.Fprintf(&b, "\nkept in: %s\n", d.Kept)
	}
	return b.String()
}

// writeFigures writes the figures of one size.
func writeFigures(b *strings.Builder, lines int, runs []runFigures, s *summaryFigures) {
	if s == nil {
		fmt.Fprintf(b, "Figures at %d lines\n  no run finished\n\n", lines)
		return
	}
	n := len(runs)
	title := "range of " + fmt.Sprint(n) + " runs"
	if n == 1 {
		title = "1 run"
	}
	fmt.Fprintf(b, "Figures at %d lines (%s)\n", lines, title)
	row := func(label, lo, hi string) {
		fmt.Fprintf(b, "  %s: %s\n", label, rangeText(n, lo, hi))
	}
	row("wall time", fmtSeconds(s.WallSeconds.Min), fmtSeconds(s.WallSeconds.Max))
	row("cpu time (user and system)", fmtSeconds(s.CPUSeconds.Min), fmtSeconds(s.CPUSeconds.Max))
	if s.PeakMemoryBytes.Min == nil || s.PeakMemoryBytes.Max == nil {
		b.WriteString("  peak memory: not measured on this system\n")
	} else {
		row("peak memory", fmtBytes(*s.PeakMemoryBytes.Min), fmtBytes(*s.PeakMemoryBytes.Max))
	}
	row("log size", fmtBytes(uint64(s.LogBytes.Min)), fmtBytes(uint64(s.LogBytes.Max)))
	row("database size", fmtBytes(uint64(s.DatabaseBytes.Min)), fmtBytes(uint64(s.DatabaseBytes.Max)))
	row("records", fmt.Sprint(s.Records.Min), fmt.Sprint(s.Records.Max))
	row("patterns", fmt.Sprint(s.Patterns.Min), fmt.Sprint(s.Patterns.Max))
	row("seen once (records in patterns of one record)", fmt.Sprint(s.SeenOnceRecords.Min), fmt.Sprint(s.SeenOnceRecords.Max))
	row("catch-all (records in no pattern)", fmt.Sprint(s.CatchAllRecords.Min), fmt.Sprint(s.CatchAllRecords.Max))
	b.WriteString("\n")
}
