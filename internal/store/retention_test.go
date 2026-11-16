// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"os"
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
