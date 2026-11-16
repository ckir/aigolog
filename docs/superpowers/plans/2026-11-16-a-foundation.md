# aigolog Sub-project A (Foundation) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go program named `aigolog` that builds for the four targets with every dependency of part 1 in place, a single gate (`just check`), and a workflow that runs that gate on Linux and on GitHub's Windows x64 and ARM64 machines. This is acceptance test A1.

**Architecture:** One command, `cmd/aigolog`, that hands its arguments to `internal/cli` and exits with the code it returns. The two third-party modules of part 1 are each used once, so that they are built and run on every target now: `modernc.org/sqlite` behind `internal/store`, and `golang.org/x/sys` behind `internal/winlog`, which the Windows build of the program links through its `version` command. Everything a person or a machine runs goes through `just`.

**Tech Stack:** Go 1.27.4 (module floor `go 1.26.0`); `modernc.org/sqlite` v1.62.0; `golang.org/x/sys`; just 1.58.0; lefthook 2.1.17; addlicense v1.2.0; GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-11-15-engine-and-report-design.md` (sections 5, 7.4, 8 "A1", 9, 10). The roadmap, `docs/superpowers/plans/2026-11-16-part-1-roadmap.md`, fixes the decisions that hold for all of part 1; this plan repeats the ones it uses.

**No code exists yet.** Every file below is created by this plan, except `.gitignore`, `header.txt` and `README.md`, which exist and were read on 2026-11-16.

**What was checked before this plan was handed over (2026-11-16, on the development machine, in a throwaway folder outside the repository).** The code blocks of this plan were copied out by a script and run against a minimal implementation written from the plan's own descriptions; this was done again after the plan's review changed it. The tests of Tasks 1 to 4 passed on Linux. The four builds succeeded with `CGO_ENABLED=0`, and `aigolog version` printed `sqlite 3.54.1`. `go mod tidy` left `golang.org/x/sys v0.49.0` as a direct requirement, and the Windows build of the program lists both modules (`go version -m`). `just check` passed, and each fault in Task 4's table was caught with the message the table gives. The Linux branch of the install script downloaded and verified its file. `actionlint` and `lefthook validate` accepted the two configuration files. `just setup` installed the two hooks; a commit of a well-formed change went through the pre-commit hook and a commit of a badly formatted file was refused by it. The declared-tools file parsed and the session hook that reads it reported nothing missing. Against the real repository, read-only: no tracked file is stored with CRLF line endings, so the rule in `.gitattributes` changes none; and the licence check of Task 4, run as written, listed no file, so its ignore list covers the captured test files. **Not checked, because it cannot be done there:** anything on Windows, which is the two Windows tests of Task 3, the Windows branches of the install script, and the workflow itself. Task 6 is where those are first run.

## Global Constraints

- Targets: `linux/amd64`, `linux/arm64`, `windows/amd64`, `windows/arm64` (design section 5).
- No C code: every build is `CGO_ENABLED=0` (design A1; goal G4 allows C only where measured, and nothing here has been).
- The store is SQLite through `modernc.org/sqlite` (design 7.4), at v1.62.0, whose `go.mod` says `go 1.26.0`. Its driver is registered under the name `sqlite`.
- Module path `github.com/ckir/aigolog`. `go.mod` says `go 1.26.0`. Builds use Go 1.27.4.
- Third-party modules: `modernc.org/sqlite` and `golang.org/x/sys`, and whatever they require. No other module is added in this sub-project.
- `just check` is the gate; anything automated runs it (design section 10). Recipes are plain command lines that run under `sh` on Linux and on Windows.
- `go test ./...` passes with no network once the module cache is filled (design section 8).
- Every `.go`, `.yml` and `.sh` file starts with the text of `header.txt` as comments, then one empty line. For a Go file that is exactly:

  ```go
  // Copyright (c) 2026 Costas Kirgoussios
  //
  // This software is licensed under the PolyForm Noncommercial License 1.0.0.
  // You may obtain a copy of the License at:
  // https://polyformproject.org/licenses/noncommercial/1.0.0/
  ```

  For `.yml` and `.sh` the same lines start with `#` in place of `//`; in a `.sh` file they follow the `#!` line.
