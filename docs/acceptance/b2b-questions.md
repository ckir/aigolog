# B2b: the questions, stored answers and `query`

This record is written task by task. It holds, so far, the section of Task 1.

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
