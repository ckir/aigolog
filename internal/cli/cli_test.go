// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

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
