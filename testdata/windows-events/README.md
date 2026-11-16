# Windows events captured for the Event Log reader

Real events, captured on 2026-11-16 on GitHub's hosted Windows machines by the throwaway script
`.github/probe/windows-events.ps1` (branch `probe/windows-events`, commit `5309a24`, workflow run
43776936291). The script made every one of these events itself on those disposable machines:
failed logons for made-up names, a test service that exits by itself, the Print Spooler killed
twice, and events written from a source nobody registered. The machine names, account names and
account identifiers in them belong to GitHub's machines, not to anyone's own computer.

Unless a file name says otherwise, the files come from the `windows-2025` image (Windows Server
2025, build 26100). The design, section 7.1 and section 9, says what was measured with them.

The files are kept byte for byte (see `.gitattributes`). Do not reformat them.

## One event per file, as the Event Log API returned its XML

| File | What it is |
|---|---|
| `security-4625-01.xml`, `-03.xml` | A failed logon, wrong password for an existing account (logon types 2 and 3) |
| `security-4625-05.xml`, `-09.xml` | A failed logon, unknown user (logon types 2 and 3) |
| `security-4624-01.xml` | A successful logon |
| `system-scm-7031-01.xml`, `-02.xml` | The Print Spooler died; Windows will restart it (counts 2 and 1) |
| `system-scm-7031-03.xml` | The test service died; Windows will restart it (count 6) |
| `system-scm-7034-01.xml`, `-02.xml` | The test service died; no action follows (counts 7 and 1) |
| `system-scm-7000-01.xml`, `-02.xml`, `system-scm-7009-01.xml` | A service that could not start |
| `system-scm-7036-01.xml` | A service changed state |
| `system-scm-7045-01.xml` | A service was installed |
| `application-probe-4242-04.xml` | Two unnamed values, no readable message |
| `application-probe-4243-03.xml` | One unnamed value, no readable message |
| `application-probe-4244-02.xml` | No values at all, no readable message |
| `application-probe-999-01.xml` | One unnamed value, written with `eventcreate`; it has a readable message |
| `application-probe-4245-awkward.windows-2022.xml` | Values holding a tab, a line feed, `& < > " '`, an escape character and a bell, Greek letters, a character outside the basic plane, and an empty value. On this image the escape and bell characters are in the XML as they were, which XML forbids |
| `application-probe-4245-awkward.windows-2025.xml`, `.windows-11-arm.xml` | The same event on the newer images, where Windows put an underscore in place of each forbidden character |

## The same events as `wevtutil` prints them

`wevtutil-security-4625-*.txt`, `wevtutil-system-scm-crash-*.txt` (a 7031) and
`wevtutil-application-4242-*.txt`: one event each, in the three formats `text`, `xml` and
`RenderedXml`. The `xml` form is what a person would pipe into aigolog on another system. The
`RenderedXml` form adds Windows' readable message; the `xml` form does not have it.

## Exported log files

`security-4625-since-start.evtx`, `system-scm-since-start.evtx` and
`application-probe-sources.evtx`: the script's own events from that job, saved with
`wevtutil epl`. They can be read only on Windows.
