// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The states of a database in an Info.
const (
	stateComplete   = "complete"
	stateIncomplete = "incomplete"
	stateRunning    = "running"
	stateUnreadable = "unreadable"
)

// Info describes one database in the folder of the databases.
type Info struct {
	Path    string    // the .db file
	Created time.Time // the time in the name of the file (UTC)
	Size    int64     // the size of the .db file, in bytes
	// State is "complete", "incomplete" (the run did not finish), "running"
	// (another process holds the lock of the run) or "unreadable" (the file
	// could not be read as a database of aigolog, or its lock file could not
	// be tested, for example for want of permission). Sweep removes an
	// unreadable database whose lock could be tested and was free, like an
	// incomplete one; it does not remove one whose lock could not be tested,
	// because it might be held.
	State string
	// Records and Version are read from the database. For a running database
	// the file is not opened, so Records is 0 and Version is empty; the same
	// holds for an unreadable one.
	Records int64
	Version string
}

// Policy says what Sweep removes. A database that is running is never removed.
type Policy struct {
	// All removes every database that is not running, complete or not. It
	// takes precedence over MaxAge and MaxTotalBytes.
	All bool
	// MaxAge removes a complete database whose age (Now minus the time in its
	// name) is greater than MaxAge. Zero, or less, means no age limit.
	MaxAge time.Duration
	// MaxTotalBytes is the most that the .db files of the databases that remain
	// may add up to; the oldest databases that are not running are removed
	// until they do. Zero, or less, means no size limit.
	MaxTotalBytes int64
	// Now is the time that ages are counted from; zero means time.Now().
	Now time.Time
}

// SweepResult says what Sweep did, each list with the newest database first.
type SweepResult struct {
	Removed []Info // the databases that this call removed
	Kept    []Info // the databases that are still there and are not running
	Running []Info // the databases of runs that are in progress
}

// DefaultDir returns the folder where the databases are kept, in the cache
// folder of the user. It does not make the folder.
func DefaultDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("finding the cache folder of the user: %w", err)
	}
	return filepath.Join(base, "aigolog"), nil
}

// candidate is a database found in the folder.
type candidate struct {
	info     Info // Path, Created and Size only
	lockPath string
}

// listing is what scanDir finds in the folder.
type listing struct {
	dbs         []candidate
	orphanLocks []string // lock files with no database next to them
}

// scanDir reads the folder. It considers only regular files (the type of the
// directory entry, so a link is not followed) whose names are ours; any other
// entry is ignored. A missing folder gives an empty listing.
func scanDir(dir string) (listing, error) {
	var ls listing
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return ls, nil
	}
	if err != nil {
		return ls, fmt.Errorf("reading the folder of the databases %s: %w", dir, err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() {
			continue
		}
		switch {
		case strings.HasSuffix(name, dbExt):
			created, ok := ParseName(name)
			if !ok {
				continue
			}
			fi, err := e.Info()
			if errors.Is(err, fs.ErrNotExist) {
				continue // removed since the folder was read
			}
			if err != nil {
				return ls, fmt.Errorf("reading the entry %s: %w", filepath.Join(dir, name), err)
			}
			path := filepath.Join(dir, name)
			ls.dbs = append(ls.dbs, candidate{
				info:     Info{Path: path, Created: created, Size: fi.Size()},
				lockPath: strings.TrimSuffix(path, dbExt) + lockExt,
			})
		case strings.HasSuffix(name, lockExt):
			base := strings.TrimSuffix(name, lockExt)
			if _, ok := ParseName(base + dbExt); ok && !names[base+dbExt] {
				ls.orphanLocks = append(ls.orphanLocks, filepath.Join(dir, name))
			}
		}
	}
	return ls, nil
}

