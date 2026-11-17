// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Command genlog writes a generated test log, for measurements: the same
// generators the tests use, with a seed and a number of lines.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/testlog"
)

const usage = "Usage: genlog -kind ssh|restart|web [-seed N] [-lines N] [-o file]"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the command: it returns 0 when the log was written, 1 when it could
// not be, and 2 when the command line was wrong.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("genlog", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, usage)
		fs.PrintDefaults()
	}
	kind := fs.String("kind", "", "the kind of log: ssh, restart or web")
	seed := fs.Int64("seed", 1, "the seed: the same seed gives the same log")
	lines := fs.Int("lines", 1000, "the number of lines, at least 1")
	out := fs.String("o", "", "the file to write; the log goes to standard output when it is not given")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return exitcode.OK
		}
		return exitcode.Usage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "genlog: unexpected argument %q\n%s\n", fs.Arg(0), usage)
		return exitcode.Usage
	}
	if *lines < 1 {
		fmt.Fprintf(stderr, "genlog: -lines must be at least 1, not %d\n%s\n", *lines, usage)
		return exitcode.Usage
	}
	var data []byte
	switch *kind {
	case "ssh":
		data, _ = testlog.SSH(*seed, *lines)
	case "restart":
		data, _ = testlog.Restart(*seed, *lines)
	case "web":
		data, _ = testlog.Web(*seed, *lines)
	default:
		fmt.Fprintf(stderr, "genlog: unknown kind %q: want ssh, restart or web\n%s\n", *kind, usage)
		return exitcode.Usage
	}
	if *out == "" {
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "genlog: writing the log: %v\n", err)
			return exitcode.Failure
		}
		return exitcode.OK
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "genlog: %v\n", err)
		return exitcode.Failure
	}
	return exitcode.OK
}
