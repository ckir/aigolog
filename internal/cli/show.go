// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"bytes"
	"flag"
	"fmt"
	"io"

	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/query"
	reports "github.com/ckir/aigolog/internal/report"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/summary"
	"github.com/ckir/aigolog/internal/textsafe"
)

const showUsage = "Usage: aigolog show --db PATH"

// parseShowArgs reads the command line of show: --db and nothing else. When it
// returns ok false it has said why, and code is the exit code of the command.
func parseShowArgs(env Env, args []string) (path string, code int, ok bool) {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	// The flag package would print its messages as they are, and they quote
	// what the user typed; they are printed here, through textsafe.Clean.
	fs.SetOutput(io.Discard)
	db := fs.String("db", "", "the database to show")
	fail := func(msg string) (string, int, bool) {
		report(env.Stderr, msg)
		fmt.Fprintln(env.Stderr, showUsage)
		return "", exitcode.Usage, false
	}
	if err := fs.Parse(args); err != nil {
		return fail(err.Error())
	}
	if fs.NArg() > 0 {
		return fail("show takes no arguments")
	}
	if *db == "" {
		return fail("show needs --db PATH, the database to show")
	}
	return *db, exitcode.OK, true
}

// show prints a kept database: the line of the run, the digest and the full
// summary as analyze printed them, the stored answers, and the line about the
// model exchange (none is recorded yet).
func show(env Env, args []string) int {
	path, code, ok := parseShowArgs(env, args)
	if !ok {
		return code
	}
	ctx := env.Context
	if ctx == nil {
		var stop func()
		ctx, stop = interruptContext(env.Stderr)
		defer stop()
	}
	db, err := store.Open(ctx, path)
	if err != nil {
		report(env.Stderr, err.Error())
		return exitcode.Failure
	}
	defer db.Close()

	sum, dig, err := buildSummary(ctx, db, summary.DefaultOptions())
	if err != nil {
		if ctx.Err() != nil {
			report(env.Stderr, "interrupted")
		} else {
			report(env.Stderr, "the report could not be built: "+err.Error())
		}
		return exitcode.Failure
	}
	stored, err := db.Answers(ctx)
	if err != nil {
		if ctx.Err() != nil {
			report(env.Stderr, "interrupted")
		} else {
			report(env.Stderr, err.Error())
		}
		return exitcode.Failure
	}
	size, sizeErr := db.Size()
	if sizeErr != nil {
		warn(env.Stderr, sizeErr)
		size = 0
	}

	// The text is made whole and written once, as analyze does, so that a
	// write that fails is known and said.
	var text bytes.Buffer
	fmt.Fprintf(&text, "database: %s (%d bytes)\n", textsafe.Clean(db.Path()), size)
	reports.WriteDigest(&text, dig)
	reports.WriteSummary(&text, sum)
	fmt.Fprintf(&text, "answers: %d stored\n", len(stored))
	for _, s := range stored {
		text.WriteString("\n")
		id := query.ID(s.ID)
		a, err := query.ParseAnswer(s.Answer)
		if err != nil {
			fmt.Fprintf(&text, "answer %s: stored text could not be read\n", textsafe.Clean(id))
			continue
		}
		text.WriteString(query.RenderText(id, a))
	}
	if len(stored) > 0 {
		text.WriteString("\n")
	}
	text.WriteString("model exchange: none recorded\n")
	if _, err := env.Stdout.Write(text.Bytes()); err != nil {
		report(env.Stderr, "the report could not be written: "+err.Error())
		return exitcode.Failure
	}
	return exitcode.OK
}
