// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package cli is the command line of aigolog.
package cli

import (
	"io"
	"os"
	"time"
)

const usage = `aigolog reads a log and says what it reveals.

Usage:
  aigolog <command> [arguments]

Commands:
  analyze   read logs into a database, find the patterns in them and print what was found
  list      show the kept databases
  clean     remove the kept databases that no run is using
  query     ask a question of a kept database and store the answer with the question
  show      print a kept database: its run, its summary and the stored answers
  version   print the version of the program and of its parts
  help      print this text
`

// Run runs the command named by args and returns the process exit code. It
// runs in the real environment: the standard input, the environment variables,
// the clock and the local time zone.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunEnv(Env{
		Stdin:     os.Stdin,
		Stdout:    stdout,
		Stderr:    stderr,
		LookupEnv: os.LookupEnv,
		Now:       time.Now,
		Zone:      time.Local,
	}, args)
}
