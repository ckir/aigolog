// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/ckir/aigolog/internal/buildinfo"
	"github.com/ckir/aigolog/internal/config"
	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/textsafe"
)

// Env is everything a command takes from the world around it, so that a test
// can give it a private one. A nil Stdin is an empty input, a nil LookupEnv an
// empty environment, a nil Now is time.Now and a nil Zone is time.Local.
// Stdout and Stderr are used as given. A nil Context makes analyze catch
// SIGINT and SIGTERM itself (and Ctrl-C on Windows); a test gives its own.
type Env struct {
	// Context ends a run that is in progress when it is cancelled: analyze
	// stops, removes its database and exits with code 1.
	Context   context.Context
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	LookupEnv func(string) (string, bool)
	Now       func() time.Time
	Zone      *time.Location
}

// RunEnv runs the command named by args in env and returns the process exit
// code.
func RunEnv(env Env, args []string) int {
	if env.Stdin == nil {
		env.Stdin = strings.NewReader("")
	}
	if env.LookupEnv == nil {
		env.LookupEnv = func(string) (string, bool) { return "", false }
	}
	if env.Now == nil {
		env.Now = time.Now
	}
	if env.Zone == nil {
		env.Zone = time.Local
	}
	stdout, stderr := env.Stdout, env.Stderr
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitcode.Usage
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitcode.OK
	case "analyze":
		return analyze(env, args[1:])
	case "list":
		return list(env, args[1:])
	case "clean":
		return clean(env, args[1:])
	case "version":
		fmt.Fprintf(stdout, "aigolog %s\n", buildinfo.Version)
		fmt.Fprintf(stdout, "go %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
		engine, err := store.EngineVersion(context.Background())
		if err != nil {
			fmt.Fprintf(stderr, "aigolog: the store's engine does not work: %v\n", err)
			return exitcode.Failure
		}
		fmt.Fprintf(stdout, "sqlite %s\n", engine)
		for _, part := range platformParts() {
			fmt.Fprintln(stdout, part)
		}
		return exitcode.OK
	default:
		fmt.Fprintf(stderr, "aigolog: unknown command %q\n", args[0])
		fmt.Fprintln(stderr, `Run "aigolog help" for the commands.`)
		return exitcode.Usage
	}
}

// report prints "aigolog: " and msg to w. msg may hold text that came from
// outside (a name, a path, an error that quotes one), so all of it goes
// through textsafe.Clean.
func report(w io.Writer, msg string) {
	fmt.Fprintf(w, "aigolog: %s\n", textsafe.Clean(msg))
}

// warn prints err as a warning.
func warn(w io.Writer, err error) {
	report(w, "warning: "+err.Error())
}

// splitErrors returns the errors that err joins, or err itself.
func splitErrors(err error) []error {
	if err == nil {
		return nil
	}
	var j interface{ Unwrap() []error }
	if errors.As(err, &j) {
		return j.Unwrap()
	}
	return []error{err}
}

// cacheDir returns the folder of the databases: the one the configuration
// names, or the default one. On failure it has said so and returns false.
func cacheDir(env Env, cfg config.Config) (string, bool) {
	if cfg.CacheDir != "" {
		return cfg.CacheDir, true
	}
	dir, err := store.DefaultDir()
	if err != nil {
		report(env.Stderr, err.Error())
		return "", false
	}
	return dir, true
}

// noArguments is the start of list and clean: it refuses arguments, reads the
// configuration and finds the folder. When ok is false the command is over and
// code is its exit code.
func noArguments(env Env, name string, args []string) (cfg config.Config, dir string, code int, ok bool) {
	if len(args) > 0 {
		report(env.Stderr, name+" takes no arguments")
		return cfg, "", exitcode.Usage, false
	}
	cfg, err := config.Load(env.LookupEnv)
	if err != nil {
		report(env.Stderr, err.Error())
		return cfg, "", exitcode.Usage, false
	}
	dir, ok = cacheDir(env, cfg)
	if !ok {
		return cfg, "", exitcode.Failure, false
	}
	return cfg, dir, exitcode.OK, true
}
