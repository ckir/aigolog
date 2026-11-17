// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/testlog"
)

// binDir holds the programs the tests build once: the real aigolog, and the
// stubs. It is made by TestMain and removed after the last test.
var (
	binDir      string
	programOnce sync.Once
	programPath string
	programErr  error
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "measure-test-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// exe adds the suffix of a program on this system.
func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// program builds ./cmd/aigolog once and returns its path.
func program(t *testing.T) string {
	t.Helper()
	programOnce.Do(func() {
		goTool, err := exec.LookPath("go")
		if err != nil {
			programErr = err
			return
		}
		programPath = filepath.Join(binDir, exe("aigolog"))
		cmd := exec.Command(goTool, "build", "-o", programPath, "github.com/ckir/aigolog/cmd/aigolog")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			programErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if programErr != nil {
		t.Fatal(programErr)
	}
	return programPath
}

// exitingProgram builds a program that exits with code and returns its path.
func exitingProgram(t *testing.T, code int) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "stub.go")
	text := fmt.Sprintf("package main\n\nimport \"os\"\n\nfunc main() { os.Exit(%d) }\n", code)
	if err := os.WriteFile(src, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, exe("stub"))
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build of the stub: %v\n%s", err, b)
	}
	return out
}

// measure runs the command and returns its exit code and what it wrote.
func measure(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// decode reads the JSON document of a run.
func decode(t *testing.T, doc string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("the output is not one JSON document: %v\n%s", err, doc)
	}
	return m
}

// withPeak replaces peakMemory for the rest of the test.
func withPeak(t *testing.T, f func(*exec.Cmd) peakReader) {
	t.Helper()
	old := peakMemory
	peakMemory = f
	t.Cleanup(func() { peakMemory = old })
}

// unavailablePeak is a system that gives no peak memory.
func unavailablePeak(*exec.Cmd) peakReader {
	return func(*os.ProcessState) (uint64, bool) { return 0, false }
}

func TestMeasureRunsASmallLogAndReportsEveryFigure(t *testing.T) {
	bin := program(t)
	code, out, errOut := measure(t, "-lines", "2000", "-runs", "1", "-bin", bin)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	for _, want := range []string{
		"wall time", "cpu time", "peak memory", "log size", "database size",
		"records", "patterns", "seen once", "catch-all",
		runtime.GOOS + "/" + runtime.GOARCH, runtime.Version(), "cpus", "program",
		"background load of the machine was not controlled",
		"probably in the file cache", "does not drop the file cache", "range of two or more runs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the text does not say %q:\n%s", want, out)
		}
	}
	// The old claim, that the first run starts cold, is false: the log is written
	// just before it. It must not come back.
	for _, old := range []string{"starts with a cold", "cold file cache"} {
		if strings.Contains(out, old) {
			t.Errorf("the text claims %q about the first run:\n%s", old, out)
		}
	}
	if runtime.GOOS == "linux" {
		if strings.Contains(out, "not measured on this system") {
			t.Errorf("Linux gives a peak memory, the text says it is not measured:\n%s", out)
		}
		if !strings.Contains(out, "load average at start") {
			t.Errorf("Linux gives a load average, the text has none:\n%s", out)
		}
	}
	// 2,000 lines of the ssh generator are 2,000 records, none lost.
	if !regexp.MustCompile(`(?m)^\s*records:\s+2000$`).MatchString(out) {
		t.Errorf("the text does not give 2000 records:\n%s", out)
	}
	// It names no path of this machine.
	for _, private := range []string{os.TempDir(), binDir} {
		if private != "" && strings.Contains(out+errOut, private) {
			t.Errorf("the report names the path %q:\n%s", private, out)
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" && strings.Contains(out+errOut, home) {
		t.Errorf("the report names the home folder %q", home)
	}
	if errOut != "" {
		t.Errorf("stderr %q, want nothing", errOut)
	}

	// The figures behind the text.
	_, doc, _ := measure(t, "-lines", "2000", "-runs", "1", "-bin", bin, "-json")
	runs := decode(t, doc)["runs"].([]any)
	r := runs[0].(map[string]any)
	if r["records"].(float64) != 2000 {
		t.Errorf("records = %v, want 2000", r["records"])
	}
	if r["patterns"].(float64) < 1 || r["database_bytes"].(float64) < 1 || r["log_bytes"].(float64) < 2000 {
		t.Errorf("a figure is missing or zero: %v", r)
	}
	if r["wall_seconds"].(float64) <= 0 {
		t.Errorf("wall_seconds = %v", r["wall_seconds"])
	}
	if runtime.GOOS == "linux" && (r["peak_memory_bytes"] == nil || r["peak_memory_bytes"].(float64) < 1<<20) {
		t.Errorf("peak_memory_bytes = %v, want a figure of at least 1 MiB", r["peak_memory_bytes"])
	}
}

