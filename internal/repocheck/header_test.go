// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package repocheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// headerAsGoComment turns the text of source-header.txt into the comment block
// addlicense writes: "// " before each line, nothing after an empty one.
func headerAsGoComment(header string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(header, "\n"), "\n") {
		b.WriteString(strings.TrimRight("// "+line, " \t"))
		b.WriteString("\n")
	}
	return b.String()
}

// startsWithHeader reports whether a file begins with the header and one empty line.
func startsWithHeader(content, want string) bool {
	return strings.HasPrefix(content, want+"\n")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's folder")
		}
		dir = parent
	}
}

func TestHeaderCheckAcceptsTheRightHeaderOnly(t *testing.T) {
	want := headerAsGoComment("Copyright (c) 2026 X\n\nSecond line.\n")
	if want != "// Copyright (c) 2026 X\n//\n// Second line.\n" {
		t.Fatalf("headerAsGoComment = %q", want)
	}
	if !startsWithHeader(want+"\npackage p\n", want) {
		t.Error("the right header was refused")
	}
	if startsWithHeader("// Copyright (c) 1999 Someone Else\n\npackage p\n", want) {
		t.Error("another holder's header was accepted")
	}
	if startsWithHeader("package p\n", want) {
		t.Error("a file with no header was accepted")
	}
}

func TestEveryGoFileStartsWithTheLicenceHeader(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "source-header.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := headerAsGoComment(string(raw))
	checked := 0
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			checked++
			if !startsWithHeader(string(b), want) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s does not start with the header in source-header.txt", filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if checked == 0 {
		t.Fatal("no Go file was checked")
	}
}
