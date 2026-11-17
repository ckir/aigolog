# aigolog part 1: the road from the design to the program

**Design:** `docs/superpowers/specs/2026-11-15-engine-and-report-design.md`, approved by the owner
on 2026-11-16 ("I approve"). Section and test numbers below (7.1, A1 and so on) are that
document's.

**What this file is.** The design is too large for one implementation plan, and a detailed plan
may be written only against code that exists. So part 1 is built as eight sub-projects, one after
another. Each ends in a program that runs and in named acceptance tests that pass. Only the first
has a detailed plan today. Each later one gets its plan when the one before it has been merged.
The owner accepted this cut, this order and the branching ("yes", 2026-11-16), after the second
model and I had each recommended the same. B2 was split in two on 2026-11-17, at the owner's
choice, after the second model and I had each recommended it: the line-level plan of the questions
can only be written against the tables that B2a builds.

## The sub-projects, in order

| | Sub-project | It builds | It proves | Plan |
|---|---|---|---|---|
| A | Foundation | The Go module and command; `just`, `lefthook`, `addlicense`; a workflow that builds the four targets and runs the gate on Linux and on GitHub's Windows x64 and ARM64 machines | A1 | `2026-11-16-a-foundation.md` |
| B1 | Ingest | Records and the reader plug point; the text reader (7.1 "Text lines"); the time rules (7.2); the store (7.4): one database per run, lock, complete mark, version, retention, `--no-keep`; `analyze` that stores, `list`, `clean`; the configuration file (7.13) | The time cases and the two-analyses case of A2 | after A is merged |
| B2a | Patterns and the report | The open items of `TODO.md` (first task); the store's second layout (patterns, slots, values; schema 2); the two-pass miner (7.3); the digest and the summary (7.5); the report with no model (7.10), `--summary`, `--json`; the generators of the test logs and the fetch recipe of the sshd sample; a first measurement of the layout on a generated log of 655,146 lines, made by a command that ships with the program (`just measure`) so that anyone can repeat it on their own machine | A2 for text logs (the miner's patterns and values), A7 | after B1 is merged |
| B2b | Questions | The eight questions and stored answers (7.6); `query`, `show`; A3 on the sshd sample; the first measurement of size on the full OpenSSH log | A2 for text logs (through the query tools), A3, A6 (first measurement) | after B2a is merged |
| C | The model | The OpenAI-compatible client (7.8); the tools offered to it; the bounded investigation (7.7); findings and the evidence rule (7.9); the report with findings; the privacy rules; extra request fields | A4 on text logs, A8, A10, A11 | after B2b is merged |
| D | JSON lines | The second reader, the journal's export (7.1 "JSON lines"); named fields as slots | The JSON-lines and journal cases of A2 | after C is merged |
| E | Windows event XML | The decoder for event XML, in ordinary Go, with the rewriting of forbidden characters and the `eventxml` format | The Windows cases of A2, from `testdata/windows-events/` | after D is merged |
| F | Windows live reader | The Event Log API from Go; Windows' words (a pattern per kind, a cited event's message); rights and the refusal; where a read stops; `--eventlog`, `.evtx` | A9, the Windows part of A4 | after E is merged |
| G | Acceptance and documents | The owner's own logs; the full size test; README and the community documents (section 10) | A5, A6 | after F is merged |

