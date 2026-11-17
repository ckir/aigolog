# Commands for working on aigolog. `just check` is the gate.
# Recipes are plain command lines, so that they run under sh on Linux and on Windows.

version := "0.1.0-dev"
ldflags := "-X github.com/ckir/aigolog/internal/buildinfo.Version=" + version
license_args := "-f source-header.txt -ignore 'testdata/**' -ignore '**/testdata/**' -ignore 'docs/**' -ignore 'ideas/**' -ignore 'marketing/**' -ignore 'dist/**'"

# List the recipes.
default:
    @just --list

# Fail with a hint when a tool is not on the path.
_need tool hint:
    @command -v {{tool}} >/dev/null 2>&1 || { echo "missing: {{tool}} ({{hint}})"; exit 1; }

# Check that the tools are present, install the git hooks, and run the program once.
setup: (_need "go" "Go 1.26 or newer, https://go.dev/dl/") (_need "lefthook" "go install github.com/evilmartians/lefthook/v2@v2.1.17") (_need "addlicense" "go install github.com/google/addlicense@v1.2.0")
    lefthook install
    go run ./cmd/aigolog version

# Rewrite the Go files in the standard format.
fmt:
    gofmt -w cmd internal

# Fail if a Go file is not in the standard format.
fmt-check:
    @out=$(gofmt -l cmd internal 2>&1) || { echo "gofmt failed (a file may not parse):"; echo "$out"; exit 1; }; test -z "$out" || { echo "not formatted (run: just fmt):"; echo "$out"; exit 1; }

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

# Fetch the sshd sample into testdata/fetched/ (git-ignored), checked against its recorded checksum; the test that needs it is skipped while it is absent.
fetch-openssh:
    sh scripts/ci/fetch-openssh.sh

# Fetch the whole OpenSSH log (655,146 lines) into testdata/fetched/ (git-ignored), checked against its recorded checksums.
fetch-openssh-full:
    sh scripts/ci/fetch-openssh-full.sh

# The gate.
check: fmt-check vet vet-cross license-check test

# Write a generated test log (kind: ssh, restart or web) to a file, or to standard output when none is given.
genlog kind seed="1" lines="1000" out="":
    go run ./cmd/genlog -kind {{quote(kind)}} -seed {{quote(seed)}} -lines {{quote(lines)}} {{ if out == "" { "" } else { "-o " + quote(out) } }}

# Measure the speed and memory of an analyze run on this machine; with no arguments it uses the size of the full OpenSSH log (see docs/measuring.md).
measure *args:
    go run ./cmd/measure {{args}}

# A small measurement, to see that the command works: 20,000 lines, one run.
measure-smoke:
    go run ./cmd/measure -lines 20000 -runs 1

# Measure an analyze run on the whole OpenSSH log, which fetch-openssh-full puts in testdata/fetched/ (see docs/measuring.md).
measure-full *args:
    go run ./cmd/measure -log testdata/fetched/SSH_full.log {{args}}

# Build the program for the four targets into dist/.
build:
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "{{ldflags}}" -o dist/aigolog-linux-amd64 ./cmd/aigolog
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "{{ldflags}}" -o dist/aigolog-linux-arm64 ./cmd/aigolog
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "{{ldflags}}" -o dist/aigolog-windows-amd64.exe ./cmd/aigolog
    CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags "{{ldflags}}" -o dist/aigolog-windows-arm64.exe ./cmd/aigolog

# Remove what build made.
clean:
    rm -rf dist
