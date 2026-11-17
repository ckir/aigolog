// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/record"
)

var now = time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)

// made makes a database in dir that was created at "created". It is closed
// (its run is over), complete or not, with about "rows" records in it.
func made(t *testing.T, dir string, created time.Time, complete bool, rows int) string {
	t.Helper()
	d, err := Create(ctx, Options{Dir: dir, Now: fixed(created)})
	if err != nil {
		t.Fatal(err)
	}
	src, _ := d.AddSource(ctx, Source{Name: "s", Format: "text", Encoding: "utf-8", TimeKind: TimeNone})
	recs := make([]record.Record, rows)
	for i := range recs {
		recs[i] = record.Record{Source: src, Raw: strings.Repeat("x", 200)}
	}
	if rows > 0 {
		if err := d.Append(ctx, recs); err != nil {
			t.Fatal(err)
		}
	}
	d.FinishSource(ctx, src, int64(rows), 0)
	if complete {
		d.MarkComplete(ctx)
	}
	path := d.Path()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestListShowsStateRecordsAndVersionNewestFirst(t *testing.T) {
	dir := t.TempDir()
	older := made(t, dir, now.Add(-48*time.Hour), true, 10)
	newer := made(t, dir, now.Add(-1*time.Hour), false, 0)
	infos, err := List(dir)
	if err != nil || len(infos) != 2 {
		t.Fatalf("List = %+v, %v", infos, err)
	}
	if infos[0].Path != newer || infos[0].State != "incomplete" {
		t.Errorf("first = %+v", infos[0])
	}
	if infos[1].Path != older || infos[1].State != "complete" || infos[1].Records != 10 || infos[1].Version == "" || infos[1].Size <= 0 {
		t.Errorf("second = %+v", infos[1])
	}
	if got, err := List(dir + "-missing"); err != nil || len(got) != 0 {
		t.Errorf("List of a missing folder = %+v, %v", got, err)
	}
}

func TestListShowsALiveRunWithoutOpeningItsDatabase(t *testing.T) {
	dir := t.TempDir()
	live, err := Create(ctx, Options{Dir: dir, Now: fixed(now)})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	infos, err := List(dir)
	if err != nil || len(infos) != 1 || infos[0].State != "running" || infos[0].Path != live.Path() {
		t.Errorf("List = %+v, %v", infos, err)
	}
}

func TestSweepRemovesAnInterruptedRunButNeverALiveOne(t *testing.T) {
	dir := t.TempDir()
	dead := made(t, dir, now.Add(-time.Hour), false, 5)
	live, err := Create(ctx, Options{Dir: dir, Now: fixed(now.Add(-time.Minute))})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	res, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, MaxTotalBytes: 1 << 30, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if exists(dead) || !exists(live.Path()) {
		t.Errorf("dead exists %v, live exists %v", exists(dead), exists(live.Path()))
	}
	if len(res.Removed) != 1 || res.Removed[0].Path != dead || len(res.Running) != 1 {
		t.Errorf("result = %+v", res)
	}
	if exists(strings.TrimSuffix(dead, ".db") + ".lock") {
		t.Error("the removed run's lock file was left behind")
	}
}

func TestSweepRemovesCompleteDatabasesOlderThanTheLimitOnly(t *testing.T) {
	dir := t.TempDir()
	old := made(t, dir, now.Add(-8*24*time.Hour), true, 3)
	edge := made(t, dir, now.Add(-6*24*time.Hour-23*time.Hour), true, 3)
	fresh := made(t, dir, now.Add(-time.Hour), true, 3)
	if _, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, MaxTotalBytes: 1 << 30, Now: now}); err != nil {
		t.Fatal(err)
	}
	if exists(old) || !exists(edge) || !exists(fresh) {
		t.Errorf("old %v, edge %v, fresh %v", exists(old), exists(edge), exists(fresh))
	}
}