func TestMeasureJSONHasTheDocumentedKeys(t *testing.T) {
	bin := program(t)
	code, out, errOut := measure(t, "-lines", "2000", "-runs", "2", "-bin", bin, "-json")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if !strings.HasSuffix(out, "}\n") || strings.HasSuffix(out, "\n\n") {
		t.Errorf("the document must be one JSON value and end in one newline: %q", out[max(0, len(out)-20):])
	}
	if !strings.Contains(out, "\n  \"machine\": {") {
		t.Errorf("the document is not indented with two spaces:\n%s", out)
	}
	doc := decode(t, out)
	for _, k := range []string{"machine", "runs", "summary"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("the document has no key %q", k)
		}
	}
	figures := []string{"wall_seconds", "cpu_seconds", "peak_memory_bytes", "log_bytes", "database_bytes", "records", "patterns", "seen_once_records", "catch_all_records"}
	runs := doc["runs"].([]any)
	if len(runs) != 2 {
		t.Fatalf("%d runs, want 2", len(runs))
	}
	for i, r := range runs {
		for _, k := range figures {
			if _, ok := r.(map[string]any)[k]; !ok {
				t.Errorf("run %d has no key %q", i+1, k)
			}
		}
	}
	sum := doc["summary"].(map[string]any)
	for _, k := range figures {
		span, ok := sum[k].(map[string]any)
		if !ok {
			t.Errorf("the summary has no entry for %q", k)
			continue
		}
		if _, ok := span["min"]; !ok {
			t.Errorf("the summary of %q has no min", k)
		}
		if _, ok := span["max"]; !ok {
			t.Errorf("the summary of %q has no max", k)
		}
	}
	// The minimum and the maximum are those of the runs.
	for _, k := range []string{"wall_seconds", "log_bytes", "records"} {
		a := runs[0].(map[string]any)[k].(float64)
		b := runs[1].(map[string]any)[k].(float64)
		span := sum[k].(map[string]any)
		if span["min"].(float64) != min(a, b) || span["max"].(float64) != max(a, b) {
			t.Errorf("the summary of %q is %v, the runs gave %v and %v", k, span, a, b)
		}
	}
	machine := doc["machine"].(map[string]any)
	if machine["os"] != runtime.GOOS || machine["arch"] != runtime.GOARCH {
		t.Errorf("machine = %v", machine)
	}
}

func TestMeasureWithTwoRunsReportsARangeNotAPoint(t *testing.T) {
	bin := program(t)
	code, out, errOut := measure(t, "-lines", "2000", "-runs", "2", "-bin", bin)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	for _, label := range []string{"wall time", "cpu time", "log size", "database size", "records", "patterns", "seen once", "catch-all"} {
		re := regexp.MustCompile(`(?m)^\s*` + label + `[^:\n]*:\s+\S+( \S+)? to \S+( \S+)?$`)
		if !re.MatchString(out) {
			t.Errorf("%q is not shown as `low to high`:\n%s", label, out)
		}
	}
	re := regexp.MustCompile(`(?m)^\s*peak memory:\s+(\S+ \S+ to \S+ \S+|not measured on this system)$`)
	if !re.MatchString(out) {
		t.Errorf("the peak memory is not a range and not the not-measured sentence:\n%s", out)
	}
	// One run is a point.
	_, one, _ := measure(t, "-lines", "2000", "-runs", "1", "-bin", bin)
	if strings.Contains(one, " to ") {
		t.Errorf("one run shows a range:\n%s", one)
	}
}

