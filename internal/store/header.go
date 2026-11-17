// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"context"
	"fmt"
	"net/url"
)

// The two numbers that Create writes to the header of a database, and the
// "schema" row of meta. A header can be read whatever the tables are (a later
// version may rename meta), so Sweep and List read it before they look at a
// table.
const (
	// applicationID is SQLite's application_id of a database of aigolog: the
	// bytes "AILG".
	applicationID = 0x41494C47
	// schemaNumber is the user_version of the layout this version reads.
	schemaNumber = 2
	// schemaVersion is the value of the "schema" row of meta: the same layout,
	// as text.
	schemaVersion = "2"
)

// SchemaError is returned by Open for a database of a layout that this version
// of aigolog does not read.
type SchemaError struct {
	Have string // the "schema" row of the database (empty when it has none)
	Want string // the layout this version reads
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf(`the database has layout %s, and this aigolog reads layout %s; "aigolog clean" removes it`, e.Have, e.Want)
}

// readHeader returns the application_id and the user_version in the header of
// the database. It reads no table.
func (d *DB) readHeader(ctx context.Context) (appID, userVersion int64, err error) {
	if d.conn == nil {
		return 0, 0, ErrClosed
	}
	if err := d.conn.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&appID); err != nil {
		return 0, 0, fmt.Errorf("reading the header of the database: %w", err)
	}
	if err := d.conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&userVersion); err != nil {
		return 0, 0, fmt.Errorf("reading the header of the database: %w", err)
	}
	return appID, userVersion, nil
}

// readOnlyURI returns the URI with which the driver opens the file at path for
// reading only: SQLite then never makes the file, so a path that a cleaner has
// just deleted fails to open. It takes the path as given and does not depend on
// the system it runs on; a path that starts with a drive letter and a colon
// (a Windows volume path) gets the leading "/" that a file URI has before it.
// Space, "#", "?" and "%" are percent-encoded by net/url.
// UNPROVEN ON WINDOWS until the hosted machines run it.
func readOnlyURI(path string) string {
	if len(path) >= 2 && path[1] == ':' && isASCIILetter(path[0]) {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path}
	return u.String() + "?mode=ro"
}

func isASCIILetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }
