# aigolog part 1: the engine and the one-shot report

Status: draft under revision. After the review rounds the owner moved the Event Log reader into
part 1, allowed C code, and chose how the Event Log is read. The reader's details (section 7.1)
wait for real events from the owner's Windows machine, asked for in
`docs/handoff/windows-session.md`. The revisions made since the review rounds have not been
reviewed.
Date: 2026-11-15.

This document describes the first of two parts of aigolog. It is written so that a builder can
plan from it without going back to the owner. Where a choice was the owner's, it says so and quotes
them. Where a choice is mine, it is marked **(my call)** and listed again in section 12.

## 1. Why this exists

The owner's words:

> superseed the functionality of similar products by using ai technologgy. Each log has something to
> reveal e.g. ssh logs can reveal if a box is under attack by bots. This is only an example. Other logs
> reveal other things

> check the failing service restarts to add to the design

> Add windows support to design

> System must have the ability to manage more log types

What this means for the product: existing log tools store lines and wait for a person to ask the
right question. aigolog reads a log and says what it reveals, without being asked, and shows the
numbers and the log lines behind every statement so that a person can check it.

Two examples are required to work: a machine under automated ssh password guessing, and a service
that keeps failing and being restarted. They are examples. The same program must handle other kinds
of log without code written for each kind.

## 2. The two parts

The owner chose "3 both, report first": one engine with two faces, a one-shot report and a
watcher with a dashboard, the report built first.

1. **The engine and the one-shot report** (this document). Text logs, JSON-lines logs and the
   Windows Event Log. Runs on Linux and Windows.
2. **The watcher and the dashboard.** Keeps reading logs as they grow and shows current findings on
   a local web page.

Part 2 gets its own design. Section 11 lists what part 1 must leave room for.

The Event Log reader was first planned as a part of its own, after this one. The owner moved it:
"Move the Event Log reader into part 1". So the Windows forms of both examples, failed logons and
services that stop unexpectedly, belong to the first release.

What each of the owner's statements gets from part 1:

| Owner's statement | From part 1 |
|---|---|
| ssh logs reveal an attack by bots | On Linux: the sshd text log, or the journal's export piped in. On Windows: failed logons in the Security log. Reading any of these usually needs elevated rights |
| failing service restarts | On Linux: the journal's export piped in (`journalctl -o json`), or a syslog text file where the machine keeps one. On Windows: the System log. Reading the system journal needs elevated rights |
| Windows support | The program runs on Windows and reads text logs, JSON-lines logs and the Event Log |
| more log types | Any log of text lines or JSON lines with no new code; a plug point for readers, of which the Event Log reader is the first that is neither |

## 3. What part 1 does

One program, `aigolog`, with one main command:

```
aigolog analyze <file> [<file> ...]      # or "-" to read standard input
```

It reads the input to its end, keeps every line, and prints a report with two layers:

- **The digest.** What the log contains, worked out by plain code: the largest groups of lines and
  their counts. It is always printed, with or without a model.
- **The findings.** What the log reveals, written by a model. Each finding has the model's words,
  a list of facts whose values the program fills in from stored query answers, the lines that show
  it, and a way to re-run the queries.

This is what a finding for a real sshd log should look like. The figures are real, measured on the
public sample named in section 9:

```
1. Automated password guessing over ssh                                   [high]
   Many addresses are trying many user names, and almost none succeed.
     failed passwords                 520
     source addresses                  23
     user names tried                  63
     successful logins                  1   line 956
     busiest address       183.62.140.253   attempts 286, names 10, lines 1024-1997
     its attempts on root             276
     second address       187.141.143.180   attempts 80, names 28
   Lines: 6, 13, 20
   Check: aigolog query --db <path> --finding 1
```

The title and the sentence are the model's words. Every value in the table is printed by the
program from a stored answer, and where a row shows several values the words between them are the
answer's column names.

Who it is for **(my call)**: one person looking after their own machines, running the program on
the machine whose logs it reads. No accounts, no server, no other users.

## 4. Goals, non-goals, anti-goals

**Goals**

- G1. State what a log reveals without a question being asked.
- G2. Every figure in a finding is printed by the program from a stored query answer that the user
  can re-run.
- G3. A new kind of log that is plain text lines or JSON lines works with no code written for that
  kind.
- G4. One code base runs on Linux and Windows. C code is allowed (the owner: "We can use cgo too")
  and is used only where it buys something that has been measured, because every use needs a C
  compiler on each machine that builds **(my call on "only where measured")**.
- G5. Works with a local model or a cloud model, chosen in configuration (the owner chose
  "3 either, set in configuration").
- G6. Works with no model at all, by printing the digest and the summary.
- G7. A log of any size produces model requests of bounded size.

**Non-goals for part 1** (later parts or later versions)

- Watching logs as they grow; the dashboard (part 2).
- The systemd journal's binary files. The journal is reached through its export (section 7.1).
- Asking questions in plain language. Part 1 reports; it does not converse.
- OpenTelemetry input and an MCP server, both in the OPUS proposal's first version **(my call to
  defer)**.
- Alerts, several machines, accounts, redaction of personal data, search by meaning (embeddings),
  sequence models, a baseline learned across runs, storage tiers.

**Anti-goals** (the product has failed if any of these is true, even if everything else works)

- A finding shows a figure that no stored query answer contains.
- Log content leaves the machine when no cloud model or remote endpoint has been configured.
- Supporting a new kind of text log needs a parser written for it.
- Text taken from a log changes what the program or the model does (section 7.11).

## 5. Constraints

| Constraint | Source |
|---|---|
| Written in Go. C code through cgo is allowed | Owner: "We can use cgo too". This replaced an earlier rule of no C code at all |
| Targets: linux/amd64, linux/arm64, windows/amd64, windows/arm64 | Owner: "Add windows support to design" |
| The Windows Event Log is read in part 1 | Owner: "Move the Event Log reader into part 1" |
| Every line is kept, with its values | Measured need, section 6 |
| Local or cloud model, set in configuration | Owner |
| No per-kind code; a plug point for kinds that are neither text lines nor JSON lines | Owner: "more log types" |
| Developer tooling: `just`, `lefthook`, `addlicense` | Owner, section 10 |
| Licence: PolyForm Noncommercial 1.0.0 (`LICENSE.md`) | Existing repository |

## 6. Approaches considered

### 6.1 Which of the four proposals

The repository holds four proposals in `ideas/`, all answering one generic prompt
(`ideas/prompts.txt`). They share one core: break each line into a pattern plus values, store the
values, and keep the language model off the per-line path. They were scored on the two tests that
measurement made decisive.

| Proposal | Keeps every line's values | Builds for Windows without C | Defines a first version |
|---|---|---|---|
| ARGON | No, folds repetitive lines into counters (`AGY-ARGON.md:36`) | Unknown, names no engine | No |
| GEMINI | No, samples repetitive lines (`AGY-GEMINI.md:29`) | No with DuckDB, one of its two named engines (`:37`) | No |
| HYDROGEN | No, collapses healthy lines (`AGY-HYDROGEN.md:30`) | No, embeds DuckDB and Tantivy (`:64`) | No |
| OPUS | Yes (`AGY-OPUS.md:18`) | Yes, if its open storage choice (`:124`) avoids DuckDB | Yes (`:105-115`) |