- Exit codes (roadmap decision 4): `0` done, `1` failed, `2` wrong usage, `3` a model was configured and failed, `4` Windows refused a log, `5` the store cannot be written.
- Nothing printed to a terminal carries a raw control character, whatever its source (design 7.11).
- GitHub Actions used in a workflow are pinned to a full commit id; the repository refuses anything else.
- The repository is public. No name, address or path from the development machine goes into a file or a commit message.
- Work is on the branch `a-foundation`, made from `main` after the design branch has been merged. Commit when a task says so. **Push only when the owner says so.**
- Stop and report, without adapting, if a file this plan says does not exist already exists, if a step that should pass fails, or if a step that should fail passes. Where a step says a command fails, the reason it gives is what matters; Go's exact wording may differ.

## Review Focus

Conditions the design implies and a person would hit, each pinned by a test in the task named:

1. **A Windows checkout.** With CRLF line endings `gofmt` reports every file. `.gitattributes` forces LF (Task 1) and the gate runs on both Windows machines (Task 6).
2. **`aigolog` with no command, an unknown command, or a command holding control characters.** It explains itself, exits with 2, and prints no raw control character (Task 1).
3. **A source file with the wrong header that still contains the word "copyright".** `addlicense -check` passes it (design section 10 names this limit). A test compares every Go file with `header.txt` (Task 4).
4. **Code that compiles only on the machine it was written on.** The gate vets all four targets (Task 4), and the Windows-only package has a test that runs on Windows (Tasks 3 and 6).
5. **Setting up on a machine that lacks a tool.** `just setup` names the missing tool and how to get it (Task 5).

---

### Task 1: The module and the command

**Files:**
- Create: `go.mod`, `.gitattributes`
- Create: `cmd/aigolog/main.go`
- Create: `internal/exitcode/exitcode.go`, `internal/exitcode/exitcode_test.go`
- Create: `internal/buildinfo/buildinfo.go`
- Create: `internal/cli/cli.go`, `internal/cli/cli_test.go`
- Modify: `.gitignore` (append one line)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `exitcode.OK`, `exitcode.Failure`, `exitcode.Usage`, `exitcode.ModelFailed`, `exitcode.LogRefused`, `exitcode.StorageUnavailable` (untyped integer constants 0 to 5).
  - `buildinfo.Version` (a `string` variable, `"0.1.0-dev"` unless set at build time).
  - `cli.Run(args []string, stdout, stderr io.Writer) int`.

- [ ] **Step 1: Create the module and the line-ending rule**

`go.mod`:

```
module github.com/ckir/aigolog

go 1.26.0
```

`.gitattributes`:

```
* text=auto eol=lf
*.evtx binary
```

Append to `.gitignore`:

```
/dist/
```

- [ ] **Step 2: Write the failing tests**

`internal/exitcode/exitcode_test.go` (header, then):

```go
package exitcode

import "testing"

func TestCodesAreTheOnesTheRoadmapFixes(t *testing.T) {
	got := []int{OK, Failure, Usage, ModelFailed, LogRefused, StorageUnavailable}
	for want, code := range got {
		if code != want {
			t.Errorf("code at position %d = %d, want %d", want, code, want)
		}
	}
}
```

`internal/cli/cli_test.go` (header, then):

```go
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
```

- [ ] **Step 3: Run the tests and see them fail**

Run: `go test ./...`
Expected: FAIL. `internal/cli` does not build, with `no required module provides package github.com/ckir/aigolog/internal/buildinfo`; `internal/exitcode` does not build, with `undefined: OK` and the same for the other five constants.

- [ ] **Step 4: Write the code**

- `internal/exitcode/exitcode.go`: the six constants `OK = 0`, `Failure = 1`, `Usage = 2`, `ModelFailed = 3`, `LogRefused = 4`, `StorageUnavailable = 5`, each with a one-line comment saying when it is returned.
- `internal/buildinfo/buildinfo.go`: `var Version = "0.1.0-dev"`, with a comment that `just build` sets it.
- `internal/cli/cli.go`: `func Run(args []string, stdout, stderr io.Writer) int`.
  - No argument: the usage text to `stderr`; return `exitcode.Usage`.
  - `help`, `-h` or `--help`: the usage text to `stdout`; return `exitcode.OK`.
  - `version`: two lines to `stdout`, `aigolog <buildinfo.Version>` and `go <runtime.Version()> <GOOS>/<GOARCH>`; return `exitcode.OK`.
  - Anything else: to `stderr`, `aigolog: unknown command <the argument formatted with %q>` and the line `Run "aigolog help" for the commands.`; return `exitcode.Usage`. `%q` is what keeps control characters out of the output.
  - The usage text, exactly:

    ```
    aigolog reads a log and says what it reveals.

    Usage:
      aigolog <command> [arguments]

    Commands:
      version   print the version of the program and of its parts
      help      print this text
    ```
