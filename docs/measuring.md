# Measuring aigolog on your own machine

`measure` is a command in this repository that shows how fast `aigolog analyze` is, and how much memory it uses, on the machine you run it on. It prints figures; it does not judge them, except against four provisional bars that are described at the end.

## What it does

For each run it:

1. writes a generated log to a temporary folder (never anywhere else). The log comes from the same generators the tests use, so the same `-kind`, `-lines` and `-seed` give the same log on every machine;
2. runs `aigolog analyze --as-of 2026-12-31 <log>` as a child process (never inside `measure`, so that the memory and the time are the program's own);
3. takes the wall time around the start and the end of the child, the child's user and system CPU time, and its peak memory;
4. opens the database that the run made and reads the number of records and patterns from it, how many records sit in a pattern that holds only one record (seen once), and how many are in no pattern (the catch-all);
5. removes the temporary folder, unless you gave `-keep`.

Each run has a cache folder of its own inside the temporary folder, so a run never reads the database of another. The child gets a minimal environment: the cache folder, a configuration file that does not exist (so the defaults apply), and home, cache, configuration and temporary folders that are inside the temporary folder. It inherits nothing that could point it to your own cache or configuration.

Without `-bin`, `measure` builds `./cmd/aigolog` with `go build` into the temporary folder, so you need the Go toolchain and you run `measure` from inside the repository. With `-bin PATH` it measures the program you give; that program must be the same version as `measure` (the database is read with the code of this repository).

## What it does not control

- **The background load of the machine.** Other programs, the operating system's own services, antivirus, indexing and updates all take time and memory that `measure` cannot stop. The report prints the load average at the start and at the end where the system gives it (Linux), and says the load was not controlled. Close what you can, plug the machine in, and read a figure as a figure of a machine in the state it was in.
- **The file cache.** The log is written just before the first run, so it is probably in the file cache for every run, and the first run can still be slower because the program, the database folder and the processor caches are not warm yet. `measure` does not drop the file cache (that needs administrator rights), so a figure says nothing about a cold-cache run.
- **Frequency scaling and heat.** A laptop on battery, or one that is hot, is slower.

## How to run it

```sh
just measure                                  # the size of the full OpenSSH log (655,146 lines), two runs
just measure -kind web -lines 100000 -runs 3  # another kind, size and number of runs
just measure-smoke                            # 20,000 lines, one run: only to see that it works
go run ./cmd/measure -json                    # the figures as one JSON document
go run ./cmd/measure -bars                    # also run a smaller size and decide the four bars
```

The flags:

| Flag | Default | Meaning |
|---|---|---|
| `-kind ssh\|restart\|web` | `ssh` | the kind of generated log |
| `-lines N` | 655146 | the number of lines of the log |
| `-seed N` | 1 | the seed; the same seed gives the same log |
| `-runs N` | 2 | how many times the log is analyzed |
| `-bin PATH` | built from `./cmd/aigolog` | the program to measure |
| `-keep` | off | keep the temporary folder and print its path |
| `-json` | off | print one JSON document instead of text |
| `-bars` | off | also run `-small-lines` lines and decide the four bars |
| `-small-lines N` | 100000 | the smaller size, used with `-bars` |

Exit codes: 0 when it measured (whether or not the bars are met), 1 when a run failed or the program could not be made (the message says which run), 2 when the command line was wrong.

## How to read each figure

- **wall time**: seconds from the start of the program to its end, as a clock on the wall would show.
- **cpu time (user and system)**: seconds of processor time the program used. It can be more than the wall time when the program uses more than one core.
- **peak memory**: the most memory the program held in RAM at any moment (on Linux the peak resident set size; on Windows the peak working set). If the system gives no figure the report says `peak memory: not measured on this system` and the JSON has `null`. A missing figure is never shown as 0.
  `measure` does not let its own memory into this figure: Linux gives a child at least the high-water mark of the process that started it, so `measure` frees the log before each run and resets that mark; if the reset cannot be done or checked, the peak is `not measured`. A program whose true peak is below the few MiB `measure` itself holds shows that size.
- **log size**: bytes of the generated log.
- **database size**: bytes of the `.db` file the run made.
- **records**: how many records the database holds.
- **patterns**: how many patterns the miner made (the catch-all is not one).
- **seen once**: records in patterns that hold exactly one record. A high share means the log is mostly different lines.
- **catch-all**: records that match no pattern (this happens only past the cap on the number of patterns).

With two runs or more each figure is a range, `low to high`. With one run it is one value, which is not a result: it cannot show how much the figure varies.

## How to quote a result

- Quote the range of two or more runs, never a single run.
- Quote the machine facts the report prints: operating system and architecture, Go version, number of CPUs, program version.
- Say that the background load of the machine was not controlled, and quote the load averages if the report has them.
- Say which kind, lines and seed you used.
- Do not compare figures from different machines as if they measured the same thing.

## The bars of the acceptance record (A6) and the rule

With `-bars` and `-lines` larger than `-small-lines`, `measure` also runs the log at `-small-lines` lines (same kind, same seed) and says, for each of four bars, `met`, `not met` or `not decided`, with the figures behind it:

1. all runs completed;
2. the peak memory at `-lines` is at most 1.5 times that at `-small-lines`, plus 16 MiB;
3. the wall time at `-lines` is at most 300 seconds;
4. the database is at most 6 times the size of the log.

The rule:

- A bar whose figure is not measured is `not decided`, never `met`. Bar 2 is also `not decided` when there is no second size.
- Each bar is decided on the worst of the runs of its size: the slowest run, the highest peak, the largest database.
- The exit code stays 0 whether the bars are met or not. They are the owner's provisional bars and a slower machine may miss them; a missed bar is a figure to look at, not a failure.
- The layout stands if the four bars are all `met`. When one fails, the layout and miner choices go back to the owner with the figures, and nothing else is built on them.
- If a run fails, no later run is tried, bar 1 is `not met` and the exit code is 1. Each other bar is decided from the runs of its size that finished, when at least one run of that size finished (so a bar can be `met` or `not met` from fewer runs than were asked for); a bar whose size has no finished run is `not decided`. Bar 2 needs a finished run at both sizes.
