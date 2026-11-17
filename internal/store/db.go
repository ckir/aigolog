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
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ckir/aigolog/internal/buildinfo"
	"github.com/ckir/aigolog/internal/record"
	"github.com/ckir/aigolog/internal/timeparse"

	sqlite "modernc.org/sqlite"
)

// How the times of a source were found (Source.TimeKind).
const (
	TimeNone             = "none"
	TimeExact            = "exact"
	TimeWall             = "wall"
	TimeWallYearInferred = "wall-year-inferred"
)

// Where the anchor of a source came from (Source.AnchorKind).
const (
	AnchorAsOf    = "as-of"
	AnchorModTime = "modification time"
	AnchorNow     = "current time"
)

const (
	// maxTries is how many names Create tries before it gives up.
	maxTries = 5
	// yearChunk is how many pending records ResolveYears reads at a time.
	yearChunk = 5000
)

// ErrIncomplete is returned by Open for a database whose run did not finish.
var ErrIncomplete = errors.New("the database is not complete: the run that made it did not finish")

// ErrClosed is returned by the methods of a DB that read or write the database
// after Close.
var ErrClosed = errors.New("the database is closed")

// errNoMeta marks an error that shows the file has no meta table: it is not a
// database of aigolog.
var errNoMeta = errors.New("the database has no meta table")

// VersionError is returned by Open for a database written by another version
// of aigolog.
type VersionError struct {
	Have string // the version that wrote the database
	Want string // the version of this program
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("the database was written by aigolog %s, and this is aigolog %s", e.Have, e.Want)
}

// Source describes one input of a run.
type Source struct {
	Name        string
	Format      string
	Encoding    string
	Layout      string
	TimeKind    string
	AnchorKind  string
	AnchorNanos int64
}

// SourceTotals are the totals of a source, worked out by FinishSource.
type SourceTotals struct {
	Records, Lines, Bytes int64
	// HasTimes is true when at least one record has a resolved time;
	// FirstNanos and LastNanos are then the smallest and the largest.
	HasTimes              bool
	FirstNanos, LastNanos int64
}

// Options say where Create makes the database of a run.
type Options struct {
	// Dir is the cache folder; it is made with mode 0700 if it is absent.
	Dir string
	// Now returns the time of creation; nil means time.Now.
	Now func() time.Time
}

// DB is the database of one analysis run. It holds one connection for its
// whole life, so the settings made on it hold for every statement. It is not
// safe for concurrent use. After Close, AddSource, Append, ResolveYears,
// FinishSource, MarkComplete, Records, WritePatterns, RecordTexts, Assign,
// FinishMining, SourceInfos, MinerReport, PatternStats, SlotStats and
// TimeBuckets return ErrClosed; Path, Size, Close and Remove still work.
type DB struct {
	path     string
	lockPath string
	madeDB   bool // this call made the .db file (used while Create is failing)
	sqlDB    *sql.DB
	conn     *sql.Conn
	lock     *os.File // nil for a database made by Open
}

// Create makes the database of a new run in opt.Dir: <name>.lock, taken and
// held until Close, and <name>.db, both with mode 0600. The run is not
// complete until MarkComplete.
func Create(ctx context.Context, opt Options) (*DB, error) {
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	// The generated name never holds a character the driver misreads, so the
	// folder is what to check, and before anything is made.
	if err := checkDriverDir(opt.Dir); err != nil {
		return nil, fmt.Errorf("making the folder of the databases: %w", err)
	}
	if err := os.MkdirAll(opt.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("making the folder of the databases: %w", err)
	}
	created := now().UTC()
	for try := 0; try < maxTries; try++ {
		d, retry, err := tryCreate(ctx, opt.Dir, created)
		if err != nil {
			return nil, err
		}
		if !retry {
			return d, nil
		}
	}
	return nil, fmt.Errorf("making a database in %s: no free name after %d tries", opt.Dir, maxTries)
}

// afterLockHook, when not nil, is called by tryCreate right after it holds the
// lock of the lock file it made and before it checks that the file is still
// the one on disk. A test sets it to act as a cleaner does.
var afterLockHook func(lockPath string)