func TestBarsAreDecidedFromTwoSizesAndNamedWithTheirFigures(t *testing.T) {
	bin := program(t)
	args := []string{"-bars", "-lines", "3000", "-small-lines", "1000", "-runs", "1", "-bin", bin}
	results := map[string]bool{"met": true, "not met": true, "not decided": true}

	t.Run("text and JSON hold the four bars", func(t *testing.T) {
		code, out, errOut := measure(t, args...)
		if code != 0 {
			t.Fatalf("exit %d, stderr %q", code, errOut)
		}
		re := regexp.MustCompile(`(?m)^\s*([1-4])\. .*: (met|not met|not decided) \(.+\)$`)
		if got := re.FindAllStringSubmatch(out, -1); len(got) != 4 {
			t.Errorf("the text holds %d bars, want 4:\n%s", len(got), out)
		}
		_, doc, _ := measure(t, append(args, "-json")...)
		d := decode(t, doc)
		bars := d["bars"].([]any)
		if len(bars) != 4 {
			t.Fatalf("%d bars in the JSON, want 4", len(bars))
		}
		for i, b := range bars {
			bar := b.(map[string]any)
			if bar["number"].(float64) != float64(i+1) {
				t.Errorf("bar %d has number %v", i+1, bar["number"])
			}
			if res, _ := bar["result"].(string); !results[res] {
				t.Errorf("bar %d result %q", i+1, bar["result"])
			}
			if f, _ := bar["figures"].(map[string]any); len(f) == 0 {
				t.Errorf("bar %d has no figures", i+1)
			}
		}
		if bars[0].(map[string]any)["result"] != "met" || bars[2].(map[string]any)["result"] != "met" {
			t.Errorf("bars 1 and 3 are not met on a small log: %v", bars)
		}
		small := d["small"].(map[string]any)
		if small["lines"].(float64) != 1000 || len(small["runs"].([]any)) != 1 {
			t.Errorf("the small size is not in the document: %v", small)
		}
		if d["lines"].(float64) != 3000 {
			t.Errorf("lines = %v", d["lines"])
		}
	})

	t.Run("a wall-time bar of 1 ns is not met", func(t *testing.T) {
		old := wallLimit
		wallLimit = time.Nanosecond
		t.Cleanup(func() { wallLimit = old })
		_, doc, _ := measure(t, append(args, "-json")...)
		bars := decode(t, doc)["bars"].([]any)
		if got := bars[2].(map[string]any)["result"]; got != "not met" {
			t.Errorf("the wall-time bar is %v with a limit of 1 ns, want not met", got)
		}
		_, out, _ := measure(t, args...)
		if !regexp.MustCompile(`(?m)^\s*3\. .*: not met \(`).MatchString(out) {
			t.Errorf("the text does not say bar 3 is not met:\n%s", out)
		}
	})

	t.Run("an impossible memory bar is not met", func(t *testing.T) {
		oldF, oldS := memoryFactor, memorySlackBytes
		memoryFactor, memorySlackBytes = 0, 0
		t.Cleanup(func() { memoryFactor, memorySlackBytes = oldF, oldS })
		_, doc, _ := measure(t, append(args, "-json")...)
		d := decode(t, doc)
		measured := d["runs"].([]any)[0].(map[string]any)["peak_memory_bytes"] != nil
		want := "not decided"
		if measured {
			want = "not met"
		}
		if got := d["bars"].([]any)[1].(map[string]any)["result"]; got != want {
			t.Errorf("the memory bar is %v, want %s (peak measured: %v)", got, want, measured)
		}
	})

	t.Run("a log no larger than the small size has no second size", func(t *testing.T) {
		_, doc, _ := measure(t, "-bars", "-lines", "1000", "-small-lines", "1000", "-runs", "1", "-bin", bin, "-json")
		d := decode(t, doc)
		if got := d["bars"].([]any)[1].(map[string]any)["result"]; got != "not decided" {
			t.Errorf("bar 2 with one size is %v, want not decided", got)
		}
		if _, ok := d["small"]; ok {
			t.Error("a small size was run although -lines is not larger than -small-lines")
		}
	})

	t.Run("boundaries", func(t *testing.T) {
		const mib = 1 << 20
		peak := func(v uint64) *uint64 { return &v }
		input := func(big, small *uint64, wall float64, db, logBytes int64) barInput {
			return barInput{
				Wanted: 2, Completed: 2,
				Big:   &sizeFigures{Lines: 20, MaxWall: wall, MaxPeak: big, LogBytes: logBytes, MaxDatabase: db},
				Small: &sizeFigures{Lines: 10, MaxPeak: small},
			}
		}
		result := func(in barInput, n int) string { return decideBars(in)[n-1].Result }

		// Bar 2: at most 1.5 times the small peak plus 16 MiB.
		small := uint64(64 * mib)
		limit := uint64(1.5*float64(small)) + 16*mib
		for _, c := range []struct {
			name string
			big  uint64
			want string
		}{
			{"exactly at the limit", limit, "met"},
			{"one byte over", limit + 1, "not met"},
			{"over the factor alone but within the 16 MiB", uint64(1.5*float64(small)) + 1, "met"},
			{"far under", 1, "met"},
		} {
			if got := result(input(peak(c.big), peak(small), 1, 1, 1), 2); got != c.want {
				t.Errorf("bar 2, %s: %s, want %s", c.name, got, c.want)
			}
		}
		// Bar 3: at most the limit.
		lim := wallLimit.Seconds()
		if got := result(input(peak(1), peak(1), lim, 1, 1), 3); got != "met" {
			t.Errorf("bar 3 at exactly the limit: %s, want met", got)
		}
		if got := result(input(peak(1), peak(1), lim+0.001, 1, 1), 3); got != "not met" {
			t.Errorf("bar 3 over the limit: %s, want not met", got)
		}
		// Bar 4: at most 6 times the log.
		if got := result(input(peak(1), peak(1), 1, 600, 100), 4); got != "met" {
			t.Errorf("bar 4 at exactly 6 times: %s, want met", got)
		}
		if got := result(input(peak(1), peak(1), 1, 601, 100), 4); got != "not met" {
			t.Errorf("bar 4 over 6 times: %s, want not met", got)
		}
		// Bar 1: every run completed.
		in := input(peak(1), peak(1), 1, 1, 1)
		in.Completed = 1
		if got := result(in, 1); got != "not met" {
			t.Errorf("bar 1 with a run missing: %s, want not met", got)
		}
	})
}

