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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	sqlite "modernc.org/sqlite"
)

// sqliteReadonlyRollback is the extended result code of SQLITE_READONLY_ROLLBACK
// (the primary code 8, READONLY, with 3 << 8): a read-only connection met a
// hot journal, which only a connection that can write can roll back.
const sqliteReadonlyRollback = 776

// hotJournalError reports whether err says that a read-only connection met a
// hot journal (the file of a write that was cut short, kept next to the
// database as <name>.db-journal). The full extended code is compared: other
// READONLY codes mean other things.
func hotJournalError(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code() == sqliteReadonlyRollback
}

// recoveryError is a failed recovery of a hot journal, with a plain message
// and, when the cause is a held lock or a missing lock file, the sentinel that
// errors.Is finds (ErrLocked or ErrNoLock) as its cause.
type recoveryError struct {
	msg   string
	cause error
}

func (e *recoveryError) Error() string { return e.msg }
func (e *recoveryError) Unwrap() error { return e.cause }

// recoverHotJournal rolls back the interrupted write of the database at path,
// which a read-only open found with a hot journal (SaveAnswer, which dies in
// the few milliseconds of its commit, is the one write that leaves one). It
// takes the lock of the database with acquire, the protocol of SaveAnswer and
// Sweep; opens the file once for reading and writing through fileURI (which
// never makes a file), with one connection; makes a read, whose first page
// read rolls the journal back; closes the connection; checks that the journal
// is gone; and releases the lock. Every failure is a plain error that says
// what to do: ErrLocked when another holder has the lock, ErrNoLock when there
// is no lock file to guard the recovery, and, when the file cannot be written
// or the journal stays, that "aigolog clean" removes the database.
func recoverHotJournal(ctx context.Context, path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	lock, running, err := acquire(strings.TrimSuffix(path, dbExt) + lockExt)
	if err != nil {
		return fmt.Errorf("rolling back the unfinished write of %s: %w", path, err)
	}
	if running {
		return &recoveryError{
			msg:   fmt.Sprintf("%s has an unfinished write and is in use by another process: try again in a moment", path),
			cause: ErrLocked,
		}
	}
	if lock == nil {
		return &recoveryError{
			msg:   fmt.Sprintf("%s has an unfinished write and no lock file to guard a recovery: aigolog clean removes it", path),
			cause: ErrNoLock,
		}
	}
	defer release(lock)

	notRolledBack := func(cause error) error {
		return fmt.Errorf("%s has an unfinished write that could not be rolled back (%w): aigolog clean removes it", path, cause)
	}
	if err := rollBackJournal(ctx, abs); err != nil {
		return notRolledBack(err)
	}
	if _, err := os.Stat(path + "-journal"); !errors.Is(err, fs.ErrNotExist) {
		if err == nil {
			err = errors.New("the journal file is still there")
		}
		return notRolledBack(err)
	}
	return nil
}

// rollBackJournal opens the file at abs for reading and writing, with one
// connection, and reads a table; the first read of a connection that can
// write rolls a hot journal back. The connection is closed before it returns,
// and its error is reported only when the read worked.
func rollBackJournal(ctx context.Context, abs string) (err error) {
	sqlDB, err := sql.Open(DriverName, fileURI(abs, "rw"))
	if err != nil {
		return err
	}
	defer func() {
		if cerr := sqlDB.Close(); err == nil {
			err = cerr
		}
	}()
	sqlDB.SetMaxOpenConns(1)
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := conn.Close(); err == nil {
			err = cerr
		}
	}()
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=2000`); err != nil {
		return err
	}
	var n int
	return conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&n)
}