Why this order. Whether a model can use the summary and the tools is not known (design section
14). If it cannot, the summary and the tools change, and that is cheaper to learn before three
more readers are built on them: so C comes straight after the text engine. The unknown with the
deepest rework is whether the pure-Go SQLite store is fast enough on a large log, so B2b ends with
a first measurement on the full OpenSSH log (B2a measures the layout on a generated log of the same size first) (the second model's addition). The test suite must
pass on Windows x64 and ARM64 (the owner's ruling), so those machines run the gate from A on.

F may be split in two when its turn comes (the reader; Windows' words). That is decided in its
own plan.

## Rules for every sub-project

- **Branches.** The design branch is merged into `main` first, through a pull request. Each
  sub-project then has its own branch off `main` and its own pull request. Nothing is committed to
  `main` directly.
- **Commits and pushes** happen only when the owner says so.
- **The gate** is `just check`, on the development machine and on the three hosted machines.
- **Before a sub-project is called complete:** the second model reviews the committed code until
  a round is clean, then audits the tests. Both are the owner's standing rules.
- **A sub-project's plan is written only after the one before it is merged**, against the code
  that then exists.

## Decisions that hold for all of part 1

The design leaves these to the builder (its section 13). They are fixed here so that every
sub-project builds the same program. Each is a decision of mine; the plan that first uses one
repeats it.

| # | Decision | Reason |
|---|---|---|
| 1 | Module path `github.com/ckir/aigolog` | The repository's address |
| 2 | `go 1.26.0` in `go.mod`; builds and the workflow use Go 1.27.4 | `modernc.org/sqlite` v1.62.0 requires Go 1.26 (its `go.mod`); 1.27.4 is the version on the development machine |
| 3 | Two third-party modules only: `modernc.org/sqlite` v1.62.0 and `golang.org/x/sys`. Everything else is Go's standard library: `flag` for the command line, `net/http` for the model, `encoding/json` and `encoding/xml` for input, JSON for the configuration file | Every further module is one more thing that must build for four targets; A1 is run again before a third is added |
| 4 | Exit codes: 0 done; 1 failed; 2 wrong usage; 3 a model was configured and failed; 4 Windows refused a log; 5 the store cannot be written | The design's table 7.12 asks for three distinct codes besides success and failure |
| 5 | Packages: `cmd/aigolog`; under `internal/`: `cli`, `exitcode`, `buildinfo`, `record`, `reader/text`, `reader/jsonl`, `reader/eventxml`, `timeparse`, `store`, `miner`, `query`, `summary`, `report`, `config`, `model`, `investigate`, `evidence`, `winlog`, `textsafe`, `repocheck` | One responsibility each; a later plan may rename one of its own, and says so |
| 6 | Text files are checked out with LF endings on every system (`.gitattributes`) | `gofmt` reports every file of a CRLF checkout as unformatted |
| 7 | The version is one string, `internal/buildinfo.Version`, set at build time | The store records the version that wrote a database (7.4) |

## Values the design leaves to measurement

None of these gets a silent number. Each is given a named provisional value in the plan that
first needs it, and the task that measures it replaces the value.

| Value | First needed in | Measured by |
|---|---|---|
| The summary's size budget (7.5) | B2a | C, on the first runs with a model |
| The size cap and the time limit of an answer (7.6) | B2b | B2b's size measurement |
| The total size limit of kept databases (7.4) | B1 | B2a's measurement and B2b's size measurement (the design: "set after A6"). B2a measured 215.6 MiB for a 65.4 MiB log of 655,146 lines (3.3 times), see `docs/acceptance/b2a-patterns.md` |
| The three limits of the investigation: tool calls, time, bytes sent (7.7) | C | C's first runs ("set after the first measurement") |
| The cap on the number of patterns (7.3) | B2a | B2a, on the test logs. Measured 2026-11-17 (`docs/acceptance/b2a-patterns.md`): 6 patterns on a generated ssh log of 655,146 lines, 24 on the 2,000-line sshd sample; the cap stays 1,000, a ceiling these logs do not reach |
| The default bound of a live Windows read is not one of these: the design fixes seven days (7.1) | F | |

## Still open with the owner

These do not block A. Each is needed by the sub-project named.

- A key for a cloud model endpoint (C, for A4; without one the local model must pass).
- Whether an aggregator's fallback to another provider is forced off by default (C; the second
  model's dissent, design section 12 item 9).
- Whether anything added during review is cut from part 1 (the table at the end of design section
  12). As approved, nothing is cut.
- The three choices in the community documents (G; design section 12 item 22).
