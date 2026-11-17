// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store_test

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ckir/aigolog/internal/store"
)

// answerClock is the time given to SaveAnswer; it has a fraction of a second,
// which the stored text does not keep.
var answerClock = time.Date(2026, time.January, 16, 10, 15, 30, 789_000_000, time.UTC)

// finishedPath makes the database of a finished run, closed, and returns its
// path.
func finishedPath(t *testing.T) string {
	t.Helper()
	d, err := store.Create(bg, store.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.MarkComplete(bg); err != nil {
		t.Fatal(err)
	}
	path := d.Path()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// lockPathOf returns the path of the lock file of the database at path.
func lockPathOf(path string) string { return strings.TrimSuffix(path, ".db") + ".lock" }

// stored opens the database at path, reads its answers and closes it.
func stored(t *testing.T, path string) []store.StoredAnswer {
	t.Helper()
	d, err := store.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	as, err := d.Answers(bg)
	if err != nil {
		t.Fatal(err)
	}
	return as
}

func TestAnAnswersTableIsMadeEmptyByCreate(t *testing.T) {
	path := finishedPath(t)
	db, err := sql.Open(store.DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name, type, "notnull", pk FROM pragma_table_info('answers') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name, typ string
		var notNull, pk int
		if err := rows.Scan(&name, &typ, &notNull, &pk); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s %s %d %d", name, typ, notNull, pk))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"id INTEGER 0 1", "question TEXT 1 0", "answer TEXT 1 0", "created TEXT 1 0"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("the columns of answers = %q, want %q", got, want)
	}
	as := stored(t, path)
	if as == nil || len(as) != 0 {
		t.Errorf("Answers of a new database = %#v, want an empty list that is not nil", as)
	}
	d, err := store.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Answer(bg, 1); !errors.Is(err, store.ErrNoAnswer) {
		t.Errorf("Answer(1) of an empty table = %v, want ErrNoAnswer", err)
	}
}

func TestSaveAnswerStoresTheQuestionAndTheAnswerAndGivesIncreasingIDs(t *testing.T) {
	path := finishedPath(t)
	id1, err := store.SaveAnswer(bg, path, "count --where host=a", "12 records\n", answerClock)
	if err != nil || id1 != 1 {
		t.Fatalf("first SaveAnswer = %d, %v; want 1, nil", id1, err)
	}
	later := answerClock.Add(90 * time.Second)
	id2, err := store.SaveAnswer(bg, path, "top --slot s1", "x\ty\n", later)
	if err != nil || id2 != 2 {
		t.Fatalf("second SaveAnswer = %d, %v; want 2, nil", id2, err)
	}
	as := stored(t, path)
	if len(as) != 2 {
		t.Fatalf("Answers = %+v, want 2", as)
	}
	want := []store.StoredAnswer{
		{ID: 1, Question: "count --where host=a", Answer: "12 records\n", Created: answerClock.Truncate(time.Second)},
		{ID: 2, Question: "top --slot s1", Answer: "x\ty\n", Created: later.Truncate(time.Second)},
	}
	for i, w := range want {
		g := as[i]
		if g.ID != w.ID || g.Question != w.Question || g.Answer != w.Answer || !g.Created.Equal(w.Created) {
			t.Errorf("Answers[%d] = %+v, want %+v", i, g, w)
		}
	}
	d, err := store.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	one, err := d.Answer(bg, 2)
	if err != nil || one.ID != 2 || one.Question != "top --slot s1" || one.Answer != "x\ty\n" || !one.Created.Equal(want[1].Created) {
		t.Errorf("Answer(2) = %+v, %v; want %+v", one, err, want[1])
	}
	if _, err := d.Answer(bg, 3); !errors.Is(err, store.ErrNoAnswer) {
		t.Errorf("Answer(3) = %v, want ErrNoAnswer", err)
	}
	// The lock is released: a third save goes through.
	if id, err := store.SaveAnswer(bg, path, "q", "a", answerClock); err != nil || id != 3 {
		t.Errorf("third SaveAnswer = %d, %v; want 3, nil", id, err)
	}
}

func TestSaveAnswerIsRefusedWhileAnotherHolderHasTheLock(t *testing.T) {
	d := newStore(t)
	if err := d.MarkComplete(bg); err != nil {
		t.Fatal(err)
	}
	// d is complete and not closed: it still holds its lock.
	_, err := store.SaveAnswer(bg, d.Path(), "q", "a", answerClock)
	if !errors.Is(err, store.ErrLocked) {
		t.Fatalf("SaveAnswer while the lock is held = %v, want ErrLocked", err)
	}
	path := d.Path()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if as := stored(t, path); len(as) != 0 {
		t.Errorf("a refused save stored %+v", as)
	}
}

func TestSaveAnswerWithNoLockFileStoresNothing(t *testing.T) {
	path := finishedPath(t)
	if err := os.Remove(lockPathOf(path)); err != nil {
		t.Fatal(err)
	}
	_, err := store.SaveAnswer(bg, path, "q", "a", answerClock)
	if !errors.Is(err, store.ErrNoLock) || errors.Is(err, store.ErrLocked) {
		t.Fatalf("SaveAnswer with no lock file = %v, want ErrNoLock", err)
	}
	if _, serr := os.Stat(lockPathOf(path)); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("the lock file was made: %v", serr)
	}
	if as := stored(t, path); len(as) != 0 {
		t.Errorf("a refused save stored %+v", as)
	}
}

func TestSaveAnswerOfAReadOnlyFileOrFolderStoresNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows a file takes the rules of its folder (design 7.4)")
	}
	// canWrite reports whether this process can do what the case takes away
	// from it in spite of the mode (root can): write the file, or make a file
	// in the folder.
	canWrite := func(readOnlyFile bool, dir, file string) bool {
		if readOnlyFile {
			f, err := os.OpenFile(file, os.O_WRONLY, 0)
			if err == nil {
				f.Close()
			}
			return err == nil
		}
		probe := filepath.Join(dir, "probe")
		f, err := os.OpenFile(probe, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			f.Close()
			os.Remove(probe)
		}
		return err == nil
	}
	for _, c := range []struct {
		name   string
		dirMod os.FileMode // the mode of the folder while the save runs
		dbMod  os.FileMode // the mode of the .db file while the save runs
	}{
		{"the file is read-only", 0o700, 0o400},
		{"the folder is read-only", 0o500, 0o600},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := finishedPath(t)
			dir := filepath.Dir(path)
			restore := func() {
				os.Chmod(dir, 0o700)
				os.Chmod(path, 0o600)
			}
			t.Cleanup(restore)
			if err := os.Chmod(path, c.dbMod); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, c.dirMod); err != nil {
				t.Fatal(err)
			}
			if canWrite(c.dbMod == 0o400, dir, path) {
				t.Skip("this process can write whatever the mode is")
			}
			id, err := store.SaveAnswer(bg, path, "q", "a", answerClock)
			if err == nil || errors.Is(err, store.ErrLocked) || errors.Is(err, store.ErrNoLock) {
				t.Fatalf("SaveAnswer = %d, %v; want an error that is neither ErrLocked nor ErrNoLock", id, err)
			}
			t.Logf("SaveAnswer: %v", err)
			if _, serr := os.Stat(path + "-journal"); !errors.Is(serr, os.ErrNotExist) {
				t.Errorf("the journal file was left behind: %v", serr)
			}
			restore()
			if as := stored(t, path); len(as) != 0 {
				t.Errorf("a failed save stored %+v", as)
			}
			// The lock was released: a save in a writable place goes through.
			if id, err := store.SaveAnswer(bg, path, "q", "a", answerClock); err != nil || id != 1 {
				t.Errorf("SaveAnswer after the failure = %d, %v; want 1, nil", id, err)
			}
		})
	}
}

func TestTwoSavesAtOnceNeverGiveTheSameID(t *testing.T) {
	path := finishedPath(t)
	const n = 8
	type result struct {
		id  int64
		err error
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id, err := store.SaveAnswer(bg, path, "q", "a", answerClock)
			results[i] = result{id, err}
		}()
	}
	close(start)
	wg.Wait()
	seen := map[int64]bool{}
	for i, r := range results {
		switch {
		case r.err == nil:
			if r.id < 1 || seen[r.id] {
				t.Errorf("save %d gave id %d, which is not a new id", i, r.id)
			}
			seen[r.id] = true
		case !errors.Is(r.err, store.ErrLocked):
			t.Errorf("save %d: %v; want an id or ErrLocked", i, r.err)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no save stored an answer")
	}
	if as := stored(t, path); len(as) != len(seen) {
		t.Errorf("Answers has %d rows, the saves gave %d ids", len(as), len(seen))
	}
}
