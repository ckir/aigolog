SUPERSEDED 2026-11-16 by `docs/handoff/windows-session-2.md`. Do not follow this file: it does not
keep raw captures out of the repository, does not check the masking, and does not say what to do
about elevation. It is kept only as a record. The file that replaced it is itself on hold.

# Handoff: real Windows events for the Event Log reader

For: an agent session started on the owner's Windows machine, after fetching the branch
`design/part-1-engine-and-report` of this repository.

Written: 2026-11-15, by an agent session on the owner's Linux machine, in the commit that adds
this file on top of `5e00016`.

Why that session could not do this itself (measured there): the machine runs
`Linux x86_64`; it has no `wevtutil`, no Wine and no virtual machine support, and
the Windows Event Log exists only on Windows.

What it is for: aigolog will read the Windows Event Log (design section 7.1 in
`docs/superpowers/specs/2026-11-15-engine-and-report-design.md`). That section cannot be finished
without seeing real events. This session captures a few, with everything identifying removed, and
reports four facts. It writes no code and changes nothing on the machine.

## 1. Ground rules in force

These are written by the Linux session as defaults, from the owner's standing instructions as it
understands them. The owner's own instructions on the Windows machine come first where they
differ.

- **This repository is public.** Nothing that identifies the machine or its users may be written
  into it or quoted in a pull request. See "Masking" below. This is the rule most likely to be
  broken by accident.
- Work on a branch. Never commit to `main` or to `design/part-1-engine-and-report`. Do not merge.
- Commit and push only what this file asks for, and only after the owner has seen it (section 7).
  No release, no tag, no deployment.
- Change nothing on the machine: no installs, no settings, no audit policy, no new services, no
  new accounts. Anything of that kind is marked **ask the owner first** below.
- Measure, do not assume. Every statement in the report has next to it the exact command that
  was run and its output, masked. "None found" and "could not" are correct, welcome results.
- Do not edit the design document or any other existing file.

### Masking

Before anything is written to a file in the repository or pasted into a pull request, replace the
VALUES of these with placeholders, and keep every element name, attribute name and the order of
elements exactly as Windows produced them:

| Kind of value | Replace with |
|---|---|
| Computer name, domain or workgroup name | `HOST`, `DOMAIN` |
| User and account names | `user1`, `user2`, ... (the same name always gets the same placeholder) |
| Security identifiers (values starting `S-1-`) | `S-1-5-21-0-0-0-1001`, `-1002`, ... |
| IP addresses | `203.0.113.1`, `203.0.113.2`, ... |
| Workstation names | `WS1`, `WS2`, ... |
| Logon identifiers, process identifiers, thread identifiers, record numbers, activity identifiers | a value of the same shape, for example `0x0` or `0` or an all-zero GUID |
| Names of services and programs that are not part of Windows | `service1`, `service2`, ... (**ask the owner first** if you would rather keep them) |
| File paths that contain a user name | the path with the user name replaced |

Keep unchanged: the provider's name, the event number, the level, the channel, the names of data
elements, numeric codes such as status and logon type, and the FORMAT of every time stamp (the
time itself may be replaced by another valid time in the same format).

If you are not sure whether a value identifies someone, mask it and say so in the report.

## 2. Already done - do not redo

- The design exists and is under revision:
  `docs/superpowers/specs/2026-11-15-engine-and-report-design.md`. Read its sections 7.1 and 9.
- The owner has decided that aigolog reads the Event Log through Windows' own API. That is not
  this session's question.
- No code exists in the repository. Do not write any.

## 3. Traps

- **Public repository**, as above.
- **Elevation changes the answer.** Item 4 must be run from an ordinary prompt, not an elevated
  one. For every item, say in the report whether the prompt was elevated.
- **`wevtutil qe` prints text unless told otherwise.** Microsoft's page says of `/f`: "The default
  is Text." Use `/f:xml`.
- **Several events in XML have no single root** unless `/e:<Element>` is given; the page says
  "/e:root would result in XML that contains the root element pair <root></root>".
- **Encoding.** Files written by redirecting output on Windows are not always UTF-8. Check the
  encoding of every file you add; the repository wants UTF-8.
- There are no generated files and no baselines in this repository, so nothing can be broken by
  regenerating it here.

## 4. The items, in order

The command form below is from Microsoft's documentation of `wevtutil`, read on 2026-11-15:

