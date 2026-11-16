ON HOLD since 2026-11-16. The owner decided that this work is done first in a Windows virtual
machine on the development machine. Do not follow this file unless the owner starts a session
with it for a last check on a real machine.

Update 2026-11-16: what items 1 to 5 ask was measured on GitHub's hosted Windows machines, with
test events made there (design section 7.1). Nothing was captured on the owner's machine.

# Handoff 2: real Windows events for the Event Log reader

This file replaces `docs/handoff/windows-session.md`, which a review found unsafe in places: it did
not keep raw captures out of the repository, did not check the masking, and did not say what to do
about elevation. Follow this file only.

For: an agent session started on the owner's Windows machine, after fetching the branch
`design/part-1-engine-and-report` of this repository.

Written: 2026-11-16, by an agent session on the owner's Linux machine, in the commit that adds
this file on top of `e011e6e`.

Revised later on 2026-11-16, after a review of this file. What changed: the masking check no
longer puts real values in the report (sections 1 and 3); record numbers are kept distinct
(section 3); item 3 counts only what was captured; items 5 and 6 give commands. Follow the file
as it is at the commit named in the message that started your session. If you began from an
earlier copy, stop, fetch again, and re-read sections 1, 3 and 6 before going on.

Why that session could not do this itself (measured there): the machine runs
`Linux 7.0.0-43-generic x86_64`; it has no `wevtutil`, no Wine and no virtual machine support, and
the Windows Event Log exists only on Windows.

What it is for: aigolog will read the Windows Event Log (design section 7.1 in
`docs/superpowers/specs/2026-11-15-engine-and-report-design.md`). That section cannot be finished
without seeing real events. This session captures a few, with everything identifying removed, and
reports some facts. It writes no code and changes nothing on the machine.

## 1. Ground rules in force

These are written by the Linux session as defaults, from the owner's standing instructions as it
understands them. The owner's own instructions on the Windows machine come first where they
differ.

- **This repository is public.** Nothing that identifies the machine or its users may be written
  into it or quoted in a pull request. Sections 2 and 3 say how. This is the rule most likely to
  be broken by accident.
- **Never raise your own rights.** Do not start an elevated prompt yourself, do not use
  "run as administrator" in any form, and do not add any account to any group. When an item needs
  elevation, stop and **ask the owner first**; the owner decides whether to give you an elevated
  prompt.
- Change nothing on the machine: no installs, no settings, no audit policy, no new services, no
  new accounts, no group membership. Anything of that kind is **ask the owner first**.
- Work on a branch. Never commit to `main` or to `design/part-1-engine-and-report`. Do not merge.
- Commit and push only what this file asks for, and only after the owner has seen it (section 8).
  No release, no tag, no deployment.
- Measure, do not assume. Every statement in the report has next to it the command that was run
  and its output, both masked: a real value inside a command is replaced too (section 3). "None
  found" and "could not" are correct, welcome results.
- Do not edit the design document or any other existing file.

## 2. Where captures go

- Create a folder OUTSIDE the repository's working tree, for example under the system's temporary
  folder, and write every raw capture there. Raw output never goes into the repository folder,
  not even for a moment.
- Make the masked copies in that same outside folder.
- Copy a file into the repository only after it has passed the check in section 3.
- When the session ends, tell the owner where the outside folder is, so the owner can delete it.

## 3. Masking, and the check that follows it

Replace the VALUES of these with placeholders. Keep every element name, attribute name and the
order of elements exactly as Windows produced them.

| Kind of value | Replace with |
|---|---|
| Computer name, domain or workgroup name | `HOST`, `DOMAIN` |
| User and account names, including the `name@domain` form | `user1`, `user2`, ... and `user1@DOMAIN` (the same name always gets the same placeholder) |
| Security identifiers that begin `S-1-5-21-` | `S-1-5-21-0-0-0-1001`, `-1002`, ... Leave the short well-known ones, such as `S-1-5-18`, as they are |
| IPv4 addresses | `203.0.113.1`, `203.0.113.2`, ... |
| IPv6 addresses, including those that wrap an IPv4 address | `2001:db8::1`, `2001:db8::2`, ... |
| Port numbers | `0` |
| Workstation names | `WS1`, `WS2`, ... |
| Record numbers of events | `1`, `2`, `3`, ... in the events' original order. Two different events keep two different numbers, and the same event keeps the same number wherever it appears. The design uses this number to tell events apart |
| Logon identifiers, process and thread identifiers, activity and correlation identifiers | a value of the same shape: `0x0`, `0`, or an all-zero GUID |
| Every time stamp | another valid time in exactly the same format, starting at `2026-01-01T00:00:00` and keeping the events in their original order. Always replace; never leave a real time |
| The content of any `Binary` element, and any other long run of hexadecimal digits | zeros of the same length. Such data can spell out a name |
| Names of services and programs that are not part of Windows | `service1`, `service2`, ... (**ask the owner first** if you would rather keep them) |
| File paths that contain a user name | the path with the user name replaced |