- `cmd/aigolog/main.go`: `func main()` calls `os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))` and nothing else.

- [ ] **Step 5: Run the tests and see them pass**

Run: `gofmt -l cmd internal && go vet ./... && go test ./...`
Expected: no file listed by `gofmt`, no output from `vet`, and `ok` for `internal/cli` and `internal/exitcode`.

- [ ] **Step 6: Commit**

```bash
git add go.mod .gitattributes .gitignore cmd internal
git commit -m "Add the module, the command and its version"
```

---

### Task 2: The store's engine, built for the four targets

This is the heart of A1: the design says the store "has only been read, not built".

**Files:**
- Create: `internal/store/engine.go`, `internal/store/engine_test.go`
- Modify: `internal/cli/cli.go` (the `version` command prints one more line), `internal/cli/cli_test.go` (one more test)
- Modify: `go.mod`; create `go.sum` (both written by `go get`)

**Interfaces:**
- Consumes: `cli.Run`, `exitcode.Failure` (Task 1).
- Produces:
  - `store.DriverName` (constant, `"sqlite"`): the `database/sql` driver every later part of the store opens.
  - `store.EngineVersion(ctx context.Context) (string, error)`: the version SQLite reports from a throwaway in-memory database.

- [ ] **Step 1: Add the dependency**

Run: `go get modernc.org/sqlite@v1.62.0`
Expected: `go.mod` gains `require modernc.org/sqlite v1.62.0` and `go.sum` is created. This step needs the network; nothing after it does.

- [ ] **Step 2: Write the failing tests**

`internal/store/engine_test.go` (header, then):

```go
package store

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
)

func TestEngineVersionLooksLikeSQLite(t *testing.T) {
	got, err := EngineVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^3\.\d+\.\d+$`).MatchString(got) {
		t.Errorf("EngineVersion = %q, want something like 3.54.1", got)
	}
}

