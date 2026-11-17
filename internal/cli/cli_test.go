// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unicode"

	"github.com/ckir/aigolog/internal/buildinfo"
	"github.com/ckir/aigolog/internal/exitcode"
)

func run(args ...string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	code = Run(args, &o, &e)
	return code, o.String(), e.String()
}

func TestRunWithNoArgumentsPrintsUsageAndFails(t *testing.T) {
	code, out, errOut := run()
	if code != exitcode.Usage {
		t.Errorf("code = %d, want %d", code, exitcode.Usage)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errOut, "Usage:\n  aigolog <command> [arguments]\n") {
		t.Errorf("stderr lacks the usage line:\n%s", errOut)
	}
}

func TestRunHelpPrintsUsageAndSucceeds(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, out, errOut := run(arg)
		if code != exitcode.OK {
			t.Errorf("%s: code = %d, want %d", arg, code, exitcode.OK)
		}
		if !strings.Contains(out, "Commands:\n") {
			t.Errorf("%s: stdout lacks the list of commands:\n%s", arg, out)
		}
		if errOut != "" {
			t.Errorf("%s: stderr = %q, want empty", arg, errOut)
		}
	}
}

func TestRunUnknownCommandNamesItAndFails(t *testing.T) {
	code, out, errOut := run("frobnicate")
	if code != exitcode.Usage {
		t.Errorf("code = %d, want %d", code, exitcode.Usage)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	want := "aigolog: unknown command \"frobnicate\"\nRun \"aigolog help\" for the commands.\n"
	if errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
}

func TestRunUnknownCommandNeverPrintsRawControlCharacters(t *testing.T) {
	_, _, errOut := run("a\x1b[31mb\x07")
	if strings.ContainsAny(errOut, "\x1b\x07") {
		t.Errorf("stderr carries a raw control character: %q", errOut)
	}
}

func TestRunUnknownCommandEscapesEveryControlCharacter(t *testing.T) {
	var controls []rune
	for r := rune(0x00); r <= 0x1f; r++ {
		controls = append(controls, r)
	}
	controls = append(controls, 0x7f)
	for r := rune(0x80); r <= 0x9f; r++ {
		controls = append(controls, r)
	}
	for _, r := range controls {
		code, _, errOut := run(string(r))
		if code != exitcode.Usage {
			t.Errorf("%U: code = %d, want %d", r, code, exitcode.Usage)
		}
		for _, c := range errOut {
			if unicode.IsControl(c) && c != '\n' {
				t.Errorf("%U: stderr carries raw control character %U: %q", r, c, errOut)
				break
			}
		}
	}
}

func TestRunVersionPrintsTheProgramAndGo(t *testing.T) {
	code, out, errOut := run("version")
	if code != exitcode.OK {
		t.Fatalf("code = %d, want %d; stderr: %s", code, exitcode.OK, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("stdout has %d lines, want at least 2:\n%s", len(lines), out)
	}
	if want := "aigolog " + buildinfo.Version; lines[0] != want {
		t.Errorf("line 1 = %q, want %q", lines[0], want)
	}
	if want := "go " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH; lines[1] != want {
		t.Errorf("line 2 = %q, want %q", lines[1], want)
	}
}

func TestRunVersionPrintsTheStoreEngine(t *testing.T) {
	code, out, errOut := run("version")
	if code != exitcode.OK {
		t.Fatalf("code = %d, want %d; stderr: %s", code, exitcode.OK, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("stdout has %d lines, want at least 3:\n%s", len(lines), out)
	}
	if !regexp.MustCompile(`^sqlite 3\.\d+\.\d+$`).MatchString(lines[2]) {
		t.Errorf("line 3 = %q, want something like \"sqlite 3.54.1\"", lines[2])
	}
}

func TestRunVersionNamesTheEventLogAPIOnWindowsOnly(t *testing.T) {
	code, out, errOut := run("version")
	if code != exitcode.OK {
		t.Fatalf("code = %d, want %d; stderr: %s", code, exitcode.OK, errOut)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if runtime.GOOS == "windows" {
		if len(lines) != 4 || lines[3] != "eventlog api ok" {
			t.Errorf("on Windows want 4 lines, the last \"eventlog api ok\"; got:\n%s", out)
		}
		return
	}
	if len(lines) != 3 {
		t.Errorf("want 3 lines off Windows; got %d:\n%s", len(lines), out)
	}
}

func TestVersionFailsWithOneAndSaysSoWhenTheEngineDoesNotWork(t *testing.T) {
	old := engineVersion
	t.Cleanup(func() { engineVersion = old })
	engineVersion = func(context.Context) (string, error) { return "", errors.New("boom") }

	code, out, errOut := run("version")
	if code != exitcode.Failure {
		t.Errorf("code = %d, want %d", code, exitcode.Failure)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) < 2 || lines[0] != "aigolog "+buildinfo.Version || !strings.HasPrefix(lines[1], "go ") {
		t.Errorf("stdout lacks the first two lines:\n%s", out)
	}
	if strings.Contains(out, "sqlite") {
		t.Errorf("stdout has a sqlite line:\n%s", out)
	}
	if want := "aigolog: the store's engine does not work: boom\n"; errOut != want {
		t.Errorf("stderr = %q, want %q", errOut, want)
	}
}

func TestUsageTextIsPinned(t *testing.T) {
	const want = `aigolog reads a log and says what it reveals.

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
	code, out, errOut := run("help")
	if code != exitcode.OK {
		t.Errorf("code = %d, want %d", code, exitcode.OK)
	}
	if out != want {
		t.Errorf("the usage text changed; if on purpose, change this pin.\ngot:\n%s\nwant:\n%s", out, want)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestHelpVersionAndUsageReportAWriteError(t *testing.T) {
	for _, arg := range []string{"help", "version"} {
		var e bytes.Buffer
		code := RunEnv(Env{Stdout: failingWriter{}, Stderr: &e}, []string{arg})
		if code != exitcode.Failure {
			t.Errorf("%s: code = %d, want %d", arg, code, exitcode.Failure)
		}
	}
	code := RunEnv(Env{Stdout: &bytes.Buffer{}, Stderr: failingWriter{}}, nil)
	if code != exitcode.Usage {
		t.Errorf("no arguments: code = %d, want %d", code, exitcode.Usage)
	}
}

// nthFailingWriter fails the write numbered failAt (counting from 1) and
// accepts every other write.
type nthFailingWriter struct {
	failAt int
	writes int
}

func (w *nthFailingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, errors.New("write failed")
	}
	return len(p), nil
}

func TestVersionReturnsFailureWhicheverOneWriteFails(t *testing.T) {
	// aigolog line, go line, sqlite line, and one line per platform part.
	lines := 3 + len(platformParts())
	for n := 1; n <= lines; n++ {
		w := &nthFailingWriter{failAt: n}
		var e bytes.Buffer
		code := RunEnv(Env{Stdout: w, Stderr: &e}, []string{"version"})
		if code != exitcode.Failure {
			t.Errorf("write %d of %d fails: code = %d, want %d", n, lines, code, exitcode.Failure)
		}
		if w.writes != lines {
			t.Errorf("write %d of %d fails: version made %d writes, want %d (it must go on after a failed write)", n, lines, w.writes, lines)
		}
	}
	// With no write failing the same writer sees the same number of writes.
	w := &nthFailingWriter{}
	code := RunEnv(Env{Stdout: w, Stderr: &bytes.Buffer{}}, []string{"version"})
	if code != exitcode.OK || w.writes != lines {
		t.Errorf("no write fails: code = %d (want %d), writes = %d (want %d)", code, exitcode.OK, w.writes, lines)
	}
}

func TestVersionWithAFailingEngineAndAFailingWriteSaysTheSame(t *testing.T) {
	old := engineVersion
	t.Cleanup(func() { engineVersion = old })
	engineVersion = func(context.Context) (string, error) { return "", errors.New("boom") }

	var e bytes.Buffer
	code := RunEnv(Env{Stdout: failingWriter{}, Stderr: &e}, []string{"version"})
	if code != exitcode.Failure {
		t.Errorf("code = %d, want %d", code, exitcode.Failure)
	}
	if want := "aigolog: the store's engine does not work: boom\n"; e.String() != want {
		t.Errorf("stderr = %q, want %q", e.String(), want)
	}
}