**Picked: OPUS as the base, with changes.** The owner was told "unless you object, OPUS with these
changes is the base" and went on to pick the first version without objecting. Bias to weigh: OPUS
carries the name of the model family that wrote this document. The third column mattered while
the design allowed no C code. The owner has since allowed it, so that column no longer rules a
proposal out. The second column still does, and OPUS is the only proposal that passes it.

Why keeping every line decided it: on the sshd sample, one address made 286 of the 520 failed
attempts, 276 of them on root, while another tried 28 user names in 80 attempts. Those facts exist
only in the per-line values.

Changes to OPUS:

- **Findings without being asked.** All four proposals look for new or spiking patterns. On the sshd
  sample that reports a spike, not who or how, and none of the four counts distinct or
  most-frequent values. This design adds a summary of values per pattern, which a model reads first.
- **Plain Go by default.** C code is allowed and is used only where it has been measured to help
  (goal G4).
- **Structured records as well as text lines**, so the Windows Event Log can be added without
  flattening it to text.
- **The evidence rule** (section 7.9). OPUS (`:41`, `:61`) and HYDROGEN (`AGY-HYDROGEN.md:52`) both
  ask for claims backed by lines and queries; this design makes it a check in code.

### 6.2 A summary only, or every line kept

A second model (agy, consulted over two rounds) first proposed a smaller design: per pattern, keep
a count, an approximate count of distinct values per slot, and about ten example lines, then hand
that to a model. Tested on the sshd sample it could not say which address made the most attempts or
how many names an address tried. It could describe the restart loop. It then changed its position
to the one in this document: keep one row per line and answer by exact query.

**Picked: keep every line.** The cost is a store and a query step in the first version.

### 6.3 What the first version looks like

| Option | Tradeoff |
|---|---|
| One-shot report | Smallest; proves the revealing part; no screen |
| Watcher with a dashboard | Matches the README ("Logs analyzer dashboard"); much more to build before anything can be checked |
| Both, report first | The same engine serves both; the report makes every finding testable before a screen exists |

**Picked by the owner: both, report first.**

### 6.4 Which model reasons

| Option | Tradeoff |
|---|---|
| Local only | Nothing leaves the machine; whether a small local model reasons well enough is unknown |
| Cloud only | Expected to reason better (not measured); the summary and cited lines go to the provider |
| Either, set in configuration | Two clients to build; the tests measure what a local model can do |

**Picked by the owner: either, set in configuration.**

### 6.5 How a cloud model is reached

After reading the design the owner asked: "Consider other options for the cloud model e.g.
openrourer". OpenRouter speaks the OpenAI-compatible chat interface (section 9), which the design
already used for local models, so the question became what else part 1 builds.

| Option | Tradeoff |
|---|---|
| One generic client only | Smallest; no SDK; reaches local models, aggregators and any provider with that interface; a Claude model only through an aggregator |
| Generic client plus Anthropic's own API through its SDK (the design before this question) | Claude with no company in between; a second client and an SDK whose requirements include AWS, Google and OpenTelemetry libraries (section 9) |
| Generic client plus a hand-written client for Anthropic's own API (the second model's recommendation) | The same direct access with no SDK; the program must then follow that API's changes itself |

**Picked by the owner: "1 generic client only".**

### 6.6 How the Windows Event Log is read

The owner moved the Event Log reader into part 1, and then said "We can use cgo too".

| Option | Tradeoff |
|---|---|
| Windows' own API, called from Go, with a decoder for event XML that runs anywhere | Reads live logs and exported files; no parser of the binary format and no C code; runs only on Windows, where alone its calls can be tested |
| Exported files parsed by a Go library | Works on any system; the two libraries found were last changed in 2021 (section 9) |
| Exported files parsed by the C library libevtx | Maintained; C code, an LGPL licence, a status of "alpha" and a build that fetches other libraries (section 9) |
| The output of Windows' own tools piped in | Least code; one more step for the user every time |

A second model was asked twice, the second time in a fresh conversation without being told the
first answer or the author's, and recommended the first option both times. It also advised using C
code nowhere in part 1.

**Picked by the owner: "A Windows' own API".**

## 7. How it works

```
input  ->  reader  ->  records  ->  pattern miner  ->  store (one row per record)
                                                          |
                              digest, summary  <----------+-------  query tools
                                     |                                  ^
                                     v                                  |
                                   model  -------- asks queries --------+
                                     |
                                  findings  ->  evidence rule  ->  report
```

Everything left of the model is plain code and gives the same output for the same input. The model
never sees the whole log.

### 7.1 Readers and records

A reader turns an input into records. A record carries:

- which source it came from, and its position there (line number of its first line, byte offset,
  length)
- its time, when one was found
- its raw text, kept exactly
- optionally, named fields. A reader that supplies named fields may mark one field as the entry's
  kind (an event identifier, for example) and one as its free text (the message).

The reader is the plug point for more kinds of log. Part 1 ships three readers. The first two are
chosen by looking at the first lines of the input, with a flag to force either. Looking ahead must
not use up input, so that the choice also works on standard input. The third reads the Windows
Event Log.

**The Windows Event Log.** The owner moved this reader into part 1 after the review rounds and
chose how it reads: "A Windows' own API" (section 6.6).

- **Getting the events.** On Windows, aigolog asks Windows' own Event Log API for a live log by
  name (Security, System, Application or any other) or for an exported log file, and receives
  each event as XML. The calls are made directly from Go, the way Elastic's winlogbeat makes them
  (section 9); no C code is involved. This part exists only in the Windows build.
- **Decoding them.** A decoder turns one event's XML into one record. It is ordinary Go, runs on
  every system, and is what the tests exercise on the development machine, from events captured
  on Windows. The same decoder reads event XML from a file or from standard input, so the output
  of Windows' `wevtutil` tool can be analysed on any system. That route is also the fallback if
  the direct calls prove hard to get right.
- **What a record carries.** The time the event was created. As its kind, the provider's name
  together with the event's number. As named fields: the event's level, the computer, the log it
  came from, and the event's own values, each under its own name where the event names it and
  under its position where it does not. The whole XML as its raw text. The event's readable
  message is the record's free text when Windows can supply it; a record is complete without it.
- **Command line (my call).** `aigolog analyze --eventlog <name>` for a live log and
  `aigolog analyze <file>.evtx` for an exported one. Both work on Windows only; elsewhere they
  stop with a message that says so.
- **Rights.** Reading some logs needs rights the user may not have; the Security log is the usual
  case. When Windows refuses, aigolog says which log it could not read and why, and exits with a
  distinct code. It never shows an empty log in place of a refusal.
- **Coverage.** The report states how many events were read and the time they span, because a log
  may no longer hold its older events.