func TestSweepKeepsToTheTotalSizeLimitByRemovingTheOldestFirst(t *testing.T) {
	dir := t.TempDir()
	a := made(t, dir, now.Add(-3*time.Hour), true, 2000)
	b := made(t, dir, now.Add(-2*time.Hour), true, 2000)
	c := made(t, dir, now.Add(-1*time.Hour), true, 2000)
	fa, _ := os.Stat(a)
	limit := fa.Size()*2 + 1000 // room for two of the three
	if _, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, MaxTotalBytes: limit, Now: now}); err != nil {
		t.Fatal(err)
	}
	if exists(a) || !exists(b) || !exists(c) {
		t.Errorf("oldest exists %v, middle %v, newest %v; want only the oldest gone", exists(a), exists(b), exists(c))
	}
}

func TestSweepNeverRemovesALiveRunToMeetTheSizeLimit(t *testing.T) {
	dir := t.TempDir()
	live, _ := Create(ctx, Options{Dir: dir, Now: fixed(now)})
	defer live.Close()
	if _, err := Sweep(dir, Policy{MaxAge: time.Hour, MaxTotalBytes: 1, Now: now}); err != nil {
		t.Fatal(err)
	}
	if !exists(live.Path()) {
		t.Error("a live run was removed")
	}
}

func TestSweepAllRemovesEveryDatabaseThatIsNotRunning(t *testing.T) {
	dir := t.TempDir()
	a := made(t, dir, now.Add(-time.Hour), true, 1)
	b := made(t, dir, now.Add(-time.Minute), false, 1)
	live, _ := Create(ctx, Options{Dir: dir, Now: fixed(now)})
	defer live.Close()
	res, err := Sweep(dir, Policy{All: true, Now: now})
	if err != nil || exists(a) || exists(b) || !exists(live.Path()) || len(res.Removed) != 2 {
		t.Errorf("a %v, b %v, live %v, removed %d, err %v", exists(a), exists(b), exists(live.Path()), len(res.Removed), err)
	}
}

func TestSweepRemovesAnOrphanLockFileAndLeavesForeignFilesAlone(t *testing.T) {
	dir := t.TempDir()
	orphan := dir + "/20260101T000000Z-deadbeef.lock"
	foreign := dir + "/notes.txt"
	os.WriteFile(orphan, nil, 0o600)
	os.WriteFile(foreign, []byte("mine"), 0o600)
	if _, err := Sweep(dir, Policy{MaxAge: time.Hour, MaxTotalBytes: 1 << 30, Now: now}); err != nil {
		t.Fatal(err)
	}
	if exists(orphan) || !exists(foreign) {
		t.Errorf("orphan exists %v, foreign exists %v", exists(orphan), exists(foreign))
	}
}

func TestSweepOfAMissingFolderIsNotAnError(t *testing.T) {
	if _, err := Sweep(t.TempDir()+"/nope", Policy{All: true, Now: now}); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestAnOrphanLockStaysHeldWhileSweepDeletesIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot delete a file that this process holds open, so there the fallback applies")
	}
	dir := t.TempDir()
	orphan := dir + "/20260101T000000Z-deadbeef.lock"
	if err := os.WriteFile(orphan, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	calls, held := 0, false
	removeFile = func(path string) error {
		if path == orphan {
			calls++
			f, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Errorf("opening the orphan lock: %v", err)
			} else {
				locked, err := tryLockFile(f)
				if err != nil {
					t.Errorf("locking the orphan lock: %v", err)
				}
				held = !locked
				if locked {
					unlockFile(f)
				}
				f.Close()
			}
		}
		return os.Remove(path)
	}
	t.Cleanup(func() { removeFile = os.Remove })
	if _, err := Sweep(dir, Policy{MaxAge: time.Hour, MaxTotalBytes: 1 << 30, Now: now}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !held {
		t.Errorf("the delete was called %d times, lock held at that moment: %v; want once, held", calls, held)
	}
	if exists(orphan) {
		t.Error("the orphan lock was not removed")
	}
}

