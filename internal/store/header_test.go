// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateWritesTheHeaderOfLayoutTwo(t *testing.T) {
	d := newDB(t)
	if err := d.MarkComplete(ctx); err != nil {
		t.Fatal(err)
	}
	path := d.Path()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	// A bare connection: what it reads is what is in the file.
	db, err := sql.Open(DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var appID, userVersion int64
	if err := db.QueryRow(`PRAGMA application_id`).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		t.Fatal(err)
	}
	if appID != 1095322695 || userVersion != 2 {
		t.Errorf("application_id = %d, user_version = %d; want 1095322695 and 2", appID, userVersion)
	}
}

func TestTheHeaderSurvivesARenamedMetaTable(t *testing.T) {
	dir := t.TempDir()
	path := made(t, dir, now.Add(-time.Hour), true, 1)
	db, err := sql.Open(DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE meta RENAME TO meta_later`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// openReadOnly reads the meta table, which is gone; the connection alone
	// reads no table, and the header is still there.
	d, err := openConnReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.closeConn()
	appID, userVersion, err := d.readHeader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if appID != applicationID || userVersion != schemaNumber {
		t.Errorf("header = %d, %d; want %d, %d", appID, userVersion, applicationID, schemaNumber)
	}
	if _, _, err := openReadOnly(ctx, path); !provenNotOurs(err) {
		t.Errorf("openReadOnly of a database with no meta table: %v; want an error that shows no meta table", err)
	}
}

func TestReadOnlyURI(t *testing.T) {
	// The function takes the path as given and does not ask the running
	// system what a path looks like: the Windows cases run here too.
	for _, c := range []struct{ path, want string }{
		{"/tmp/x.db", "file:///tmp/x.db?mode=ro"},
		{"/tmp/a b#c.db", "file:///tmp/a%20b%23c.db?mode=ro"},
		{"/tmp/100%.db", "file:///tmp/100%25.db?mode=ro"},
		{"/tmp/a?b.db", "file:///tmp/a%3Fb.db?mode=ro"},
		{"/tmp/κόσμος.db", "file:///tmp/%CE%BA%CF%8C%CF%83%CE%BC%CE%BF%CF%82.db?mode=ro"},
		{`C:\Users\x\a b#c.db`, "file:///C:%5CUsers%5Cx%5Ca%20b%23c.db?mode=ro"},
		{`C:/Users/x/a b#c?.db`, "file:///C:/Users/x/a%20b%23c%3F.db?mode=ro"},
		{`z:\a.db`, "file:///z:%5Ca.db?mode=ro"},
	} {
		if got := readOnlyURI(c.path); got != c.want {
			t.Errorf("readOnlyURI(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestOpenReadsADatabaseWhosePathNeedsEscapingInAURI(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "a b#c%41 κόσμος")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Skipf("a folder with this name cannot be made here: %v", err)
	}
	path := made(t, dir, now.Add(-time.Hour), true, 4)
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	defer d.Close()
	var n int
	if err := d.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM records`).Scan(&n); err != nil || n != 4 {
		t.Errorf("records = %d, %v; want 4", n, err)
	}
	infos, err := List(dir)
	if err != nil || len(infos) != 1 || infos[0].State != "complete" || infos[0].Records != 4 {
		t.Errorf("List = %+v, %v", infos, err)
	}
}

func TestOurRawHeader(t *testing.T) {
	// header makes the first n bytes of a file: the magic text, filler, and the
	// application_id at offset 68, big-endian.
	header := func(n int, magic string, id uint32) []byte {
		b := make([]byte, n)
		copy(b, magic)
		if n >= 72 {
			binary.BigEndian.PutUint32(b[68:72], id)
		}
		return b
	}
	const magic = "SQLite format 3\x00"
	for _, c := range []struct {
		name string
		b    []byte
		want bool
	}{
		{"the magic and our id", header(100, magic, applicationID), true},
		{"more than a header", header(4096, magic, applicationID), true},
		{"the magic and another id", header(100, magic, 0x12345678), false},
		{"the magic and no id", header(100, magic, 0), false},
		{"our id and a bad magic", header(100, "SQLite format 4\x00", applicationID), false},
		{"our id and no magic", header(100, "", applicationID), false},
		{"our id little-endian", func() []byte {
			b := header(100, magic, 0)
			binary.LittleEndian.PutUint32(b[68:72], applicationID)
			return b
		}(), false},
		{"the id one byte off", func() []byte {
			b := make([]byte, 100)
			copy(b, magic)
			binary.BigEndian.PutUint32(b[67:71], applicationID)
			return b
		}(), false},
		{"a slice of 99 bytes", header(99, magic, applicationID), false},
		{"a slice of 72 bytes", header(72, magic, applicationID), false},
		{"a slice of 16 bytes", []byte(magic), false},
		{"an empty slice", []byte{}, false},
		{"nil", nil, false},
	} {
		if got := ourRawHeader(c.b); got != c.want {
			t.Errorf("%s: ourRawHeader = %v, want %v", c.name, got, c.want)
		}
	}
}
