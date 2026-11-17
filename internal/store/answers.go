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
	"path/filepath"
	"strings"
	"time"
)

// ErrLocked is returned by SaveAnswer when another holder has the lock of the
// database: a run that still writes it, a sweep that removes it, or another
// save.
var ErrLocked = errors.New("the database is in use: another holder has its lock")

// ErrNoLock is returned by SaveAnswer when the database has no lock file to
// take: a database that cannot be guarded is not written.
var ErrNoLock = errors.New("the database has no lock file")

// ErrNoAnswer is returned by Answer for an id that no stored answer has.
var ErrNoAnswer = errors.New("there is no such stored answer")

// StoredAnswer is a question that was answered, with the text of the answer.
type StoredAnswer struct {
	ID       int64
	Question string
	Answer   string
	// Created is the time the answer was stored, to the second, in UTC.
	Created time.Time
}

// SaveAnswer stores an answer, with its question, in the database of a
// finished run at path, and returns its id (1 for the first, then one more
// each). It is the one write that is made to a finished database, and it is
// guarded: it takes the lock of the database with the protocol of Sweep
// (acquire), so that no sweep removes the file during the write and no run or
// other save writes it at the same time; it returns ErrLocked when another
// holder has the lock, and ErrNoLock when there is no lock file (the lock file
// is never made). The write goes through a connection of its own, in the
// default rollback journal mode, so that a crash in the middle of the insert
// leaves the data as it was: the crash leaves a hot journal (the file
// <name>.db-journal), and the next Open rolls the interrupted write back. A
// file or a folder that cannot be written gives an error and stores nothing. The connection is closed and the lock is
// released before SaveAnswer returns. Any other failure is returned wrapped.
// now is the time stored with the answer, to the second.
func SaveAnswer(ctx context.Context, path, question, answer string, now time.Time) (int64, error) {
	if err := checkDriverPath(path); err != nil {
		return 0, fmt.Errorf("storing the answer: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, fmt.Errorf("storing the answer: %w", err)
	}
	lock, running, err := acquire(strings.TrimSuffix(path, dbExt) + lockExt)
	if err != nil {
		return 0, fmt.Errorf("storing the answer: %w", err)
	}
	if running {
		return 0, ErrLocked
	}
	if lock == nil {
		return 0, ErrNoLock
	}
	defer release(lock)

	sqlDB, err := sql.Open(DriverName, fileURI(abs, "rw"))
	if err != nil {
		return 0, fmt.Errorf("storing the answer: opening the database: %w", err)
	}
	// Closing the database after the insert has committed cannot undo it, so
	// the error of the close is not reported as a failure to store.
	defer sqlDB.Close()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("storing the answer: opening the database: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=2000`); err != nil {
		return 0, fmt.Errorf("storing the answer: setting up the database: %w", err)
	}
	res, err := conn.ExecContext(ctx, `INSERT INTO answers (question, answer, created) VALUES (?, ?, ?)`,
		question, answer, now.UTC().Format(createdLayout))
	if err != nil {
		return 0, fmt.Errorf("storing the answer: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("storing the answer: %w", err)
	}
	return id, nil
}

// Answers returns the stored answers, in the order of their ids. The list is
// empty, not nil, when there are none.
func (d *DB) Answers(ctx context.Context) ([]StoredAnswer, error) {
	if d.conn == nil {
		return nil, ErrClosed
	}
	rows, err := d.conn.QueryContext(ctx, `SELECT id, question, answer, created FROM answers ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("reading the stored answers: %w", err)
	}
	defer rows.Close()
	out := []StoredAnswer{}
	for rows.Next() {
		a, err := scanAnswer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the stored answers: %w", err)
	}
	return out, nil
}

// Answer returns the stored answer with the id given, or ErrNoAnswer.
func (d *DB) Answer(ctx context.Context, id int64) (StoredAnswer, error) {
	if d.conn == nil {
		return StoredAnswer{}, ErrClosed
	}
	a, err := scanAnswer(d.conn.QueryRowContext(ctx, `SELECT id, question, answer, created FROM answers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return StoredAnswer{}, ErrNoAnswer
	}
	return a, err
}

// scanAnswer reads one row of answers: id, question, answer, created.
func scanAnswer(row interface{ Scan(dest ...any) error }) (StoredAnswer, error) {
	var (
		a       StoredAnswer
		created string
	)
	if err := row.Scan(&a.ID, &a.Question, &a.Answer, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return StoredAnswer{}, err
		}
		return StoredAnswer{}, fmt.Errorf("reading a stored answer: %w", err)
	}
	t, err := time.Parse(createdLayout, created)
	if err != nil {
		return StoredAnswer{}, fmt.Errorf("reading stored answer %d: its time %q is not a time: %w", a.ID, created, err)
	}
	a.Created = t
	return a, nil
}
