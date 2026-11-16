// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ckir/aigolog/internal/buildinfo"
	"github.com/ckir/aigolog/internal/config"
	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/ingest"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/textsafe"
	"github.com/ckir/aigolog/internal/timeparse"
)

const analyzeUsage = "Usage: aigolog analyze [--format auto|text] [--as-of TIME] [--no-keep] <file>... | -"

// analyzeArgs is what the command line of analyze says.
type analyzeArgs struct {
	files  []string // in the order given; - is standard input
	asOf   *int64   // --as-of, in wall-clock nanoseconds, or nil
	noKeep bool     // --no-keep
}

// parseAnalyzeArgs reads the command line of analyze. Flags may come before,
// between or after the files, and -- ends them. When it returns ok false it
// has said why, and code is the exit code of the command.
func parseAnalyzeArgs(env Env, args []string) (a analyzeArgs, code int, ok bool) {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	// The flag package would print its messages as they are, and they quote
	// what the user typed; they are printed here, through textsafe.Clean.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), analyzeUsage)
		fs.PrintDefaults()
	}
	format := fs.String("format", "auto", "the format of the input: auto or text")
	asOf := fs.String("as-of", "", "the date that logs without a year are placed before: 2006-01-02, 2006-01-02 15:04 or 2006-01-02 15:04:05")
	fs.BoolVar(&a.noKeep, "no-keep", false, "remove the database at the end of the run")

	rest := args
	for {
		err := fs.Parse(rest)
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(env.Stderr)
			fs.Usage()
			return a, exitcode.OK, false
		}
		if err != nil {
			report(env.Stderr, err.Error())
			fmt.Fprintln(env.Stderr, analyzeUsage)
			return a, exitcode.Usage, false
		}
		consumed := rest[:len(rest)-fs.NArg()]
		rest = fs.Args()
		if endedByDoubleDash(fs, consumed) {
			a.files = append(a.files, rest...)
			break
		}
		if len(rest) == 0 {
			break
		}
		a.files = append(a.files, rest[0])
		rest = rest[1:]
	}

	if len(a.files) == 0 {
		fmt.Fprintln(env.Stderr, "aigolog: analyze needs a file to read, or - for standard input")
		fmt.Fprintln(env.Stderr, analyzeUsage)
		return a, exitcode.Usage, false
	}
	switch *format {
	case "auto", "text":
	case "jsonl", "eventxml":
		report(env.Stderr, fmt.Sprintf("the format %q is not built yet", *format))
		return a, exitcode.Usage, false
	default:
		report(env.Stderr, fmt.Sprintf("unknown format %q: want auto or text", *format))
		return a, exitcode.Usage, false
	}
	if *asOf != "" {
		n, err := timeparse.ParseAsOf(*asOf)
		if err != nil {
			report(env.Stderr, err.Error())
			return a, exitcode.Usage, false
		}
		a.asOf = &n
	}
	stdin := 0
	for _, f := range a.files {
		if f == "-" {
			stdin++
		}
	}
	if stdin > 1 {
		fmt.Fprintln(env.Stderr, `aigolog: standard input ("-") may be named only once`)
		return a, exitcode.Usage, false
	}
	return a, exitcode.OK, true
}

// endedByDoubleDash says whether the arguments that fs.Parse consumed ended
// with a -- that ended the flags. The flag package takes the -- away, so this
// walks the consumed arguments again: a -- that begins an argument is the end
// of the flags, and one that follows a flag that takes a value is that value.
func endedByDoubleDash(fs *flag.FlagSet, consumed []string) bool {
	for i := 0; i < len(consumed); i++ {
		arg := consumed[i]
		if arg == "--" {
			return true
		}
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		if f := fs.Lookup(name); f != nil {
			if b, isBool := f.Value.(interface{ IsBoolFlag() bool }); !isBool || !b.IsBoolFlag() {
				i++ // the next argument is the value
			}
		}
	}
	return false
}