Nothing about this reader can be run on the development machine. Four things are settled only by
real events from the owner's Windows machine (section 14): whether the values of service events
are named; which events a failed logon and a failing service produce there; what a read of the
Security log without elevation returns; and how fast events arrive through the API.

**Text lines.**

- Encodings: UTF-8, and UTF-16 with a byte-order mark (some Windows programs write it). Invalid
  bytes are replaced, not fatal. Both `\n` and `\r\n` end a line. **(my call)**
- A line that does not start a new entry (it begins with white space, or the file's lines carry a
  time stamp and this one does not) is joined to the record before it, so a stack trace is one
  record. When there is no record before it, it starts one. **(my call)**
- Input that is not text (it contains NUL bytes outside UTF-16) is refused with a message.

**JSON lines (my call to include in part 1).** Each line is one JSON object. Its members become
named fields; nested objects are flattened with dotted names; arrays are kept as text. The free
text and the time are taken from fields with common names, a short list the builder owns. A line
that is not valid JSON is kept as a text record. Two reasons to include it now: JSON lines are
line-oriented text, so goal G3 would be false without it; and a second reader proves the
named-field contract in a reader that can be tested on any machine. It also gives a path to the
systemd journal without linking to its library: `journalctl -o json` piped into standard input. That export must be read as it comes
(section 9): the time is in `__REALTIME_TIMESTAMP`, a string of digits giving microseconds since
1970; the free text is in `MESSAGE`; the unit is in `UNIT` or `USER_UNIT`; and every value is a
string, so types are worked out from the values. More generally, a time held in a field may be a
count of seconds, milliseconds or microseconds since 1970.

Common to both: input is one or more files, or standard input. Several files go into one analysis,
and each finding says which source it is about **(my call)**. `analyze` reads to the end of the
input; a file is read up to the size it had when opened; input that never ends is part 2's
business.

### 7.2 Time

The reader finds the time stamp at or near the start of a line by trying a list of common layouts
on the first lines of the file, and keeps the layout that fits. Rules:

- Times are taken as written. No time zone is assumed or converted; a zone is shown only when the
  log states one.
- Syslog-style stamps carry no year. Years are worked out for each source on its own, from the end
  backwards. The anchor is the file's modification time, or the current time for standard input.
  The last record gets the latest year that does not put it after the anchor. Going back, each
  record gets the latest year that does not put it after the record that follows it. Both
  comparisons allow one day of slack, so that lines written slightly out of order, or stamped in
  another time zone, do not count as a change of year. A stamp for 29 February can only be placed
  in a leap year. This keeps a rotated file that ends in December and a current file that starts
  in January in the right order, and it holds when lines cross midnight on New Year's Eve out of
  order.
- An `--as-of` flag replaces that anchor for every source. It takes a date, meaning the end of
  that day, or a date and time. Tests always pass it, so their output does not depend on when a
  file was fetched. It is also the remedy for two cases the rule gets wrong: a file whose
  modification time is not when it was last written (a copy, a download), and an old archive piped
  through standard input. The report states the anchor it used.
- Two cases have no remedy. A gap of a year or more between two records of one source: nothing in
  the stamps shows it, so the records before the gap are placed a year too late. Lines out of
  order by more than the day of slack: the line that comes first, and everything before it, is
  placed a year too early.
- When no layout fits, the analysis still runs, leaves out everything that depends on time, and
  says so in the report.

The list of layouts is left to the builder; the test logs set the bar.

### 7.3 Patterns and slots

The miner turns each record into a pattern and its values. A pattern is the fixed text with slots
where values go; the values are kept per record.

For text:

- The same input always gives the same patterns.
- Obvious variables become typed slots: whole and decimal numbers, hexadecimal strings, IPv4 and
  IPv6 addresses, UUIDs. The builder may add types.
- A word that varies in one position across otherwise equal lines also becomes a slot. This is what
  finds the user name in `Failed password for <name> from <address> port <n> ssh2`. The four
  proposals all name Drain-style clustering for this; the builder may write it or adopt a library
  after reading the library's source.

For records with named fields:

- The pattern is identified by the kind field when the reader marked one; otherwise by the mined
  pattern of the free text when there is free text; otherwise by the set of field names.
- The named fields are slots that keep their names. Slots mined from the free text are added after
  them.

For both:

- Mining runs in two passes over the whole input. The first pass stores each record's raw text
  and position as it is read, and learns the patterns. The second pass reads the records back
  from the store and gives each its pattern and values, in the patterns' final form. Standard
  input cannot be read twice, which is why the second pass reads from the store.
- A cap on the number of patterns. Records beyond it fall into one catch-all pattern.
- The miner reports on itself: number of patterns, share of records in patterns seen once, share of
  records in the catch-all. The summary carries these so that a bad grouping is visible.

### 7.4 The store

One row per record: source, position, time, pattern, raw text, and the slot values. The store must
answer the questions in section 7.6 exactly, using queries whose values are passed as parameters,
never pasted into query text.

**(my call)** The store is SQLite through the pure-Go port `modernc.org/sqlite`, behind an
interface so that it can be replaced. Its documentation states it is free of cgo and lists Windows
targets (section 9). It stays the starting point because it needs no C compiler on any machine
that builds. Now that the owner allows C code, SQLite's own C library or DuckDB may take its place
if A6 measures the pure-Go port too slow; DuckDB's bundled Windows library is for amd64 only
(section 9).

**(my call)** The expected table layout is one table of records and one table of values, with a
row for each record and slot, where every slot of every pattern has a number. Combining slots
from several patterns is then a question over a set of slot numbers (section 7.6). The builder may
choose another layout if the acceptance tests hold. Names that come from a log, such as JSON field
names, are stored as data and referred to by number; they never become part of the text of a
query.

**(my call)** The raw text is stored, not only the byte offset, so that citations work for standard
input and after a file is rotated.

The database is a copy of the log, so it is handled as carefully as the log:

- One database per run, with a name no other run can share. Analysing the same input again makes
  a new database; nothing is reused **(my call)**. It is marked complete only when the analysis
  finishes, and `aigolog query` refuses one that is not complete. `aigolog list` shows the
  databases that are kept.
- A run holds its database for as long as it is working, and nothing removes a database that a
  live run holds. Two analyses at the same time must not disturb each other. How a run shows that
  it is alive is left to the builder; a lock held on the file is the expected way.
- It is created readable and writable by its owner only, in the user's cache directory, and the
  report prints its path and size.
- The copy belongs to whoever runs aigolog. A log that needs elevated rights to read, piped into
  aigolog run without them, leaves a copy that the ordinary user can read. The report says where
  the copy is, and `--no-keep` avoids it.
- It records the version of the program that wrote it; a different version refuses it with a clear
  message.
- **(my call)** Databases older than seven days are removed at the next run; `aigolog clean`
  removes them all; `--no-keep` removes the database when the run ends, and the report then leaves
  out the re-run commands. The kept databases also have a total size limit; when it is passed,
  the oldest are removed first. Its default is set after A6 has measured how large a database is
  next to its log.
