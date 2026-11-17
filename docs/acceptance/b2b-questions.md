# B2b: the questions, stored answers, `query` and `show`

This record is written task by task. It holds the sections of Task 1 (the caps), Task 6 (A2 and A3) and Task 7 (A6).

## What B2b built

In the order it was done:

1. **Task 1, the caps.** `Scan` takes at most 8 typed values from a token and 64 from a message, so that a pattern has at most 320 slots, and the number-dense generator `testlog.Dense` shows the effect on memory and on the size of the database (the section below).
2. **Task 2, layout 3 and the write of an answer.** The database gains the table of stored answers (layout 3), and `SaveAnswer` writes one under the lock of `acquire`, so that no sweep and no other writer meets it; SQLite's own locking is the guard against a reader that has the file open.
3. **Task 3, the model of a question and an answer.** The package `query` holds the question (eight verbs, columns of slots, filters, limits, a canonical text and a validation that names the field), the answer (columns, rows, facts, `Cut`, `Total`) and the rendering of an answer.
4. **Task 4, the eight questions and the time limit.** `DB.Run` answers `patterns`, `slots`, `count`, `top`, `pairs`, `buckets`, `rows` and `where` over the records, cuts the rows and the bytes of an answer, and gives up with `ErrTimeLimit` when a question runs out of its time.
5. **Task 5, `query` and `show`.** `aigolog query` asks a question of a kept database, prints the answer and stores it in the database as `A1`, `A2`, ...; `aigolog show` prints a kept database again (the run, the digest and the summary) with every stored answer, and `aigolog query --db P --answer A1` re-runs one of them.
6. **Task 6, A2 and A3 through the command.** The sshd sample and the generated logs are carried through `analyze` and `query` to prove the acceptance tests A2 and A3 (the section below).
7. **Task 7, the fetch recipe, `measure -log` and the measurement on the full log.** `just fetch-openssh` fetches the full OpenSSH log, `measure -log` measures a log of the user's own, and the first measurement of A6 is taken over it (the section below).
8. **Task 8, the record.** This file, the tracker and the roadmap.

## The caps on the typed values of a token and of a message (Task 1)

B2a left the miner's memory and the size of the database unbounded in practice for one shape of log: lines of 256 tokens that hold 30 to 42 integers each. Every integer was a slot of the pattern and a row of `slot_values`, so a pattern could have about 9,000 slots. From Task 1 on, `Scan` takes at most `MaxTokenValues = 8` typed values from a token and at most `MaxMessageValues = 64` from a message; past either, the rest of the token (and the tokens after it) is copied as text, as a marker that does not fit already was. A pattern then has at most 64 typed slots and one Word slot for each wildcard token (320 slots in all at most). `Learn` and `Match` both call `Scan`, so they cannot disagree.

The log is `testlog.Dense(1, 300)`: 300 lines, one cluster each, 12,011,303 bytes. Each run is `aigolog analyze` as a child process, made by `go run ./cmd/measure -kind dense -lines 300 -runs 2 -json`, with `TMPDIR` on a disk (`/var/tmp`, not the RAM-backed `/tmp`). "Before" is the same program with the two constants set to `1 << 20` (the behaviour of B2a) for the measurement only; the commit holds 8 and 64.

| | before the caps | with the caps |
|---|---|---|
| peak resident memory, run 1 and run 2 | 433,060 KiB and 437,464 KiB | 89,120 KiB and 90,016 KiB |
| database, both runs | 211,894,272 bytes | 25,718,784 bytes |
| database / log | 17.6 times | 2.14 times |
| records, patterns, catch-all | 300, 300, 0 | 300, 300, 0 |
| time | not measured here | not measured here |

Machine: linux/amd64, Go go1.27.4, 8 CPUs, program `aigolog 0.1.0-dev`. The load average at the start of the runs with the caps was 3.04 3.07 2.24: the background load of the machine was not controlled, and other sessions were running. Memory and size are the figures read; the time of a run is measured by the owner's session at top level, not here, so no time is quoted.

