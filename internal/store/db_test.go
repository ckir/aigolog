// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/buildinfo"
	"github.com/ckir/aigolog/internal/record"
	"github.com/ckir/aigolog/internal/timeparse"
)

var ctx = context.Background()

func fixed(t time.Time) func() time.Time { return func() time.Time { return t } }

func newDB(t *testing.T) *DB {
	t.Helper()
	d, err := Create(ctx, Options{Dir: t.TempDir(), Now: fixed(time.Date(2026, 1, 16, 10, 15, 0, 0, time.UTC))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestNamesRoundTripAndNoTwoRunsShareOne(t *testing.T) {
	created := time.Date(2026, 1, 16, 10, 15, 0, 0, time.UTC)
	name := DatabaseName(created, "1a2b3c4d")
	if name != "20260116T101500Z-1a2b3c4d.db" {
		t.Errorf("name = %q", name)
	}
	if got, ok := ParseName(name); !ok || !got.Equal(created) {
		t.Errorf("ParseName(%q) = %v, %v", name, got, ok)
	}
	for _, bad := range []string{"", "x.db", "20260116T101500Z-1a2b3c4d", "20260116T101500Z-1a2b3c4.db", "20260116T101500Z-1A2B3C4D.db", "20261316T101500Z-1a2b3c4d.db"} {
		if _, ok := ParseName(bad); ok {
			t.Errorf("ParseName(%q) accepted a name that is not ours", bad)
		}
	}
	dir := t.TempDir()
	a, err := Create(ctx, Options{Dir: dir, Now: fixed(created)})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Create(ctx, Options{Dir: dir, Now: fixed(created)})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if a.Path() == b.Path() {
		t.Error("two runs in the same second got the same database")
	}
}

func TestCreateMakesTheFilesForTheOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows a file takes the rules of its folder (design 7.4)")
	}
	dir := filepath.Join(t.TempDir(), "cache", "aigolog")
	d, err := Create(ctx, Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, p := range []string{d.Path(), strings.TrimSuffix(d.Path(), ".db") + ".lock"} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v, %v; want mode 0600", p, fi, err)
		}
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("folder: %v, %v; want mode 0700", fi, err)
	}
}