// lockOf returns the path of the lock file of the database at path.
func lockOf(path string) string { return strings.TrimSuffix(path, ".db") + ".lock" }

// plainDatabase makes a database file named like ours, with the statements
// given and no table of aigolog, in dir; it makes the lock file too.
func plainDatabase(t *testing.T, dir string, created time.Time, statements ...string) string {
	t.Helper()
	path := filepath.Join(dir, DatabaseName(created, "0badf00d"))
	db, err := sql.Open(DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockOf(path), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSweepKeepsADatabaseThatItCannotOpenForWantOfPermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not deny access on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can open a file whatever its mode")
	}
	for _, name := range []string{"by age and state", "by the size limit"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := made(t, dir, now.Add(-time.Hour), true, 3)
			if err := os.Chmod(path, 0o000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(path, 0o600) })
			p := Policy{MaxAge: 7 * 24 * time.Hour, MaxTotalBytes: 1 << 30, Now: now}
			if name == "by the size limit" {
				p.MaxTotalBytes = 1
			}
			res, err := Sweep(dir, p)
			if err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			if !exists(path) || !exists(lockOf(path)) {
				t.Errorf("db exists %v, lock exists %v; want both kept", exists(path), exists(lockOf(path)))
			}
			if len(res.Removed) != 0 || len(res.Kept) != 1 || res.Kept[0].Path != path || res.Kept[0].State != "unreadable" {
				t.Errorf("result = %+v; want it kept and shown as unreadable", res)
			}
		})
	}
}