- Memory use must not grow with the size of the input. Records go to disk as they are read.

The database also keeps the whole exchange with the model for that run: the instruction, the
summary, each tool call and its answer, and the model's replies. `aigolog show --db <path>` prints
it. This is how the user sees exactly what was sent, and how a wrong finding is debugged.

### 7.5 The digest and the summary

Both are built by queries and are the same for the same input.

**The summary** is what the model reads first:

- The sources: records, bytes, time span, share of records with a time, the miner's report on
  itself.
- The patterns: text with slots marked, count, share, first and last time.
- For the most frequent patterns, per slot: its name or position, its type, the number of distinct
  values, the most frequent values with their counts and shares, and the range when it is numeric;
  and counts over time in coarse buckets.

It has a size budget, in bytes of its rendered text, that does not depend on the size of the log.
When all patterns fit, all are listed with their counts, so that a pattern seen once (the single
successful login among 2,000 lines) is visible. When they do not fit, the most frequent are listed,
the rest are counted, and the model finds others through the query tools.

Each block of the summary is itself an answer with an identifier (section 7.6), so a finding can
point at the summary directly.

**The digest** is the head of the summary, formatted for a person: the largest groups and their
counts. It is always in the report. With no model, or when the model fails, the digest and the
summary are what the user gets (G6). When a model does run, the digest is what makes a missing
finding visible.

### 7.6 Query tools

The model asks questions through a small fixed set of tools **(my call: fixed tools, not free
SQL; small local models handle a few simple tools better, nothing arbitrary runs against the
store, and every query has a form that can be stored and re-run)**. The builder chooses names and
shapes. These questions must be answerable:

1. Which patterns contain these words?
2. What are the slot statistics of this pattern?
3. How many rows match these patterns and these slot values?
4. What are the most frequent values of a slot, under those filters?
5. For each value of one slot, how many rows and how many distinct values of another slot? (This is
   the "names tried per address" question.)
6. How do the counts run over time, in buckets?
7. Show the rows that match, in order, first or last, with their line numbers.
8. Where else does this value occur, across all patterns? (This follows one address or one service
   name through the log.)

One thing often sits in different slots of different patterns. In the sshd sample the user name
is in one position in `Failed password for <name> from ...` and in another in
`Failed password for invalid user <name> from ...`, and the figure the user wants is the count over
both. So questions 3 to 7 accept slots from several patterns and treat them as one column. The
model chooses which slots belong together, from their statistics and their most frequent values;
the store must make that combination possible. This constrains the table layout.

Every answer:

- gets an identifier and is stored with the question that produced it;
- is a small table with named columns, and carries the derived facts a finding may need, worked out
  by the program: counts, shares, the first and last time and the span between them, and the first
  and last line number;
- states how many rows it covers;
- has a size cap and a time limit, and says so when it was cut short. A question that runs out of
  time gives no partial figures;
- can be re-run. `aigolog query --db <path> --finding <number>` re-runs every answer a finding
  points at, and `--answer <identifier>` re-runs one. A value taken from a log is never placed in
  a command line that the program shows to the user.

**(my call)** The same tools are open to the user: `aigolog query` can ask any of the eight
questions with the user's own arguments, so a person can check a different idea without leaving
the program.

A model that calls tools is preferred, not required. A model that cannot call tools still gets the
summary, whose blocks are answers, and can report from those.

### 7.7 The investigation

The model is given the summary and the tools, and a short instruction: say what this log reveals,
most important first; work only from the summary and the tools; for each finding give what is
happening, who or what is involved, how much, since when, and whether it is still going on.

The instruction must not name any kind of log, program or attack. A test enforces this
(section 8, A10).

It runs in a bounded loop. Three limits, all configurable: the number of tool calls, the time, and
the total size of what has been sent. Their default values are set after the first measurement, not
guessed here. A failed request is retried a small fixed number of times and then counts as failing.

It returns a list of findings in one format for every model. A finding has:

- a title and a statement, in the model's words, containing no figures;
- an importance: high, medium or low;
- facts: each is the model's label plus a pointer to one row of one answer and one or more of its
  columns (for example the address, attempts and names columns of the first row of answer 7). The
  program prints each value, with the column's name where there is more than one;
- lines: rows taken from answers the finding points at.

### 7.8 Models