func TestEngineKeepsWhatIsPassedAsAParameter(t *testing.T) {
	db, err := sql.Open(DriverName, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // each connection to ":memory:" is its own database

	const text = `it's "quoted"; DROP TABLE t; -- κόσμος`
	if _, err := db.Exec(`CREATE TABLE t (n INTEGER, s TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t (n, s) VALUES (?, ?)`, 42, text); err != nil {
		t.Fatal(err)
	}
	var n int
	var s string
	if err := db.QueryRow(`SELECT n, s FROM t WHERE s = ?`, text).Scan(&n, &s); err != nil {
		t.Fatal(err)
	}
	if n != 42 || s != text {
		t.Errorf("got (%d, %q), want (42, %q)", n, s, text)
	}
}
```

Add to `internal/cli/cli_test.go` (and add `"regexp"` to its imports):

```go
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
```

- [ ] **Step 3: Run the tests and see them fail**

Run: `go test ./...`
Expected: FAIL, `EngineVersion` and `DriverName` undefined.

- [ ] **Step 4: Write the code**

- `internal/store/engine.go`: imports `modernc.org/sqlite` for its registration only (a blank import). `const DriverName = "sqlite"`. `EngineVersion` opens `:memory:` with `DriverName`, runs `SELECT sqlite_version()` with the context, closes the database and returns the string.
- `internal/cli/cli.go`: after the two lines of `version`, call `store.EngineVersion(context.Background())`. On success print a third line, `sqlite <version>`. On error print `aigolog: the store's engine does not work: <error>` to `stderr` and return `exitcode.Failure`.

- [ ] **Step 5: Run the tests and see them pass**

Run: `gofmt -l cmd internal && go vet ./... && go test ./...`
Expected: nothing from `gofmt` or `vet`; `ok` for `internal/cli`, `internal/exitcode` and `internal/store`.

- [ ] **Step 6: Build for the four targets**

Run each line; each must print nothing and exit 0:

```bash
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -o /dev/null ./cmd/aigolog
CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -o /dev/null ./cmd/aigolog
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/aigolog
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -o /dev/null ./cmd/aigolog
```

**If any of the four fails, stop here and report the output.** The design says that the choice of store is then settled with the owner before anything else is built. Do not try another module or another version.

- [ ] **Step 7: Run the program**

Run: `go run ./cmd/aigolog version`
Expected: three lines, the third like `sqlite 3.54.1`.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal
git commit -m "Prove the store's engine builds for the four targets"
```

---

### Task 3: The Windows Event Log API can be reached

Part 1's second module, `golang.org/x/sys`, is used by the Event Log reader (design 7.1, sub-project F). This task proves on real Windows machines, x64 and ARM64, that the calls the reader will make can be found from Go with no C code, and it links that module into the Windows program, so that A1's "every dependency in place" is true of the program and not only of a test.

**Files:**
- Create: `internal/winlog/doc.go` (every system)
- Create: `internal/winlog/api_windows.go`, `internal/winlog/api_windows_test.go` (Windows build only)
- Create: `internal/cli/parts_windows.go` (Windows build only), `internal/cli/parts_other.go` (every other system)
- Modify: `internal/cli/cli.go` (the `version` command prints the platform's lines), `internal/cli/cli_test.go` (one more test)
- Modify: `go.mod`, `go.sum` (`golang.org/x/sys` becomes a direct requirement)

**Interfaces:**
- Consumes: `cli.Run` and its `version` command (Tasks 1 and 2).
- Produces:
  - Windows build only: `winlog.Available() error`: nil when `wevtapi.dll` loads and every procedure in the list below is found; otherwise an error that names the first one missing.
  - In `internal/cli`, unexported: `platformParts() []string`, the lines `version` prints after the `sqlite` line. On Windows it is one line, `eventlog api ok`, or `eventlog api missing: <error>`. On every other system it is no line.

- [ ] **Step 1: Write the failing tests**

`internal/winlog/api_windows_test.go` (header, then):

```go
//go:build windows

package winlog

import "testing"

func TestEventLogAPIIsReachable(t *testing.T) {
	if err := Available(); err != nil {
		t.Fatal(err)
	}
}
```

Add to `internal/cli/cli_test.go`:

```go
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
```

- [ ] **Step 2: See it fail to compile for Windows**

Run: `GOOS=windows GOARCH=amd64 go vet ./internal/winlog/`
Expected: `undefined: Available`, from the test file (the package has no other file yet). The new test in `internal/cli` passes on this machine already, because nothing is printed after the `sqlite` line yet; on Windows it would fail.

- [ ] **Step 3: Write the code**

- `internal/winlog/doc.go`: the package comment and `package winlog`. The comment says: this package reads the Windows Event Log through Windows' own API; only the Windows build has code in it.
- `internal/winlog/api_windows.go`, with `//go:build windows`:
  - a package variable for the library: `windows.NewLazySystemDLL("wevtapi.dll")` from `golang.org/x/sys/windows`;
  - the complete list of procedures, in this order: `EvtQuery`, `EvtNext`, `EvtRender`, `EvtClose`, `EvtFormatMessage`, `EvtOpenPublisherMetadata`, `EvtOpenEventMetadataEnum`, `EvtNextEventMetadata`, `EvtGetEventMetadataProperty`;
  - `Available()`: for each name, `NewProc(name).Find()`; return the first error, wrapped so that it names the procedure; nil if all are found. It calls none of them.
- `internal/cli/parts_windows.go`, with `//go:build windows`: `platformParts()` calls `winlog.Available()` and returns `[]string{"eventlog api ok"}`, or `[]string{"eventlog api missing: " + err.Error()}`.
- `internal/cli/parts_other.go`, with `//go:build !windows`: `platformParts()` returns nil.
- `internal/cli/cli.go`: after the `sqlite` line, `version` prints each line of `platformParts()`. The exit code does not depend on them.
- Run `go mod tidy` so that `golang.org/x/sys` is listed as a direct requirement at the version already in `go.mod`. Do not change the version.

- [ ] **Step 4: See it compile for both Windows targets and still build on Linux**

Run:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go vet ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/aigolog
CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -o /dev/null ./cmd/aigolog
go build ./... && go test ./...
```

Expected: nothing from the first four lines; the tests pass. The two Windows tests themselves run in Task 6, on Windows.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/winlog internal/cli
git commit -m "Check that the Event Log API can be reached from Go"
```

---

### Task 4: The gate

**Files:**
- Create: `justfile`
- Create: `internal/repocheck/doc.go`, `internal/repocheck/header_test.go`

**Interfaces:**
- Consumes: the files of Tasks 1 to 3.
- Produces: the recipes `fmt`, `fmt-check`, `vet`, `vet-cross`, `license`, `license-check`, `test`, `check`, `build`, `clean`. `just check` is what Tasks 5 and 6 call.

- [ ] **Step 1: Write the failing test**

`internal/repocheck/doc.go`: the package comment (this package holds tests about the repository itself, not about the program) and `package repocheck`.

`internal/repocheck/header_test.go` (header, then):

```go
package repocheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// headerAsGoComment turns the text of header.txt into the comment block
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
	raw, err := os.ReadFile(filepath.Join(root, "header.txt"))
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
				t.Errorf("%s does not start with the header in header.txt", filepath.ToSlash(rel))
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
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/repocheck/`
Expected: PASS if every file of Tasks 1 to 3 was written with its header. If it names a file, add the header to that file (it is in Global Constraints) and run it again. Then prove the test can fail: remove the first line of `internal/buildinfo/buildinfo.go`, run it, see `internal/buildinfo/buildinfo.go does not start with the header in header.txt`, and put the line back.

- [ ] **Step 3: Write the `justfile`**

```just
# Commands for working on aigolog. `just check` is the gate.
# Recipes are plain command lines, so that they run under sh on Linux and on Windows.

version := "0.1.0-dev"
ldflags := "-X github.com/ckir/aigolog/internal/buildinfo.Version=" + version
license_args := "-f header.txt -ignore 'testdata/**' -ignore 'docs/**' -ignore 'ideas/**' -ignore 'marketing/**' -ignore 'dist/**'"

# List the recipes.
default:
    @just --list

# Rewrite the Go files in the standard format.
fmt:
    gofmt -w cmd internal

# Fail if a Go file is not in the standard format.
fmt-check:
    @test -z "$(gofmt -l cmd internal)" || { echo "not formatted (run: just fmt):"; gofmt -l cmd internal; exit 1; }

# Vet the code for this machine.
vet:
    go vet ./...

# Vet the code for all four targets, so that code which compiles only here is caught.
vet-cross:
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go vet ./...
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go vet ./...
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
    CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go vet ./...

# Add the licence header to source files that lack one.
license:
    addlicense {{license_args}} .

# Fail if a source file lacks a licence header.
license-check:
    addlicense -check {{license_args}} .

# Run the tests that need neither a network nor a model.
test:
    go test ./...

# The gate.
check: fmt-check vet vet-cross license-check test

# Build the program for the four targets into dist/.
build:
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "{{ldflags}}" -o dist/aigolog-linux-amd64 ./cmd/aigolog
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "{{ldflags}}" -o dist/aigolog-linux-arm64 ./cmd/aigolog
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "{{ldflags}}" -o dist/aigolog-windows-amd64.exe ./cmd/aigolog
    CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "{{ldflags}}" -o dist/aigolog-windows-arm64.exe ./cmd/aigolog

# Remove what build made.
clean:
    rm -rf dist
```

The `justfile` has no licence header: addlicense does not know the file type and skips it.

Two things about this list of recipes. `vet-cross` is this plan's addition to the gate the design describes (section 10); it is what catches Windows-only mistakes before a push. The design's other two recipes, the one that fetches the test logs and the one for the model tests, are added by sub-projects B2 and C, which are the first to have something for them to do.

- [ ] **Step 4: Run the gate**

Run: `just check`
Expected: the five recipes run in order and the last lines are `ok` lines from `go test`; exit 0.

- [ ] **Step 5: See the gate catch each fault, then undo each**

| Fault to introduce | Run | Expected |
|---|---|---|
| Add two spaces before `package cli` in `internal/cli/cli.go` | `just fmt-check` | `not formatted (run: just fmt):` and the file's name; exit 1 |
| Create `internal/cli/scratch.go` holding only `package cli` | `just license-check` | the file's name; exit 1 |
| In `internal/winlog/api_windows.go` rename `Available` to `available` | `just vet-cross` | `undefined: Available` (or `undefined: winlog.Available`) from a `GOOS=windows` line; exit 1 |

Undo each fault before the next (`git checkout -- <file>`, and delete `scratch.go`), and finish with `just check` passing.

- [ ] **Step 6: Build and run the result**

Run: `just build && ls dist && ./dist/aigolog-linux-amd64 version`
Expected: four files, `aigolog-linux-amd64`, `aigolog-linux-arm64`, `aigolog-windows-amd64.exe`, `aigolog-windows-arm64.exe`; then three lines beginning `aigolog 0.1.0-dev`, `go go1.27.4 linux/amd64`, `sqlite 3.`.

- [ ] **Step 7: Commit**

```bash
git add justfile internal/repocheck
git commit -m "Add the gate: format, vet for four targets, headers, tests"
```

---

### Task 5: Hooks, setup and the declared tools

**Files:**
- Create: `lefthook.yml`
- Create: `.claude/recommended-tools.json`
- Modify: `justfile` (add `_need` and `setup`)

**Interfaces:**
- Consumes: `just check`, `just fmt-check`, `just vet`, `just license-check` (Task 4).
- Produces: `just setup`.

- [ ] **Step 1: Add the two recipes to the `justfile`**, after `default`:

```just
# Fail with a hint when a tool is not on the path.
_need tool hint:
    @command -v {{tool}} >/dev/null 2>&1 || { echo "missing: {{tool}} ({{hint}})"; exit 1; }

# Check that the tools are present, install the git hooks, and run the program once.
setup: (_need "go" "Go 1.26 or newer, https://go.dev/dl/") (_need "lefthook" "go install github.com/evilmartians/lefthook/v2@v2.1.17") (_need "addlicense" "go install github.com/google/addlicense@v1.2.0")
    lefthook install
    go run ./cmd/aigolog version
```

`_need` checks only that a tool is on the path. A Go older than 1.26 is reported by Go itself, which refuses the module with a message that names the version `go.mod` requires.

- [ ] **Step 2: See the hint for a missing tool**

Run: `just _need no-such-tool "how to get it"; echo "exit: $?"`
Expected: `missing: no-such-tool (how to get it)`, then an error line from just, then `exit: 1`.

- [ ] **Step 3: Write `lefthook.yml`** (header as `#` comments, an empty line, then):

```yaml
pre-commit:
  commands:
    quick:
      run: just fmt-check vet license-check
pre-push:
  commands:
    check:
      run: just check
```

- [ ] **Step 4: Check the file and install the hooks**

Run: `lefthook validate && just setup`
Expected: `All good`; then lefthook reports that it installed the hooks; then the three lines of `aigolog version`.

- [ ] **Step 5: Write `.claude/recommended-tools.json`**

```json
[
  {"name": "go", "why": "builds and tests aigolog; go.mod needs 1.26 or newer", "install": "https://go.dev/dl/", "in_path": "go"},
  {"name": "just", "why": "every project command; `just check` is the gate", "install": "https://github.com/casey/just/releases/tag/1.58.0", "in_path": "just"},
  {"name": "lefthook", "why": "runs the quick checks before a commit and the gate before a push", "install": "go install github.com/evilmartians/lefthook/v2@v2.1.17", "in_path": "lefthook"},
  {"name": "addlicense", "why": "checks that source files carry the licence header", "install": "go install github.com/google/addlicense@v1.2.0", "in_path": "addlicense"},
  {"name": "source: modernc.org/sqlite v1.62.0", "why": "the store's engine; read its source at this tag before reasoning about it", "install": "git clone --depth 1 --branch v1.62.0 https://gitlab.com/cznic/sqlite"},
  {"name": "source: elastic/beats v9.5.6 (winlogbeat)", "why": "a shipping reader of the Windows Event Log in Go; design section 9 cites it", "install": "git clone --depth 1 --branch v9.5.6 https://github.com/elastic/beats"},
  {"name": "source: google/addlicense v1.2.0", "why": "design section 10 cites its header test", "install": "git clone --depth 1 --branch v1.2.0 https://github.com/google/addlicense"},
  {"name": "source: casey/just 1.58.0", "why": "design section 9 cites how it picks a shell on Windows", "install": "git clone --depth 1 --branch 1.58.0 https://github.com/casey/just"},
  {"name": "source: evilmartians/lefthook v2.1.17", "why": "design section 9 cites the hook it installs", "install": "git clone --depth 1 --branch v2.1.17 https://github.com/evilmartians/lefthook"}
]
```

The entries named `source:` have no presence check; they record where to read from. Ollama is added by sub-project C, which is the first to need it.

- [ ] **Step 6: Check the file parses and the gate still passes**

Run: `python3 -c "import json; print(len(json.load(open('.claude/recommended-tools.json'))))" && just check`
Expected: `9`, then the gate passes.

- [ ] **Step 7: Commit**

```bash
git add justfile lefthook.yml .claude/recommended-tools.json
git commit -m "Add the git hooks, the setup recipe and the declared tools"
```

---

### Task 6: The workflow on three machines, and the record of A1

**Files:**
- Create: `scripts/ci/install-just.sh`
- Create: `.github/workflows/check.yml`
- Create: `docs/acceptance/a1-builds.md`

**Interfaces:**
- Consumes: `just check`, `just build` (Task 4); `winlog.Available` through its Windows test (Task 3).
- Produces: a workflow named `check` that every later pull request runs.

- [ ] **Step 1: Write `scripts/ci/install-just.sh`**

```sh
#!/bin/sh
# Copyright (c) 2026 Costas Kirgoussios
#
# This software is licensed under the PolyForm Noncommercial License 1.0.0.
# You may obtain a copy of the License at:
# https://polyformproject.org/licenses/noncommercial/1.0.0/

# Puts the pinned version of just on the path of a GitHub-hosted runner.
# The download is checked against a checksum fixed here.
set -eu

version=1.58.0
case "${RUNNER_OS:?}-${RUNNER_ARCH:?}" in
Linux-X64)
	asset="just-$version-x86_64-unknown-linux-musl.tar.gz"
	sum=4a5cc2f53e6f0f8c59092a6cc38291eb729d46a7dd95d3ae582008881b84931d
	;;
Windows-X64)
	asset="just-$version-x86_64-pc-windows-msvc.zip"
	sum=759f16fb7aa17c5c8b9594b6d4a8c1a6630dfd042cf2b3ff84841454d3d188dc
	;;
Windows-ARM64)
	asset="just-$version-aarch64-pc-windows-msvc.zip"
	sum=3a39ed629eb67678976c811a4da46f7985a2c22f4dbabe017b8b2eb5ceb5d01c
	;;
*)
	echo "install-just: no pinned build for $RUNNER_OS-$RUNNER_ARCH" >&2
	exit 1
	;;
esac

tmp="${RUNNER_TEMP:?}"
if command -v cygpath >/dev/null 2>&1; then
	work="$(cygpath -u "$tmp")/just-bin"
else
	work="$tmp/just-bin"
fi
mkdir -p "$work"

curl -fsSL -o "$work/$asset" "https://github.com/casey/just/releases/download/$version/$asset"
echo "$sum  $work/$asset" | sha256sum -c -

case "$asset" in
*.tar.gz) tar -xzf "$work/$asset" -C "$work" just ;;
*.zip)
	if command -v unzip >/dev/null 2>&1; then
		unzip -o -q "$work/$asset" just.exe -d "$work"
	else
		# Windows' own tar reads zip files; the tar that comes with Git does not.
		"$(cygpath -u "${SYSTEMROOT:?}")/System32/tar.exe" -xf "$(cygpath -w "$work/$asset")" -C "$(cygpath -w "$work")" just.exe
	fi
	;;
esac

if command -v cygpath >/dev/null 2>&1; then
	cygpath -w "$work" >>"${GITHUB_PATH:?}"
else
	echo "$work" >>"${GITHUB_PATH:?}"
fi
"$work/just" --version
```

The three checksums are the ones in the release's own `SHA256SUMS` file, read on 2026-11-16. Git for Windows ships `unzip` (its packaging list, `make-file-list.sh` in `git-for-windows/build-extra`, names it); the other branch is there in case an image lacks it, and has not been run.

- [ ] **Step 2: Run the script's Linux branch here**

Run:

```bash
t=$(mktemp -d); p=$(mktemp)
RUNNER_OS=Linux RUNNER_ARCH=X64 RUNNER_TEMP="$t" GITHUB_PATH="$p" sh scripts/ci/install-just.sh; cat "$p"
RUNNER_OS=Linux RUNNER_ARCH=S390X RUNNER_TEMP="$t" GITHUB_PATH="$p" sh scripts/ci/install-just.sh; echo "exit: $?"
```

Expected: first `…just-1.58.0-x86_64-unknown-linux-musl.tar.gz: OK`, `just 1.58.0`, and the folder's path; then `install-just: no pinned build for Linux-S390X` and `exit: 1`. This step needs the network.

- [ ] **Step 3: Write `.github/workflows/check.yml`** (header as `#` comments, an empty line, then):

```yaml
name: check

on:
  push:
    branches:
      - main
  pull_request:

permissions: {}

concurrency:
  group: check-${{ github.ref }}
  cancel-in-progress: true

jobs:
  check:
    name: check (${{ matrix.os }})
    runs-on: ${{ matrix.os }}
    timeout-minutes: 20
    permissions:
      contents: read
    strategy:
      fail-fast: false
      matrix:
        os:
          - ubuntu-24.04
          - windows-2025
          - windows-11-arm
    defaults:
      run:
        shell: bash
    steps:
      - name: Check out
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false

      - name: Install Go
        uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version: "1.27.4"

      - name: Install just
        run: sh scripts/ci/install-just.sh

      - name: Install addlicense
        run: go install github.com/google/addlicense@v1.2.0

      - name: The gate
        run: just check

      - name: Build the four targets
        if: matrix.os == 'ubuntu-24.04'
        run: just build

      - name: Run the program
        run: go run ./cmd/aigolog version
```

`actions/setup-go` puts Go's own `bin` folder on the path, which is where `go install` leaves `addlicense`.

- [ ] **Step 4: Lint the workflow and run the gate**

Run: `actionlint .github/workflows/check.yml && just check`
Expected: no output from `actionlint` (if it is not installed, say so in the report and go on); the gate passes, which now also checks the headers of the `.yml` and `.sh` files.

- [ ] **Step 5: Commit**

```bash
git add scripts/ci/install-just.sh .github/workflows/check.yml
git commit -m "Run the gate on Linux and on Windows x64 and ARM64"
```

- [ ] **Step 6: Push and open a pull request (only with the owner's go-ahead)**

Ask the owner. Then push `a-foundation` and open a draft pull request against `main`. The workflow starts by itself.

Expected: three jobs, `check (ubuntu-24.04)`, `check (windows-2025)`, `check (windows-11-arm)`, all green. In each job's "Run the program" step the second line ends `linux/amd64`, `windows/amd64` and `windows/arm64` in that order and the third begins `sqlite 3.`; the two Windows jobs print a fourth line, `eventlog api ok`. In the two Windows jobs "The gate" shows `ok` for `internal/winlog`; in the Linux job that package is listed as having no test files.

If a job fails: where the cause can be reproduced on this machine (`just check`, the script's Linux branch), fix it here first. Where it cannot, because it happens only on Windows, report the failing step and its log lines to the owner before changing anything. Push again only with the owner's say.

- [ ] **Step 7: Write the record of A1**

`docs/acceptance/a1-builds.md`, filled from what Step 6 showed, with nothing guessed:

```markdown
# A1: the program builds, with every dependency in place

Date: <the date of the run>. Commit: <the commit the workflow ran on>. Workflow run: <its number>.

| Target | Cross-built on ubuntu-24.04 by `just build` | Built, tested and run on its own kind of machine |
|---|---|---|
| linux/amd64 | yes | yes, ubuntu-24.04 |
| linux/arm64 | yes | no |
| windows/amd64 | yes | yes, windows-2025 |
| windows/arm64 | yes | yes, windows-11-arm |

What `aigolog version` printed on each machine that ran it:

<the lines from each of the three jobs: three on Linux, four on Windows>

Dependencies: `modernc.org/sqlite` v1.62.0 and `golang.org/x/sys` <the version in go.mod>, both
with `CGO_ENABLED=0`. The Windows Event Log API was found from Go on both Windows machines, by
the test `TestEventLogAPIIsReachable` and by the program itself (its fourth line).
```

Commit it (`git add docs/acceptance/a1-builds.md && git commit -m "Record acceptance test A1"`), and push with the owner's say.

---

## Changes made after the tasks were done

- 2026-11-16: the owner's commit `5cac83c` made `header.txt` the full licence text (156 lines), and the gate failed on all three machines because the header test compared every Go file with the whole file. The owner chose to keep `header.txt` and give the files the short header from a new file, `source-header.txt` (the five lines of Global Constraints). `justfile` (`license_args`) and `internal/repocheck/header_test.go` read `source-header.txt` where this plan says `header.txt`.

## When every task is done

1. `just check` passes here and the three jobs are green on the last commit.
2. The second model reviews the committed code until a round is clean, then audits the tests (the owner's standing rules). Its findings are checked by measurement before any is acted on.
3. The owner is asked to merge the pull request. Sub-project B1's plan is written after that merge, against the code on `main`.