func TestSweepKeepsADatabaseWhoseRecordsItCannotCount(t *testing.T) {
	// A complete database whose records table another version renamed: the
	// program cannot tell what it is, so it does not remove it.
	dir := t.TempDir()
	path := made(t, dir, now.Add(-time.Hour), true, 3)
	db, err := sql.Open(DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE records RENAME TO entries`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	res, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, MaxTotalBytes: 1 << 30, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !exists(path) || !exists(lockOf(path)) || len(res.Kept) != 1 || res.Kept[0].State != "unreadable" {
		t.Errorf("db %v, lock %v, result %+v; want both kept as unreadable", exists(path), exists(lockOf(path)), res)
	}
}

func TestSweepRefusesAFolderTheDriverWouldMisread(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a name with \"?\" cannot be made on Windows")
	}
	src := made(t, t.TempDir(), now.Add(-time.Hour), true, 3)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "a?b")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filepath.Base(src))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockOf(path), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Sweep(dir, Policy{All: true, Now: now})
	if err == nil || !strings.Contains(err.Error(), dir) {
		t.Errorf("Sweep error = %v; want one that names the folder", err)
	}
	if len(res.Removed)+len(res.Kept)+len(res.Running) != 0 {
		t.Errorf("result = %+v; want it empty", res)
	}
	if !exists(path) || !exists(lockOf(path)) {
		t.Errorf("db exists %v, lock exists %v; want both left alone", exists(path), exists(lockOf(path)))
	}
}

func TestSweepRemovesAZeroLengthDatabaseAndAnEmptyMetaDatabase(t *testing.T) {
	dir := t.TempDir()
	zero := filepath.Join(dir, DatabaseName(now.Add(-time.Hour), "00000001"))
	garbage := filepath.Join(dir, DatabaseName(now.Add(-2*time.Hour), "00000002"))
	noComplete := plainDatabase(t, dir, now.Add(-3*time.Hour), `CREATE TABLE meta (key TEXT, value TEXT)`, `INSERT INTO meta VALUES ('version', 'x')`)
	noMeta := plainDatabase(t, dir, now.Add(-4*time.Hour), `CREATE TABLE x (a)`)
	if err := os.WriteFile(zero, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(garbage, []byte(strings.Repeat("this is not a database ", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{zero, garbage} {
		if err := os.WriteFile(lockOf(p), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, MaxTotalBytes: 1 << 30, Now: now})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	for name, p := range map[string]string{"zero length": zero, "not a database": garbage, "no complete row": noComplete, "no meta table": noMeta} {
		if exists(p) || exists(lockOf(p)) {
			t.Errorf("%s: db exists %v, lock exists %v; want both removed", name, exists(p), exists(lockOf(p)))
		}
	}
	if len(res.Removed) != 4 || len(res.Kept) != 0 {
		t.Errorf("result = %+v; want four removed", res)
	}
}

func TestAnUnreadableDatabaseDoesNotCountTowardTheSizeLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not deny access on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can open a file whatever its mode")
	}
	dir := t.TempDir()
	big := made(t, dir, now.Add(-3*time.Hour), true, 2000)
	a := made(t, dir, now.Add(-2*time.Hour), true, 0)
	b := made(t, dir, now.Add(-1*time.Hour), true, 0)
	fa, _ := os.Stat(a)
	fb, _ := os.Stat(b)
	fbig, _ := os.Stat(big)
	limit := fa.Size() + fb.Size() + 100 // room for the two small ones
	if fbig.Size() < limit {
		t.Fatalf("the unreadable database (%d bytes) must be at least the limit (%d)", fbig.Size(), limit)
	}
	if err := os.Chmod(big, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(big, 0o600) })
	res, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, MaxTotalBytes: limit, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 || len(res.Kept) != 3 || !exists(a) || !exists(b) || !exists(big) {
		t.Errorf("result = %+v; want nothing removed, three kept", res)
	}
}

func TestCleanAllRemovesAnUnsureDatabaseWhoseLockCanBeTested(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not deny access on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can open a file whatever its mode")
	}
	dir := t.TempDir()
	path := made(t, dir, now.Add(-time.Hour), true, 3)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })
	res, err := Sweep(dir, Policy{All: true, Now: now})
	if err != nil || exists(path) || len(res.Removed) != 1 {
		t.Errorf("exists %v, removed %d, err %v; want it removed by All", exists(path), len(res.Removed), err)
	}
}

// threeDatabases makes three complete databases, the oldest first, with the
// number of records given for each.
func threeDatabases(t *testing.T, dir string, rows [3]int) (a, b, c string) {
	t.Helper()
	a = made(t, dir, now.Add(-3*time.Hour), true, rows[0])
	b = made(t, dir, now.Add(-2*time.Hour), true, rows[1])
	c = made(t, dir, now.Add(-1*time.Hour), true, rows[2])
	return a, b, c
}

func TestSweepDoesNotRemoveARunThatBeganBetweenTheScanAndTheSizePhase(t *testing.T) {
	dir := t.TempDir()
	a, b, _ := threeDatabases(t, dir, [3]int{2000, 2000, 2000})
	fa, _ := os.Stat(a)
	limit := fa.Size() + 1000 // room for one of the three: A and B are to go
	var held *os.File
	beforeSizePhase = func() {
		beforeSizePhase = nil
		lock, running, err := acquire(lockOf(b))
		if err != nil || running || lock == nil {
			t.Errorf("taking the lock of the second database: %v, running %v, lock %v", err, running, lock != nil)
		}
		held = lock
	}
	t.Cleanup(func() { beforeSizePhase = nil; release(held) })
	res, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, MaxTotalBytes: limit, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if held == nil {
		t.Fatal("beforeSizePhase did not run")
	}
	// The newest (C) goes too: a running database counts toward the limit and
	// cannot be removed, so the next one is removed instead; the test does not
	// depend on that.
	if exists(a) {
		t.Error("the oldest database was not removed")
	}
	if !exists(b) || !exists(lockOf(b)) {
		t.Errorf("the database that began to run was removed: db %v, lock %v", exists(b), exists(lockOf(b)))
	}
	var running []string
	for _, i := range res.Running {
		running = append(running, i.Path)
	}
	if len(running) != 1 || running[0] != b {
		t.Errorf("Running = %v, want [%s]", running, b)
	}
	for _, i := range res.Removed {
		if i.Path == b {
			t.Error("the database that began to run is in Removed")
		}
	}
	for _, i := range res.Kept {
		if i.Path == b {
			t.Error("the database that began to run is in Kept")
		}
	}
}

func TestSweepCountsTheSizeAtTheTimeOfTheSizePhase(t *testing.T) {
	dir := t.TempDir()
	a, b, c := threeDatabases(t, dir, [3]int{8000, 0, 0})
	fa, _ := os.Stat(a)
	fb, _ := os.Stat(b)
	fc, _ := os.Stat(c)
	const grown = 1 << 20
	if fa.Size() < grown {
		t.Fatalf("the oldest database (%d bytes) must be at least the growth (%d)", fa.Size(), grown)
	}
	limit := fa.Size() + fb.Size() + fc.Size() + 1000 // the sizes at the scan fit
	beforeSizePhase = func() {
		beforeSizePhase = nil
		f, err := os.OpenFile(c, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		if _, err := f.Write(make([]byte, grown)); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { beforeSizePhase = nil })
	res, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, MaxTotalBytes: limit, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if exists(a) || !exists(b) || !exists(c) {
		t.Errorf("oldest exists %v, middle %v, newest %v; want only the oldest gone", exists(a), exists(b), exists(c))
	}
	for _, i := range res.Kept {
		if i.Path == c && i.Size != fc.Size()+grown {
			t.Errorf("the size of the newest in Kept is %d, want %d (the size at the size phase)", i.Size, fc.Size()+grown)
		}
	}
}

// failsFirst makes removeFile fail with "in use" for the first "fails" calls
// and call os.Remove after that; it returns how many times it was called. It
// also checks the state of the lock at each call: at the first call
// removeOrphanLock still holds it (the delete is made under the lock), at a
// later call it has been released and closed, so that the lock can be taken
// here (where a file that is open cannot be deleted, this is what lets the
// second delete succeed). The lock taken here is released before the delete.
func failsFirst(t *testing.T, fails int) *int {
	t.Helper()
	calls := 0
	removeFile = func(path string) error {
		calls++
		lock, running, err := acquire(path)
		switch {
		case err != nil:
			t.Errorf("call %d: taking the lock of the orphan: %v", calls, err)
		case calls == 1 && !running:
			t.Error("call 1: the lock was not held during the first delete")
		case calls > 1 && (running || lock == nil):
			t.Errorf("call %d: the lock was not released and closed before the next delete (held by another: %v)", calls, running)
		}
		release(lock)
		if calls <= fails {
			return errors.New("in use")
		}
		return os.Remove(path)
	}
	t.Cleanup(func() { removeFile = os.Remove })
	return &calls
}

func TestRemoveOrphanLockClosesTheHandleAndTriesAgainWhenTheFirstDeleteFails(t *testing.T) {
	orphan := filepath.Join(t.TempDir(), "20260101T000000Z-deadbeef.lock")
	if err := os.WriteFile(orphan, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	calls := failsFirst(t, 1)
	if err := removeOrphanLock(orphan); err != nil {
		t.Errorf("removeOrphanLock = %v, want nil", err)
	}
	if exists(orphan) {
		t.Error("the orphan lock was not removed by the second delete")
	}
	if *calls != 2 {
		t.Errorf("removeFile was called %d times, want 2", *calls)
	}
}

func TestRemoveOrphanLockLeavesAFileThatCannotBeDeletedAndReportsNothing(t *testing.T) {
	orphan := filepath.Join(t.TempDir(), "20260101T000000Z-deadbeef.lock")
	if err := os.WriteFile(orphan, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	calls := failsFirst(t, 2)
	if err := removeOrphanLock(orphan); err != nil {
		t.Errorf("removeOrphanLock = %v, want nil", err)
	}
	if !exists(orphan) {
		t.Error("the orphan lock was removed although both deletes failed")
	}
	if *calls != 2 {
		t.Errorf("removeFile was called %d times, want 2", *calls)
	}
}

// layoutOne makes a complete database as version B1 of aigolog made it: a
// meta table with schema 1, and no header.
func layoutOne(t *testing.T, dir string, created time.Time) string {
	t.Helper()
	return plainDatabase(t, dir, created,
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID`,
		`INSERT INTO meta VALUES ('version', 'b1')`,
		`INSERT INTO meta VALUES ('schema', '1')`,
		`INSERT INTO meta VALUES ('created', '2026-01-20T11:00:00Z')`,
		`INSERT INTO meta VALUES ('complete', '1')`)
}