One interface to a model and, in part 1, one implementation of it, as the owner chose ("1 generic
client only", section 6.5): a client for the OpenAI-compatible chat interface, written against
Go's standard library. The core does not know what is behind the address.

- **None.** The digest and the summary are printed.
- **An OpenAI-compatible endpoint**, given in configuration as an address, a model name, and the
  name of the environment variable that holds its key when it needs one. This one client reaches:
  - a local model. Ollama serves this interface and accepts tools (section 9);
  - a cloud provider that speaks the interface itself;
  - an aggregator such as OpenRouter (section 9).

  Findings come back as text in the common format and are parsed and checked by aigolog, with one
  repair attempt. Tool calls are used when the model offers them (section 7.6).
- Configuration can add fields to the request. This is how an aggregator is told which providers
  it may use. For OpenRouter that means its settings for no fallback to another provider, for
  zero data retention and for named providers; the program's documentation gives that example.
  **(my call: these are configuration with a documented example, not forced by the program)**

Part 1 has no client for any one provider's own interface, and no default cloud model. A Claude
model is reached through an aggregator. A provider's own client is added later only if the
acceptance run shows the generic one loses findings, or if the owner wants that provider without
a company in between.

For every model: a turn that ends with neither a tool call nor findings in the common format is
not yet a failure. The program asks once for the findings in that format, and only a second miss
counts as failing.

What a model is sent: the instruction, the summary, the answers, and the cited lines. These contain
values from the log, such as addresses and user names. The whole log is never sent.

Privacy rules:

- Nothing is sent anywhere unless a model is configured. There is no default model.
- What the program says about where data went depends on the configured address, not on the kind
  of endpoint. Only when the address is the machine itself does it say that nothing left the
  machine. Otherwise it names the host.
- The program cannot know what a host does with a request. An aggregator passes it to another
  company, and by default may pass it to a second one when the first fails (section 9). So the
  report also prints the request fields set in configuration, and when none restrict the
  providers it says that the host chose where the data went.
- Before the first request the program prints, on the error stream, where it is about to send and
  what. The report's header repeats it and names the model that actually answered.
- With no model, or an endpoint on the machine itself, the program opens no other network
  connection.
- API keys are read from the environment, never written by the program, and a key is sent only to
  the service it belongs to.
- **(my call)** No redaction in part 1. Values go to a configured model as they are.

The report also prints how many tokens the model used.

### 7.9 The evidence rule

The model's words and the program's figures are kept apart. The model never types a figure; it
points at a cell of an answer and the program prints the cell's value. After the model returns,
code checks each finding:

1. It has at least one fact, and every cell a fact points at exists in an answer from this run.
2. Its title and statement contain no figure. A figure here is a number standing on its own: a
   count, an amount, a share, a port, a status code. A name that happens to contain a digit, such
   as the name of a protocol or a device, is not a figure. A figure in the model's words is a
   failure, not something to be matched.
3. It cites at least one line, and each cited line is a row of an answer the finding points at.

A finding that fails is not shown as a finding. The model is told once which findings failed and
why, and may correct them; that round counts against the limits and there is no second one. What
still fails is not thrown away, because it may be the one statement about a rare event that the
digest cannot show. It is printed in a separate part of the report headed as not verified, with
the rule it broke, every digit in the model's words masked, and the facts of it that did check out
**(my call)**. The machine-readable output carries it too, flagged.

What this rule does not prove, stated plainly: that the model's label for a cell is the right
label. A model can point at the count of user names and call it "source addresses". To keep that
visible, each fact is stored with the question behind its answer, and `--explain` prints the
question next to the fact. A name without digits that the model types in its own words is not
caught either. And in the part of the report for statements that were not verified, masking the
digits does not remove amounts written as words or alarming claims; the heading and the rule that
was broken are what tell the reader not to rely on them.

How pointers are written and how figures are detected in text are left to the builder. The tests
fix the behaviour: a finding whose words contain a count is rejected; a finding whose words contain
only a name with a digit in it passes; a finding pointing at a cell
that does not exist is rejected; a finding citing a line from an answer it does not point at is
rejected; a correct finding passes.

### 7.10 The report

- Text for a terminal: a header (sources, records, time span and the anchor used for years, model,
  where data went, the database's path and size), the digest, the findings in order of importance, the statements that
  were not verified (section 7.9), and a footer (limits reached, token use).
- Each finding: title and importance, statement, facts with their values, the cited lines with line
  numbers and up to three quoted, and the command that re-runs its answers.
- `--explain` adds, for each fact, the question behind its answer.
- `--summary` prints the full summary.
- `--json` gives the same content in one structure that carries: a format version; the sources; the
  model, the endpoint and where data went; the digest; each finding with its title, statement,
  importance, facts (label, answer identifier, cells, value, question) and lines (source, line
  number, text); statements not verified, with the rule each broke; limits reached; token use; the
  database's path. Part 2's dashboard reads this.

### 7.11 Text from a log is untrusted

Much of a log is written by whoever is talking to the machine. A user name in an sshd log is typed
by the attacker. That text reaches the model and the user's screen, so:

- **The model.** Everything taken from a log is passed to the model as data, marked off from the
  instruction, and the instruction says that log content may contain text that looks like
  instructions and must never be followed. This reduces the risk; it does not remove it. The digest
  is the backstop: it is computed without the model and always printed, so a finding that the model
  was talked out of is still visible as a large group in the digest.
- **The screen.** Control characters and escape sequences are made harmless in everything printed
  to a terminal, whatever its source: log text, the model's words, labels. The model's words can
  carry what a log told the model to say.
- **Commands.** No command line shown to the user contains text from a log (section 7.6).
- **The store.** Values reach queries as parameters (section 7.4).
- **Tools.** The tools only read the store. Nothing the model can call writes, deletes, or reaches
  the network.

### 7.12 When things go wrong

| Situation | Behaviour |
|---|---|
| No model configured | Print the digest and the summary; exit normally |
| Model configured but unreachable or failing after retries | Print the digest and the summary, say why there are no findings, exit with a distinct code |
| Model cannot call tools | It reports from the summary alone; the report says so |
| Model returns a malformed tool call or findings | One repair attempt, then treated as failing |
| The model declines to answer | Say so; print the digest and the summary |
| A limit is reached | Report what was found, say which limit was reached |
| Too many patterns | Cap, catch-all pattern, and the miner's report shows it |
| A line is very long | Mined on a prefix; kept whole |
| Input is empty or not text | A clear message; nothing stored |
| The run is interrupted | The database stays marked incomplete and is removed by a later run, once no live run holds it |
| Two analyses run at the same time | Each has its own database; neither removes the other's |
| A question runs out of time | The answer says so and carries no figures; the model may ask a narrower one |
| The disk is full or the cache directory cannot be written | A clear message; a distinct exit code |

### 7.13 Configuration

One file in the user's configuration directory, overridden by environment variables and then by
flags. It holds the model choice (none, or an endpoint with its address, model name, the name of
the environment variable for its key, and any extra request fields), the limits, the summary's
size budget, and how long databases are kept. The file format is left to the builder.

## 8. Acceptance tests

Numbers come from section 9. What "done" means **(my call)**:

- Part 1 is done on Linux when A1 to A8, A10 and A11 hold.
- Windows support is claimed only when A9 also holds.
- In A4 at least one cloud model, named for the run, must pass the checklists. The local model's
  result is a measurement: it is recorded, pass or fail, and does not block. This follows the
  owner's choice of "either", which was offered as a way to measure what a local model can do. If
  the owner provides no key for a cloud endpoint, the local model takes the cloud model's place
  in this rule and must pass.
- If no cloud model passes a checklist, the builder may change the summary and the instruction
  and try again, at most three times, and then brings the result to the owner.
- `go test` works with no network: tests that need a fetched log or a model are skipped with a
  visible notice.

The tests:

- **A1. Builds.** The program builds for linux and windows, amd64 and arm64, with every dependency
  in place. While no C code is in use, these are `CGO_ENABLED=0` builds made on the development
  machine. Once any C code is used, the Windows builds are made on the Windows machine or with a
  cross toolchain, and that route is proven before the C code is relied on. This is the first
  implementation task, because the store has only been read, not built. If any target fails, the
  choice of store or library is settled before anything else is built.
- **A2. Exact engine, generated logs.** The repository carries generators for an ssh-like log, a
  log with a restart loop mixed with other services, a web-server-like log in which one client
  probes many paths, and a JSON-lines log, each with known truth. The query tools return exactly
  that truth, from a file and from standard input. A captured JSON export of the journal for a
  restart loop (section 9) gives the unit, the number of restarts and the order of events. The
  decoder for Windows event XML is tested the same way, from events captured on the owner's
  Windows machine with their identifying values masked: an event whose values are named, one
  whose values are not, and one with no message. Two analyses run at the same time finish with
  correct, separate databases. Five cases pin the rules for time (section 7.2): two files that
  run from December into January keep their order; lines that cross midnight on New Year's Eve a
  few seconds out of order keep their years; an `--as-of` date that is the day of the last record
  keeps that record in that year, and the report states that anchor; a stamp for 29 February,
  with an anchor in a year that is not a leap year, is placed in the leap year before it (Go's
  date arithmetic turns 29 February of an ordinary year into 1 March without an error, section
  9); and a log with no time stamps is analysed without time, and the report says so. No model is
  involved.
- **A3. Exact engine, real sshd sample.** On the LogHub OpenSSH 2,000-line sample the query tools
  return: 520 failed-password lines; 23 source addresses; 63 user names; one accepted login, at
  line 956; for 183.62.140.253, 286 attempts on 10 names, 276 of them on root, first and last at
  lines 1024 and 1997; for 187.141.143.180, 80 attempts on 28 names. Section 9 defines each figure
  by the command that counted it. The sample is fetched by a just recipe from the pinned revision
  in section 9 and checked against the recorded checksum. When it is absent the test is skipped
  with a visible notice; acceptance requires it present.
- **A4. Findings, three logs, both kinds of model.** With one local model and with at least one
  cloud model, named for the run and reached through the configured endpoint, each shown finding
  passes the evidence rule, and a person checks the report against these lists. Through an
  aggregator the run can compare two or three cloud models with one key. The third log is from another domain than the owner's two examples **(my call)**.
  - sshd sample: (1) it says the machine is under automated password guessing; (2) it gives the
    number of failed attempts and of source addresses; (3) it names the busiest address; (4) it
    says whether any login succeeded.
  - restart log: (1) it names the service; (2) it gives the number of restarts; (3) it says whether
    the service manager gave up or the loop is still running.
  - web-server-like log (generated): (1) it says one client is probing many paths; (2) it gives the
    number of requests and of distinct paths; (3) it names the client.
  - on the owner's Windows machine, the Security log and the System log, read through the Event
    Log reader, are checked against the first two lists: failed logons in place of failed
    passwords, and a service that keeps stopping in place of the restart loop. How such events
    are produced on that machine, if it has none, is agreed with the owner first.

  A model that fails is recorded as failing; the test exists to measure this. The cloud run spends
  money and needs the owner's key and the owner's go-ahead.
- **A5. A kind of log nobody tuned it for.** The builder keeps a list of every log used during
  development. At acceptance the owner chooses logs of kinds not on it. Every shown finding passes
  the evidence rule, and for each log the owner judges each finding true or false, useful or not. A
  finding that only restates the size of the log does not count as useful. The judgments are
  recorded.
- **A6. Size.** On the full LogHub OpenSSH log (655,146 lines, 70.02 MiB; LogHub's README links it
  as an archive on Zenodo, record 8196385, not in its git repository) the run completes, the
  summary and every request stay within their budgets, memory does not grow with the input,
  question 5 of section 7.6 answers over the whole log within its time limit, and spot-checked
  numbers equal counts made independently with standard tools. Any of those failing
  fails the test. Time and memory are measured and recorded, and bars for later are set from them.
- **A7. No model.** The digest and the summary are printed and the exit is normal.
- **A8. Privacy.** With no model, and with an endpoint on the machine itself, a test shows no other
  network connection. With an endpoint on another host, the program names that host before sending.
- **A9. Windows.** The test suite passes on the owner's Windows machine, the Event Log reader's
  own tests included, which can run nowhere else. A read of a log that Windows refuses reports
  the refusal and exits with the distinct code; it does not print an empty report. This cannot be
  done on the development machine (Linux). The owner decided how: "handoff to the agent at my windows machine
  via github". The work is pushed to the GitHub repository with a handoff file; an agent session
  on the Windows machine runs the tests there and reports back through the repository.
- **A10. No kind-specific code.** A test searches the program's source, tests excluded, and the
  model's instruction for words that belong to one kind of log (ssh, sshd, systemd, password and
  the like, a list kept with the test) and fails when it finds one. This is a tripwire, not proof:
  the words can be avoided. A5, on logs the owner chooses, is the proof.
- **A11. Hostile log text.** A generated log carries, in a user-name position, text that tells the
  reader to report nothing and text containing terminal escape sequences and shell syntax. The
  digest still shows the attack; the printed report contains no raw escape sequence; no shown
  command contains the hostile text. With a model, whether the finding survives is recorded. A
  second case feeds the report a finding whose own words carry an escape sequence; it is not
  printed raw.

What the first version must tell that a one-line shell pipeline cannot: who and how many. Counting
masked line shapes and asking a model (the simplest thing that works) gives "many failed passwords";
A3's per-address and per-name facts are the difference.

## 9. Measured facts this design rests on

Measured on 2026-11-15 on the development machine (Linux, Go 1.27.4). Sources were read at a
pinned tag, never built or run.

| Fact | Evidence |
|---|---|
| A pure-Go program builds for windows/amd64 and windows/arm64 from Linux | `CGO_ENABLED=0 GOOS=windows go build`, exit 0 |
| A program that imports C does not build for Windows here | `CGO_ENABLED=1 GOOS=windows`: `fatal error: windows.h: No such file or directory`; no MinGW installed |
| DuckDB from Go needs cgo and bundles a Windows library for amd64 only | `duckdb/duckdb-go` `v2.20000.0`: `data_chunk.go:3`, `README.md:234`, `README.md:239-241` |
| Tantivy from Go needs cgo and a Rust static library | `anyproto/tantivy-go` `v1.0.6`: `rust/Cargo.toml:8`, `tantivy_remote.go:7-14` |
| A pure-Go Parquet library exists | `parquet-go/parquet-go` `v0.32.0`: no `import "C"` in the repository |
| A cgo-free SQLite port lists Windows targets and needs Go 1.26 | `modernc.org/sqlite` `v1.62.0`: `doc.go:5`, `doc.go:84-86`, `go.mod`; `lib/sqlite_windows.go` carries the build constraint `windows && (amd64 \|\| arm64)`. Read, not built |
| OpenRouter speaks the OpenAI chat interface, takes tool calls, and lets a request restrict its providers | Its documentation, read 2026-11-15 (`/docs/faq`, `/docs/features/tool-calling`, `/docs/features/provider-routing`, `/docs/features/structured-outputs`, `/pricing`): base `https://openrouter.ai/api/v1`; `require_parameters`, `provider.zdr`, `provider.only`, `allow_fallbacks`; "Prompt and completion are not logged by default"; on a provider error it falls back to another provider unless told not to; a 5.5% fee when credits are bought, none per request; free models limited to 50 requests a day |
| OpenRouter's model list | `GET https://openrouter.ai/api/v1/models` on 2026-11-15: 467 models, 404 declare tool support, 392 structured output, 14 free; the Claude entries checked carry the same per-token prices as Anthropic's own list |
| Anthropic's Go SDK, considered and not used in part 1 | `anthropics/anthropic-sdk-go` `v2.9.0`: 509 Go files, no cgo; its `go.mod` requires AWS, Google and OpenTelemetry libraries |
| The Windows Event Log is read from Go without cgo in a shipping product | `elastic/beats` `v9.5.6`: `winlogbeat/sys/wineventlog/zsyscall_windows.go:70` loads `wevtapi.dll`; the package binds `EvtOpenLog`, `EvtOpenPublisherMetadata`, `EvtSeek` and `EvtSubscribe` and defines `EvtRenderEventXml` and `EvtFormatMessageEvent`. Two files checked |
| Windows' Event Log API reads live logs and exported files and returns events as XML | Microsoft's documentation, read 2026-11-15: `EvtQuery` with `EvtQueryChannelPath` or `EvtQueryFilePath` ("You can specify an .evt, .evtx, or.etl log file"); `EvtRender` with `EvtRenderEventXml`; `EvtFormatMessage` needs the publisher's metadata. An event's values are not always named: "The Data value takes event data in an unnamed field. For example, events in classic event logs" (the Get-WinEvent page). Who may read the Security log by default was not stated on the pages read |
| Windows' own tool for querying events | Microsoft's wevtutil page, read 2026-11-15: `wevtutil qe <Path> [/lf:<Logfile>] [/sq:<Structquery>] [/q:<Query>] [/bm:<Bookmark>] [/sbm:<Savebm>] [/rd:<Direction>] [/f:<Format>] [/l:<Locale>] [/c:<Count>] [/e:<Element>]`; its one example is `wevtutil qe Application /c:3 /rd:true /f:text` |
| Libraries that parse exported event log files | `Velocidex/evtx` `v0.2.0` (last commit 2021-10-19) and `0xrawsec/golang-evtx` `v1.2.9` (2021-09-26), both Go without cgo; `libyal/libevtx` (C, last commit 2026-11-07): its `README` says "Status: alpha" and "Licence: LGPL-3.0-or-later", and `synclibs.sh` fetches other libraries when it is built |
| What the repository lacks as community documents | GitHub's community profile for the repository on 2026-11-15: 42 percent; code of conduct, contributing guide, issue template and pull-request template missing; licence, README and description present; no security policy |
| Ollama serves an OpenAI-compatible chat endpoint that accepts tools | `ollama/ollama` `v0.40.16`: `server/routes.go:2436`, `openai/openai.go:111-124` |
| sshd sample figures (A3) | LogHub `OpenSSH/OpenSSH_2k.log`, 2,000 lines, counted with the commands below |
| The restart loop's lines | 23 journal lines from a throwaway systemd unit: `Started`, `Main process exited, code=exited, status=1/FAILURE`, `Failed with result 'exit-code'`, `Scheduled restart job, restart counter is at N` (N from 1 to 5), `Start request repeated too quickly`, `Failed to start` |
| The journal's JSON export for the same kind of loop | 23 entries from `journalctl --user -u <unit> -o json` for a second throwaway unit: every value is a string; `__REALTIME_TIMESTAMP` is 16 digits, microseconds since 1970; `MESSAGE` holds the text; `USER_UNIT` names the unit; `N_RESTARTS` is present on the five restart entries; 45 different field names |
| LogHub's terms | Its `LICENSE`: free for research or academic work, with a reference, a citation and the notice in every copy |
| addlicense's options and header test | `google/addlicense` `v1.2.0`: `main.go:57-72`, `main.go:427-435`, `tmpl.go:43-46` |
| The rule for years in section 7.2 behaves as that section says | The rule was written as a throwaway script and run on eleven inputs: nine ordinary and awkward ones (two files across New Year with file times and with `--as-of`, lines out of order at New Year's midnight, an `--as-of` on the day of the last record, a long quiet stretch, fourteen months of monthly records, a stamp ten hours after the anchor) all got the right years; the two cases the section says it cannot fix came out wrong in the direction it states |
| Go turns 29 February of an ordinary year into 1 March without an error | `time.Date(2025, 2, 29, ...)` printed `2025-03-01` under Go 1.27.4; the same call for 2024 printed `2024-02-29` |
| just on Windows uses the `sh` from Git for Windows, or a `windows-shell` setting | `casey/just` `1.58.0`: `README.md:107-108`, `README.md:2192` |
| lefthook's installed hook is a `/bin/sh` script | `evilmartians/lefthook` `v2.1.17`: `internal/templates/hook.tmpl:1` |

**The sshd sample and how its figures were counted.** The file is
`OpenSSH/OpenSSH_2k.log` in `https://github.com/logpai/loghub` at revision
`dd61d0952749ee7963bde24220d1be5ede023033`: 225,216 bytes, 2,000 lines with no newline after the
last, SHA-256 `1e4912727fa88245113d41b16a0cd25ceadba7f931e1c406542885b91254264f`. With `F` as the
file and `NAME` as `sed -E 's/.*Failed password for (invalid user )?([^ ]+) from.*/\2/'`:

| Figure | Command | Result |
|---|---|---|
| Failed-password lines | `grep -c 'Failed password' F` | 520 |
| Source addresses | `grep 'Failed password' F \| grep -o -E 'from [0-9.]+' \| sort -u \| wc -l` | 23 |
| User names | `grep 'Failed password' F \| NAME \| sort -u \| wc -l` | 63 |
| Accepted logins | `grep -n 'Accepted ' F` | one line, number 956 |
| Attempts by an address | `grep 'Failed password' F \| grep -c 'from ADDRESS '` | 286 and 80 |
| Names tried by an address | `grep 'Failed password' F \| grep 'from ADDRESS ' \| NAME \| sort -u \| wc -l` | 10 and 28 |
| Attempts on root by the first address | the same lines, `grep -c 'for root from'` | 276 |
| First and last line of the first address | `grep -n 'Failed password.*from ADDRESS ' F`, first and last | 1024 and 1997 |

All of these count lines containing `Failed password`, in both of its shapes. Lines of the shape
`Invalid user <name> from <address>` are a different pattern and are not part of the 520.

Limits of these measurements: the restart sample is a five-restart probe that ran for three
seconds, not a loop that ran for days. The 2,000-line sshd file is a sample of a larger log, so its
distribution over time may reflect how it was sampled. No model, local or cloud, has been run on
anything yet. What OpenRouter does is taken from its documentation and its model list, not from a
request sent to it.

## 10. Developer tooling

The owner asked for these: "Add just and lefthook" and "add https://github.com/google/addlicense".
All three are installed on the development machine (just 1.58.0, lefthook 2.1.17, addlicense
v1.2.0).

- **just.** A `justfile` holds the project's commands: `setup` (installs the git hooks and checks
  the tools are present), `check` (format, vet, licence headers, and the tests that need neither
  network nor model), `build` (the four cross-builds of A1), the recipe that fetches the test logs,
  and the model tests. `just check` is the gate; anything automated runs it. The recipes must work
  on Linux and on Windows. On Windows just runs recipes with the `sh` that comes with Git for
  Windows unless a `windows-shell` is set (section 9), so recipes stay plain command lines that
  work under either.
- **lefthook.** A `lefthook.yml` runs the quick checks before a commit (format, vet, licence
  headers) and `just check` before a push **(my call on the split)**. Hooks call the just recipes,
  so there is one definition of each check. Hooks exist only in a clone where `just setup` has been
  run, so they are a convenience and not the gate. No hook runs a test that needs the network or a
  model. The hook lefthook installs is a `/bin/sh` script (section 9), which git on Windows runs
  with its own `sh`.
- **addlicense.** Every source file carries a header naming the copyright holder and the licence.
  PolyForm Noncommercial is not one of addlicense's built-in licences (`-l` takes apache, bsd, mit
  or mpl), so the header comes from `header.txt` at the root of the repository, which the owner
  supplied, passed with `-f`. A recipe runs it
  with `-check`, ignoring test data. Know its limit: `-check` passes any file whose first 1,000
  bytes contain the word "copyright", so it proves a header is present, not that it is the right
  one.