```
wevtutil qe <Path> [/q:<Query>] [/rd:<Direction>] [/f:<Format>] [/c:<Count>] [/e:<Element>]
```

with the documented example `wevtutil qe Application /c:3 /rd:true /f:text`. The page says `/q`
"Defines the XPath query to filter the events", `/c` "Sets the maximum number of events to read",
and for `/rd` "If true, the most recent events are returned first."

The XPath queries suggested below were NOT checked on a Windows machine. If one fails, find the
form that works and write down the exact command you ran.

### Item 1 - the machine (this item gates the others)

Goal: record, with the command used for each and its output:

- the Windows edition and version, and the processor architecture (x64 or ARM64)
- the PowerShell version
- whether the session is elevated
- whether `wevtutil` runs (`wevtutil el` lists the log names, per Microsoft's page)
- whether `go`, `git`, and a C compiler (`gcc`, `clang` or `cl`) are on the path, with versions

Mask the computer name and user name wherever they appear. If `wevtutil` does not run, stop and
report that.

### Item 2 - failed logons in the Security log

Goal: up to five real events of a failed logon, as XML, masked. Microsoft documents this event as
number 4625, "An account failed to log on".

Suggested command (query not verified):

```
wevtutil qe Security /c:5 /rd:true /f:xml /q:"*[System[(EventID=4625)]]"
```

Write back:

- how many such events the log holds and the time span they cover (numbers only)
- each captured event as its own file, masked
- for the report: whether every data element of the event carries a name, with the list of names

Decision rule, fixed before looking: if every data element carries a name, the reader uses the
names; if any does not, the reader numbers the unnamed ones by position.

If the Security log cannot be read, record the exact message and go on to item 3. If it holds no
such event, report "none found". Producing a failed logon on purpose, or changing what Windows
audits, is **ask the owner first**.

### Item 3 - services that fail, in the System log

Goal: see what Windows records when a service stops unexpectedly or fails to start.

Suggested command (query not verified), which reads the 200 most recent events written by the
Service Control Manager:

```
wevtutil qe System /c:200 /rd:true /f:xml /q:"*[System[Provider[@Name='Service Control Manager']]]"
```

Write back:

- the distinct event numbers found, with how many of each and the level of each
- up to three events of each number as files, masked
- for each number: whether its data elements are named; if a restart count or the name of the
  service appears as a value, in which element
- which of them, if any, records a service that terminated unexpectedly

Decision rule: the same as item 2.

If none of the events is about a failing service, report that. Creating a service that fails on
purpose is **ask the owner first**.

### Item 4 - the Security log from an ordinary prompt

Goal: learn what a program without elevation gets. From a prompt that is NOT elevated, run:

```
wevtutil qe Security /c:1 /f:xml
```

Write back the exact output, masked, and the exit code. Say how you confirmed the prompt was not
elevated.

Decision rule: if Windows refuses, the design's rule stands (aigolog reports the refusal and
never an empty log). If it does not refuse, say so; the design will be corrected.

### Item 5 - the readable message (only if items 2 or 3 produced events)

Goal: see whether an event's readable message comes with it. For one event already captured, get
it once as text and once as XML (the documented example form `wevtutil qe <Path> /c:1 /rd:true
/f:text`, and the same with `/f:xml`), and report whether the sentence that the text form shows
appears anywhere in the XML form. Masked, as always.

## 5. Not for this session

- Writing or building any code, in Go or anything else.
- Measuring how fast events can be read through the API. That needs code that does not exist yet.
- Producing failed logons or failing services, or changing audit settings, without the owner.
- Installing Go, a C compiler or anything else.
- Changing the design.

## 6. How to finish and report back

1. Create a branch `handoff/windows-events` from `design/part-1-engine-and-report`.
2. Add `docs/handoff/windows-session-report.md`: one section per item, each with the commands run,
   their masked output, the answers asked for, and anything that could not be done and why.
3. Add the captured events under `docs/handoff/windows-events/`, one event per file, UTF-8,
   named for what they are (for example `security-failed-logon-1.xml`,
   `system-scm-<number>-1.xml`).
4. **Show the owner the files and ask before pushing.** Everything pushed is public.
5. Push the branch and open a pull request against `design/part-1-engine-and-report`. Its body
   says what was captured, what was not, and whether each prompt was elevated.

There is no test suite and no CI in this repository yet, so there are no checks to run before
pushing. If the session ends early, the report says which items are still open.
