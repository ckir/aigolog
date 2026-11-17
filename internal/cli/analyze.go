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
	iofs "io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"
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

// givenString is a string flag that knows whether it was given, so that an
// empty value (an unset shell variable in --as-of "$ASOF") is not taken for an
// absent flag. When a flag is repeated the last value stays.
type givenString struct {
	value string
	given bool
}

func (g *givenString) String() string {
	if g == nil {
		return ""
	}
	return g.value
}

func (g *givenString) Set(s string) error {
	g.value, g.given = s, true
	return nil
}

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
	var asOf givenString
	fs.Var(&asOf, "as-of", "the date that logs without a year are placed before: YYYY-MM-DD, YYYY-MM-DD HH:MM or YYYY-MM-DD HH:MM:SS")
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
	if asOf.given {
		n, err := timeparse.ParseAsOf(asOf.value)
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

// signalsToCatch returns the signals that interruptContext catches: SIGINT
// and SIGTERM, except one that the process was started with ignored (a
// background job of a script, nohup). Catching such a signal would take over
// from the ignoring, and then the second one, after stop, would still be
// ignored; an ignored signal stays ignored. ignored is signal.Ignored.
func signalsToCatch(ignored func(os.Signal) bool) []os.Signal {
	var sigs []os.Signal
	for _, s := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		if !ignored(s) {
			sigs = append(sigs, s)
		}
	}
	return sigs
}

// interruptContext returns a context that ends at the first SIGINT or SIGTERM
// (Ctrl-C on Windows). At that moment it stops catching signals, so that a
// second one ends the process at once, which matters for a run that is blocked
// reading standard input and notices the first only when the read returns, and
// it says so on w. stop must be called when the command ends; it waits for the
// goroutine that says so, which says nothing when the command ended without a
// signal.
func interruptContext(w io.Writer) (ctx context.Context, stop func()) {
	sigs := signalsToCatch(signal.Ignored)
	if len(sigs) == 0 {
		return context.Background(), func() {}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), sigs...)
	finished := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-finished:
			return
		case <-ctx.Done():
		}
		select {
		case <-finished:
			return // the command ended and stop cancelled the context
		default:
		}
		cancel() // from here a second signal has its default effect
		fmt.Fprintln(w, "aigolog: signal received; send it again to stop at once")
	}()
	return ctx, func() {
		close(finished)
		cancel()
		<-exited
	}
}

// removeRun removes the database at the end of a --no-keep run; a test
// replaces it.
var removeRun = func(db *store.DB) error { return db.Remove() }

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

	ctx := env.Context
	if ctx == nil {
		var stop func()
		ctx, stop = interruptContext(env.Stderr)
		defer stop()
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
			// A *PathError names the file again in its text; its cause is enough.
			detail := err.Error()
			var pe *iofs.PathError
			if errors.As(err, &pe) {
				detail = pe.Err.Error()
			}
			report(env.Stderr, fmt.Sprintf("cannot read %s: %s", name, detail))
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
	swept, err := store.Sweep(dir, store.Policy{
		MaxAge:        time.Duration(cfg.KeepDays) * 24 * time.Hour,
		MaxTotalBytes: cfg.MaxTotalBytes,
		Now:           now,
	})
	for _, e := range splitErrors(err) {
		warn(env.Stderr, e)
	}
	unfinished := 0
	for _, r := range swept.Removed {
		if r.State != "complete" {
			unfinished++
		}
	}
	if unfinished > 0 {
		// A database that is cut short but was finished is judged not to be
		// ours and goes with the others; the owner can see it go.
		report(env.Stderr, fmt.Sprintf("note: removed %d databases that were not finished", unfinished))
	}
	unreadable := 0
	for _, k := range swept.Kept {
		if k.State == "unreadable" {
			unreadable++
		}
	}
	if unreadable > 0 {
		report(env.Stderr, fmt.Sprintf(`warning: %d kept databases could not be read and were left alone; "aigolog list" shows them and "aigolog clean" removes them`, unreadable))
	}

	if ctx.Err() != nil {
		report(env.Stderr, "interrupted")
		return exitcode.Failure
	}
	db, err := store.Create(ctx, store.Options{Dir: dir, Now: env.Now})
	if err != nil {
		if ctx.Err() != nil {
			// A signal during Create: it has removed its own files.
			report(env.Stderr, "interrupted")
			return exitcode.Failure
		}
		report(env.Stderr, fmt.Sprintf("cannot write the database in %s: %v", dir, err))
		return exitcode.StorageUnavailable
	}
	res, err := ingest.Run(ctx, db, inputs, ingest.Options{AsOf: a.asOf, Now: now, Zone: env.Zone})
	if err != nil {
		if ctx.Err() != nil {
			// An interruption, whatever error it made on the way: the
			// incomplete database goes, with or without --no-keep.
			if rmErr := db.Remove(); rmErr != nil {
				warn(env.Stderr, rmErr)
			}
			report(env.Stderr, "interrupted")
			return exitcode.Failure
		}
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
		rmErr := removeRun(db)
		if rmErr != nil {
			report(env.Stderr, "warning: could not remove the database: "+rmErr.Error())
		}
		// A file that stayed (the lock file, on Windows) is not the database:
		// when the .db file is gone the summary says so.
		var re *store.RemoveError
		kept = rmErr != nil && !(errors.As(rmErr, &re) && re.DBGone)
	}
	printSummary(env.Stdout, res, db.Path(), size, kept, cfg.KeepDays)
	if kept && !a.noKeep && size > cfg.MaxTotalBytes {
		// The summary says how long the database is kept, but the size limit
		// removes the oldest databases first at the next analysis, this one
		// included when it alone is over the limit.
		report(env.Stderr, fmt.Sprintf("warning: this database is %d bytes, over the limit of %d bytes (max_total_bytes); the next analysis will remove it", size, cfg.MaxTotalBytes))
	}
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
		if s.CutRecords > 0 {
			fmt.Fprintf(w, "  note: joined text was cut at 1 MiB %d times; each cut started a new record\n", s.CutRecords)
		}
	}
	if kept {
		fmt.Fprintf(w, "database: %s (%d bytes), kept %d days\n", textsafe.Clean(path), size, keepDays)
	} else {
		fmt.Fprintln(w, "database: removed at the end of the run (--no-keep)")
	}
}
