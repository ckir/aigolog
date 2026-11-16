# What the capture script printed on Windows, 2026-11-16

Evidence for the design's sections 7.1 and 9. Kept at the owner's request, because GitHub
deletes a run's files after five days.

Each folder is one run of `.github/probe/windows-events.ps1` (branch `probe/windows-events`)
on GitHub's hosted Windows machines, one sub-folder per machine image:

| Folder | Commit of the script | What that run added |
|---|---|---|
| `run-43775716340` | `2b25519` | Failed logons, failing services, unnamed values, messages, a standard user's rights, bounding a read |
| `run-43776936291` | `5309a24` | An administrator who is not elevated, a file written elevated, forbidden characters, an open query |
| `run-43786199757` | `23edb36` | Message patterns for kinds of event |

In each image's folder, `report.txt` is everything the script printed in that job. The first
run also has `survey-<log>.tsv` (the shapes of the newest events in each log) and the third has
`patterns-<log>.tsv` (whether a message pattern was found for each kind of event).

The script made its own test accounts, services and events on those disposable machines. The
machine names, account names and identifiers in these files belong to GitHub's machines. The
script never printed a password.

The files are kept byte for byte (see `.gitattributes`). A few lines hold raw control
characters, which is part of what was measured; open them in an editor rather than printing
them to a terminal. In the first two runs a display fault printed `{U+X4}` where a character
code belonged; the captured event files in `testdata/windows-events/` have the exact bytes.