- The project's `.claude/recommended-tools.json` declares the three tools, Ollama for the local
  model tests, and the source repositories and tags from section 9.

The header text is the owner's, in `header.txt`:

```
Copyright (c) 2026 Costas Kirgoussios

This software is licensed under the PolyForm Noncommercial License 1.0.0.
You may obtain a copy of the License at:
https://polyformproject.org/licenses/noncommercial/1.0.0/
```

### Repository documents

The owner noted: "the repo is missing the community documents". GitHub's own check of the
repository agrees (section 9). Setting the project up adds:

- `CONTRIBUTING.md`: how to build and check a change (`just setup`, `just check`), how to propose
  one, and the terms on which a contribution is accepted.
- `CODE_OF_CONDUCT.md`.
- `SECURITY.md`: how to report a vulnerability.
- `.github/ISSUE_TEMPLATE/`, with a bug report and a feature request, and
  `.github/PULL_REQUEST_TEMPLATE.md`.
- A real `README.md`: what the program does, how to install and run it, where log data goes, and
  the licence.

Three choices in these documents are the owner's. My proposals **(my call)**: contributions are
accepted under the project's own licence, and the contributing guide says so; the code of conduct
is the Contributor Covenant; security reports go through GitHub's private vulnerability
reporting, so that no personal address has to be published.

