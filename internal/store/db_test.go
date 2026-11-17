// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"context"
	"database/sql"
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

func TestRemoveReportsADatabaseThatWentWhileTheLockFileStayed(t *testing.T) {
	d := newDB(t)
	db, lock := d.Path(), strings.TrimSuffix(d.Path(), ".db")+".lock"
	inUse := errors.New("lock in use")
	removeAll = func(path string) error {
		if path == lock {
			return inUse
		}
		return os.Remove(path)
	}
	t.Cleanup(func() { removeAll = os.Remove })

	err := d.Remove()
	var re *RemoveError
	if !errors.As(err, &re) || !re.DBGone {
		t.Fatalf("Remove = %#v, want a *RemoveError with DBGone", err)
	}
	if !errors.Is(err, inUse) || re.Err != inUse || err.Error() != "lock in use" {
		t.Errorf("the error must be the cause, unchanged: %q, Err %v", err, re.Err)
	}
	if _, statErr := os.Stat(db); !os.IsNotExist(statErr) {
		t.Errorf("the database file is still there: %v", statErr)
	}
	if _, statErr := os.Stat(lock); statErr != nil {
		t.Errorf("the lock file was removed although the seam refused: %v", statErr)
	}
}

func TestRemoveGivesAPlainErrorWhenTheDatabaseFileStays(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a folder is made read-only with chmod on Unix only")
	}
	if os.Geteuid() == 0 {
		t.Skip("root is not stopped by the permissions of a folder")
	}
	d := newDB(t)
	dir := filepath.Dir(d.Path())
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	err := d.Remove()
	var re *RemoveError
	if err == nil || errors.As(err, &re) {
		t.Fatalf("Remove = %#v, want a plain error: the database file is still there", err)
	}
	if _, statErr := os.Stat(d.Path()); statErr != nil {
		t.Errorf("the database file is gone: %v", statErr)
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
		"schema":   "2",
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

// useRandomParts makes randomPartFunc return the given random parts in turn, and fail
// when they are used up; it restores the real function when the test ends.
func useRandomParts(t *testing.T, parts ...string) {
	t.Helper()
	next := 0
	randomPartFunc = func() (string, error) {
		if next >= len(parts) {
			return "", errors.New("the test has no more random parts")
		}
		next++
		return parts[next-1], nil
	}
	t.Cleanup(func() { randomPartFunc = randomPart })
}

// readFile returns the content of path, or fails the test.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCreateRetriesWhenTheLockFileNameIsTaken(t *testing.T) {
	useRandomParts(t, "00000001", "00000002", "00000003")
	dir := t.TempDir()
	created := time.Date(2026, 1, 16, 10, 15, 0, 0, time.UTC)
	taken := filepath.Join(dir, DatabaseName(created, "00000001"))
	takenLock := lockOf(taken)
	if err := os.WriteFile(takenLock, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Create(ctx, Options{Dir: dir, Now: fixed(created)})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if want := filepath.Join(dir, DatabaseName(created, "00000002")); d.Path() != want {
		t.Errorf("the database is %s, want %s", d.Path(), want)
	}
	if got := readFile(t, takenLock); got != "theirs" {
		t.Errorf("the lock file that was taken holds %q, want it untouched", got)
	}
	if exists(taken) {
		t.Error("a database was made under the name whose lock file was taken")
	}
}

func TestCreateRetriesWhenTheDatabaseFileNameIsTaken(t *testing.T) {
	useRandomParts(t, "00000001", "00000002", "00000003")
	dir := t.TempDir()
	created := time.Date(2026, 1, 16, 10, 15, 0, 0, time.UTC)
	taken := filepath.Join(dir, DatabaseName(created, "00000001"))
	if err := os.WriteFile(taken, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := Create(ctx, Options{Dir: dir, Now: fixed(created)})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if want := filepath.Join(dir, DatabaseName(created, "00000002")); d.Path() != want {
		t.Errorf("the database is %s, want %s", d.Path(), want)
	}
	if got := readFile(t, taken); got != "theirs" {
		t.Errorf("the database file that was taken holds %q, want it untouched", got)
	}
	if exists(lockOf(taken)) {
		t.Error("the lock file of the first name was left behind")
	}
}

// skipIfAnOpenFileCannotBeDeleted skips the test where the system does not
// delete a file that is open. It is decided here, before Create is called, and
// the probe closes its handle: a skip inside the hook would end the test in the
// middle of Create and leave the lock file open. A cleaner can delete the lock
// file of a run only where an open file can be deleted, so where it cannot
// (Windows: Go opens files without the delete share mode) the cases of the
// cleaner cannot happen and there is nothing to test.
func skipIfAnOpenFileCannotBeDeleted(t *testing.T) {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "probe")
	f, err := os.OpenFile(probe, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	removeErr := os.Remove(probe)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if removeErr != nil {
		t.Skipf("this system does not delete a file that is open: %v", removeErr)
	}
}

// cleanerActsOnce sets afterLockHook to a function that, the first time it is
// called, removes the lock file (and, if replace, puts another file in its
// place), as a cleaner does between the creation of the file and its check. It
// skips the test first, where the system does not let a file that is open be
// deleted; the hook itself never skips.
func cleanerActsOnce(t *testing.T, replace bool) {
	t.Helper()
	skipIfAnOpenFileCannotBeDeleted(t)
	done := false
	afterLockHook = func(lockPath string) {
		if done {
			return
		}
		done = true
		if err := os.Remove(lockPath); err != nil {
			t.Errorf("the cleaner could not delete the lock file: %v", err)
			return
		}
		if replace {
			if err := os.WriteFile(lockPath, []byte("theirs"), 0o600); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { afterLockHook = nil })
}

func TestCreateRetriesWhenACleanerRemovesTheLockFileBeforeItIsChecked(t *testing.T) {
	useRandomParts(t, "00000001", "00000002", "00000003")
	cleanerActsOnce(t, false)
	dir := t.TempDir()
	created := time.Date(2026, 1, 16, 10, 15, 0, 0, time.UTC)
	first := filepath.Join(dir, DatabaseName(created, "00000001"))
	d, err := Create(ctx, Options{Dir: dir, Now: fixed(created)})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if want := filepath.Join(dir, DatabaseName(created, "00000002")); d.Path() != want {
		t.Errorf("the database is %s, want %s", d.Path(), want)
	}
	if exists(first) || exists(lockOf(first)) {
		t.Errorf("the first name left something behind: db %v, lock %v", exists(first), exists(lockOf(first)))
	}
}

func TestCreateRetriesWhenACleanerReplacesTheLockFileBeforeItIsChecked(t *testing.T) {
	useRandomParts(t, "00000001", "00000002", "00000003")
	cleanerActsOnce(t, true)
	dir := t.TempDir()
	created := time.Date(2026, 1, 16, 10, 15, 0, 0, time.UTC)
	first := filepath.Join(dir, DatabaseName(created, "00000001"))
	d, err := Create(ctx, Options{Dir: dir, Now: fixed(created)})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if want := filepath.Join(dir, DatabaseName(created, "00000002")); d.Path() != want {
		t.Errorf("the database is %s, want %s", d.Path(), want)
	}
	if exists(first) {
		t.Error("a database was made under the name whose lock file was replaced")
	}
	// The file in the place of ours is not ours to remove.
	if got := readFile(t, lockOf(first)); got != "theirs" {
		t.Errorf("the file that replaced the lock file holds %q, want it untouched", got)
	}
}

func TestCreateMakesTheTablesOfLayoutTwo(t *testing.T) {
	d := newDB(t)
	rows, err := d.conn.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	want := []string{"meta", "patterns", "records", "slot_values", "slots", "sources"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tables = %v, want %v", got, want)
	}
	// The columns that Task 6 and the summary rely on, with their types.
	for table, cols := range map[string][]string{
		"patterns":    {"id INTEGER", "text TEXT", "slots TEXT", "catch_all INTEGER", "records INTEGER", "first_nanos INTEGER", "last_nanos INTEGER"},
		"slots":       {"id INTEGER", "pattern INTEGER", "pos INTEGER", "type TEXT"},
		"slot_values": {"record INTEGER", "slot INTEGER", "value TEXT"},
		"records":     {"t_len INTEGER", "pattern INTEGER", "raw TEXT"},
	} {
		have := map[string]bool{}
		info, err := d.conn.QueryContext(ctx, `SELECT name, type FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for info.Next() {
			var name, typ string
			if err := info.Scan(&name, &typ); err != nil {
				t.Fatal(err)
			}
			have[name+" "+typ] = true
		}
		info.Close()
		for _, c := range cols {
			if !have[c] {
				t.Errorf("table %s has no column %q (has %v)", table, c, have)
			}
		}
	}
	// slot_values has no index at creation: the indexes are made after the load.
	var idx int
	if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'slot_values'`).Scan(&idx); err != nil || idx != 0 {
		t.Errorf("slot_values has %d indexes, %v; want none", idx, err)
	}
}

func TestAppendAndReadBackKeepsTheStampLength(t *testing.T) {
	d := newDB(t)
	src, err := d.AddSource(ctx, Source{Name: "a.log", Format: "text", Encoding: "utf-8", TimeKind: TimeWall})
	if err != nil {
		t.Fatal(err)
	}
	in := []record.Record{
		{Source: src, Line: 1, Raw: "2026-01-16 10:15:00 up", Stamp: record.Stamp{State: record.Resolved, Nanos: 42, Len: 19}},
		{Source: src, Line: 2, Raw: "no stamp here"},
		{Source: src, Line: 3, Raw: "<34>Jan  5 10:15:00 x", Stamp: record.Stamp{State: record.YearPending, Month: 1, Day: 5, NanoOfDay: 7, Len: 19}},
	}
	if err := d.Append(ctx, in); err != nil {
		t.Fatal(err)
	}
	var got []record.Record
	err = d.Records(ctx, src, func(id int64, r record.Record) error { got = append(got, r); return nil })
	if err != nil || len(got) != len(in) {
		t.Fatalf("read back %d records, %v", len(got), err)
	}
	for i, wantLen := range []int{19, 0, 19} {
		if got[i].Stamp.Len != wantLen {
			t.Errorf("record %d: Stamp.Len = %d, want %d", i+1, got[i].Stamp.Len, wantLen)
		}
	}
	// Every new record starts in the catch-all, pattern 0, until it is mined.
	var nonzero int
	if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE pattern <> 0`).Scan(&nonzero); err != nil || nonzero != 0 {
		t.Errorf("%d records have a pattern, %v; want none", nonzero, err)
	}
}

// bareExec runs statements with a bare connection on the database at path,
// which no run holds.
func bareExec(t *testing.T, path string, statements ...string) {
	t.Helper()
	db, err := sql.Open(DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func TestOpenRefusesADatabaseOfLayoutOne(t *testing.T) {
	dir := t.TempDir()
	path := plainDatabase(t, dir, now.Add(-time.Hour),
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID`,
		`INSERT INTO meta VALUES ('version', '`+buildinfo.Version+`')`,
		`INSERT INTO meta VALUES ('schema', '1')`,
		`INSERT INTO meta VALUES ('created', '2026-01-20T11:00:00Z')`,
		`INSERT INTO meta VALUES ('complete', '1')`)
	_, err := Open(ctx, path)
	var se *SchemaError
	if !errors.As(err, &se) || se.Have != "1" || se.Want != "2" {
		t.Fatalf("Open = %v, want a *SchemaError from 1 to 2", err)
	}
	if !strings.Contains(err.Error(), `"aigolog clean"`) || !strings.Contains(err.Error(), "layout 1") || !strings.Contains(err.Error(), "layout 2") {
		t.Errorf("message = %q; want it to name both layouts and aigolog clean", err.Error())
	}
	const want = `the database has layout 1, and this aigolog reads layout 2; "aigolog clean" removes it`
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

func TestOpenChecksTheVersionThenTheLayoutThenTheCreatedRowThenTheCompleteMark(t *testing.T) {
	// Each database is a finished one of this version, damaged in the ways
	// named, by a bare connection.
	build := func(t *testing.T, damage ...string) string {
		t.Helper()
		path := made(t, t.TempDir(), now.Add(-time.Hour), true, 1)
		bareExec(t, path, damage...)
		return path
	}
	const (
		badVersion  = `UPDATE meta SET value = '0.0.0-other' WHERE key = 'version'`
		badSchema   = `UPDATE meta SET value = '1' WHERE key = 'schema'`
		badCreated  = `UPDATE meta SET value = 'last Tuesday' WHERE key = 'created'`
		noCreated   = `DELETE FROM meta WHERE key = 'created'`
		notComplete = `UPDATE meta SET value = '0' WHERE key = 'complete'`
		noSchema    = `DELETE FROM meta WHERE key = 'schema'`
	)
	isVersion := func(err error) bool { var e *VersionError; return errors.As(err, &e) }
	isSchema := func(err error) bool { var e *SchemaError; return errors.As(err, &e) }
	isCreated := func(err error) bool {
		return err != nil && strings.Contains(err.Error(), "not a database of aigolog") && strings.Contains(err.Error(), "created") &&
			!isVersion(err) && !isSchema(err) && !errors.Is(err, ErrIncomplete)
	}
	isIncomplete := func(err error) bool { return errors.Is(err, ErrIncomplete) }
	for _, c := range []struct {
		name   string
		damage []string
		check  func(error) bool
		what   string
	}{
		{"the version only", []string{badVersion}, isVersion, "a *VersionError"},
		{"the layout only", []string{badSchema}, isSchema, "a *SchemaError"},
		{"no layout row", []string{noSchema}, isSchema, "a *SchemaError"},
		{"the created row only", []string{badCreated}, isCreated, "the error of the created row"},
		{"no created row", []string{noCreated}, isCreated, "the error of the created row"},
		{"the complete mark only", []string{notComplete}, isIncomplete, "ErrIncomplete"},
		{"all four", []string{badVersion, badSchema, badCreated, notComplete}, isVersion, "a *VersionError"},
		{"layout, created and complete", []string{badSchema, badCreated, notComplete}, isSchema, "a *SchemaError"},
		{"created and complete", []string{badCreated, notComplete}, isCreated, "the error of the created row"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := build(t, c.damage...)
			d, err := Open(ctx, path)
			if err == nil {
				d.Close()
			}
			if !c.check(err) {
				t.Errorf("Open = %v, want %s", err, c.what)
			}
		})
	}
	// And the database that nothing is wrong with opens.
	d, err := Open(ctx, build(t))
	if err != nil {
		t.Fatalf("Open of a good database: %v", err)
	}
	d.Close()
}

func TestOpenReadOnlyDoesNotRecreateADeletedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DatabaseName(now, "0badf00d"))
	for i := 0; i < 2; i++ {
		d, meta, err := openReadOnly(ctx, path)
		if err == nil {
			d.closeConn()
			t.Fatalf("call %d: openReadOnly of a path that does not exist returned no error (meta %v)", i+1, meta)
		}
		if _, serr := os.Stat(path); serr == nil {
			t.Fatalf("call %d: the file exists after openReadOnly: it was made again", i+1)
		}
	}
	if _, err := Open(ctx, path); err == nil {
		t.Error("Open of a path that does not exist returned no error")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the file exists after Open: it was made again")
	}
	if ents, err := os.ReadDir(dir); err != nil || len(ents) != 0 {
		t.Errorf("the folder holds %v, %v; want it empty", ents, err)
	}
	// A path given with a folder that is gone behaves the same.
	if _, _, err := openReadOnly(ctx, filepath.Join(dir, "no-such-folder", "x.db")); err == nil {
		t.Error("openReadOnly with no folder returned no error")
	}
}

func TestResolveYearsLeavesAnUnresolvableDateAsNoStampAndKeepsItsStampLength(t *testing.T) {
	d := newDB(t)
	src, err := d.AddSource(ctx, Source{Name: "s", Format: "text", Encoding: "utf-8", Layout: "syslog", TimeKind: TimeWallYearInferred})
	if err != nil {
		t.Fatal(err)
	}
	// 30 February exists in no year, so YearResolver.Resolve gives no year to
	// it; the record next to it has a date that exists.
	const badLen, goodLen = 15, 16
	recs := []record.Record{
		{Source: src, Raw: "Feb 30 10:00:00 h a", Stamp: record.Stamp{State: record.YearPending, Month: 2, Day: 30, NanoOfDay: int64(10 * time.Hour), Len: badLen}},
		{Source: src, Raw: "Jan  1 01:00:00 h b", Stamp: record.Stamp{State: record.YearPending, Month: 1, Day: 1, NanoOfDay: int64(time.Hour), Len: goodLen}},
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
	if len(got) != 2 {
		t.Fatalf("read back %d records, want 2", len(got))
	}
	if want := (record.Stamp{State: record.NoStamp, Len: badLen}); got[0] != want {
		t.Errorf("the record with 30 February = %+v, want %+v (no stamp, no time, the length kept)", got[0], want)
	}
	want := record.Stamp{State: record.Resolved, Nanos: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC).UnixNano(), Len: goodLen}
	if got[1] != want {
		t.Errorf("the record with 1 January = %+v, want %+v", got[1], want)
	}
}