// tryCreate makes the database with one new name. It is retry when the name
// cannot be used (it exists, or another process has its lock).
func tryCreate(ctx context.Context, dir string, created time.Time) (d *DB, retry bool, err error) {
	random, err := randomPartFunc()
	if err != nil {
		return nil, false, fmt.Errorf("making the name of a database: %w", err)
	}
	dbPath := filepath.Join(dir, DatabaseName(created, random))
	d = &DB{path: dbPath, lockPath: strings.TrimSuffix(dbPath, dbExt) + lockExt}

	// The lock file first, so that no cleaner takes the database for an orphan.
	f, err := os.OpenFile(d.lockPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("making the lock file of a database: %w", err)
	}
	locked, err := tryLockFile(f)
	if err != nil {
		f.Close()
		os.Remove(d.lockPath)
		return nil, false, fmt.Errorf("locking the lock file of a database: %w", err)
	}
	if !locked {
		// Someone else holds a file we made a moment ago; it is theirs now.
		f.Close()
		return nil, true, nil
	}
	d.lock = f
	if afterLockHook != nil {
		afterLockHook(d.lockPath)
	}
	// A cleaner may have removed the file between its creation and its lock.
	onDisk, err := os.Stat(d.lockPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, false, d.abort(fmt.Errorf("checking the lock file of a database: %w", err))
	}
	held, herr := f.Stat()
	if herr != nil {
		return nil, false, d.abort(fmt.Errorf("checking the lock file of a database: %w", herr))
	}
	if err != nil || !os.SameFile(onDisk, held) {
		unlockFile(f)
		f.Close()
		return nil, true, nil
	}

	// SQLite makes its file with the umask of the process, so the file is made
	// here, with mode 0600; an empty file is a new database to SQLite.
	dbf, err := os.OpenFile(dbPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		d.abort(nil)
		return nil, true, nil
	}
	if err != nil {
		return nil, false, d.abort(fmt.Errorf("making the file of a database: %w", err))
	}
	d.madeDB = true
	if err := dbf.Close(); err != nil {
		return nil, false, d.abort(fmt.Errorf("making the file of a database: %w", err))
	}

	if err := d.connect(ctx, d.path); err != nil {
		return nil, false, d.abort(err)
	}
	for _, pragma := range []string{`PRAGMA journal_mode=MEMORY`, `PRAGMA synchronous=OFF`, `PRAGMA busy_timeout=2000`} {
		if _, err := d.conn.ExecContext(ctx, pragma); err != nil {
			return nil, false, d.abort(fmt.Errorf("setting up the database (%s): %w", pragma, err))
		}
	}
	if err := d.makeSchema(ctx, created); err != nil {
		return nil, false, d.abort(err)
	}
	return d, false, nil
}

// checkDriverPath refuses a path that the SQLite driver would misread when it
// is given as the name of a database. Measured with modernc.org/sqlite
// v1.62.0 (driver conn.go, newConn): a name is cut at its first "?" that is
// not the first character, and what follows is taken as options, so the data
// goes to a stray file; a name that starts with "file:" is read as a URI and
// is not opened as the path it is. Other characters (# % & = ; ' " : a space,
// letters outside ASCII, a leading dash) reach the file as they are.
func checkDriverPath(path string) error {
	if strings.Contains(path, "?") {
		return fmt.Errorf(`the database path %q contains "?", which the SQLite driver reads as the start of options`, path)
	}
	if strings.HasPrefix(path, "file:") {
		return fmt.Errorf(`the database path %q starts with "file:", which the SQLite driver reads as a URI`, path)
	}
	return nil
}

// checkDriverDir refuses a folder in which the SQLite driver would misread the
// path of a database (see checkDriverPath). The generated names of the
// databases never hold such a character, so the folder is what to check. The
// error names the folder.
func checkDriverDir(dir string) error {
	if checkDriverPath(filepath.Join(dir, DatabaseName(time.Time{}, "00000000"))) == nil {
		return nil
	}
	if strings.Contains(dir, "?") {
		return fmt.Errorf(`the folder of the databases %q contains "?", which the SQLite driver reads as the start of options`, dir)
	}
	return fmt.Errorf(`the folder of the databases %q starts with "file:", which the SQLite driver reads as a URI`, dir)
}

