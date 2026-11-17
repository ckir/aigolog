# Open items

Tracked debt found by reviews and test audits of sub-projects A and B1. Each line is a fact, with the file it concerns.
Nothing here blocks a merged sub-project. Entries dated 2026-11-16.

## Test gaps (deferred by the owner's scoping)

- [x] The `version` command's engine-failure branch (error from `store.EngineVersion`, message on stderr, exit 1) has no test. `internal/cli/env.go` closed by part 1b
- [x] `store.EngineVersion` has no cancelled-context test and returns driver errors unwrapped. `internal/store/engine.go` closed by part 1b
- [x] The full usage text is not pinned; `cli_test.go` checks only the fragments `Usage:` and `Commands:`. `internal/cli/cli_test.go` closed by part 1b
- [x] `winlog.Available`'s first-missing-procedure error path has no test (the package variables `procedures` and `wevtapi` cannot be swapped). `internal/winlog/api_windows.go` closed by part 1f (the loop is `available`, with the finder passed in; its tests run on the Windows machines of CI)
- [ ] The `eventlog api missing` branch of `platformParts` has no test (it calls `winlog.Available`, which cannot be made to fail). `internal/cli/parts_windows.go`
- [x] No test sends a real SIGINT or SIGTERM to a running `aigolog`; the cli tests only cancel a context. The second-signal kill and `signal.Ignored` were checked by hand on Linux only. `internal/cli/analyze.go` closed by part 1e
- [x] `Create`'s `O_EXCL`-collision and `os.SameFile` retry branches, the re-take path in the size phase (an entry that became running) and `Info.Size` staleness have no test (they need a seam). `internal/store/db.go`, `internal/store/retention.go` closed by part 1d
- [x] The Windows fallback of `removeOrphanLock` (delete fails while the process holds the handle, close, retry once) is tested nowhere; the only test skips on Windows. `internal/store/retention.go` closed by part 1d
- [x] The A2 concurrency tests run goroutines in one process, so flock (per open file description) and `LockFileEx` (per handle) are not exercised by two real processes. `internal/cli/a2_test.go` closed by part 1e
- [x] Reader edge tests missing: a lone CR at the very end of the input (currently dropped), a partial line when the underlying reader fails mid-line, a sample of exactly 64 KiB, and `textsafe` truncated sequences and U+FFFD. `internal/reader/text/text_test.go`, `internal/textsafe/textsafe_test.go` closed by part 1c
- [x] Unpinned: `ParseAsOf` leniency (one-digit hour, fractional seconds, wording for a nonexistent date) and `Resolve` with a negative or over-long `nanoOfDay`. `internal/timeparse/years_test.go` closed by part 1c
- [x] Unpinned: a `signalsToCatch` test that asserts nothing about the real `signal.Ignored`. `internal/cli` closed by part 1e
- [x] The memory tests measure the Go heap, not resident size. `internal/ingest/ingest_test.go` closed by part 1e

## Defects and gaps in the gate

- [x] `just fmt-check` exits 0 for a Go file that does not parse (measured: `gofmt -l` prints the parse error to stderr and nothing to stdout, so `test -z` passes); only `go vet` later in `just check` catches it. `justfile` closed by part 1a
- [x] `cli.Run` ignores write errors from `Fprint*`, so help and version return OK when stdout is closed. `internal/cli/env.go` closed by part 1b
- [x] The header test walks `internal/**/testdata` while `license_args` ignores only the top-level `testdata/**`; a future fixture `.go` file there fails the test but not `license-check`. `internal/repocheck/header_test.go`, `justfile` closed by part 1a
- [ ] `Open` never reads the `schema` row or validates the `meta` rows it created. B2 should read the version and schema before `Sweep` classifies a database (see the B2 inputs in the roadmap). `internal/store/db.go`, `internal/store/retention.go`

## Costs of B1 that had no home yet (found by its reviews; added 2026-11-17)

- [x] Exact stamps are shown in the summary converted to UTC (UTC half: CLOSED-BY-RULING: the summary line says UTC), and the layouts with a zone written without a colon and joined to the time (`2026-11-16T08:15:22+0200`, `2026-11-16 08:15:22+0200`) are not known (layouts half: `iso-t-offset-plain` and `iso-space-offset-plain` added). `internal/cli/analyze.go` (`summaryTime`), `internal/timeparse/layouts.go` closed by part 1c
- [x] `timeparse.WallNanos` is exported and does no range check: a modification time outside 1677 to 2262 wraps. `internal/timeparse/layouts.go`, called from `internal/ingest/ingest.go` closed by part 1c
- [ ] A truncated finished database is judged "not ours" and removed by the next sweep without a word. `internal/store/retention.go` (part 1e added the note; Task 5 closes it)
- [ ] `List` can recreate a path that a cleaner deleted between its stat and its open, as an empty file with mode 0644. `internal/store/db.go` (`openReadOnly`)
- [x] On Windows a `--no-keep` run whose `.lock` file cannot be deleted removes the database and then prints the "kept" form of the summary. `internal/cli/analyze.go` closed by part 1e
- [x] The text reader finds a time stamp only at the start of a line, so a log whose lines start with something else gets no times: an Apache or nginx access log starts with the client address (measured 2026-11-17: `203.0.113.7 - - [16/Nov/2026:08:15:22 +0000] ...` is read by no layout, and `Detect` returns none). `internal/timeparse/layouts.go`, `internal/reader/text/text.go` CLOSED-BY-RULING: the design's 7.2 reads the stamp "at or near the start of a line" and says that with no layout the analysis runs without time and says so; the item stays described here with its measured example, and the owner decides when a layout that skips a leading address is built

## CI

- [ ] CI never runs the cross-built `dist/` Windows binaries (`just build` runs on ubuntu only; the Windows runners use `go run`), never runs linux/arm64 natively, and never builds Go 1.26.0 (the `go.mod` floor; the workflow pins 1.27.4). The "Run the program" step asserts the exit status only. `.github/workflows/check.yml` (run on the first push; tick when the jobs are green: run id ...)
