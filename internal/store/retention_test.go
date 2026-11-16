// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"database/sql"
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
