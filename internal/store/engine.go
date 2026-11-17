// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package store is the persistent store of aigolog, built on SQLite.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" driver; written in Go, no C code
)

// DriverName is the name of the database/sql driver the store opens.
const DriverName = "sqlite"

// EngineVersion returns the version SQLite reports, read from a throwaway
// in-memory database.
func EngineVersion(ctx context.Context) (string, error) {
	db, err := sql.Open(DriverName, ":memory:")
	if err != nil {
		return "", fmt.Errorf("opening an in-memory database: %w", err)
	}
	defer db.Close()

	var version string
	if err := db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&version); err != nil {
		return "", fmt.Errorf("asking SQLite for its version: %w", err)
	}
	return version, nil
}