func TestARunHoldsItsLockUntilItIsClosed(t *testing.T) {
	d := newDB(t)
	lockPath := strings.TrimSuffix(d.Path(), ".db") + ".lock"
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if ok, err := tryLockFile(f); err != nil || ok {
		t.Fatalf("a second holder got the lock of a live run: %v, %v", ok, err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	ok, err := tryLockFile(f)
	if err != nil || !ok {
		t.Fatalf("the lock was not released by Close: %v, %v", ok, err)
	}
	unlockFile(f)
}

func TestAppendAndReadBackKeepsEveryField(t *testing.T) {
	d := newDB(t)
	src, err := d.AddSource(ctx, Source{Name: "a.log", Format: "text", Encoding: "utf-8", Layout: "iso-space", TimeKind: TimeWall})
	if err != nil || src != 1 {
		t.Fatalf("AddSource = %d, %v", src, err)
	}
	in := []record.Record{
		{Source: src, Line: 1, Offset: 0, Length: 9, Raw: "κόσμος 1", Stamp: record.Stamp{State: record.Resolved, Nanos: 42, Exact: true, Zone: "+02:00"}},
		{Source: src, Line: 2, Offset: 10, Length: 3, Raw: "it's \"q\"; DROP TABLE records; --"},
		{Source: src, Line: 3, Offset: 20, Length: 1, Raw: "x", Stamp: record.Stamp{State: record.YearPending, Month: 2, Day: 29, NanoOfDay: 7}},
	}
	if err := d.Append(ctx, in); err != nil {
		t.Fatal(err)
	}
	var got []record.Record
	err = d.Records(ctx, src, func(id int64, r record.Record) error { got = append(got, r); return nil })
	if err != nil || len(got) != len(in) {
		t.Fatalf("read back %d records, %v", len(got), err)
	}
	for i := range in {
		if got[i] != in[i] {
			t.Errorf("record %d = %+v, want %+v", i+1, got[i], in[i])
		}
	}
}

func TestResolveYearsRewritesPendingRecordsFromTheEnd(t *testing.T) {
	d := newDB(t)
	src, _ := d.AddSource(ctx, Source{Name: "s", Format: "text", Encoding: "utf-8", Layout: "syslog", TimeKind: TimeWallYearInferred})
	pend := func(m, day int, tod time.Duration) record.Record {
		return record.Record{Source: src, Raw: "x", Stamp: record.Stamp{State: record.YearPending, Month: m, Day: day, NanoOfDay: int64(tod)}}
	}
	recs := []record.Record{pend(12, 31, 23*time.Hour), {Source: src, Raw: "no stamp"}, pend(1, 1, time.Hour)}
	if err := d.Append(ctx, recs); err != nil {
		t.Fatal(err)
	}
	anchor, _ := timeparse.ParseAsOf("2026-01-05")
	if err := d.ResolveYears(ctx, src, timeparse.NewYearResolver(anchor)); err != nil {
		t.Fatal(err)
	}
	var got []record.Stamp
	d.Records(ctx, src, func(id int64, r record.Record) error { got = append(got, r.Stamp); return nil })
	if got[0].State != record.Resolved || got[0].Nanos != time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC).UnixNano() {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].State != record.NoStamp {
		t.Errorf("a record with no stamp changed: %+v", got[1])
	}
	if got[2].State != record.Resolved || got[2].Nanos != time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC).UnixNano() {
		t.Errorf("third = %+v", got[2])
	}
}

func TestResolveYearsHandlesMoreRecordsThanOneBatch(t *testing.T) {
	d := newDB(t)
	src, _ := d.AddSource(ctx, Source{Name: "s", Format: "text", Encoding: "utf-8", Layout: "syslog", TimeKind: TimeWallYearInferred})
	var recs []record.Record
	for i := 0; i < 12000; i++ {
		recs = append(recs, record.Record{Source: src, Raw: "x", Stamp: record.Stamp{State: record.YearPending, Month: 3, Day: 1, NanoOfDay: int64(i)}})
	}
	if err := d.Append(ctx, recs); err != nil {
		t.Fatal(err)
	}
	anchor, _ := timeparse.ParseAsOf("2026-06-01")
	if err := d.ResolveYears(ctx, src, timeparse.NewYearResolver(anchor)); err != nil {
		t.Fatal(err)
	}
	pending := 0
	d.Records(ctx, src, func(id int64, r record.Record) error {
		if r.Stamp.State == record.YearPending {
			pending++
		}
		return nil
	})
	if pending != 0 {
		t.Errorf("%d records are still waiting for a year", pending)
	}
}

func TestFinishSourceTotalsAndMarkComplete(t *testing.T) {
	d := newDB(t)
	src, _ := d.AddSource(ctx, Source{Name: "s", Format: "text", Encoding: "utf-8", Layout: "iso-space", TimeKind: TimeWall})
	d.Append(ctx, []record.Record{
		{Source: src, Raw: "a", Stamp: record.Stamp{State: record.Resolved, Nanos: 100}},
		{Source: src, Raw: "b", Stamp: record.Stamp{State: record.Resolved, Nanos: 300}},
		{Source: src, Raw: "c"},
	})
	tot, err := d.FinishSource(ctx, src, 5, 77)
	want := SourceTotals{Records: 3, Lines: 5, Bytes: 77, HasTimes: true, FirstNanos: 100, LastNanos: 300}
	if err != nil || tot != want {
		t.Errorf("FinishSource = %+v, %v; want %+v", tot, err, want)
	}
	path := d.Path()
	if _, err := Open(ctx, path); !errors.Is(err, ErrIncomplete) {
		t.Errorf("Open of an incomplete database: %v, want ErrIncomplete", err)
	}
	if err := d.MarkComplete(ctx); err != nil {
		t.Fatal(err)
	}
	d.Close()
	o, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
}

func TestOpenRefusesADatabaseWrittenByAnotherVersion(t *testing.T) {
	d := newDB(t)
	d.MarkComplete(ctx)
	path := d.Path()
	d.Close()
	old := buildinfo.Version
	buildinfo.Version = "9.9.9-other"
	t.Cleanup(func() { buildinfo.Version = old })
	_, err := Open(ctx, path)
	var ve *VersionError
	if !errors.As(err, &ve) || ve.Have != old || ve.Want != "9.9.9-other" {
		t.Errorf("err = %v, want a VersionError from %q to %q", err, old, "9.9.9-other")
	}
}

func TestOpenCannotWrite(t *testing.T) {
	d := newDB(t)
	d.MarkComplete(ctx)
	path := d.Path()
	d.Close()
	o, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if err := o.Append(ctx, []record.Record{{Source: 1, Raw: "x"}}); err == nil {
		t.Error("a database opened for reading accepted a write")
	}
}

func TestValuesAreParametersNeverQueryText(t *testing.T) {
	d := newDB(t)
	name := "x'); DROP TABLE records; --"
	src, err := d.AddSource(ctx, Source{Name: name, Format: "text", Encoding: "utf-8", Layout: "", TimeKind: TimeNone})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Append(ctx, []record.Record{{Source: src, Raw: name}}); err != nil {
		t.Fatal(err)
	}
	n := 0
	d.Records(ctx, src, func(id int64, r record.Record) error { n++; return nil })
	if n != 1 {
		t.Errorf("records = %d after a hostile name", n)
	}
}

func TestRemoveDeletesTheRunsFiles(t *testing.T) {
	d := newDB(t)
	db, lock := d.Path(), strings.TrimSuffix(d.Path(), ".db")+".lock"
	if err := d.Remove(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{db, lock} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists: %v", p, err)
		}
	}
}

// parentIsEmpty fails the test if the folder holds anything at all.
func parentIsEmpty(t *testing.T, parent string) {
	t.Helper()
	ents, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		t.Errorf("%s holds %q, want nothing", parent, e.Name())
	}
}

func TestCreateRefusesAFolderTheDriverWouldMisread(t *testing.T) {
	// The check is made before anything is created, so it holds on Windows
	// too, where a name with "?" could not be made anyway.
	parent := t.TempDir()
	d, err := Create(ctx, Options{Dir: filepath.Join(parent, "a?b")})
	if err == nil {
		d.Close()
		t.Fatal("Create accepted a folder with a \"?\" in its path")
	}
	if !strings.Contains(err.Error(), `"?"`) {
		t.Errorf("err = %v, want it to name the \"?\"", err)
	}
	parentIsEmpty(t, parent)

	t.Run("file: prefix", func(t *testing.T) {
		parent := t.TempDir()
		t.Chdir(parent)
		d, err := Create(ctx, Options{Dir: "file:x"})
		if err == nil {
			d.Close()
			t.Fatal("Create accepted a folder that starts with \"file:\"")
		}
		parentIsEmpty(t, parent)
	})
}

func TestOpenRefusesAPathTheDriverWouldMisread(t *testing.T) {
	parent := t.TempDir()
	if _, err := Open(ctx, filepath.Join(parent, "a?b", "x.db")); err == nil {
		t.Error("Open accepted a path with a \"?\" in it")
	}
	parentIsEmpty(t, parent)

	if runtime.GOOS == "windows" {
		t.Skip("a name with \"?\" cannot be made on Windows")
	}
	// A real, complete database in a folder with a "?" in its name: without
	// the check the driver cuts the path at the "?" and opens a stray file.
	d := newDB(t)
	d.MarkComplete(ctx)
	d.Close()
	data, err := os.ReadFile(d.Path())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "a?b")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filepath.Base(d.Path()))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if o, err := Open(ctx, path); err == nil {
		o.Close()
		t.Error("Open accepted a path with a \"?\" in it that names a real database")
	}
	ents, err := os.ReadDir(parent)
	if err != nil || len(ents) != 1 || ents[0].Name() != "a?b" {
		t.Errorf("the parent holds %v, %v; want only the folder a?b (no stray file)", ents, err)
	}
}