Beside the figures of B2a (`docs/acceptance/b2a-patterns.md`: 185,680 KiB for 100 clusters and 501,924 KiB for 300 clusters, a database 20.2 times its log, measured with `/usr/bin/time` on a shape of the same kind), the peak for 300 clusters falls to about a fifth of that, and the database is far below bar 4 (6 times): 2.14 times. The two shapes are not the same log (B2a's was measured with another generator), so the "before" column here, 17.6 times and about 435,000 KiB, is the one to compare with the "with the caps" column.

One cost the caps have: a pattern shows at most 64 typed values of a long message, and later numbers stay as text in the pattern (plan, decision 7).

## A2 and A3 through the query tools (Task 6)

`TestTheSshdSampleAnswersTheQuestionsOfA3` (internal/cli/a3_test.go) carries the real 2,000-line sshd sample (LogHub, fetched, never committed) through `aigolog analyze` and asks the questions with `aigolog query`, from a file and from standard input. Every figure of design A3 came out on the first run: 520 failed-password lines; 23 addresses; 63 user names; the one accepted login at line 956; for 183.62.140.253, 286 attempts on 10 names (276 on `root`), first and last at lines 1,024 and 1,997; for 187.141.143.180, 80 attempts on 28 names. The columns are found through the command, not by number: `addr` is the `ip4` slots of the three patterns that hold `Failed password` and `names` the word slots that hold the user name in two of them (the third, `message repeated <int> times: [ Failed password for root ...]`, has the name in its text). `TestGeneratedLogsGiveTheirTruthThroughTheQueryTools` checks the truth of the generated ssh (3,000 lines), web (3,000) and restart logs (300 lines, seeds 1 and 2, so that the unit gives up in one of them) the same way.

## The first measurement on the full OpenSSH log (A6, Task 7)

The log is LogHub's OpenSSH log, fetched by `just fetch-openssh-full` from Zenodo record 8196385 (`SSH.tar.gz`, SHA-256 `296610a3...2712`, MD5 `a44e40b4348697dabf2bba56885e0a38` as the record states; the member `SSH.log` has the SHA-256 `89eee9e3...aa2f3`). It has 73,417,506 bytes, 655,146 newlines and a last line with no newline, so `aigolog` reads **655,147 records**: the design's 655,146 counts newlines (`wc -l`).

Command: `just measure-full -runs 2` (that is `go run ./cmd/measure -log testdata/fetched/SSH_full.log -runs 2`), with the temporary folder on a disk-backed folder. Machine: linux/amd64, Go go1.27.4, 8 CPUs, program `aigolog 0.1.0-dev`. **Conditions, stated as they were:** the machine is shared with a test run of another project of the owner (a vitest run and a Postgres test database), which this record could not stop. The measurement waited for the machine to calm (the one-minute load average fell below 1.5 after 380 s) and started at load 1.37 (the command printed 1.90 3.13 3.85); it ended at 2.87 2.94 3.67. So the machine was quiet at the start and not controlled during the runs. Antivirus, indexing and updates were not looked at.

| Figure (range of 2 runs) | the full log, 655,147 records |
|---|---|
| wall time | 69.35 s to 72.44 s |
| CPU time (user and system) | 107.44 s to 112.27 s |
| peak memory | 25.8 MiB to 28.4 MiB |
| log size | 70.0 MiB |
| database size | 234.7 MiB (3.35 times the log) |
| patterns (cap 1,000) | 47 |
| records in patterns seen once | 5 |
| records in the catch-all | 0 |

A third run, `aigolog analyze --as-of 2026-12-31` under `/usr/bin/time -v` (kept database, one run, load 2.87 to 4.03): wall 1:15.59, user 95.38 s, system 19.31 s, peak 31,304 KiB. The peak memory is the same size as for the generated log of B2a (31.8 to 38.7 MiB at 655,146 lines; 26.6 to 41.4 MiB at 100,000), so it does not grow with the input; the database is 3.35 times its log against the bar of 6 times. The figures are for this machine and are a ceiling, not a figure to compare with another machine.

### The questions over the whole log

Asked with `aigolog query` on the kept database (each answer stored; the time is the one the answer prints, `answered in N ms`; **the load during these was 5 to 6 from the other project**, so they are contended). The failed-password patterns are 10, 11, 6 and 41 (197,587 records); `addr` is their four `ip4` slots, `names` their three word slots.

| Question | rows covered | cut | time |
|---|---|---|---|
| 3 `count` of the four patterns | 197,587 | no | 680 ms |
| 4 `top --by addr` (1,008 addresses) | 197,587 | rows | 2,608 ms |
| 4 `top --by names` (1,745 names) | 197,587 | rows | 2,195 ms |
| **5 `pairs --by addr --with names`** (the names tried per address) | 197,587 | rows | **3,468 ms** |
| 6 `buckets` (24) | 197,587 | no | 1,068 ms |
| 7 `rows --last --limit 10` | 655,147 | rows | 547 ms |
| 8 `where 59.63.188.30` (the busiest address; in 3 slots) | | no | 142 ms |
| `top --by names --where addr=59.63.188.30` | 28,766 | no | 3,859 ms |
| `count --where addr=59.63.188.30` | 28,766 | no | 944 ms |

Question 5 answered over the whole log in 3.5 s, within the provisional limit of 5 s, but with a margin of 1.4 times on a contended machine: a slower machine will need `--timeout`. The limits (100 rows, 64 KiB, 5 s) were **not changed**; whether to raise the time limit (about 15 s would be four times the slowest measured answer) is the owner's decision.

### Spot-check against standard tools

| Figure | `aigolog query` | standard tools |
|---|---|---|
| lines with `Failed password` | 197,587 (the four patterns) | `grep -c 'Failed password'`: 197,587 |
| distinct addresses in them | 1,008 | `grep -oE 'from [0-9.]+ port' \| sort -u \| wc -l`: 1,008 |
| distinct user names | 1,745 | `sed -nE 's/.*Failed password for (invalid user )?([^ ]+) from ...' \| sort -u \| wc -l`: 1,745 |
| lines on `root` | 176,784 | `grep -cE 'Failed password for root from'`: 176,784 |
| busiest address 59.63.188.30: lines | 28,766 | `grep ... from 59\.63\.188\.30 port`: 28,766 |
| its first and last line | 491,470 and 638,997 | `grep -n`: 491,470 and 638,997 |
| its distinct names | 1 (`root`) | 1 |
| `Accepted ` lines | 182 (one pattern) | `grep -c 'Accepted '`: 182 |
| records | 655,147 | 655,146 newlines plus one last line with no newline |

All equal. (The four records of pattern 41, `Failed password for invalid user from <ip4>`, have no name; they are in the 197,587 and in the 1,008 addresses and not in the names.)

### Findings the real log showed (for the owner)

1. **The time of the log is placed in a span of two years.** The summary says `time: 2024-12-10 06:55:46 to 2026-01-07 17:22:01; as written, no zone stated`, and its time buckets are almost all empty, for a log that holds only the days Dec 10 to 12 and Jan 1 to 7. The stamps have no year, and `--as-of 2026-12-31` was given. The rule of B1 (`internal/timeparse/years.go`) gives each record the latest year that does not put it after the next record plus one day. This log has 8 steps backwards in its stamps; the one at line 421,064 goes back by one day and three minutes (Jan 2 22:41:30, then Jan 1 22:38:31), which is more than the day of slack, so every record before it is moved one year back, and the December days land in 2024. The log is not wrong; the rule has no room for a disorder of more than a day. This is B1 behaviour exposed by the first real long log; it does not touch any figure above. It is recorded in `TODO.md`.
2. **655,146 or 655,147 lines.** The log has a last line with no newline; the figure of the design counts newlines (see above).
3. **The margin of question 5** is 1.4 times (above).

### What these runs do not show

A Windows or ARM64 measurement of the full log; a cold file cache; an idle machine (the load was 1.4 at the start and 2.9 to 6 later, from another project); the memory of `aigolog query` (not measured); the cost of the three statements that `top` and `pairs` run over the same record set (it is why `top` takes 2 to 4 s; a later change may share them).

`SaveAnswer` (the write of an answer: `fileURI` with `mode=rw`, the lock of `acquire`, SQLite's locking with a reader open) and `query` and `show` are proven on Linux only, until CI runs them on the hosted Windows and ARM64 machines; a UNC cache folder is unproven for them, as it is for `readOnlyURI`. The CI run ids are added after the push.
