// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package cli is the command line of aigolog.
package cli

import (
	"context"
	"fmt"
	"io"
	"runtime"

	"github.com/ckir/aigolog/internal/buildinfo"
	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/store"
)

const usage = `aigolog reads a log and says what it reveals.

Usage:
  aigolog <command> [arguments]

Commands:
  version   print the version of the program and of its parts
  help      print this text
`

// Run runs the command named by args and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitcode.Usage
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitcode.OK
	case "version":
		fmt.Fprintf(stdout, "aigolog %s\n", buildinfo.Version)
		fmt.Fprintf(stdout, "go %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
		engine, err := store.EngineVersion(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "aigolog: the store's engine does not work: %v\n", err)
			return exitcode.Failure
		}
		fmt.Fprintf(stdout, "sqlite %s\n", engine)
		return exitcode.OK
	default:
		fmt.Fprintf(stderr, "aigolog: unknown command %q\n", args[0])
		fmt.Fprintln(stderr, `Run "aigolog help" for the commands.`)
		return exitcode.Usage
	}
}