func TestABarWithNoFigureIsNotDecidedNeverMet(t *testing.T) {
	bin := program(t)
	withPeak(t, unavailablePeak)
	args := []string{"-bars", "-lines", "3000", "-small-lines", "1000", "-runs", "1", "-bin", bin}
	code, out, errOut := measure(t, args...)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if !regexp.MustCompile(`(?m)^\s*2\. .*: not decided \(`).MatchString(out) {
		t.Errorf("bar 2 is not `not decided` when no peak memory is measured:\n%s", out)
	}
	_, doc, _ := measure(t, append(args, "-json")...)
	if got := decode(t, doc)["bars"].([]any)[1].(map[string]any)["result"]; got != "not decided" {
		t.Errorf("bar 2 in the JSON is %v, want not decided", got)
	}

	// The same through the pure decision, with one of the two figures missing.
	peak := uint64(1)
	for _, in := range []barInput{
		{Wanted: 1, Completed: 1, Big: &sizeFigures{MaxPeak: nil}, Small: &sizeFigures{MaxPeak: &peak}},
		{Wanted: 1, Completed: 1, Big: &sizeFigures{MaxPeak: &peak}, Small: &sizeFigures{MaxPeak: nil}},
		{Wanted: 1, Completed: 1, Big: &sizeFigures{MaxPeak: &peak}, Small: nil},
		{Wanted: 1, Completed: 1, Big: nil, Small: nil},
	} {
		if got := decideBars(in)[1].Result; got != "not decided" {
			t.Errorf("bar 2 with a missing figure is %q, want not decided: %+v", got, in)
		}
	}
	if got := decideBars(barInput{Wanted: 1, Completed: 1})[2].Result; got != "not decided" {
		t.Errorf("bar 3 with no figure is %q, want not decided", got)
	}
	if got := decideBars(barInput{Wanted: 1, Completed: 1})[3].Result; got != "not decided" {
		t.Errorf("bar 4 with no figure is %q, want not decided", got)
	}
}