Keep unchanged: the provider's name, the event number, the level, the channel, the names of data
elements, and numeric codes such as status and logon type.

If you are not sure whether a value identifies someone or something, mask it and say so.

**The check.** Before any file is copied into the repository, and again before the owner is
shown the files, search every masked file for each real value you replaced: the real computer
name, every real user name, the real domain, the first parts of the machine's own security
identifier, and every real address. Each search must find nothing. If one finds something, fix
the file and search again.

In the report, say for each kind of value that the search found nothing. **Never write a real
value into the report.** That includes the commands: a command that contained a real value is
written with the value replaced by a description in angle brackets, for example
`<the real computer name>`. The same holds for commit messages, branch names and the pull
request.

## 4. Already done - do not redo

- The design exists and is under revision:
  `docs/superpowers/specs/2026-11-15-engine-and-report-design.md`. Read its sections 7.1 and 9.
- The owner has decided that aigolog reads the Event Log through Windows' own API. That is not
  this session's question.
- No code exists in the repository. Do not write any.

## 5. Traps

- **Public repository**, as above.
- **Elevation changes the answer.** Item 4 must be run from an ordinary prompt, not an elevated
  one. For every item, say in the report whether the prompt was elevated.
- **`wevtutil qe` prints text unless told otherwise.** Microsoft's page says of `/f`: "The default
  is Text." Use `/f:xml`.
- **Several events in XML have no single root** unless `/e:<Element>` is given; the page says
  "/e:root would result in XML that contains the root element pair <root></root>".
- **Encoding.** Files written by redirecting output on Windows are not always UTF-8. Check the
  encoding of every file you add; the repository wants UTF-8.
- **The list of log names is itself private.** `wevtutil el` shows which products are installed.
  Do not put its output in the report; give only the number of names and whether `Security`,
  `System` and `Application` are among them.
- There are no generated files and no baselines in this repository, so nothing can be broken by
  regenerating it here.

## 6. The items, in order

The command form below is from Microsoft's documentation of `wevtutil`, read on 2026-11-15:

```
wevtutil qe <Path> [/q:<Query>] [/rd:<Direction>] [/f:<Format>] [/c:<Count>] [/e:<Element>]
```

with the documented example `wevtutil qe Application /c:3 /rd:true /f:text`. The page says `/q`
"Defines the XPath query to filter the events", `/c` "Sets the maximum number of events to read",
and for `/rd` "If true, the most recent events are returned first."

Every XPath query and every event number suggested below is from memory and was NOT checked on a
Windows machine. If one fails or finds nothing, find the form that works and write down the exact
command you ran. Microsoft's own pages are the authority.

### Item 1 - the machine (this item gates the others)

Goal: record, with the command used for each and its output:

- the Windows edition and version, and the processor architecture (x64 or ARM64)
- the PowerShell version
- whether the session is elevated
- whether `wevtutil` runs (see the trap about `wevtutil el` above)
- whether `go`, `git`, and a C compiler (`gcc`, `clang` or `cl`) are on the path, with versions

Mask the computer name and user name wherever they appear. If `wevtutil` does not run, stop and
report that.

### Item 2 - the Security log from an ordinary prompt

Do this before item 3, from a prompt that is NOT elevated:

```
wevtutil qe Security /c:1 /f:xml
```

Write back the exact output, masked, and the exit code. Say how you confirmed the prompt was not
elevated.

Decision rule, fixed before looking: if Windows refuses, the design's rule stands (aigolog reports
the refusal and never an empty log). If it does not refuse, say so; the design will be corrected.

### Item 3 - failed logons in the Security log

Goal: up to five real events of a failed logon, as XML, masked. Microsoft documents this event as
number 4625, "An account failed to log on".

If item 2 showed that an ordinary prompt cannot read the Security log, **ask the owner first**
whether to give you an elevated prompt for this item. If the owner says no, report that and go on.