// laterLayout makes a database whose header is ours and whose user_version is
// the one given; its meta table is named metaName, with the rows of a finished
// run.
func laterLayout(t *testing.T, dir string, created time.Time, userVersion int, metaName string) string {
	t.Helper()
	return plainDatabase(t, dir, created,
		fmt.Sprintf(`PRAGMA application_id = %d`, applicationID),
		fmt.Sprintf(`PRAGMA user_version = %d`, userVersion),
		`CREATE TABLE `+metaName+` (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID`,
		`INSERT INTO `+metaName+` VALUES ('version', 'later')`,
		`INSERT INTO `+metaName+` VALUES ('schema', '3')`,
		`INSERT INTO `+metaName+` VALUES ('complete', '1')`)
}

func TestAListedDatabaseOfLayoutOneIsShownAsOtherAndAgedOut(t *testing.T) {
	dir := t.TempDir()
	path := layoutOne(t, dir, now.Add(-48*time.Hour))
	infos, err := List(dir)
	if err != nil || len(infos) != 1 {
		t.Fatalf("List = %+v, %v", infos, err)
	}
	if infos[0].State != "other" || infos[0].unsure || infos[0].Version != "b1" {
		t.Errorf("info = %+v; want state other, not unsure, version b1", infos[0])
	}

	// A larger age limit keeps it (the edit of Sweep: other is treated like
	// complete, not like an interrupted run that goes at once).
	if _, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, Now: now}); err != nil || !exists(path) || !exists(lockOf(path)) {
		t.Fatalf("a database of another layout was removed by an age limit it is younger than: %v, exists %v", err, exists(path))
	}
	// A smaller one removes it, with its lock file.
	res, err := Sweep(dir, Policy{MaxAge: 24 * time.Hour, Now: now})
	if err != nil || exists(path) || exists(lockOf(path)) || len(res.Removed) != 1 {
		t.Errorf("Sweep with a smaller limit: %+v, %v; want it removed", res, err)
	}

	// All removes it, whatever its age.
	path = layoutOne(t, dir, now.Add(-time.Hour))
	if _, err := Sweep(dir, Policy{Now: now}); err != nil || !exists(path) {
		t.Fatalf("a young database of another layout was removed with no limit: %v", err)
	}
	if _, err := Sweep(dir, Policy{All: true, Now: now}); err != nil || exists(path) {
		t.Errorf("Sweep All left it: %v, exists %v", err, exists(path))
	}
}

