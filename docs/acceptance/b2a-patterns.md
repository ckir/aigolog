# B2a: patterns and the report with no model

Date: 2026-11-17. Measured on commit `89e6554` (the last commit of the branch before this record). Workflow run: not yet; the branch is not pushed. The run id, the figures of the three hosted machines and of the arm64 Linux one are added after the owner pushes (Task 12b, step 3).

What B2a built, in the order it was done: the open items of `TODO.md` (Task 1); the length of a record's time stamp (2); the typed scanner and the Drain-style clustering of the miner, with a cap (3, 4); layout 2 of the store with its header marks, read before the sweep decides (5); two passes inside `ingest.Run`, patterns and values on disk, indexes made after the load (6); the generators of the test logs with their truth (10); the queries (7), the summary and the digest (8), the report with `--summary` and `--json` (9); the fetch recipe of the sshd sample and its figures (11); and `cmd/measure`, a command that anyone can run to see the speed and memory of a run on their own machine (12a). With no model, `analyze` prints the source lines, the digest, the full summary and the database line (design 7.12, A7).

## The design's figures, through the miner, on the real sample

`TestTheSshdSampleKeepsTheFiguresOfTheDesignThroughTheMiner` reads the 2,000-line sshd sample of LogHub (pinned revision, SHA-256 checked, not committed), mines it, and checks with SQL written in the test, through a file and through standard input: 2,000 records; 520 `Failed password` records; 23 distinct addresses in their `ip4` slots; one `Accepted ` record, at line 956; 286 rows for `183.62.140.253` (first line 1,024, last line 1,997); 80 rows for `187.141.143.180`. All of them came out on the first run, on this machine. The names (63 in all, 10 and 28 for the two addresses, 276 on `root`) need combined slots and are the first work of B2b.

## The first measurement of size and speed

Command: `just measure -bars -kind ssh -lines 655146 -runs 2` (which also runs 100,000 lines), on this machine, with the temporary folder on a disk-backed folder (`TMPDIR`), not on the RAM-backed `/tmp`. 655,146 is the number of lines of the full OpenSSH log; the log here is the generator's ssh-like log of that size (seed 1), not the LogHub log, which is B2b's work (A6). The decision rule was fixed before the run (Task 12b, step 1): the layout stands if the four bars are all `met`.

| Figure (range of 2 runs) | 655,146 lines | 100,000 lines |
|---|---|---|
| wall time | 101.72 s to 107.84 s | 15.00 s to 16.69 s |
| CPU time (user and system) | 157.77 s to 162.65 s | 24.63 s to 25.51 s |
| peak memory | 31.8 MiB to 38.7 MiB | 26.6 MiB to 41.4 MiB |
| log size | 65.4 MiB | 10.0 MiB |
| database size | 215.6 MiB | 32.5 MiB |
| records | 655,146 | 100,000 |
| patterns (cap 1,000) | 6 | 6 |
| records in patterns seen once | 1 | 1 |
| records in the catch-all | 0 | 0 |

The four bars, as `cmd/measure` printed them:

1. all runs completed: met (4 of 4 runs);
2. the peak memory at 655,146 lines is at most 1.5 times that at 100,000 lines plus 16 MiB: met (38.7 MiB against 41.4 MiB, limit 78.1 MiB);
3. the wall time at 655,146 lines is at most 300 s: met (107.84 s for the slowest run);
4. the database is at most 6 times the size of the log: met (215.6 MiB for 65.4 MiB, 3.30 times).

So the layout stands: plain tables, the indexes made after the load. The peak memory did not grow with the input (the two sizes overlap within the spread).

What was not controlled: the machine was not idle. The load average was 5.39, 6.18, 5.41 at the start and 5.91, 5.96, 5.48 at the end, on 8 CPUs, from other programs of the owner's session that this record could not stop. Contention makes a time longer, never shorter, so the "met" of bar 3 is the conservative reading, and a run on an idle machine will be faster; the memory and size figures do not depend on the load. The log is written just before the first run, so it is probably in the file cache for every run; the command does not drop the cache. Antivirus, indexing and updates were not looked at. The probe of the load was shown to see a busy process before the run (a loop of known load read 100 %). The speed is therefore a ceiling for this machine, not a figure to compare with another machine: at 655,146 lines about 155 to 165 microseconds of wall time and 241 to 248 microseconds of CPU time for each line.

