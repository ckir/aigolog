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
	"regexp"
	"strings"
	"testing"
)

func TestEngineVersionLooksLikeSQLite(t *testing.T) {
	got, err := EngineVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^3\.\d+\.\d+$`).MatchString(got) {
		t.Errorf("EngineVersion = %q, want something like 3.54.1", got)
	}
}

func TestEngineVersionWithACancelledContextIsAnErrorThatNamesWhatFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := EngineVersion(ctx)
	if err == nil {
		t.Fatal("EngineVersion with a cancelled context returned no error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap context.Canceled", err)
	}
	if !strings.HasPrefix(err.Error(), "asking SQLite for its version:") {
		t.Errorf("err = %q, want it to start with %q", err, "asking SQLite for its version:")
	}
}

func TestEngineKeepsWhatIsPassedAsAParameter(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // each connection to ":memory:" is its own database

	const text = `it's "quoted"; DROP TABLE t; -- κόσμος`
	if _, err := db.Exec(`CREATE TABLE t (n INTEGER, s TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t (n, s) VALUES (?, ?)`, 42, text); err != nil {
		t.Fatal(err)
	}
	var n int
	var s string
	if err := db.QueryRow(`SELECT n, s FROM t WHERE s = ?`, text).Scan(&n, &s); err != nil {
		t.Fatal(err)
	}
	if n != 42 || s != text {
		t.Errorf("got (%d, %q), want (42, %q)", n, s, text)
	}
}