func TestADatabaseWhoseMetaTableWasRenamedIsOtherNotRemoved(t *testing.T) {
	dir := t.TempDir()
	recent := laterLayout(t, dir, now.Add(-time.Hour), 3, "meta2")
	old := laterLayout(t, dir, now.Add(-10*24*time.Hour), 3, "meta2")
	infos, err := List(dir)
	if err != nil || len(infos) != 2 {
		t.Fatalf("List = %+v, %v", infos, err)
	}
	for _, in := range infos {
		if in.State != "other" || in.unsure {
			t.Errorf("%s: %+v; want state other, not unsure", in.Path, in)
		}
		// The meta table is not there to read, so the version is unknown.
		if in.Version != "" {
			t.Errorf("%s: version %q; want it empty (the meta table has another name)", in.Path, in.Version)
		}
	}
	res, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !exists(recent) || exists(old) || len(res.Removed) != 1 || res.Removed[0].Path != old {
		t.Errorf("recent exists %v, old exists %v, result %+v; want only the old one removed by age", exists(recent), exists(old), res)
	}
	old = laterLayout(t, dir, now.Add(-10*24*time.Hour), 3, "meta2")
	if _, err := Sweep(dir, Policy{All: true, Now: now}); err != nil || exists(recent) || exists(old) {
		t.Errorf("All: %v, recent exists %v, old exists %v; want both gone", err, exists(recent), exists(old))
	}
}

