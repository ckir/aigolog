// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

// createdLayout is the layout of the "created" row of meta.
const createdLayout = "2006-01-02T15:04:05Z"

// schema is the list of statements that make an empty database of a run
// (layout 3: the records, the patterns, the slots and the values that the
// miner finds in them, and the answers that "aigolog query" stores, empty at
// first). They are run in one transaction by Create. slot_values
// has no index: the indexes are made after the load.
var schema = []string{
	`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID`,
	`CREATE TABLE sources (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL, format TEXT NOT NULL, encoding TEXT NOT NULL,
  layout TEXT NOT NULL, time_kind TEXT NOT NULL, anchor_kind TEXT NOT NULL, anchor_nanos INTEGER NOT NULL,
  records INTEGER NOT NULL DEFAULT 0, lines INTEGER NOT NULL DEFAULT 0, bytes INTEGER NOT NULL DEFAULT 0,
  first_nanos INTEGER, last_nanos INTEGER)`,
	`CREATE TABLE records (
  id INTEGER PRIMARY KEY, source INTEGER NOT NULL, line INTEGER NOT NULL,
  byte_offset INTEGER NOT NULL, byte_length INTEGER NOT NULL,
  t_state INTEGER NOT NULL, t_nanos INTEGER NOT NULL, t_exact INTEGER NOT NULL, t_zone TEXT NOT NULL,
  t_month INTEGER NOT NULL, t_day INTEGER NOT NULL, t_tod INTEGER NOT NULL, raw TEXT NOT NULL,
  t_len INTEGER NOT NULL, pattern INTEGER NOT NULL DEFAULT 0)`,
	`CREATE TABLE patterns (
  id INTEGER PRIMARY KEY, text TEXT NOT NULL, slots TEXT NOT NULL, catch_all INTEGER NOT NULL,
  records INTEGER NOT NULL DEFAULT 0, first_nanos INTEGER, last_nanos INTEGER)`,
	`CREATE TABLE slots (id INTEGER PRIMARY KEY, pattern INTEGER NOT NULL, pos INTEGER NOT NULL, type TEXT NOT NULL)`,
	`CREATE TABLE slot_values (record INTEGER NOT NULL, slot INTEGER NOT NULL, value TEXT NOT NULL)`,
	`CREATE TABLE answers (id INTEGER PRIMARY KEY, question TEXT NOT NULL, answer TEXT NOT NULL, created TEXT NOT NULL)`,
}