// connect opens the database that dsn names and takes the one connection of d.
// dsn is the path of the file, or a URI (readOnlyURI).
func (d *DB) connect(ctx context.Context, dsn string) error {
	sqlDB, err := sql.Open(DriverName, dsn)
	if err != nil {
		return fmt.Errorf("opening the database: %w", err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		sqlDB.Close()
		return fmt.Errorf("opening the database: %w", err)
	}
	d.sqlDB, d.conn = sqlDB, conn
	return nil
}

// makeSchema makes the tables, the header and the rows of meta, in one
// transaction.
func (d *DB) makeSchema(ctx context.Context, created time.Time) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("making the tables of the database: %w", err)
	}
	defer tx.Rollback()
	for _, stmt := range schema {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("making the tables of the database: %w", err)
		}
	}
	// The header says the file is ours, and which layout it has, whatever its
	// tables are called later.
	for _, pragma := range []string{
		fmt.Sprintf(`PRAGMA application_id = %d`, applicationID),
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaNumber),
	} {
		if _, err := tx.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("writing the header of the database (%s): %w", pragma, err)
		}
	}
	meta := [][2]string{
		{"version", buildinfo.Version},
		{"schema", schemaVersion},
		{"created", created.UTC().Format(createdLayout)},
		{"complete", "0"},
	}
	for _, kv := range meta {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)`, kv[0], kv[1]); err != nil {
			return fmt.Errorf("writing the meta rows of the database: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("making the tables of the database: %w", err)
	}
	return nil
}

// abort undoes a failed Create: it closes the connection, releases and closes
// the lock, deletes the .db file if this call made it, then deletes the .lock
// file. It returns err, so that it can be used in a return statement.
func (d *DB) abort(err error) error {
	d.closeConn()
	if d.lock != nil {
		unlockFile(d.lock)
		d.lock.Close()
		d.lock = nil
	}
	if d.madeDB {
		os.Remove(d.path)
	}
	os.Remove(d.lockPath)
	return err
}

// Open opens the database of a finished run, for reading. It does not take
// the lock of the run and it never makes a file. It checks, in this order, and
// returns for the first that fails: the version of aigolog that wrote the
// database (a *VersionError), its layout (a *SchemaError), the time of its
// creation (an error that says it is not a database of aigolog), and that the
// run that made it finished (ErrIncomplete). A database that a write of
// SaveAnswer left unfinished (a hot journal: the process died in its commit)
// cannot be read through a read-only connection; Open then rolls the write
// back, once, under the lock of the database (recoverHotJournal), and goes on
// as for any database; if it cannot, it returns a plain error that says so.
func Open(ctx context.Context, path string) (*DB, error) {
	d, meta, err := openReadOnly(ctx, path)
	if err != nil && hotJournalError(err) {
		if err := recoverHotJournal(ctx, path); err != nil {
			return nil, err
		}
		d, meta, err = openReadOnly(ctx, path)
	}
	if err != nil {
		return nil, err
	}
	version, ok := meta["version"]
	if !ok {
		d.closeConn()
		return nil, fmt.Errorf("%s is not a database of aigolog: it has no version", path)
	}
	if version != buildinfo.Version {
		d.closeConn()
		return nil, &VersionError{Have: version, Want: buildinfo.Version}
	}
	if meta["schema"] != schemaVersion {
		d.closeConn()
		return nil, &SchemaError{Have: meta["schema"], Want: schemaVersion}
	}
	if _, err := time.Parse(createdLayout, meta["created"]); err != nil {
		d.closeConn()
		return nil, fmt.Errorf(`%s is not a database of aigolog: its "created" row is missing or is not a time`, path)
	}
	if meta["complete"] != "1" {
		d.closeConn()
		return nil, ErrIncomplete
	}
	return d, nil
}

// openConnReadOnly opens the file at path for reading and returns the
// database with its one connection and the settings of Open. It reads no
// table. The file is opened through a read-only URI (readOnlyURI), so SQLite
// never makes it: a path that a cleaner has just deleted fails to open, and
// stays absent. The caller closes the database (closeConn) when the error is
// nil.
func openConnReadOnly(ctx context.Context, path string) (*DB, error) {
	if err := checkDriverPath(path); err != nil {
		return nil, fmt.Errorf("opening the database: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("opening the database: %w", err)
	}
	d := &DB{path: path, lockPath: strings.TrimSuffix(path, dbExt) + lockExt}
	if err := d.connect(ctx, readOnlyURI(abs)); err != nil {
		// The driver's error says the file could not be opened; if the file is
		// not there, say so in a form that errors.Is(err, fs.ErrNotExist) finds.
		if _, serr := os.Stat(path); errors.Is(serr, fs.ErrNotExist) {
			err = fmt.Errorf("%w: %w", fs.ErrNotExist, err)
		}
		return nil, err
	}
	for _, pragma := range []string{`PRAGMA query_only=1`, `PRAGMA busy_timeout=2000`} {
		if _, err := d.conn.ExecContext(ctx, pragma); err != nil {
			d.closeConn()
			return nil, fmt.Errorf("setting up the database (%s): %w", pragma, err)
		}
	}
	return d, nil
}

// openReadOnly opens the file at path for reading and returns it with the rows
// of its meta table. It makes the connection of Open, with its settings, but
// makes none of its version, layout or completeness checks. The caller closes
// the database (closeConn) when the error is nil.
func openReadOnly(ctx context.Context, path string) (*DB, map[string]string, error) {
	d, err := openConnReadOnly(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	meta, err := d.readMeta(ctx)
	if err != nil {
		d.closeConn()
		return nil, nil, err
	}
	return d, meta, nil
}

// readMeta returns the rows of meta.
func (d *DB) readMeta(ctx context.Context) (map[string]string, error) {
	rows, err := d.conn.QueryContext(ctx, `SELECT key, value FROM meta`)
	if err != nil {
		// The driver gives no code for a missing table, only its text.
		var se *sqlite.Error
		if errors.As(err, &se) && strings.Contains(se.Error(), "no such table: meta") {
			err = fmt.Errorf("%w: %w", errNoMeta, err)
		}
		return nil, fmt.Errorf("reading the meta rows of the database: %w", err)
	}
	defer rows.Close()
	meta := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("reading the meta rows of the database: %w", err)
		}
		meta[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the meta rows of the database: %w", err)
	}
	return meta, nil
}

// Path returns the path of the .db file.
func (d *DB) Path() string { return d.path }

// Size returns the size of the .db file on disk, in bytes.
func (d *DB) Size() (int64, error) {
	fi, err := os.Stat(d.path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// AddSource adds a source to the run and returns its number, from 1.
func (d *DB) AddSource(ctx context.Context, s Source) (int, error) {
	if d.conn == nil {
		return 0, ErrClosed
	}
	res, err := d.conn.ExecContext(ctx,
		`INSERT INTO sources (name, format, encoding, layout, time_kind, anchor_kind, anchor_nanos) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.Format, s.Encoding, s.Layout, s.TimeKind, s.AnchorKind, s.AnchorNanos)
	if err != nil {
		return 0, fmt.Errorf("adding a source: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("adding a source: %w", err)
	}
	return int(id), nil
}