// acquire looks at the lock file of a run and tries to take its lock without
// waiting. The lock file is never made. It returns:
//   - a file, when the lock is taken: the caller holds it and must release it
//     (release, or removeRun);
//   - nil and running, when another holder has the lock;
//   - nil and not running, when there is no lock file to hold (it is absent or
//     it vanished meanwhile, or it is not a regular file, which is ignored).
func acquire(lockPath string) (lock *os.File, running bool, err error) {
	fi, err := os.Lstat(lockPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("looking at the lock file %s: %w", lockPath, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, false, nil
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("opening the lock file %s: %w", lockPath, err)
	}
	// What was opened must be the file that was looked at: a link put in its
	// place between the two calls is not followed.
	held, err := f.Stat()
	onDisk, onDiskErr := os.Lstat(lockPath)
	if err != nil || onDiskErr != nil || !onDisk.Mode().IsRegular() || !os.SameFile(onDisk, held) {
		f.Close()
		return nil, false, nil
	}
	locked, err := tryLockFile(f)
	if err != nil {
		f.Close()
		return nil, false, fmt.Errorf("locking the lock file %s: %w", lockPath, err)
	}
	if !locked {
		f.Close()
		return nil, true, nil
	}
	return f, false, nil
}

// release releases and closes a lock taken by acquire. It does nothing for nil.
func release(lock *os.File) {
	if lock == nil {
		return
	}
	unlockFile(lock)
	lock.Close()
}

// inspect opens the database at path, which no run holds, and reads its state,
// the number of its records and the version that wrote it. The connection is
// closed before it returns.
func inspect(path string) (state string, records int64, version string) {
	ctx := context.Background()
	d, meta, err := openReadOnly(ctx, path)
	if err != nil {
		return stateUnreadable, 0, ""
	}
	defer d.closeConn()
	version, ok := meta["version"]
	if !ok {
		return stateUnreadable, 0, ""
	}
	if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM records`).Scan(&records); err != nil {
		return stateUnreadable, 0, ""
	}
	if meta["complete"] == "1" {
		return stateComplete, records, version
	}
	return stateIncomplete, records, version
}

// sortInfos puts the newest first, and orders those of the same time by path.
func sortInfos(infos []Info) {
	sort.Slice(infos, func(i, j int) bool {
		if !infos[i].Created.Equal(infos[j].Created) {
			return infos[i].Created.After(infos[j].Created)
		}
		return infos[i].Path < infos[j].Path
	})
}

// List describes the databases in dir, newest first. A missing folder gives an
// empty list. The database of a run in progress is not opened. List holds
// nothing when it returns: it takes the lock of a run only to test it, and
// releases it at once.
func List(dir string) ([]Info, error) {
	ls, err := scanDir(dir)
	if err != nil {
		return nil, err
	}
	infos := make([]Info, 0, len(ls.dbs))
	for _, c := range ls.dbs {
		info := c.info
		lock, running, err := acquire(c.lockPath)
		switch {
		case err != nil:
			// The lock cannot be tested, so the run cannot be told from a dead one.
			info.State = stateUnreadable
		case running:
			info.State = stateRunning
		default:
			release(lock)
			info.State, info.Records, info.Version = inspect(info.Path)
		}
		infos = append(infos, info)
	}
	sortInfos(infos)
	return infos, nil
}

// removeRun deletes the files of a run whose lock the caller holds (lock is nil
// when there is no lock file to hold, and then no lock file is touched). The
// order is: the .db file, its journal, the close of the lock (which releases
// it), the .lock file. A file that is already gone is not an error. When the
// .db file or its journal cannot be deleted the lock file is left, and the lock
// is released. gone is true when the .db file no longer exists.
func removeRun(path string, lock *os.File) (gone bool, err error) {
	var errs []error
	keep := func(err error) bool {
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			return false
		}
		return true
	}
	gone = keep(os.Remove(path))
	journalGone := keep(os.Remove(path + "-journal"))
	if lock != nil {
		if !gone || !journalGone {
			release(lock)
		} else {
			unlockErr := unlockFile(lock)
			closeErr := lock.Close()
			keep(unlockErr)
			keep(closeErr)
			keep(os.Remove(strings.TrimSuffix(path, dbExt) + lockExt))
		}
	}
	return gone, errors.Join(errs...)
}

// Sweep removes the databases in dir that the policy says to remove, and the
// lock files that no database owns. A database whose run is in progress (its
// lock is held) is never touched. An incomplete database is removed, and so is
// an unreadable one whose lock could be tested and is free; an entry whose lock
// file cannot be tested (for example, permission denied) is shown as
// unreadable but is not removed, because it might be held. A complete database
// is removed when p.All, or when it is older than p.MaxAge; then,
// if the .db files that remain add up to more than p.MaxTotalBytes, the oldest
// databases that are not running are removed until they do not. Files whose
// names are not ours, and anything that is not a regular file, are left alone.
//
// An error with one database does not stop the sweep: the errors are joined and
// returned with the result. A missing folder is not an error.
func Sweep(dir string, p Policy) (SweepResult, error) {
	var res SweepResult
	var errs []error
	ls, err := scanDir(dir)
	if err != nil {
		return res, err
	}
	if p.Now.IsZero() {
		p.Now = time.Now()
	}

	// What is removed is decided by the state and the age; what remains is
	// then looked at for its size.
	var kept []Info
	for _, c := range ls.dbs {
		info := c.info
		lock, running, err := acquire(c.lockPath)
		if err != nil {
			// Not knowing whether the run is alive, leave it.
			errs = append(errs, err)
			info.State = stateUnreadable
			kept = append(kept, info)
			continue
		}
		if running {
			info.State = stateRunning
			res.Running = append(res.Running, info)
			continue
		}
		info.State, info.Records, info.Version = inspect(info.Path)
		remove := p.All || info.State != stateComplete || (p.MaxAge > 0 && p.Now.Sub(info.Created) > p.MaxAge)
		if !remove {
			release(lock)
			kept = append(kept, info)
			continue
		}
		gone, err := removeRun(info.Path, lock)
		if err != nil {
			errs = append(errs, fmt.Errorf("removing the database %s: %w", info.Path, err))
		}
		if gone {
			res.Removed = append(res.Removed, info)
		} else {
			kept = append(kept, info)
		}
	}

	if !p.All && p.MaxTotalBytes > 0 {
		kept = sweepSize(kept, &res, p.MaxTotalBytes, &errs)
	}
	res.Kept = kept

	for _, path := range ls.orphanLocks {
		if err := removeOrphanLock(path); err != nil {
			errs = append(errs, err)
		}
	}
	sortInfos(res.Removed)
	sortInfos(res.Kept)
	sortInfos(res.Running)
	return res, errors.Join(errs...)
}

// sweepSize removes the oldest of kept, one at a time, while the .db files of
// the running and the kept databases add up to more than limit. It returns the
// databases that are still kept; one that has become running is moved to
// res.Running, and one that is removed to res.Removed.
func sweepSize(kept []Info, res *SweepResult, limit int64, errs *[]error) []Info {
	var total int64
	for _, i := range res.Running {
		total += i.Size
	}
	for _, i := range kept {
		total += i.Size
	}
	sort.Slice(kept, func(i, j int) bool {
		if !kept[i].Created.Equal(kept[j].Created) {
			return kept[i].Created.Before(kept[j].Created)
		}
		return kept[i].Path < kept[j].Path
	})
	var still []Info
	for n, info := range kept {
		if total <= limit {
			still = append(still, kept[n:]...)
			break
		}
		lock, running, err := acquire(strings.TrimSuffix(info.Path, dbExt) + lockExt)
		if err != nil {
			*errs = append(*errs, err)
			still = append(still, info)
			continue
		}
		if running {
			// A run began since the first look at it.
			info.State, info.Records, info.Version = stateRunning, 0, ""
			res.Running = append(res.Running, info)
			continue
		}
		gone, err := removeRun(info.Path, lock)
		if err != nil {
			*errs = append(*errs, fmt.Errorf("removing the database %s: %w", info.Path, err))
		}
		if gone {
			total -= info.Size
			res.Removed = append(res.Removed, info)
		} else {
			still = append(still, info)
		}
	}
	return still
}

// removeFile deletes the lock file of removeOrphanLock; a test replaces it.
var removeFile = os.Remove

// removeOrphanLock deletes a lock file that has no database, if nobody holds
// it. The file is deleted while the lock is still held: if the lock were
// released first, a run that made the file a moment ago (Create makes it, then
// locks it) could take the lock and have its file deleted under it. Where a
// file that this process holds open cannot be deleted (Windows), the handle is
// closed and the delete is tried once more. If that fails too, the file is left
// and this is not an error: it is in use by someone, or not ours to remove, and
// the next sweep tries again.
func removeOrphanLock(path string) error {
	lock, _, err := acquire(path)
	if err != nil {
		return err
	}
	if lock == nil {
		return nil // held by a run that is starting, or gone
	}
	if err := removeFile(path); err == nil || errors.Is(err, fs.ErrNotExist) {
		release(lock)
		return nil
	}
	release(lock)
	removeFile(path)
	return nil
}
