# B1: ingest

Date: 2026-11-16. Commit: `c63a2a0`. Workflow run: [43877617990](https://github.com/ckir/aigolog/actions/runs/43877617990) (the `check` workflow), on pull request 5. All three jobs succeeded.

The first run of this branch, on `d6e273e`, was [43876928984](https://github.com/ckir/aigolog/actions/runs/43876928984): ubuntu-24.04 passed, windows-2025 and windows-11-arm failed on one test, `TestCreateNamesTheFolderNotAPlaceholderPath`. The refusal message prints the folder with `%q`, which doubles backslashes on Windows, and the test looked for the plain path. The message was right and the test was wrong; commit `c63a2a0` changed the test to compare against the message's own quoting. Every other package passed on both Windows machines in that first run too.

What the three machines ran: `just check`, which runs every test of this sub-project, among them the five time cases of A2 (`TestA2...` in `internal/cli/a2_test.go`) and the two-analyses case, and the case of a cleaner that runs beside a live analysis.

| Machine | `just check` | Lock, removal and store tests (`internal/store`) | A2 cases and commands (`internal/cli`) |
|---|---|---|---|
| ubuntu-24.04 | passed | `ok`, 0.314 s | `ok`, 0.969 s |
| windows-2025 | passed | `ok`, 0.752 s | `ok`, 1.261 s |
| windows-11-arm | passed | `ok`, 0.718 s | `ok`, 1.032 s |

The same run also passed `internal/ingest` (3.4 s, 5.1 s and 5.0 s, the memory tests included), `internal/reader/text`, `internal/timeparse` and `internal/config` on all three machines.

This was the first time the file lock (`LockFileEx` on Windows), the removal of files, the retention sweep and the store tests ran on Windows. They passed.

Not shown by these runs, and not claimed:

- Ctrl-C on a real Windows console. The tests cancel the run through a context; the real SIGINT and SIGTERM handling was tried by hand on Linux only.
- Interference from antivirus or indexing programs that hold a `.db` or `.lock` file open. By design the program then leaves the file for the next sweep and says so.
- Network file systems, for the lock.
- A log of more than a few hundred megabytes on Windows. On Linux, 420 MB of input stayed under 45 MB of resident memory.

Measured while writing the plan (this machine, load not controlled, two runs each): 1,000,000 rows inserted in 5.3 to 6.6 s and rewritten in reverse in 6.0 to 7.3 s, the same in each of three journal settings. The speed on a large log is B2's first measurement (roadmap).