func TestADatabaseOfALaterLayoutShowsItsVersionWhenMetaIsStillThere(t *testing.T) {
	dir := t.TempDir()
	laterLayout(t, dir, now.Add(-time.Hour), 3, "meta")
	infos, err := List(dir)
	if err != nil || len(infos) != 1 || infos[0].State != "other" || infos[0].Version != "later" {
		t.Errorf("List = %+v, %v; want state other and version later", infos, err)
	}
}

func TestADatabaseWithOurHeaderAndNoMetaTableIsKeptAsUnreadable(t *testing.T) {
	dir := t.TempDir()
	old := plainDatabase(t, dir, now.Add(-30*24*time.Hour),
		fmt.Sprintf(`PRAGMA application_id = %d`, applicationID),
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaNumber),
		`CREATE TABLE records (id INTEGER PRIMARY KEY)`)
	infos, err := List(dir)
	if err != nil || len(infos) != 1 || infos[0].State != "unreadable" || !infos[0].unsure {
		t.Fatalf("List = %+v, %v; want unreadable and unsure", infos, err)
	}
	res, err := Sweep(dir, Policy{MaxAge: time.Hour, MaxTotalBytes: 1, Now: now})
	if err != nil || !exists(old) || !exists(lockOf(old)) || len(res.Removed) != 0 || len(res.Kept) != 1 {
		t.Errorf("Sweep: %+v, %v; want it kept even when old and over the size limit", res, err)
	}
	// A database with no header and no meta table is still not ours.
	other := plainDatabase(t, dir, now.Add(-31*24*time.Hour), `CREATE TABLE records (id INTEGER PRIMARY KEY)`)
	if _, err := Sweep(dir, Policy{MaxAge: time.Hour, Now: now}); err != nil || exists(other) || !exists(old) {
		t.Errorf("Sweep: %v, other exists %v, old exists %v; want only the one with no header removed", err, exists(other), exists(old))
	}
	if _, err := Sweep(dir, Policy{All: true, Now: now}); err != nil || exists(old) {
		t.Errorf("Sweep All: %v, exists %v; want it removed (its lock could be tested)", err, exists(old))
	}
}

func TestADamagedHeaderIsStillRemovedAsNotADatabase(t *testing.T) {
	dir := t.TempDir()
	path := made(t, dir, now.Add(-time.Hour), true, 3)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	copy(data, "not a sqlite header ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	infos, err := List(filepath.Dir(path))
	if err != nil || len(infos) != 1 || infos[0].State != "unreadable" || infos[0].unsure {
		t.Fatalf("List = %+v, %v; want unreadable and not unsure", infos, err)
	}
	if _, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, Now: now}); err != nil || exists(path) {
		t.Errorf("Sweep: %v, exists %v; want it removed", err, exists(path))
	}
}