func TestResolveYearsPlacesYearsAcrossAChunkBoundary(t *testing.T) {
	d := newDB(t)
	other, _ := d.AddSource(ctx, Source{Name: "other", Format: "text", Encoding: "utf-8", Layout: "syslog", TimeKind: TimeWallYearInferred})
	src, _ := d.AddSource(ctx, Source{Name: "s", Format: "text", Encoding: "utf-8", Layout: "syslog", TimeKind: TimeWallYearInferred})
	if err := d.Append(ctx, []record.Record{{Source: other, Raw: "o", Stamp: record.Stamp{State: record.YearPending, Month: 12, Day: 31, NanoOfDay: int64(23 * time.Hour)}}}); err != nil {
		t.Fatal(err)
	}
	const dec, jan = 5500, 500
	var recs []record.Record
	for i := 0; i < dec; i++ {
		recs = append(recs, record.Record{Source: src, Raw: "x", Stamp: record.Stamp{State: record.YearPending, Month: 12, Day: 31, NanoOfDay: int64(23*time.Hour) + int64(i)}})
	}
	for i := 0; i < jan; i++ {
		recs = append(recs, record.Record{Source: src, Raw: "x", Stamp: record.Stamp{State: record.YearPending, Month: 1, Day: 1, NanoOfDay: int64(time.Hour) + int64(i)}})
	}
	if err := d.Append(ctx, recs); err != nil {
		t.Fatal(err)
	}
	anchor, _ := timeparse.ParseAsOf("2026-01-05")
	if err := d.ResolveYears(ctx, src, timeparse.NewYearResolver(anchor)); err != nil {
		t.Fatal(err)
	}
	var got []record.Stamp
	if err := d.Records(ctx, src, func(id int64, r record.Record) error { got = append(got, r.Stamp); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != dec+jan {
		t.Fatalf("read back %d records, want %d", len(got), dec+jan)
	}
	wrong := 0
	for i, st := range got {
		want := 2025
		if i >= dec {
			want = 2026
		}
		if st.State != record.Resolved || time.Unix(0, st.Nanos).UTC().Year() != want {
			if wrong++; wrong <= 3 {
				t.Errorf("record %d = %+v, want Resolved in %d", i+1, st, want)
			}
		}
	}
	if wrong > 3 {
		t.Errorf("%d records in all are wrong", wrong)
	}
	var otherStates []record.StampState
	d.Records(ctx, other, func(id int64, r record.Record) error { otherStates = append(otherStates, r.Stamp.State); return nil })
	if len(otherStates) != 1 || otherStates[0] != record.YearPending {
		t.Errorf("the record of the other source = %v, want it left YearPending", otherStates)
	}
}

func TestCloseTwiceAndRemoveAfterCloseAreNotErrors(t *testing.T) {
	d := newDB(t)
	db, lock := d.Path(), strings.TrimSuffix(d.Path(), ".db")+".lock"
	if err := d.Close(); err != nil {
		t.Errorf("first Close = %v", err)
	}
	if err := d.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
	for _, p := range []string{db, lock} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s is gone after Close: %v", p, err)
		}
	}
	if err := d.Remove(); err != nil {
		t.Errorf("Remove after Close = %v", err)
	}
	for _, p := range []string{db, lock} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists: %v", p, err)
		}
	}
}

func TestAFailedCreateLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	d, err := Create(cancelled, Options{Dir: dir})
	if err == nil {
		d.Close()
		t.Fatal("Create with a cancelled context succeeded")
	}
	parentIsEmpty(t, dir)
}

func TestMethodsAfterCloseReturnErrClosedInsteadOfPanicking(t *testing.T) {
	d := newDB(t)
	src, err := d.AddSource(ctx, Source{Name: "s", Format: "text", Encoding: "utf-8", TimeKind: TimeNone})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	calls := map[string]func() error{
		"AddSource":    func() error { _, err := d.AddSource(ctx, Source{Name: "s"}); return err },
		"Append":       func() error { return d.Append(ctx, []record.Record{{Source: src, Raw: "x"}}) },
		"ResolveYears": func() error { return d.ResolveYears(ctx, src, timeparse.NewYearResolver(0)) },
		"FinishSource": func() error { _, err := d.FinishSource(ctx, src, 0, 0); return err },
		"MarkComplete": func() error { return d.MarkComplete(ctx) },
		"Records":      func() error { return d.Records(ctx, src, func(int64, record.Record) error { return nil }) },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrClosed) {
			t.Errorf("%s after Close = %v, want ErrClosed", name, err)
		}
	}
	if d.Path() == "" {
		t.Error("Path is empty after Close")
	}
	if _, err := d.Size(); err != nil {
		t.Errorf("Size after Close: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if err := d.Remove(); err != nil {
		t.Errorf("Remove after Close: %v", err)
	}
}

func TestCreateNamesTheFolderNotAPlaceholderPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a?b")
	d, err := Create(ctx, Options{Dir: dir})
	if err == nil {
		d.Close()
		t.Fatal("Create accepted a folder with a \"?\" in its path")
	}
	if !strings.Contains(err.Error(), strconv.Quote(dir)) || strings.Contains(err.Error(), "00010101T000000Z") {
		t.Errorf("err = %v; want it to name the folder %q and no placeholder file name", err, dir)
	}
	// A relative folder: a regression would make it in the working directory.
	t.Chdir(t.TempDir())
	d, err = Create(ctx, Options{Dir: "file:x"})
	if err == nil {
		d.Close()
		t.Fatal("Create accepted a folder that starts with \"file:\"")
	}
	if !strings.Contains(err.Error(), `"file:x"`) || strings.Contains(err.Error(), "00010101T000000Z") {
		t.Errorf("err = %v; want it to name the folder file:x and no placeholder file name", err)
	}
}

func TestOpenChecksTheVersionBeforeTheCompleteMark(t *testing.T) {
	d := newDB(t) // never marked complete
	path := d.Path()
	d.Close()
	old := buildinfo.Version
	buildinfo.Version = "9.9.9-other"
	t.Cleanup(func() { buildinfo.Version = old })
	_, err := Open(ctx, path)
	var ve *VersionError
	if !errors.As(err, &ve) || errors.Is(err, ErrIncomplete) {
		t.Errorf("Open of an unfinished database of another version: %v, want a *VersionError and not ErrIncomplete", err)
	}
}

func TestCreateWritesTheMetaRows(t *testing.T) {
	d := newDB(t) // the clock is 2026-01-16 10:15:00 UTC
	want := map[string]string{
		"version":  buildinfo.Version,
		"schema":   "1",
		"created":  "2026-01-16T10:15:00Z",
		"complete": "0",
	}
	check := func(stage string, want map[string]string) {
		t.Helper()
		got, err := d.readMeta(ctx)
		if err != nil {
			t.Fatalf("%s: readMeta: %v", stage, err)
		}
		if len(got) != len(want) {
			t.Errorf("%s: meta = %v, want %v", stage, got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: meta[%q] = %q, want %q", stage, k, got[k], v)
			}
		}
	}
	check("after Create", want)
	if err := d.MarkComplete(ctx); err != nil {
		t.Fatal(err)
	}
	want["complete"] = "1"
	check("after MarkComplete", want)
}