## 11. What part 1 must leave room for

- **Part 2.** Patterns that change as more lines arrive, which means a pattern can later be merged
  into another and its rows re-pointed. A database that lives across runs. The `--json` report as
  the dashboard's input; every string in it, the model's words included, is untrusted text that the
  page must escape.
- **Later.** Plain-language questions use the same query tools and evidence rule.

## 12. Calls I made, for the owner to check

1. Part 1 is for one person on their own machines; no accounts or server.
2. OPUS's OpenTelemetry input and MCP server are deferred.
3. JSON-lines logs are read in part 1, as a second reader.
4. Several files may go into one analysis, and each finding says which source it is about.
5. UTF-8 and UTF-16 input; continuation lines are joined to the record before them.
6. The store is SQLite through a pure-Go port, behind an interface. C code is allowed by the
   owner; using it only where it has been measured to help is my call, and a C store may replace
   this one if it measures too slow.
7. Raw text is stored for every record, so the database is a copy of the log: owner-only, removed
   after seven days, with `--no-keep` to remove it at once.
8. The model asks through a fixed set of query tools, not free SQL.
9. Settled by the owner, no longer my call: one generic client and no provider's own client in
   part 1 (sections 6.5 and 7.8). There is no default cloud model; the owner names one in
   configuration. Still my call: restrictions for an aggregator are configuration with a
   documented example, not forced by the program.