func TestOtherDatabasesCountTowardTheSizeLimit(t *testing.T) {
	dir := t.TempDir()
	pad := func(created time.Time) string {
		return plainDatabase(t, dir, created,
			fmt.Sprintf(`PRAGMA application_id = %d`, applicationID),
			`PRAGMA user_version = 3`,
			`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID`,
			`INSERT INTO meta VALUES ('complete', '1')`,
			`CREATE TABLE pad (x BLOB)`,
			`WITH RECURSIVE c(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM c WHERE n < 200) INSERT INTO pad SELECT zeroblob(1000) FROM c`)
	}
	older := pad(now.Add(-2 * time.Hour))
	newer := pad(now.Add(-1 * time.Hour))
	fo, _ := os.Stat(older)
	fn, _ := os.Stat(newer)
	limit := fn.Size() + 100 // room for the newer one only
	if fo.Size() > limit {
		t.Fatalf("the older (%d bytes) must not be over the limit (%d) alone", fo.Size(), limit)
	}
	res, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, MaxTotalBytes: limit, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if exists(older) || !exists(newer) || len(res.Removed) != 1 || res.Removed[0].Path != older {
		t.Errorf("older exists %v, newer exists %v, result %+v; want only the older removed", exists(older), exists(newer), res)
	}
}

// copyOf writes the bytes of the file at src, cut to size bytes, as a database
// named like ours (created) in dir, with a lock file next to it.
func copyOf(t *testing.T, src, dir string, created time.Time, size int64) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, DatabaseName(created, "c0ffee00"))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockOf(path), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestATruncatedFinishedDatabaseIsKeptAsUnreadable(t *testing.T) {
	whole := made(t, t.TempDir(), now.Add(-30*24*time.Hour), true, 3000)
	fi, err := os.Stat(whole)
	if err != nil {
		t.Fatal(err)
	}
	full := fi.Size()
	if full < 20*4096 {
		t.Fatalf("the database is %d bytes; the test needs more than 20 pages", full)
	}
	pageBelow := (full/4096 - 3) * 4096
	for _, c := range []struct {
		name string
		size int64
	}{
		{"a page multiple below the real size", pageBelow},
		{"in the middle of a page", pageBelow/2 + 1234},
		{"exactly one page", 4096},
		{"one page and 100 bytes", 4196},
		{"exactly the header", 100},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			path := copyOf(t, whole, dir, now.Add(-30*24*time.Hour), c.size)
			if got, _ := os.Stat(path); got.Size() != c.size {
				t.Fatalf("the copy is %d bytes, want %d", got.Size(), c.size)
			}
			infos, err := List(dir)
			if err != nil || len(infos) != 1 || infos[0].State != "unreadable" || !infos[0].unsure {
				t.Fatalf("List = %+v, %v; want unreadable and unsure", infos, err)
			}
			// Old, over every limit: not removed.
			res, err := Sweep(dir, Policy{MaxAge: time.Nanosecond, MaxTotalBytes: 1, Now: now})
			if err != nil || !exists(path) || !exists(lockOf(path)) || len(res.Removed) != 0 || len(res.Kept) != 1 {
				t.Fatalf("Sweep with a tiny age and size limit: %+v, %v; want it kept", res, err)
			}
			// All removes it, as the lock can be tested.
			res, err = Sweep(dir, Policy{All: true, Now: now})
			if err != nil || exists(path) || exists(lockOf(path)) || len(res.Removed) != 1 {
				t.Errorf("Sweep All: %+v, %v; want it removed with its lock file", res, err)
			}
		})
	}
}

func TestATruncatedFileShorterThanItsHeaderIsStillRemoved(t *testing.T) {
	whole := made(t, t.TempDir(), now.Add(-30*24*time.Hour), true, 100)
	for _, size := range []int64{50, 99} {
		dir := t.TempDir()
		path := copyOf(t, whole, dir, now.Add(-time.Hour), size)
		infos, err := List(dir)
		if err != nil || len(infos) != 1 || infos[0].State != "unreadable" || infos[0].unsure {
			t.Fatalf("%d bytes: List = %+v, %v; want unreadable and not unsure", size, infos, err)
		}
		if _, err := Sweep(dir, Policy{MaxAge: 7 * 24 * time.Hour, Now: now}); err != nil || exists(path) {
			t.Errorf("%d bytes: Sweep: %v, exists %v; want it removed", size, err, exists(path))
		}
	}
}
