// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package summary builds the summary and the digest of a mined database, and
// renders them as plain text.
//
// The summary is what a model reads first and what a person reads when no model
// runs: the sources, how well the patterns hold the records, the patterns, the
// values of the slots of the most frequent ones, and counts over time. It has a
// size budget, in bytes of its rendered text, that does not depend on the size
// of the log: len(Render(s)) <= Budget holds for every Summary that Build
// returns, and Build keeps it by rendering, not by estimating. The digest is the
// head of the summary, the largest groups and their counts.
//
// Both are built only by the read-only queries of package store, and both are
// plain data with json tags, so that the report can encode them as they are.
// The same database gives the same bytes: no map, clock or random number
// reaches Build or Render. Times are written from stored nanoseconds, and no
// zone is assumed: the word UTC follows a time only where the log states its
// zone (a source whose times are exact); any other time is shown as written,
// with no zone word.
//
// Every string that comes from a log (a pattern, a value, a source name) goes
// through textsafe.Clean and is then cut, so that no control character, no
// escape sequence and no long line reaches the terminal, and a command in a log
// is shown as text and nothing else.
package summary