10. No redaction in part 1; a configured model receives values from the log as they are.
11. The model never types a figure. A statement that breaks the evidence rule is shown apart from
    the findings, marked as not verified, with its digits masked.
12. LogHub's logs are fetched at test time and never committed, because its terms speak of research
    and academic use.
13. Quick checks run before a commit, `just check` before a push.
14. Settled by the owner, no longer my call: the licence header is the text in `header.txt`
    (section 10).
15. What "done" means (section 8): at least one cloud model must pass the checklists of A4, the
    local model is measured and does not block, and Windows support is claimed only after the
    tests have run on Windows. With no key for a cloud endpoint, the local model must pass instead.
16. Each run makes a new database; nothing is reused between runs.
17. Settled by the owner, no longer my call: the Event Log reader is in part 1, and it reads
    through Windows' own API (sections 6.6 and 7.1). Still my call: the shape of its command
    line.
18. The expected store layout is a records table and a values table; the builder may change it if
    the tests hold.
19. The user can run the eight query tools directly, with their own arguments.
20. A third generated log, from another domain, is part of the tests.
21. Kept databases have a total size limit as well as an age limit.
22. The community documents (section 10): contributions accepted under the project's licence, the
    Contributor Covenant as the code of conduct, and GitHub's private vulnerability reporting for
    security reports.

**Part 1 grew during review.** It was described to the owner as "the engine and the one-shot
report, on text logs", with one command. Review added the things below. Each can be left out of
part 1 without touching goals G1 to G7 or section 7.11; the owner may cut any of them.

| Added in review | If it is left out |
|---|---|
| The JSON-lines reader (item 3) | Goal G3 covers text lines only; the journal is reached by piping its text output; the named-field contract is first proven by the Event Log reader, which can be tested only on Windows |
| Several files in one analysis (item 4) | One file or standard input per run |
| `aigolog query` with the user's own arguments (item 19) | Re-running a finding's own answers stays; a different idea needs another tool |
| `aigolog list` and `aigolog clean` | The report prints the database's path; removal by age stays |
| `aigolog show`, the stored exchange with the model | The report still says where data went, but not exactly what was sent |
| `--explain` | The question behind each fact is in the `--json` output only |
| `--json` | It moves to part 2, which is what reads it |
| A total size limit for kept databases (item 21) | The age limit and `--no-keep` stay |
| A third generated log from another domain (item 20) | The tests rest on the owner's two examples and on A5 |

## 13. Left to the builder

- The pattern miner: its algorithm, thresholds and the cap.
- The list of time layouts, and the field names the JSON-lines reader treats as time and free text.
- The table layout of the store and the shapes and names of the query tools.
- The summary's size budget and how many patterns get full detail.
- The instruction given to the model, and the default limits, set after measurement.
- How pointers to cells are written, and how figures are detected in the model's words.
- The configuration file's format, the exit codes, the wording of the report.
- Which local model the tests use, chosen after looking at the development machine's hardware.
- Which cloud models the acceptance run uses, agreed with the owner before it spends anything.

## 14. What cannot be judged yet

- Whether a small local model can run this investigation well enough, with or without tools. A4
  measures it.
- How fast a pure-Go SQLite store takes in and queries a large log. A6 measures it; if it is too
  slow, the store is replaced behind its interface.
- How long a large log takes when every run starts from nothing. A6 measures it; reusing a
  database for a file that has not changed is the remedy if it is too slow.
- How well one miner handles logs that are not syslog-like: logs in other languages, very long
  records, deeply nested JSON. A5 probes it.
- Whether the evidence rule leaves too few findings standing with weaker models.
- How well the instruction holds against hostile log text. A11 records it; it cannot be made
  certain.
- Anything about behaviour on Windows, until a Windows machine runs the tests.
- How the Event Log is read and what its events look like for the two examples, until the owner
  chooses and real events come back from the Windows machine. Section 7.1 and the Windows cases of
  A2 and A4 are unfinished until then.

## Stand-downs

Findings raised in review and set aside, with the reason. Added by the review panel.

- Exit codes and the configuration file's format are not specified: left to the builder on purpose
  (section 13).
- In the part of the report for statements that were not verified, masking digits also masks the
  digit inside a name such as a protocol's. Set aside: that part is headed as not verified and is
  not relied on (section 7.9).

End of the part 1 design, sections 1 to 14 and the stand-downs.
