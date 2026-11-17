// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package query is the model of a question put to a finished database and of
// the answer to it (design 7.6): the eight verbs, the validation of a question,
// its canonical JSON (the text that is stored with an answer and that a re-run
// reads back), the answer as a small table with the facts about the rows it
// covers, the plain-text rendering of an answer, and the ids of stored answers.
//
// The package is pure. It does not know the database, and it imports no
// package of aigolog but textsafe: the SQL that gives an answer is in the
// store, and the command line is in the cli. Everything a log can put into an
// answer (a cell, a value in a question) goes through textsafe.Clean before
// RenderText writes it.
package query