// analyze reads logs into a new database and says what was read.
func analyze(env Env, args []string) int {
	a, code, ok := parseAnalyzeArgs(env, args)
	if !ok {
		return code
	}
	cfg, err := config.Load(env.LookupEnv)
	if err != nil {
		report(env.Stderr, err.Error())
		return exitcode.Usage
	}

	var closers []io.Closer
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	inputs := make([]ingest.Input, 0, len(a.files))
	for _, name := range a.files {
		if name == "-" {
			inputs = append(inputs, ingest.Input{Name: "-", R: env.Stdin})
			continue
		}
		in, c, err := ingest.OpenFile(name)
		if err != nil {
			report(env.Stderr, fmt.Sprintf("cannot read %s: %v", name, err))
			return exitcode.Failure
		}
		closers = append(closers, c)
		inputs = append(inputs, in)
	}

	dir, ok := cacheDir(env, cfg)
	if !ok {
		return exitcode.Failure
	}
	now := env.Now()
	_, err = store.Sweep(dir, store.Policy{
		MaxAge:        time.Duration(cfg.KeepDays) * 24 * time.Hour,
		MaxTotalBytes: cfg.MaxTotalBytes,
		Now:           now,
	})
	for _, e := range splitErrors(err) {
		warn(env.Stderr, e)
	}

	ctx := context.Background()
	db, err := store.Create(ctx, store.Options{Dir: dir, Now: env.Now})
	if err != nil {
		report(env.Stderr, fmt.Sprintf("cannot write the database in %s: %v", dir, err))
		return exitcode.StorageUnavailable
	}
	res, err := ingest.Run(ctx, db, inputs, ingest.Options{AsOf: a.asOf, Now: now, Zone: env.Zone})
	if err != nil {
		var inErr *ingest.InputError
		code := exitcode.StorageUnavailable
		if errors.As(err, &inErr) || errors.Is(err, ingest.ErrNothingToAnalyse) {
			report(env.Stderr, err.Error())
			code = exitcode.Failure
		} else {
			report(env.Stderr, "the database could not be written: "+err.Error())
		}
		if rmErr := db.Remove(); rmErr != nil {
			warn(env.Stderr, rmErr)
		}
		return code
	}

	size, sizeErr := db.Size()
	if sizeErr != nil {
		warn(env.Stderr, sizeErr)
		size = 0
	}
	kept := true
	if a.noKeep {
		if rmErr := db.Remove(); rmErr != nil {
			report(env.Stderr, "warning: could not remove the database: "+rmErr.Error())
		} else {
			kept = false
		}
	}
	printSummary(env.Stdout, res, db.Path(), size, kept, cfg.KeepDays)
	if kept && !a.noKeep {
		if err := db.Close(); err != nil {
			warn(env.Stderr, err)
		}
	}
	return exitcode.OK
}

// summaryTime formats the nanoseconds of a time as the summary shows it.
// Wall-clock nanoseconds are as if the zone were UTC, so UTC is right for
// every kind.
func summaryTime(n int64) string {
	return time.Unix(0, n).UTC().Format("2006-01-02 15:04:05")
}

// anchorKinds says how the summary calls each kind of anchor.
var anchorKinds = map[string]string{
	store.AnchorAsOf:    "--as-of",
	store.AnchorModTime: "the file's modification time",
	store.AnchorNow:     "the current time",
}

// printSummary writes the summary of a run. kept says whether the database is
// still there; path and size are those of the database.
func printSummary(w io.Writer, res ingest.Result, path string, size int64, kept bool, keepDays int) {
	stored := 0
	for _, s := range res.Sources {
		if s.ID != 0 {
			stored++
		}
	}
	fmt.Fprintf(w, "aigolog %s: sources %d, records %d, lines %d\n", buildinfo.Version, stored, res.Records, res.Lines)
	n := 0
	for _, s := range res.Sources {
		name := textsafe.Clean(s.Source.Name)
		if s.Source.Name == "-" {
			name = "standard input"
		}
		if s.ID == 0 {
			fmt.Fprintf(w, "source -: %s is empty and was not stored\n", name)
			continue
		}
		n++
		layout := s.Source.Layout
		if layout == "" {
			layout = "none"
		}
		fmt.Fprintf(w, "source %d: %s (%s, layout %s)\n", n, name, textsafe.Clean(s.Source.Encoding), textsafe.Clean(layout))
		fmt.Fprintf(w, "  records %d, lines %d, bytes %d\n", s.Totals.Records, s.Totals.Lines, s.Totals.Bytes)
		switch {
		case !s.Totals.HasTimes:
			fmt.Fprintln(w, "  time: none found; everything that depends on time is left out")
		case s.Source.TimeKind == store.TimeExact:
			fmt.Fprintf(w, "  time: %s to %s UTC; exact, the log states a zone\n", summaryTime(s.Totals.FirstNanos), summaryTime(s.Totals.LastNanos))
		default:
			fmt.Fprintf(w, "  time: %s to %s; as written, no zone stated\n", summaryTime(s.Totals.FirstNanos), summaryTime(s.Totals.LastNanos))
		}
		if s.Source.TimeKind == store.TimeWallYearInferred {
			kind, known := anchorKinds[s.Source.AnchorKind]
			if !known {
				kind = textsafe.Clean(s.Source.AnchorKind)
			}
			fmt.Fprintf(w, "  years: worked out from the anchor %s, %s\n", summaryTime(s.Source.AnchorNanos), kind)
		}
	}
	if kept {
		fmt.Fprintf(w, "database: %s (%d bytes), kept %d days\n", textsafe.Clean(path), size, keepDays)
	} else {
		fmt.Fprintln(w, "database: removed at the end of the run (--no-keep)")
	}
}