Peak memory on Linux is read from the child's resource usage. A first version would have been wrong: a child started by the Go runtime carries the high-water mark of its parent (measured: a parent that held and freed 300 MiB makes `/bin/true` read 310,224 KiB; after the parent resets its own mark, 3,296 KiB). `cmd/measure` frees the log, resets the mark and checks it, and says `not measured` when it cannot, so the figures above are the program's own.

## Patterns the miner made

- On the generated ssh log of 655,146 lines: 6 patterns, 1 record in a pattern seen once, 0 in the catch-all. The cap of 1,000 is not approached.
- On the sshd sample of 2,000 lines: 24 patterns for the 2,000 records, 4 records in patterns seen once, 0 in the catch-all. The largest groups are 414 `Received disconnect from <ip4>: <int>: ...` lines, 384 `pam_unix` authentication failures, 383 `Failed password for <*> from <ip4> ...` and 135 `Failed password for invalid user <*> ...`.

The cap is therefore a ceiling that these logs do not reach. It stays at 1,000, a named constant; a log made of almost unique lines reaches it, and then the rest go to the catch-all, which the summary reports (tested with a cap of 5, and with 300 shapes in a budget of 4,096 bytes).

## Known properties of the method (what a reader of a report should know)

- The first four tokens after the length are the path of the tree. A variable prefix longer than the fixed one (a changing worker name before the program name, for example) can merge event types that differ only after the fourth token. Tasks 3 and 4 pin this with tests.
- A digit-free word in the first four tokens makes a pattern of its own for each value, up to 100 for a node and then one more for the rest. In a log that starts with a client address, a quoted method and a path (a web access log) the path is the third token, so a log whose paths hold no digit splits into one pattern for each path; the generator of the web log therefore makes paths that hold a digit (the miner then gives one pattern, with the path in a slot). The layout for logs that start with a client address is the owner's open question, decided with A5 (the owner's ruling of 2026-11-17: later, with A5).
- A name that holds a digit is a slot at any position; a name without one at the fourth token is a different pattern for each name (the test of Task 4 uses names with a digit for this reason).
- A literal marker such as `<int>` in a log is counted by its values, not by its text, so it cannot make a pattern with a wrong number of slots.

## What the runs do not show, and are not claimed

- Windows: the peak memory read through `psapi.dll` (`peak_windows.go` is compiled by `vet-cross` and was reviewed against the Win32 documentation, never run); the read-only URI form of a Windows volume path in `openReadOnly` (the backslashes are percent-encoded; if a hosted Windows job fails on it, the plan's rule is to stop and fall back to the plain path); a Windows UNC cache folder, which that form refuses; the tests of `internal/winlog`, `parts_windows.go` and the process tests on Windows; the `ubuntu-24.04-arm` runner and its `just` download; the `floor` job that builds with Go 1.26.0; the fetch script under the `sh` of Git for Windows (it was run under `dash` and `busybox sh`). All of these are proven or disproven by the first CI run.
- An idle machine: see above. The owner can repeat the measurement on theirs with `just measure` (`docs/measuring.md`).
- The full OpenSSH log from Zenodo (A6) and the questions over it: B2b.
- A database cut by less than one page reads as complete (SQLite reports no error); a cut of a page or more is kept as unreadable and is removed by `aigolog clean` only.
- The JSON of format 1 repeats the sources in the summary block and in `sources`; it is a machine document and is not under the text budget.
- The summary was built with a budget of 16,384 bytes; the figure is provisional (C measures it).

## How to repeat the measurement

`just measure` (655,146 lines, 2 runs), `just measure -kind web -lines 100000 -runs 3`, `just measure -bars`, or `go run ./cmd/measure -json`; the smoke form `just measure-smoke` runs on every machine of the workflow. `docs/measuring.md` says what each figure is and how to quote it: the range of two or more runs, the machine facts, and the sentence that the load was not controlled.
