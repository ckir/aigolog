// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	sqlite "modernc.org/sqlite"
)

// hotJournal is a database with an interrupted write, as a process that was
// killed in the middle of a commit leaves it.
type hotJournal struct {
	path      string   // the .db file of the copy
	committed []string // the answers that the copy has committed, in order
}

// makeHotJournal makes a database that has a hot journal, in a folder of its
// own, and returns it. It makes a complete database with two stored answers,
// then opens a writable connection with the default rollback journal and a
// cache of five pages, and in one transaction that is never committed
// rewrites the two answers and adds a third, all large, so that the cache
// spills: SQLite writes and syncs the journal, then writes pages of the
// database file. While that transaction is still open the .db file and the
// -journal file are copied to a new folder under the same names (the copy is
// what a process that died at that moment leaves), and then the transaction
// is rolled back. withLock also copies the .lock file.
//
// The test fails, not passes, when the machine produces no hot journal, and
// when a plain read-only open of the copy does not fail with code 776 (so a
// test that uses it cannot pass without a hot journal).
func makeHotJournal(t *testing.T, withLock bool) hotJournal {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a hot journal is made by copying the files of a database that a connection holds open; that is unproven on Windows, where CI runs it")
	}
	src := made(t, t.TempDir(), now.Add(-time.Hour), true, 0)
	big := func(c string) string { return strings.Repeat(c, 100_000) }
	committed := []string{big("a"), big("b")}
	for i, text := range committed {
		if _, err := SaveAnswer(ctx, src, "q", text, now); err != nil {
			t.Fatalf("saving answer %d: %v", i+1, err)
		}
	}

	abs, err := filepath.Abs(src)
	if err != nil {
		t.Fatal(err)
	}
	w, err := sql.Open(DriverName, fileURI(abs, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SetMaxOpenConns(1)
	conn, err := w.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA cache_size=5`); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE answers SET answer = ? || answer`, big("z")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO answers (question, answer, created) VALUES ('q', ?, '2026-01-20T12:00:00Z')`, big("c")); err != nil {
		t.Fatal(err)
	}

	// The transaction is open: copy the files now.
	copyDir := t.TempDir()
	dst := filepath.Join(copyDir, filepath.Base(src))
	files := []string{src, src + "-journal"}
	if withLock {
		files = append(files, lockOf(src))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("this machine did not make a hot journal: reading %s: %v", f, err)
		}
		if err := os.WriteFile(filepath.Join(copyDir, filepath.Base(f)), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if fi, err := os.Stat(dst + "-journal"); err != nil || fi.Size() <= 512 {
		t.Fatalf("this machine did not make a hot journal: %v, %v", fi, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	// The proof that the copy is hot: a read-only open cannot read it.
	d, _, err := openReadOnly(ctx, dst)
	if err == nil {
		d.closeConn()
		t.Fatal("a read-only open of the copy succeeded: it has no hot journal")
	}
	var se *sqlite.Error
	if !errors.As(err, &se) || se.Code() != sqliteReadonlyRollback {
		t.Fatalf("a read-only open of the copy gave %v; want an SQLite error with code %d", err, sqliteReadonlyRollback)
	}
	if !exists(dst + "-journal") {
		t.Fatal("the journal of the copy is gone after a read-only open")
	}
	return hotJournal{path: dst, committed: committed}
}

func TestOpenRollsBackAnInterruptedSaveAndReadsTheDatabase(t *testing.T) {
	hj := makeHotJournal(t, true)
	d, err := Open(ctx, hj.path)
	if err != nil {
		t.Fatalf("Open of a database with a hot journal: %v", err)
	}
	answers, err := d.Answers(ctx)
	if err != nil {
		d.Close()
		t.Fatal(err)
	}
	if len(answers) != len(hj.committed) {
		d.Close()
		t.Fatalf("Answers gave %d answers; want the %d that were committed", len(answers), len(hj.committed))
	}
	for i, a := range answers {
		if a.Answer != hj.committed[i] {
			t.Errorf("answer %d is not the committed one (length %d, want %d)", i+1, len(a.Answer), len(hj.committed[i]))
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if exists(hj.path + "-journal") {
		t.Error("the -journal file is still there after Open")
	}
	// The lock was released: a save takes it, and the next id is 3.
	id, err := SaveAnswer(ctx, hj.path, "q", "next", now)
	if err != nil || id != 3 {
		t.Errorf("SaveAnswer after the recovery = %d, %v; want 3, nil", id, err)
	}
}

func TestOpenOfAHotJournalWithNoLockFileSaysSo(t *testing.T) {
	hj := makeHotJournal(t, false)
	if exists(lockOf(hj.path)) {
		t.Fatal("the copy has a lock file")
	}
	d, err := Open(ctx, hj.path)
	if err == nil {
		d.Close()
		t.Fatal("Open succeeded without a lock file to guard the recovery")
	}
	if !errors.Is(err, ErrNoLock) {
		t.Errorf("Open = %v; want ErrNoLock", err)
	}
	if !strings.Contains(err.Error(), "aigolog clean") {
		t.Errorf("the message %q does not say that aigolog clean removes the database", err)
	}
	if !exists(hj.path + "-journal") {
		t.Error("the journal was touched")
	}
	if exists(lockOf(hj.path)) {
		t.Error("Open made a lock file")
	}
}

func TestOpenOfAHotJournalWhoseLockIsHeldSaysSo(t *testing.T) {
	hj := makeHotJournal(t, true)
	lock, running, err := acquire(lockOf(hj.path))
	if err != nil || running || lock == nil {
		t.Fatalf("acquire = %v, %v, %v; want the lock", lock, running, err)
	}
	d, err := Open(ctx, hj.path)
	release(lock)
	if err == nil {
		d.Close()
		t.Fatal("Open succeeded while another holder had the lock")
	}
	if !errors.Is(err, ErrLocked) {
		t.Errorf("Open = %v; want ErrLocked", err)
	}
	if !strings.Contains(err.Error(), "another process") || !strings.Contains(err.Error(), "try again") {
		t.Errorf("the message %q does not say that another process uses the database and to try again", err)
	}
	if !exists(hj.path + "-journal") {
		t.Error("the journal was touched")
	}
	// Once the lock is released the same database opens.
	d, err = Open(ctx, hj.path)
	if err != nil {
		t.Fatalf("Open after the lock was released: %v", err)
	}
	d.Close()
}

func TestOpenOfAHotJournalThatCannotBeRolledBackSaysSo(t *testing.T) {
	hj := makeHotJournal(t, true)
	dir := filepath.Dir(hj.path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := os.Chmod(hj.path, 0o400); err != nil {
		t.Fatal(err)
	}
	if f, err := os.CreateTemp(dir, "probe"); err == nil {
		f.Close()
		os.Remove(f.Name())
		t.Skip("this process can write a read-only folder (it runs with more rights than a user), so a recovery cannot be made to fail")
	}
	d, err := Open(ctx, hj.path)
	if err == nil {
		d.Close()
		t.Fatal("Open succeeded in a folder that cannot be written")
	}
	for _, want := range []string{"unfinished write", "aigolog clean"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message %q does not contain %q", err, want)
		}
	}
	if errors.Is(err, ErrLocked) || errors.Is(err, ErrNoLock) {
		t.Errorf("Open = %v; the cause is neither a held lock nor a missing lock file", err)
	}
	// The lock was released even though the recovery failed.
	lock, running, aerr := acquire(lockOf(hj.path))
	if aerr != nil || running || lock == nil {
		t.Errorf("acquire after the failed recovery = %v, %v, %v; want the lock", lock, running, aerr)
	}
	release(lock)
}

func TestListAndSweepLeaveAHotJournalDatabaseAlone(t *testing.T) {
	hj := makeHotJournal(t, true)
	dir := filepath.Dir(hj.path)
	infos, err := List(dir)
	if err != nil || len(infos) != 1 {
		t.Fatalf("List = %+v, %v", infos, err)
	}
	if infos[0].Path != hj.path || infos[0].State != "unreadable" {
		t.Errorf("List = %+v; want the database in the state unreadable", infos[0])
	}
	res, err := Sweep(dir, Policy{MaxAge: 1, Now: now.Add(1000 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 || !exists(hj.path) {
		t.Errorf("Sweep = %+v, exists %v; want nothing removed", res, exists(hj.path))
	}
	if !exists(hj.path + "-journal") {
		t.Error("Sweep or List touched the journal: they must stay pure reads")
	}
}