func TestMeasureAcceptsTheDenseKind(t *testing.T) {
	// Through the stub: the kind passes the check and the run is started.
	stub := exitingProgram(t, 3)
	code, _, errOut := measure(t, "-kind", "dense", "-lines", "20", "-runs", "1", "-bin", stub)
	if code != 1 || !strings.Contains(errOut, "run 1") || !strings.Contains(errOut, "code 3") {
		t.Errorf("exit %d, stderr %q: want the failure of run 1 with code 3, not a usage error", code, errOut)
	}
	// Through the real program: the log is the dense one.
	code, out, errOut := measure(t, "-kind", "dense", "-lines", "20", "-runs", "1", "-bin", program(t), "-json")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	doc := decode(t, out)
	if got := doc["kind"]; got != "dense" {
		t.Errorf("kind = %v, want dense", got)
	}
	run := doc["runs"].([]any)[0].(map[string]any)
	if got, want := run["log_bytes"], float64(len(testlog.Dense(1, 20))); got != want {
		t.Errorf("log_bytes = %v, want %v, the size of the dense log of 20 lines", got, want)
	}
}

func TestAnUnknownKindOrBadFlagExitsWithTwo(t *testing.T) {
	for _, args := range [][]string{
		{"-kind", "json"},
		{"-kind", ""},
		{"-nope"},
		{"-runs", "0"},
		{"-runs", "-1"},
		{"-runs", "many"},
		{"-lines", "0"},
		{"-small-lines", "0"},
		{"-seed", "x"},
		{"extra"},
	} {
		code, out, errOut := measure(t, args...)
		if code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
		if out != "" || !strings.Contains(errOut, "measure") {
			t.Errorf("%q: stdout %q, stderr %q: want a message on stderr only", args, out, errOut)
		}
	}
}

