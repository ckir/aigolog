// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/reader/text"
	"github.com/ckir/aigolog/internal/record"
	"github.com/ckir/aigolog/internal/store"
)

var ctx = context.Background()

func newDB(t *testing.T) *store.DB {
	t.Helper()
	d, err := store.Create(ctx, store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func asOf(n int64) *int64 { return &n }

func in(name, body string) Input { return Input{Name: name, R: strings.NewReader(body)} }

func wall(y int, mo time.Month, d, h, mi, s int) int64 {
	return time.Date(y, mo, d, h, mi, s, 0, time.UTC).UnixNano()
}

func TestRunStoresEveryLineOfSeveralSourcesAndMarksTheDatabaseComplete(t *testing.T) {
	d := newDB(t)
	res, err := Run(ctx, d, []Input{in("a.log", "2026-01-05 10:00:01 one\n2026-01-05 10:00:02 two\n"), in("b.log", "plain\nlines\nhere\n")}, Options{Now: time.Now(), Zone: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	if res.Records != 5 || res.Lines != 5 || len(res.Sources) != 2 {
		t.Fatalf("result = %+v", res)
	}
	a, b := res.Sources[0], res.Sources[1]
	if a.ID != 1 || a.Source.TimeKind != store.TimeWall || a.Source.Layout != "iso-space" || a.Totals.Records != 2 || !a.Totals.HasTimes ||
		a.Totals.FirstNanos != wall(2026, 1, 5, 10, 0, 1) || a.Totals.LastNanos != wall(2026, 1, 5, 10, 0, 2) {
		t.Errorf("source a = %+v", a)
	}
	if b.ID != 2 || b.Source.TimeKind != store.TimeNone || b.Source.Layout != "" || b.Totals.Records != 3 || b.Totals.HasTimes {
		t.Errorf("source b = %+v", b)
	}
	path := d.Path()
	d.Close()
	o, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("the database is not complete: %v", err)
	}
	o.Close()
}

func TestAZonedLogIsExact(t *testing.T) {
	d := newDB(t)
	res, err := Run(ctx, d, []Input{in("z.log", "2026-01-05T10:00:01+02:00 a\n2026-01-05T10:00:02+02:00 b\n")}, Options{Now: time.Now(), Zone: time.UTC})
	if err != nil || res.Sources[0].Source.TimeKind != store.TimeExact {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestSyslogYearsComeFromAsOfAndTheSourceStatesTheAnchor(t *testing.T) {
	d := newDB(t)
	anchor := wall(2026, 1, 15, 23, 59, 59) + 999999999
	res, err := Run(ctx, d, []Input{in("auth.log", "Dec 28 23:00:00 h a\nDec 31 23:59:59 h b\n")}, Options{AsOf: asOf(anchor), Now: time.Now(), Zone: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Sources[0]
	if s.Source.TimeKind != store.TimeWallYearInferred || s.Source.AnchorKind != store.AnchorAsOf || s.Source.AnchorNanos != anchor {
		t.Errorf("source = %+v", s.Source)
	}
	if s.Totals.FirstNanos != wall(2025, 12, 28, 23, 0, 0) || s.Totals.LastNanos != wall(2025, 12, 31, 23, 59, 59) {
		t.Errorf("totals = %+v", s.Totals)
	}
}

func TestWithoutAsOfAFileTakesItsModificationTimeAndStandardInputTheCurrentTime(t *testing.T) {
	d := newDB(t)
	mod := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	cur := time.Date(2027, 3, 2, 9, 0, 0, 0, time.UTC)
	file := Input{Name: "f.log", R: strings.NewReader("Dec 28 10:00:00 h a\n"), ModTime: mod}
	stdin := Input{Name: "-", R: strings.NewReader("Dec 28 10:00:00 h a\n")}
	res, err := Run(ctx, d, []Input{file, stdin}, Options{Now: cur, Zone: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	f, s := res.Sources[0], res.Sources[1]
	if f.Source.AnchorKind != store.AnchorModTime || f.Totals.FirstNanos != wall(2025, 12, 28, 10, 0, 0) {
		t.Errorf("file: %+v %+v", f.Source, f.Totals)
	}
	if s.Source.AnchorKind != store.AnchorNow || s.Totals.FirstNanos != wall(2026, 12, 28, 10, 0, 0) {
		t.Errorf("stdin: %+v %+v", s.Source, s.Totals)
	}
}

func TestAModificationTimeOutOfRangeFallsBackToTheCurrentTime(t *testing.T) {
	d := newDB(t)
	cur := time.Date(2027, 3, 2, 9, 0, 0, 0, time.UTC)
	file := Input{Name: "f.log", R: strings.NewReader("Dec 28 10:00:00 h a\n"), ModTime: time.Date(1500, 1, 1, 0, 0, 0, 0, time.UTC)}
	res, err := Run(ctx, d, []Input{file}, Options{Now: cur, Zone: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Sources[0]
	if s.Source.AnchorKind != store.AnchorNow || s.Source.AnchorNanos != wall(2027, 3, 2, 9, 0, 0) || s.Totals.FirstNanos != wall(2026, 12, 28, 10, 0, 0) {
		t.Errorf("source = %+v, totals = %+v", s.Source, s.Totals)
	}
}

func TestAnEmptyInputIsNotStoredAndTheRunGoesOn(t *testing.T) {
	d := newDB(t)
	res, err := Run(ctx, d, []Input{in("empty.log", ""), in("full.log", "x\n")}, Options{Now: time.Now(), Zone: time.UTC})
	if err != nil || res.Records != 1 || res.Sources[0].ID != 0 || res.Sources[0].Totals.Records != 0 || res.Sources[1].ID != 1 {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestWhenEveryInputIsEmptyNothingIsAnalysed(t *testing.T) {
	d := newDB(t)
	_, err := Run(ctx, d, []Input{in("a", ""), in("b", "")}, Options{Now: time.Now(), Zone: time.UTC})
	if !errors.Is(err, ErrNothingToAnalyse) {
		t.Errorf("err = %v", err)
	}
}

func TestTextWithANulByteIsRefusedAndNamesTheSourceAndTheOffset(t *testing.T) {
	d := newDB(t)
	_, err := Run(ctx, d, []Input{in("good.log", "fine\n"), in("bad.bin", "ab\x00cd\n")}, Options{Now: time.Now(), Zone: time.UTC})
	var ie *InputError
	var nt *text.NotTextError
	if !errors.As(err, &ie) || ie.Name != "bad.bin" || !errors.As(err, &nt) || nt.Offset != 2 {
		t.Errorf("err = %v", err)
	}
}

func TestOpenFileReadsNoMoreThanTheFileHeldWhenItWasOpened(t *testing.T) {
	p := filepath.Join(t.TempDir(), "grow.log")
	os.WriteFile(p, []byte("one\ntwo\n"), 0o600)
	input, closer, err := OpenFile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("three\nfour\n")
	f.Close()
	b, _ := io.ReadAll(input.R)
	if string(b) != "one\ntwo\n" || input.Name != p || input.ModTime.IsZero() {
		t.Errorf("read %q, name %q, mod %v", b, input.Name, input.ModTime)
	}
	if _, _, err := OpenFile(p + ".absent"); err == nil {
		t.Error("a missing file was opened")
	}
}

// lines produces n lines of about 100 bytes each without keeping them.
type lines struct {
	n, i int
	buf  []byte
}

func (l *lines) Read(p []byte) (int, error) {
	if len(l.buf) == 0 {
		if l.i >= l.n {
			return 0, io.EOF
		}
		l.buf = []byte("2026-01-05 10:00:01 line " + strings.Repeat("y", 70) + "\n")
		l.i++
	}
	n := copy(p, l.buf)
	l.buf = l.buf[n:]
	return n, nil
}

func TestMemoryDoesNotGrowWithTheInput(t *testing.T) {
	d := newDB(t)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	res, err := Run(ctx, d, []Input{{Name: "big.log", R: &lines{n: 400000}}}, Options{Now: time.Now(), Zone: time.UTC})
	if err != nil || res.Records != 400000 {
		t.Fatalf("res = %+v, err = %v", res.Records, err)
	}
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&after)
	// 400,000 records of about 100 bytes are 40 MB; keeping them would show here.
	if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 24<<20 {
		t.Errorf("the heap grew by %d MiB while ingesting 400,000 lines", grown>>20)
	}
}

// sampler is lines that measures the heap while the run is reading it: at a
// line boundary, every 50,000 lines, it collects garbage and keeps the largest
// growth over the baseline.
type sampler struct {
	lines
	baseline uint64
	peak     int64
	samples  int
}

func (s *sampler) Read(p []byte) (int, error) {
	if len(s.buf) == 0 && s.i > 0 && s.i%50000 == 0 {
		var m runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m)
		if grown := int64(m.HeapAlloc) - int64(s.baseline); grown > s.peak {
			s.peak = grown
		}
		s.samples++
	}
	return s.lines.Read(p)
}

func TestMemoryDuringTheRunDoesNotGrowWithTheInput(t *testing.T) {
	d := newDB(t)
	var m runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m)
	s := &sampler{lines: lines{n: 400000}, baseline: m.HeapAlloc}
	res, err := Run(ctx, d, []Input{{Name: "big.log", R: s}}, Options{Now: time.Now(), Zone: time.UTC})
	if err != nil || res.Records != 400000 {
		t.Fatalf("res = %+v, err = %v", res.Records, err)
	}
	if s.samples == 0 {
		t.Fatal("the heap was never sampled during the run")
	}
	// Keeping the 400,000 records of about 100 bytes would show as 40 MB or more.
	if s.peak > 24<<20 {
		t.Errorf("the heap grew by %d MiB during the run (%d samples)", s.peak>>20, s.samples)
	}
	t.Logf("peak growth %d bytes over %d samples", s.peak, s.samples)
}

var _ record.Reader = (*text.Reader)(nil)

func TestAnInputOfBlankLinesOnlyIsNothingToAnalyse(t *testing.T) {
	d := newDB(t)
	_, err := Run(ctx, d, []Input{in("blank.log", "\n  \n\t\n")}, Options{Now: time.Now(), Zone: time.UTC})
	if !errors.Is(err, ErrNothingToAnalyse) {
		t.Errorf("err = %v, want ErrNothingToAnalyse", err)
	}
}

func TestRunCountsTheRecordsCutAtTheCap(t *testing.T) {
	d := newDB(t)
	line := "  " + strings.Repeat("z", 97) + "\n" // 100 bytes
	big := "2026-01-05 10:00:01 start\n" + strings.Repeat(line, 3<<20/len(line))
	res, err := Run(ctx, d, []Input{in("big.log", big), in("small.log", "2026-01-05 10:00:01 a\n"), in("big2.log", big)}, Options{Now: time.Now(), Zone: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := res.Sources[0], res.Sources[1], res.Sources[2]
	if a.CutRecords < 2 || b.CutRecords != 0 || c.CutRecords != a.CutRecords {
		t.Errorf("cuts = %d, %d, %d; want at least 2, 0, and the first again", a.CutRecords, b.CutRecords, c.CutRecords)
	}
	if res.CutRecords != a.CutRecords+b.CutRecords+c.CutRecords {
		t.Errorf("Result.CutRecords = %d, want the sum %d", res.CutRecords, a.CutRecords+b.CutRecords+c.CutRecords)
	}
	if a.Totals.Records != a.CutRecords+1 {
		t.Errorf("records = %d for %d cuts", a.Totals.Records, a.CutRecords)
	}
}

// longRecords produces a stamped line and then n bytes of indented lines, which
// the reader cuts into records of about text.MaxRecordBytes, and samples the
// heap every 8 MiB.
type longRecords struct {
	n, sent  int
	started  bool
	baseline uint64
	peak     int64
	samples  int
}

func (l *longRecords) Read(p []byte) (int, error) {
	if !l.started {
		l.started = true
		return copy(p, "2026-01-05 10:00:01 start\n"), nil
	}
	if l.sent >= l.n {
		return 0, io.EOF
	}
	const line = "  zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz\n"
	if before := l.sent / (8 << 20); (l.sent+len(line))/(8<<20) > before {
		var m runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m)
		if grown := int64(m.HeapAlloc) - int64(l.baseline); grown > l.peak {
			l.peak = grown
		}
		l.samples++
	}
	n := copy(p, line)
	l.sent += len(line)
	return n, nil
}

func TestMemoryStaysBoundedWhenRecordsAreAsLongAsTheCap(t *testing.T) {
	d := newDB(t)
	var m runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&m)
	l := &longRecords{n: 120 << 20, baseline: m.HeapAlloc}
	res, err := Run(ctx, d, []Input{{Name: "long.log", R: l}}, Options{Now: time.Now(), Zone: time.UTC})
	if err != nil || res.CutRecords < 100 {
		t.Fatalf("cuts = %d, err = %v", res.CutRecords, err)
	}
	if l.samples < 10 {
		t.Fatalf("the heap was sampled %d times", l.samples)
	}
	// Holding 5,000 records of 1 MiB would show as gigabytes; a batch of records
	// that were not appended until the count was reached holds 120 MiB here.
	if l.peak > 40<<20 {
		t.Errorf("the heap grew by %d MiB during the run (%d samples)", l.peak>>20, l.samples)
	}
	t.Logf("peak growth %d bytes over %d samples", l.peak, l.samples)
}

// storedRaws returns the Raw text of the records of the source, in order.
func storedRaws(t *testing.T, d *store.DB, source int) []string {
	t.Helper()
	var raws []string
	err := d.Records(ctx, source, func(id int64, r record.Record) error {
		raws = append(raws, r.Raw)
		return nil
	})
	if err != nil {
		t.Fatalf("reading the records back: %v", err)
	}
	return raws
}

// hookedLines is an input of n lines of the same width, each handed out by
// itself, that calls hook(i) when line i (from 1) is asked for.
type hookedLines struct {
	n, i  int
	width int
	buf   []byte
	hook  func(i int)
}

func (h *hookedLines) Read(p []byte) (int, error) {
	if len(h.buf) == 0 {
		if h.i >= h.n {
			return 0, io.EOF
		}
		h.i++
		if h.hook != nil {
			h.hook(h.i)
		}
		h.buf = []byte(fmt.Sprintf("line %02d ", h.i) + strings.Repeat("y", h.width) + "\n")
	}
	n := copy(p, h.buf)
	h.buf = h.buf[n:]
	return n, nil
}

func TestACancelledContextBetweenBatchesStopsTheRunAndDoesNotMarkItComplete(t *testing.T) {
	d := newDB(t)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The text reader looks at the first 64 KiB before it gives out a record,
	// and then reads ahead only as it needs to, so with lines of 20,000 bytes
	// the seventh line is asked for while the run is in the middle of
	// appending batches of two records.
	r := &hookedLines{n: 10, width: 20000, hook: func(i int) {
		if i == 7 {
			cancel()
		}
	}}
	res, err := Run(cctx, d, []Input{{Name: "big.log", R: r}}, Options{BatchSize: 2, Now: time.Now(), Zone: time.UTC})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// The error of the context is returned as it is (see Run), not wrapped by
	// the store when it finds the context done.
	if err != context.Canceled {
		t.Errorf("err = %q, want the error of the context itself: the batch must not be appended once the context is done", err)
	}
	if res.Records != 0 {
		t.Errorf("Result.Records = %d: no source was finished", res.Records)
	}
	stored := storedRaws(t, d, 1)
	t.Logf("%d records stored", len(stored))
	if len(stored) < 2 || len(stored) >= 10 {
		t.Errorf("%d records are stored, want the first batch or more and fewer than 10", len(stored))
	}
	path := d.Path()
	d.Close()
	if _, err := store.Open(ctx, path); !errors.Is(err, store.ErrIncomplete) {
		t.Errorf("Open of the database of a cancelled run: %v, want store.ErrIncomplete", err)
	}
}

func TestBatchBoundaries(t *testing.T) {
	for _, n := range []int{3, 4, 6} {
		t.Run(fmt.Sprintf("%d lines", n), func(t *testing.T) {
			d := newDB(t)
			var want []string
			var body strings.Builder
			for i := 1; i <= n; i++ {
				line := fmt.Sprintf("line %d", i)
				want = append(want, line)
				body.WriteString(line + "\n")
			}
			res, err := Run(ctx, d, []Input{in("b.log", body.String())}, Options{BatchSize: 3, Now: time.Now(), Zone: time.UTC})
			if err != nil {
				t.Fatal(err)
			}
			if res.Records != int64(n) || res.Sources[0].Totals.Records != int64(n) {
				t.Errorf("Result.Records = %d, source records = %d; want %d", res.Records, res.Sources[0].Totals.Records, n)
			}
			got := storedRaws(t, d, 1)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("stored %q, want %q", got, want)
			}
			// Nothing may be stored that belongs to no source, as a batch
			// that was not emptied after its append would store again.
			if stray := storedRaws(t, d, 0); len(stray) != 0 {
				t.Errorf("%d records belong to no source: %q", len(stray), stray)
			}
		})
	}
}

func TestOpenFileOnADirectoryAndOnANonRegularFile(t *testing.T) {
	dir := t.TempDir()
	t.Run("directory", func(t *testing.T) {
		input, closer, err := OpenFile(dir)
		if err == nil || !errors.Is(err, syscall.EISDIR) || !strings.Contains(err.Error(), "is a directory") {
			t.Errorf("OpenFile(a directory) = %+v, err %v; want an error that says it is a directory", input, err)
		}
		if closer != nil {
			closer.Close()
			t.Error("OpenFile(a directory) returned a closer: nothing should be left open")
		}
	})
	t.Run("named pipe", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("there is no named pipe made with mkfifo on Windows")
		}
		mkfifo, err := exec.LookPath("mkfifo")
		if err != nil {
			t.Skip("no mkfifo on this system")
		}
		fifo := filepath.Join(dir, "pipe")
		if out, err := exec.Command(mkfifo, fifo).CombinedOutput(); err != nil {
			t.Skipf("mkfifo: %v: %s", err, out)
		}
		// Opening a named pipe for reading waits for a writer, so a goroutine
		// opens it for writing, which waits for the reader.
		go func() {
			w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
			if err != nil {
				return
			}
			w.WriteString("x\n")
			w.Close()
		}()
		input, closer, err := OpenFile(fifo)
		if err != nil {
			t.Fatalf("OpenFile(a named pipe): %v", err)
		}
		defer closer.Close()
		if !input.ModTime.IsZero() {
			t.Errorf("ModTime = %v, want zero for something that is not a regular file", input.ModTime)
		}
		got, err := io.ReadAll(input.R)
		if err != nil || string(got) != "x\n" || input.Name != fifo {
			t.Errorf("read %q, %v, name %q; want %q: a pipe has no size to limit it by", got, err, input.Name, "x\n")
		}
	})
}
