// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package ingest

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

var _ record.Reader = (*text.Reader)(nil)