Suggested command (query not verified):

```
wevtutil qe Security /c:5 /rd:true /f:xml /q:"*[System[(EventID=4625)]]"
```

Write back:

- how many such events you captured (do not count the whole log; that can take very long)
- each captured event as its own file, masked
- whether every data element of the event carries a name, with the list of names

Decision rule, fixed before looking: if every data element carries a name, the reader uses the
names; if any does not, the reader also numbers the values by position.

If the log holds no such event, report "none found". Producing a failed logon on purpose, or
changing what Windows audits, is **ask the owner first**.

### Item 4 - services that fail, in the System log

Goal: see what Windows records when a service stops unexpectedly or fails to start.

First list what is there. Suggested command (query not verified), which reads the 200 most recent
events written by the Service Control Manager:

```
wevtutil qe System /c:200 /rd:true /f:xml /q:"*[System[Provider[@Name='Service Control Manager']]]"
```

Most of these are likely to be routine notices that a service changed state. So also look for
failures directly. Suggested command (query not verified), for the same provider at the levels
that mean critical, error or warning:

```
wevtutil qe System /c:50 /rd:true /f:xml /q:"*[System[Provider[@Name='Service Control Manager'] and (Level=1 or Level=2 or Level=3)]]"
```

Event numbers that may record a failing service (from memory, not checked): 7000, 7009, 7023,
7024, 7031, 7034.

Write back:

- the distinct event numbers found, with how many of each and the level of each
- up to three events of each failure number as files, masked
- for each number: whether its data elements are named; where the name of the service appears;
  whether a count of failures or restarts appears, and in which element
- which of them, if any, records a service that terminated unexpectedly

Decision rule: the same as item 3.

If none of the events is about a failing service, report that. Creating a service that fails on
purpose is **ask the owner first**.

### Item 5 - an event whose values are not named

Goal: one event in which at least one data element has no name. Events from older programs,
often in the Application log, are the likely place. Read the 200 most recent events of the
Application log as XML into a file in the outside folder (the documented form:
`wevtutil qe Application /c:200 /rd:true /f:xml`), and pick the first event in it that has a data
element with no name. Write it back as a file, masked, with the command you used. If there is none
among those 200, report "none found".

### Item 6 - the readable message

Goal: learn whether an event's readable message comes with it. Take one event already captured
and get that same event once as text and once as XML, then report whether the sentence that the
text form shows appears anywhere in the XML form.

The newest event in a log can change between two commands, so do not ask for "the newest" twice.
Select the event by its own record number, which its XML shows. Suggested form (query not
verified), run once with `/f:text` and once with `/f:xml`:

```
wevtutil qe <Path> /f:text /q:"*[System[(EventRecordID=<number>)]]"
```

In the report, write the record number as `<record number>`.

Also look for one event for which Windows has NO readable message: its text form says that the
description for the event cannot be found. If you meet one among the events you have already read,
capture it as XML and as text, masked. If you meet none, report "none found"; do not search
further than the events already read.

### If something is not found

That is a result, not a failure. The session on the Linux machine will then write examples by
hand from Microsoft's documented event format and label them as made up, so say clearly which
items found nothing.

## 7. Not for this session

- Writing or building any code, in Go or anything else.
- Measuring how fast events can be read through the API. That needs code that does not exist yet.
- Producing failed logons or failing services, or changing audit settings, without the owner.
- Raising your own rights or changing any group, in any way.
- Installing Go, a C compiler or anything else.
- Changing the design.

## 8. How to finish and report back

1. Create a branch `handoff/windows-events` from `design/part-1-engine-and-report`.
2. Add `docs/handoff/windows-session-report.md`: one section per item, each with the commands run,
   their masked output, the answers asked for, and anything that could not be done and why. Add a
   section with the masking check of section 3.
3. Add the masked events under `docs/handoff/windows-events/`, one event per file, UTF-8, named
   for what they are (for example `security-failed-logon-1.xml`, `system-scm-<number>-1.xml`,
   `application-unnamed-1.xml`).
4. **Show the owner the files and ask before pushing.** Everything pushed is public.
5. Push the branch and open a pull request against `design/part-1-engine-and-report`. Its body
   says what was captured, what was not, and whether each prompt was elevated.
6. Tell the owner where the outside folder with the raw captures is.

There is no test suite and no CI in this repository yet, so there are no checks to run before
pushing. If the session ends early, the report says which items are still open.
