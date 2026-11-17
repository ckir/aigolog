// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ckir/aigolog/internal/testlog"
)

func TestGenlogWritesTheRequestedLinesAndRefusesAnUnknownKind(t *testing.T) {
	want := map[string]func() []byte{
		"ssh":     func() []byte { b, _ := testlog.SSH(3, 50); return b },
		"restart": func() []byte { b, _ := testlog.Restart(3, 50); return b },
		"web":     func() []byte { b, _ := testlog.Web(3, 50); return b },
		"dense":   func() []byte { return testlog.Dense(3, 50) },
	}
	for _, kind := range []string{"ssh", "restart", "web", "dense"} {
		t.Run(kind, func(t *testing.T) {
			// To a file.
			path := filepath.Join(t.TempDir(), "out.log")
			var out, errOut bytes.Buffer
			if code := run([]string{"-kind", kind, "-seed", "3", "-lines", "50", "-o", path}, &out, &errOut); code != 0 {
				t.Fatalf("exit %d, stderr %q", code, errOut.String())
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want[kind]()) || bytes.Count(got, []byte{'\n'}) != 50 {
				t.Errorf("the file holds %d lines or other bytes than the generator gives", bytes.Count(got, []byte{'\n'}))
			}
			if out.Len() != 0 || errOut.Len() != 0 {
				t.Errorf("stdout %q, stderr %q: want nothing when the log goes to a file", out.String(), errOut.String())
			}
			// To standard output.
			out.Reset()
			if code := run([]string{"-kind", kind, "-seed", "3", "-lines", "50"}, &out, &errOut); code != 0 {
				t.Fatalf("exit %d, stderr %q", code, errOut.String())
			}
			if !bytes.Equal(out.Bytes(), want[kind]()) {
				t.Error("standard output differs from the generator")
			}
		})
	}

	t.Run("wrong usage exits 2 and writes no file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "never.log")
		for _, args := range [][]string{
			{"-kind", "json", "-o", path},
			{"-o", path}, // no kind
			{"-kind", "ssh", "-lines", "0", "-o", path},
			{"-kind", "ssh", "-lines", "-4", "-o", path},
			{"-kind", "ssh", "-lines", "many", "-o", path},
			{"-kind", "ssh", "-seed", "x", "-o", path},
			{"-kind", "ssh", "-nope", "-o", path},
			{"-kind", "ssh", "extra", "-o", path},
		} {
			var out, errOut bytes.Buffer
			if code := run(args, &out, &errOut); code != 2 {
				t.Errorf("%q: exit %d, want 2", args, code)
			}
			if !strings.Contains(errOut.String(), "genlog") || out.Len() != 0 {
				t.Errorf("%q: stdout %q, stderr %q: want a message on stderr only", args, out.String(), errOut.String())
			}
			if _, err := os.Stat(path); err == nil {
				t.Errorf("%q: a file was made", args)
			}
		}
	})

	t.Run("a file that cannot be written exits 1", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "no-such-folder", "out.log")
		var out, errOut bytes.Buffer
		if code := run([]string{"-kind", "web", "-lines", "5", "-o", path}, &out, &errOut); code != 1 {
			t.Errorf("exit %d, want 1; stderr %q", code, errOut.String())
		}
		if errOut.Len() == 0 {
			t.Error("no message on stderr")
		}
	})

	t.Run("help exits 0 and says the kinds", func(t *testing.T) {
		var out, errOut bytes.Buffer
		if code := run([]string{"-h"}, &out, &errOut); code != 0 {
			t.Errorf("exit %d, want 0", code)
		}
		if !strings.Contains(errOut.String(), "ssh") || !strings.Contains(errOut.String(), "restart") || !strings.Contains(errOut.String(), "web") {
			t.Errorf("usage %q does not name the kinds", errOut.String())
		}
	})
}

// failingWriter is a standard output that refuses every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk is gone") }

func TestGenlogReportsAFailureToWriteToStandardOutputAndExitsWithOne(t *testing.T) {
	var errOut bytes.Buffer
	code := run([]string{"-kind", "ssh", "-lines", "5"}, failingWriter{}, &errOut)
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if want := "genlog: writing the log: disk is gone\n"; errOut.String() != want {
		t.Errorf("stderr %q, want %q", errOut.String(), want)
	}
}
