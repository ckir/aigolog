// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"testing"
	"time"
)

// The save of an answer takes the lock with acquire, the protocol that Sweep
// uses; this test takes the lock the same way and shows that Sweep keeps a
// database whose lock is held, and removes it once the lock is released.
func TestASweepLeavesADatabaseWhoseLockTheSaveProtocolHolds(t *testing.T) {
	dir := t.TempDir()
	path := made(t, dir, now.Add(-48*time.Hour), true, 3)
	lock, running, err := acquire(lockOf(path))
	if err != nil || running || lock == nil {
		t.Fatalf("acquire = %v, %v, %v; want the lock", lock, running, err)
	}
	res, err := Sweep(dir, Policy{All: true, Now: now})
	if err != nil {
		release(lock)
		t.Fatal(err)
	}
	if len(res.Running) != 1 || res.Running[0].Path != path || len(res.Removed) != 0 || !exists(path) {
		release(lock)
		t.Fatalf("Sweep while the lock is held: %+v, exists %v; want the database in Running and on disk", res, exists(path))
	}
	release(lock)
	res, err = Sweep(dir, Policy{All: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0].Path != path || exists(path) {
		t.Errorf("Sweep after release: %+v, exists %v; want the database removed", res, exists(path))
	}
}