func TestItLeavesNothingBehindUnlessKeepIsGiven(t *testing.T) {
	bin := program(t)
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	t.Setenv("TMP", scratch)
	t.Setenv("TEMP", scratch)
	count := func() int {
		entries, err := os.ReadDir(scratch)
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := count()

	if code, _, errOut := measure(t, "-lines", "500", "-runs", "1", "-bin", bin); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if after := count(); after != before {
		entries, _ := os.ReadDir(scratch)
		t.Errorf("%d entries left in the temporary folder: %v", after-before, entries)
	}

	code, out, errOut := measure(t, "-lines", "500", "-runs", "1", "-bin", bin, "-keep")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	m := regexp.MustCompile(`(?m)^kept in: (.+)$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("-keep does not print the folder:\n%s", out)
	}
	kept := strings.TrimSpace(m[1])
	if filepath.Dir(kept) != scratch {
		t.Errorf("the kept folder %q is not in the temporary folder %q", kept, scratch)
	}
	logs, _ := filepath.Glob(filepath.Join(kept, "logs", "*.log"))
	dbs, _ := filepath.Glob(filepath.Join(kept, "cache", "run1", "*.db"))
	if len(logs) != 1 || len(dbs) != 1 {
		t.Errorf("the kept folder holds %d logs and %d databases, want 1 and 1", len(logs), len(dbs))
	}
	if after := count(); after != before+1 {
		t.Errorf("%d entries with -keep, want %d", after, before+1)
	}
}

func TestAFailingRunExitsWithOneAndSaysWhich(t *testing.T) {
	stub := exitingProgram(t, 3)
	code, out, errOut := measure(t, "-lines", "500", "-runs", "2", "-bin", stub)
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "run 1") || !strings.Contains(errOut, "code 3") {
		t.Errorf("the message does not name the run and the exit code: %q", errOut)
	}
	if out != "" {
		t.Errorf("stdout %q, want nothing without -bars", out)
	}
	// A run stops the others: no second run is tried.
	if strings.Contains(errOut, "run 2") {
		t.Errorf("run 2 was tried after run 1 failed: %q", errOut)
	}

	// With -bars the first bar says so and the others are not decided.
	code, out, _ = measure(t, "-bars", "-lines", "500", "-small-lines", "100", "-runs", "1", "-bin", stub)
	if code != 1 {
		t.Errorf("with -bars: exit %d, want 1", code)
	}
	if !regexp.MustCompile(`(?m)^\s*1\. .*: not met \(`).MatchString(out) {
		t.Errorf("bar 1 is not `not met` after a failed run:\n%s", out)
	}
	if strings.Contains(out, ": met (") {
		t.Errorf("a bar is met after a failed run:\n%s", out)
	}
}

func TestNoMeasuredFigureIsInventedWhenThePeakIsUnavailable(t *testing.T) {
	bin := program(t)
	withPeak(t, unavailablePeak)
	code, out, errOut := measure(t, "-lines", "500", "-runs", "2", "-bin", bin)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if !strings.Contains(out, "peak memory: not measured on this system") {
		t.Errorf("the text does not have the not-measured sentence:\n%s", out)
	}
	_, doc, _ := measure(t, "-lines", "500", "-runs", "2", "-bin", bin, "-json")
	if !strings.Contains(doc, `"peak_memory_bytes": null`) {
		t.Errorf("the JSON does not have a null peak:\n%s", doc)
	}
	d := decode(t, doc)
	for i, r := range d["runs"].([]any) {
		if v, ok := r.(map[string]any)["peak_memory_bytes"]; !ok || v != nil {
			t.Errorf("run %d peak_memory_bytes = %v, want null", i+1, v)
		}
	}
	span := d["summary"].(map[string]any)["peak_memory_bytes"].(map[string]any)
	if span["min"] != nil || span["max"] != nil {
		t.Errorf("the summary of the peak is %v, want null and null", span)
	}
}

func TestWithoutGoAndWithoutBinTheCommandSaysSoAndExitsWithOne(t *testing.T) {
	t.Setenv("PATH", "")
	code, out, errOut := measure(t, "-lines", "100", "-runs", "1")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut, "go") || !strings.Contains(errOut, "-bin") || out != "" {
		t.Errorf("stdout %q, stderr %q: want a message that names go and -bin", out, errOut)
	}
}

func TestRusageBytes(t *testing.T) {
	for _, c := range []struct {
		maxrss int64
		goos   string
		want   uint64
		ok     bool
	}{
		{2048, "linux", 2048 * 1024, true},
		{2048, "freebsd", 2048 * 1024, true},
		{2048, "darwin", 2048, true},
		{0, "linux", 0, false},
		{0, "darwin", 0, false},
		{-1, "linux", 0, false},
	} {
		got, ok := rusageBytes(c.maxrss, c.goos)
		if got != c.want || ok != c.ok {
			t.Errorf("rusageBytes(%d, %q) = %d, %v; want %d, %v", c.maxrss, c.goos, got, ok, c.want, c.ok)
		}
	}
}

// holdAndDrop makes this process hold a lot of memory and let it go, as the
// command does with a large log before it starts the program.
func holdAndDrop(n int) {
	ballast := make([]byte, n)
	for i := 0; i < len(ballast); i += 4096 {
		ballast[i] = 1
	}
}

func TestThePeakMemoryIsTheProgramsOwnAndNotThatOfTheCommand(t *testing.T) {
	bin := program(t)
	// On Linux a child started from a process that once held 256 MiB would
	// report at least 256 MiB, because the high-water mark of the parent's
	// memory carries into the child at the exec. The log of a real run is that
	// kind of memory.
	const ballast = 256 << 20
	holdAndDrop(ballast)
	_, doc, errOut := measure(t, "-lines", "500", "-runs", "1", "-bin", bin, "-json")
	runs, _ := decode(t, doc)["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("no run: %q", errOut)
	}
	peak := runs[0].(map[string]any)["peak_memory_bytes"]
	if peak == nil {
		t.Skip("this system gives no peak memory here, so there is no figure to check")
	}
	if got := peak.(float64); got >= ballast/2 {
		t.Errorf("the peak memory of a 500-line run is %.0f MiB after this process held and freed %d MiB: the figure is the command's, not the program's", got/(1<<20), ballast>>20)
	}
}

func TestAPeakThatCannotBeTrustedIsNotMeasured(t *testing.T) {
	bin := program(t)
	old := resetPeak
	resetPeak = func() bool { return false }
	t.Cleanup(func() { resetPeak = old })
	_, out, _ := measure(t, "-lines", "500", "-runs", "1", "-bin", bin)
	if !strings.Contains(out, "peak memory: not measured on this system") {
		t.Errorf("a peak that may be floored by the command's memory is shown:\n%s", out)
	}
}