// Append adds records to the database, all or none.
func (d *DB) Append(ctx context.Context, recs []record.Record) error {
	if d.conn == nil {
		return ErrClosed
	}
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("adding records: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO records (source, line, byte_offset, byte_length, t_state, t_nanos, t_exact, t_zone, t_month, t_day, t_tod, raw, t_len)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("adding records: %w", err)
	}
	for i := range recs {
		r := &recs[i]
		exact := 0
		if r.Stamp.Exact {
			exact = 1
		}
		_, err := stmt.ExecContext(ctx, r.Source, r.Line, r.Offset, r.Length,
			int(r.Stamp.State), r.Stamp.Nanos, exact, r.Stamp.Zone,
			r.Stamp.Month, r.Stamp.Day, r.Stamp.NanoOfDay, r.Raw, r.Stamp.Len)
		if err != nil {
			stmt.Close()
			return fmt.Errorf("adding records: %w", err)
		}
	}
	if err := stmt.Close(); err != nil {
		return fmt.Errorf("adding records: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("adding records: %w", err)
	}
	return nil
}

// pending is a record that waits for a year.
type pending struct {
	id        int64
	month     int
	day       int
	nanoOfDay int64
}

// ResolveYears gives a year to the YearPending records of a source. It works
// from the last record to the first, in chunks, each written in one
// transaction. A record for which no year fits is left as NoStamp.
func (d *DB) ResolveYears(ctx context.Context, source int, r *timeparse.YearResolver) error {
	if d.conn == nil {
		return ErrClosed
	}
	cursor := int64(math.MaxInt64)
	for {
		chunk, err := d.pendingChunk(ctx, source, cursor)
		if err != nil {
			return err
		}
		if len(chunk) == 0 {
			return nil
		}
		if err := d.writeYears(ctx, chunk, r); err != nil {
			return err
		}
		cursor = chunk[len(chunk)-1].id
	}
}

// pendingChunk reads the next chunk of pending records of a source, below
// the cursor, from the last id downward.
func (d *DB) pendingChunk(ctx context.Context, source int, cursor int64) ([]pending, error) {
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, t_month, t_day, t_tod FROM records WHERE source = ? AND t_state = ? AND id < ? ORDER BY id DESC LIMIT ?`,
		source, int(record.YearPending), cursor, yearChunk)
	if err != nil {
		return nil, fmt.Errorf("reading the records that wait for a year: %w", err)
	}
	defer rows.Close()
	var chunk []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.month, &p.day, &p.nanoOfDay); err != nil {
			return nil, fmt.Errorf("reading the records that wait for a year: %w", err)
		}
		chunk = append(chunk, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the records that wait for a year: %w", err)
	}
	return chunk, nil
}

// writeYears resolves a chunk, in the order given, and writes it in one
// transaction. The month, day and time of day, which only a pending stamp
// holds, are cleared.
func (d *DB) writeYears(ctx context.Context, chunk []pending, r *timeparse.YearResolver) error {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("writing the years of records: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE records SET t_state = ?, t_nanos = ?, t_month = 0, t_day = 0, t_tod = 0 WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("writing the years of records: %w", err)
	}
	for _, p := range chunk {
		state, nanos := int(record.Resolved), int64(0)
		n, ok := r.Resolve(p.month, p.day, p.nanoOfDay)
		if ok {
			nanos = n
		} else {
			state = int(record.NoStamp)
		}
		if _, err := stmt.ExecContext(ctx, state, nanos, p.id); err != nil {
			stmt.Close()
			return fmt.Errorf("writing the years of records: %w", err)
		}
	}
	if err := stmt.Close(); err != nil {
		return fmt.Errorf("writing the years of records: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("writing the years of records: %w", err)
	}
	return nil
}

// FinishSource works out the totals of a source, stores them with lines and
// bytes in the source's row, and returns them.
func (d *DB) FinishSource(ctx context.Context, source int, lines, bytes int64) (SourceTotals, error) {
	if d.conn == nil {
		return SourceTotals{}, ErrClosed
	}
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return SourceTotals{}, fmt.Errorf("finishing a source: %w", err)
	}
	defer tx.Rollback()
	tot := SourceTotals{Lines: lines, Bytes: bytes}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE source = ?`, source).Scan(&tot.Records); err != nil {
		return SourceTotals{}, fmt.Errorf("finishing a source: %w", err)
	}
	var first, last sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT MIN(t_nanos), MAX(t_nanos) FROM records WHERE source = ? AND t_state = ?`,
		source, int(record.Resolved)).Scan(&first, &last)
	if err != nil {
		return SourceTotals{}, fmt.Errorf("finishing a source: %w", err)
	}
	if first.Valid && last.Valid {
		tot.HasTimes, tot.FirstNanos, tot.LastNanos = true, first.Int64, last.Int64
	}
	res, err := tx.ExecContext(ctx, `UPDATE sources SET records = ?, lines = ?, bytes = ?, first_nanos = ?, last_nanos = ? WHERE id = ?`,
		tot.Records, lines, bytes, first, last, source)
	if err != nil {
		return SourceTotals{}, fmt.Errorf("finishing a source: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n != 1 {
		return SourceTotals{}, fmt.Errorf("finishing a source: there is no source %d", source)
	}
	if err := tx.Commit(); err != nil {
		return SourceTotals{}, fmt.Errorf("finishing a source: %w", err)
	}
	return tot, nil
}

// MarkComplete records that the run finished, so that Open accepts the
// database.
func (d *DB) MarkComplete(ctx context.Context) error {
	if d.conn == nil {
		return ErrClosed
	}
	if _, err := d.conn.ExecContext(ctx, `UPDATE meta SET value = '1' WHERE key = 'complete'`); err != nil {
		return fmt.Errorf("marking the database complete: %w", err)
	}
	return nil
}

// Records calls fn for each record of a source, in the order of their ids,
// one at a time. If fn returns an error the walk stops and Records returns it.
// fn must not use the database: the walk holds its connection.
func (d *DB) Records(ctx context.Context, source int, fn func(id int64, r record.Record) error) error {
	if d.conn == nil {
		return ErrClosed
	}
	rows, err := d.conn.QueryContext(ctx,
		`SELECT id, source, line, byte_offset, byte_length, t_state, t_nanos, t_exact, t_zone, t_month, t_day, t_tod, raw, t_len
FROM records WHERE source = ? ORDER BY id`, source)
	if err != nil {
		return fmt.Errorf("reading records: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id    int64
			r     record.Record
			state int
			exact int
		)
		err := rows.Scan(&id, &r.Source, &r.Line, &r.Offset, &r.Length, &state, &r.Stamp.Nanos, &exact,
			&r.Stamp.Zone, &r.Stamp.Month, &r.Stamp.Day, &r.Stamp.NanoOfDay, &r.Raw, &r.Stamp.Len)
		if err != nil {
			return fmt.Errorf("reading records: %w", err)
		}
		r.Stamp.State = record.StampState(state)
		r.Stamp.Exact = exact != 0
		if err := fn(id, r); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading records: %w", err)
	}
	return nil
}

// closeConn closes the connection and the pool; it is safe to call twice.
func (d *DB) closeConn() error {
	var first error
	if d.conn != nil {
		first = d.conn.Close()
		d.conn = nil
	}
	if d.sqlDB != nil {
		if err := d.sqlDB.Close(); first == nil {
			first = err
		}
		d.sqlDB = nil
	}
	return first
}

// closeLock releases and closes the lock file, if there is one; it is safe to
// call twice.
func (d *DB) closeLock() error {
	if d.lock == nil {
		return nil
	}
	first := unlockFile(d.lock)
	if err := d.lock.Close(); first == nil {
		first = err
	}
	d.lock = nil
	return first
}

// Close closes the database and releases the lock of the run. The files stay.
// It is safe to call twice.
func (d *DB) Close() error {
	first := d.closeConn()
	if err := d.closeLock(); first == nil {
		first = err
	}
	return first
}

// RemoveError is what Remove returns when the .db file is gone and another
// file of the run is not (on Windows the lock file may not be deletable while
// another process holds it open): the database is removed, and what stayed is
// a file of no use. Its text is the cause's, unchanged.
type RemoveError struct {
	// DBGone is true: the .db file no longer exists. Remove returns a plain
	// error when the .db file stayed.
	DBGone bool
	// Err is the first error: the file that could not be deleted, or the
	// close that failed.
	Err error
}

// Error implements error.
func (e *RemoveError) Error() string { return e.Err.Error() }

// Unwrap returns the cause.
func (e *RemoveError) Unwrap() error { return e.Err }

// removeAll deletes the lock file of Remove; a test replaces it.
var removeAll = os.Remove

// Remove closes the database and deletes its files. For a database that
// Create made and that has not been closed, the .db file and its journal go
// while the lock is still held, then the lock is released and the .lock file
// goes. For a closed database, or one that Open opened, the DB holds no lock,
// so the files are deleted without one: the caller must know that no live run
// holds them. A file that is already gone is not an error; Remove returns the
// first other error: a *RemoveError when the .db file is gone and a later
// step failed, a plain error when the .db file stayed.
func (d *DB) Remove() error {
	first := d.closeConn()
	keep := func(err error) {
		if first == nil && err != nil && !errors.Is(err, fs.ErrNotExist) {
			first = err
		}
	}
	dbErr := os.Remove(d.path)
	dbGone := dbErr == nil || errors.Is(dbErr, fs.ErrNotExist)
	keep(dbErr)
	keep(os.Remove(d.path + "-journal"))
	keep(d.closeLock())
	keep(removeAll(d.lockPath))
	if first != nil && dbGone {
		return &RemoveError{DBGone: true, Err: first}
	}
	return first
}
