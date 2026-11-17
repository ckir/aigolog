# B2b: the questions, stored answers, `query`, `show` and the full-log measurement

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The eight questions of design 7.6 are answerable exactly over the miner's patterns and slot values, from `aigolog query`; every answer is stored with its question and can be re-run; `aigolog show` prints a run; the real sshd sample (A3) and the generated logs (A2) give their truth through the query tools; the first measurement on the full OpenSSH log (A6) is made and recorded.

**Architecture:** `internal/query` is pure data and text: a `Question`, an `Answer`, the limits, the canonical JSON of a question and the rendering of an answer. The SQL lives in `internal/store` (`DB.Run` dispatches on the verb and passes every value as a parameter). A column is a list of slot numbers treated as one (design 7.6, "slots from several patterns"). Layout 3 adds an `answers` table; `query` stores each answer by default through one short write guarded by the database's own lock file; `store.Open` stays read-only. `cmd/measure` gains `-log` and `-kind dense`.

**Tech Stack:** Go 1.26.0 floor (builds with 1.27.4), `modernc.org/sqlite` v1.62.0, `golang.org/x/sys`; standard library only otherwise.

**Spec:** `docs/superpowers/specs/2026-11-15-engine-and-report-design.md` (sections 7.4, 7.6, 7.11, 7.12; acceptance A2, A3, A6) and `docs/superpowers/plans/2026-11-16-part-1-roadmap.md` (row B2b, "Values the design leaves to measurement", "Decisions that hold for all of part 1"). The code it builds on is `main` at `c20280a` (B2a merged); every file and name below was read there.

## Global Constraints

- Go `1.26.0` in `go.mod`; two third-party modules only (`modernc.org/sqlite` v1.62.0, `golang.org/x/sys`); `CGO_ENABLED=0`; targets linux and windows, amd64 and arm64.
- The gate is `just check` (fmt-check, vet, vet-cross, license-check, test). Every new `.go` and `.sh` file starts with the licence header of `source-header.txt` (`just license-check` fails without it). CI runs the gate on ubuntu-24.04, ubuntu-24.04-arm, windows-2025, windows-11-arm, and the `floor` job runs `go test ./...` on Go 1.26.0.
- Exit codes: 0 done, 1 failed, 2 wrong usage, 5 the store cannot be written (`internal/exitcode`).
- Text from a log is untrusted: every cell and message that can hold log text goes through `textsafe.Clean` before a terminal; JSON goes through the encoder of `internal/report` (post-escape of DEL, C1 and bidi controls). Values from a log are passed to SQLite as parameters, never pasted into SQL text, and are never put into a command line the program prints (design 7.6, 7.4).
- Tests do not read `/proc`, do not need `sh`, do not depend on the machine's time zone or locale, and run on Windows.
- Commit messages end with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`. Commits are made on branch `b2b-questions`; nothing is pushed or merged without the owner's say.

## Decisions made in this plan (the owner accepted the forks on 2026-11-17; the rest are mine)

| # | Decision | Reason / what it costs if wrong |
|---|---|---|
| 1 | Answer storage: layout 3 adds table `answers`; `query` stores every answer by default (ids `A1`, `A2`, ...), `--no-save` opts out; the write is one short insert guarded by the lock file of the database (the protocol of `acquire` in `internal/store/retention.go:174`); when the lock is held, absent, or the file cannot be written the answer is printed with a "not stored" note and the exit code is 0; `--answer A3` re-runs the stored question read-only; `analyze` stores nothing as an answer (C makes the summary blocks answers). | Owner's choice after two rounds with the second model. A finished database is no longer byte-immutable. The write uses the default rollback journal, not `journal_mode=MEMORY` or `OFF` as `Create` does for the load: a crash in the middle of the insert must not be able to damage a finished database, and a folder that cannot hold the `-journal` file simply means "not stored". |
| 2 | Columns are ad hoc: `--col NAME=SLOT[,SLOT...]`, part of the stored question. | No new table; a column cannot be reused across questions except by retyping it. |
| 3 | Command: `aigolog query --db PATH VERB [flags]`, verbs `patterns slots count top pairs buckets rows where`; filters are equality, repeated `--where NAME=VALUE` are ANDed. | Owner's choice. Ranges, negation and prefixes are not offered. |
| 4 | Limits per answer: 100 rows, 64 KiB of cells, 5 seconds; provisional, `--limit` and `--timeout` change them for one run; Task 7 measures and replaces them. A question that runs out of time gives no figures. | Design 7.6; the second model verified that `context` cancellation interrupts a running statement in `modernc.org/sqlite` v1.62.0 (`sqlite.go:80-125`, `stmt.go:105`); Task 4b proves it with a test. |
| 5 | `show` prints the run's database line, the summary of the database, the stored answers and the line `model exchange: none recorded`. | C adds the exchange. |
| 6 | Full-log fetch recipe and `cmd/measure -log`, run on demand, not in CI. | The log is 70 MiB and LogHub's terms forbid keeping it in the repository. |
| 7 | **Ruling (mine): the miner's slot cap is a cap on the typed values of a MESSAGE (`MaxMessageValues = 64`) plus one on a token (`MaxTokenValues = 8`), applied in `Scan`, not a cap on a pattern's slots in `Finish`.** A cap in `Finish` would leave markers in the pattern text with no slot, and `Match` and `Learn` could disagree. With the cap at the source a pattern has at most 64 typed slots and at most 256 word slots (one for each wildcard token), so at most 320 slots and 320 `slot_values` rows a record, against about 13,000 before. | The owner approved "about 8 values a token and about 64 slots a pattern" (2026-11-17); this is the form of the second that keeps `Learn`, `Match` and the pattern text consistent. Cost if wrong: a pattern shows at most 64 typed values of a long message; later numbers stay as text. |
| 8 | Times in answers are `time.Unix(0, nanos).UTC()` formatted `2006-01-02 15:04:05`; the text rendering ends with the line `times: UTC for sources that state a zone, as written in the log for the others`. | The store keeps wall-clock nanoseconds for zone-less sources (B1); the summary's zone rules (R14) are for the summary only. |
| 9 | The first four tokens stay the tree's path; digit-free web paths (R10) and the UNC read-only URI stay open (TODO.md), unchanged by B2b. | Owner's ruling of 2026-11-17: later, with A5. |

## Review Focus

1. **A hostile value from a log reaches output or a stored question.** A name or address holding ESC, a bell, a bidi control, a NUL, a newline, `'`, `"` and `%`: it must print escaped by `textsafe.Clean` in text, be valid escaped JSON in `--json`, and never appear in a line that looks like a command (Task 3, Task 5).
2. **A filter value that is SQL.** `--where name=' OR 1=1 --` must match only a record whose value is exactly that text, and return no rows otherwise (Task 4a).
3. **A column that names a slot that does not exist, or a pattern that does not exist.** A usage error naming the number (exit 2), never an empty answer that looks like a real zero (Task 4a).
4. **Two `query` runs saving at once, and a sweep.** Ids are distinct, nothing is corrupted, a refused save is a note and exit 0, `clean` and the age sweep skip a database whose lock a save holds (Task 2).
5. **A question that runs out of time, or an answer larger than its cap.** The first prints no figures, exits 1 and stores nothing; the second says `cut` and how (Task 4b, Task 5).

## File structure

- Create `internal/query/{doc,question,answer,render}.go` and their tests: the model.
- Create `internal/store/{answers,ask,ask_filters,ask_basic,ask_more}.go` and tests: layout 3 answers, `DB.Run`, the SQL.
- Modify `internal/store/{schema,header,db}.go` (layout 3, `fileURI`), `internal/report/json.go` (`WriteValue`), `internal/miner/scan.go`, `internal/cli/{cli,env}.go`; create `internal/cli/{query,show}.go`.
- Create `internal/testlog/dense.go`; modify `cmd/genlog/main.go`, `cmd/measure/main.go`, `justfile`, `docs/measuring.md`; create `scripts/ci/fetch-openssh-full.sh`.
- Record in `docs/acceptance/b2b-questions.md`, `TODO.md`, the roadmap.

---

### Task 1: The miner's value caps and the number-dense log

**Files:**
- Modify: `internal/miner/scan.go` (constants and `Scan`, `scanToken` at `scan.go:91` and `scan.go:151`), `internal/miner/scan_test.go`, `internal/miner/miner_test.go`
- Create: `internal/testlog/dense.go`
- Modify: `internal/testlog/testlog_test.go`, `cmd/genlog/main.go` (kind `dense`; usage line at `main.go:21` and the switch at `main.go:55`), `cmd/measure/main.go` (usage at `main.go:40`, the kind check at `main.go:127`, `generate` at `main.go:453`), `cmd/measure/main_test.go`, `docs/measuring.md`

**Interfaces:**
- Produces: `miner.MaxTokenValues = 8` and `miner.MaxMessageValues = 64` (exported constants in the "Limits of the scanner" block); `Scan` keeps at most `MaxTokenValues` values in a token and `MaxMessageValues` in a message; once either is reached the rest of the token is copied as text, exactly as a marker that does not fit already stops masking (`scanToken`, "masking stops here"). `len(Token.Values)` still equals the markers in `Token.Text` (ruling R6). `testlog.Dense(seed int64, lines int) []byte`.
- Consumes: nothing new.

- [ ] **Step 1: Write the failing tests** in `scan_test.go`: `TestScanKeepsAtMostEightValuesInATokenAndCopiesTheRestAsText` (`Scan("1,2,3,4,5,6,7,8,9,10")` gives one token with 8 values and `Text == "<int>,<int>,<int>,<int>,<int>,<int>,<int>,<int>,9,10"`, `Raw` unchanged); `TestScanKeepsAtMostSixtyFourValuesInAMessage` (ten tokens `1,2,3,4,5,6,7,8`: the first eight tokens hold 8 values each, tokens 9 and 10 hold none and have `Text == Raw`; the total is 64). In `miner_test.go`: `TestNumberDenseLinesGiveFewSlotsAndMatchTheyAgree` (a local generator of 40 lines of 256 tokens with 30 to 42 comma-separated integers each and a digit-free fourth token; after `Learn` of every line, `Finish`, every pattern has at most `MaxMessageValues` slots whose type is not `Word` and at most `MaxMessageValues + MaxTokens` slots in all; `Match` of each line gives a pattern id other than 0 and `len(values) == len(patterns[id].Slots)`).
- [ ] **Step 2: Update the existing expectations that the cap changes.** The row with nine integers in `ipv6Cases` (`scan_test.go:64-65`, `1:2:3:4:5:6:7:8:9::`) now expects eight values and a text that ends `...<int>:9::`; find the reference masker used by `TestTheReferenceMaskerAgreesWithTheOracleTable` (`scan_test.go:302`) and give it the same two caps. Run `go test ./internal/miner` and see the new tests fail and the old ones fail only where the caps apply.
- [ ] **Step 3: Implement the caps.** `Scan(msg string) []Token` keeps a count of values left in the message and passes it to `scanToken(raw string, left int) Token`; in the loop of `scanToken`, after the `typ == Word` check and before the marker is written, stop masking when `len(tok.Values) >= MaxTokenValues || len(tok.Values) >= left`. Document both constants and update `internal/miner/doc.go` (the sentence on bounds).
- [ ] **Step 4: Run** `go test ./internal/miner ./internal/store ./internal/ingest ./internal/summary` and see every test pass (the sshd sample test is skipped when the sample is not fetched; run `just fetch-openssh` first).
- [ ] **Step 5: The generator.** `testlog.Dense(seed, lines)`: line `i` is `dense` + ` ` + word(i%3) + ` ` + word(i/3%100) + ` values ` + 252 further tokens (256 fields in all), each of `30 + rng.Intn(13)` integers of 1 to 6 digits joined by commas (word(k) is `k` written in base 26 with letters only, so the first four tokens hold no digit and each (i%3, i/3%100) pair is a cluster); no time stamp; same seed gives the same bytes. Test `TestDenseIsDeterministicAndMakesTwoHundredFiftySixTokenLines` (two calls equal, the first line has 256 fields, the first field is `dense`, a line holds no digit before the fifth field). Add kind `dense` to `cmd/genlog` and `cmd/measure` (usage text, validation, `generate`); a `cmd/measure` test `TestMeasureAcceptsTheDenseKind` through the existing stub of the program (follow the neighbouring tests of that file).
- [ ] **Step 6: Measure before and after (top level, no other work running; `docs/measuring.md` says how).** Check out the generator commit with the caps stashed or run on the commit before Step 3: `go run ./cmd/measure -kind dense -lines 300 -runs 2` and keep the output; then with the caps. Expected, as recorded facts and not bars: before, peak memory about 500 MB for 300 clusters and a database about 20 times the log (B2a record); after, a peak far below it and a ratio. Put both in `docs/acceptance/b2b-questions.md` (created here with a heading and this section). If the ratio after the caps is still above 6, say so; do not change the bar (owner's decision, `TODO.md`).
- [ ] **Step 7: Close the B2a TODO items** ("The miner's memory is bounded..." in `TODO.md`) with the measured facts; commit: `git add -A && git commit -m "Cap the typed values of a token and of a message in the scanner"`.

---

### Task 2: Layout 3 and the stored answers

**Files:**
- Modify: `internal/store/schema.go`, `internal/store/header.go` (`schemaNumber = 3`, `schemaVersion = "3"`; `readOnlyURI` becomes a call of a new `fileURI(path, mode string) string`, same behaviour), `internal/store/db_test.go` (the `"schema": "2"` expectation near `db_test.go:569` and the layout message test at `db_test.go:871-877`: Want is now 3), `internal/store/header_test.go`
- Create: `internal/store/answers.go`, `internal/store/answers_test.go` (package `store_test`)

**Interfaces:**
- Produces:
  - table `answers (id INTEGER PRIMARY KEY, question TEXT NOT NULL, answer TEXT NOT NULL, created TEXT NOT NULL)`, made empty by `Create` in the same transaction as the other tables;
  - `type StoredAnswer struct { ID int64; Question, Answer string; Created time.Time }`;
  - `var ErrLocked`, `var ErrNoLock`, `var ErrNoAnswer` (all `errors.New`);
  - `func SaveAnswer(ctx context.Context, path, question, answer string, now time.Time) (int64, error)`: takes the lock of the database at `path` with the protocol of `acquire` (`ErrLocked` when another holder has it, `ErrNoLock` when there is no lock file), opens a separate writable connection (`fileURI(abs, "rw")`, `PRAGMA busy_timeout=2000`), inserts one row, closes, releases the lock; any other error is returned wrapped;
  - `func (d *DB) Answers(ctx context.Context) ([]StoredAnswer, error)` (id order, never nil) and `func (d *DB) Answer(ctx context.Context, id int64) (StoredAnswer, error)` (`ErrNoAnswer`).
- Consumes: `acquire` and `release` (`retention.go:174`, `:213`), `DriverName`.

- [ ] **Step 1: Write the failing tests** (package `store_test`, helpers `newDB` in `db_test.go:30`): `TestAnAnswersTableIsMadeEmptyByCreate`; `TestSaveAnswerStoresTheQuestionAndTheAnswerAndGivesIncreasingIDs` (two saves give 1 and 2; `Answers` returns both in order with the stored texts and `Created` equal to `now` at second precision); `TestSaveAnswerIsRefusedWhileAnotherHolderHasTheLock` (a database that was marked complete and not closed holds its lock: `errors.Is(err, ErrLocked)` and no row); `TestSaveAnswerWithNoLockFileStoresNothing` (remove `<name>.lock` after `Close`: `ErrNoLock`); `TestSaveAnswerOfAReadOnlyFileOrFolderStoresNothing` (two cases: the `.db` at mode 0400, and the folder at mode 0500 with the `.db` writable; no row, a non-nil error that is neither `ErrLocked` nor `ErrNoLock`, and the `-journal` file is not left behind; skipped on Windows and when the process can write anyway); `TestTwoSavesAtOnceNeverGiveTheSameID` (eight goroutines: every result is a distinct id or `ErrLocked`, at least one id, and `len(Answers)` equals the number of ids); `TestASweepLeavesADatabaseWhoseLockTheSaveProtocolHolds` (in a second file, `answers_internal_test.go`, package `store`, because it calls `acquire` itself: take the lock of a finished database with `acquire(lockPath)`, run `Sweep(dir, Policy{All: true, Now: now})`, and see the database in `res.Running` and still on disk; `release` the lock, sweep again, and see it removed); `TestALayoutTwoDatabaseIsRefusedWithTheLayoutMessage` (update of the existing test: Want `3`).
- [ ] **Step 2: Run** `go test ./internal/store` and see them fail to compile or fail.
- [ ] **Step 3: Implement** the schema, the constants, `fileURI`, and `answers.go`. `Answer` and `Answers` use the read-only connection of `Open`; they must work on a database made by `Create` that has been marked complete.
- [ ] **Step 4: Run the gate** `just check`; the layout number change is expected to touch only the tests listed above (`grep -rn '"2"\|layout 2' internal --include=*_test.go` shows any other).
- [ ] **Step 5: Commit** `Make layout 3: an answers table and a guarded write to store an answer`.

---

### Task 3: The model of a question and an answer

**Files:**
- Create: `internal/query/doc.go`, `question.go`, `answer.go`, `render.go`, `question_test.go`, `answer_test.go`, `render_test.go`
- Modify: `internal/report/json.go` (extract the encoding of `WriteJSON` into `func WriteValue(w io.Writer, v any) error`; `WriteJSON(w, r)` calls it), `internal/report/json_test.go`

**Interfaces:**
- Produces (package `query`; it imports no other package of aigolog except `textsafe`):

```go
type Verb string
const ( Patterns Verb = "patterns"; Slots Verb = "slots"; Count Verb = "count"; Top Verb = "top"
        Pairs Verb = "pairs"; Buckets Verb = "buckets"; Rows Verb = "rows"; Where Verb = "where" )
type Column struct { Name string `json:"name"`; Slots []int64 `json:"slots"` }
type Filter struct { Column string `json:"column"`; Value string `json:"value"` }
type Question struct {
	Format   int      `json:"format"` // always 1
	Verb     Verb     `json:"verb"`
	Words    []string `json:"words,omitempty"`    // patterns
	Patterns []int64  `json:"patterns,omitempty"` // restrict the records (slots: exactly one)
	Columns  []Column `json:"columns,omitempty"`
	Filters  []Filter `json:"filters,omitempty"`
	By       string   `json:"by,omitempty"`    // top, pairs: the column counted
	With     string   `json:"with,omitempty"`  // pairs: the column whose distinct values are counted
	Value    string   `json:"value,omitempty"` // where
	Buckets  int      `json:"buckets,omitempty"`
	Last     bool     `json:"last,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}
type UsageError struct{ Msg string }          // Error() returns Msg
func (q Question) Validate() error             // *UsageError
func (q Question) Canonical() string           // json, no HTML escaping, field order as above
func ParseQuestion(s string) (Question, error) // DisallowUnknownFields, Format 1, then Validate
func ParseAnswer(s string) (Answer, error)     // reads what json encoding of an Answer wrote; Rows and Columns are never nil
type Limits struct { Rows, Bytes int; Time time.Duration }
func DefaultLimits() Limits                    // {100, 64 << 10, 5 * time.Second}
const MaxCellBytes = 512
var ErrTimeLimit = errors.New("the question ran out of time")
type Ref struct { Source, Line int64 }
type Facts struct {
	Rows       int64 `json:"rows"`
	HasTimes   bool  `json:"has_times"`
	FirstNanos int64 `json:"first_nanos"`
	LastNanos  int64 `json:"last_nanos"`
	NoTime     int64 `json:"no_time"` // records of the set with no resolved time
	First      *Ref  `json:"first,omitempty"` // first and last record of the set by reading order
	Last       *Ref  `json:"last,omitempty"`
}
type Answer struct {
	Question      Question   `json:"question"`
	Columns       []string   `json:"columns"`
	Rows          [][]string `json:"rows"` // never nil
	Total         int64      `json:"total"` // rows before the cut
	Cut           string     `json:"cut"`   // "", "rows" or "bytes"
	Facts         Facts      `json:"facts"`
	ElapsedMillis int64      `json:"elapsed_ms"`
}
func RenderText(id string, a Answer) string
func ID(n int64) string                        // "A" + n
func ParseID(s string) (int64, bool)           // "A3" or "3"
```

  Validation table, exact (a violation is a `*UsageError` that names the field): `patterns` needs 1 to 8 non-empty `Words`; `slots` needs exactly one `Patterns`; `count` takes `Patterns`, `Filters`; `top` needs `By`, takes `Patterns`, `Filters`, `Limit`; `pairs` needs `By` and `With`, takes `Patterns`, `Filters`, `Limit`; `buckets` takes `Patterns`, `Filters`, `Buckets` (1 to 200, default 24 when 0); `rows` takes `Patterns`, `Filters`, `Last`, `Limit`; `where` needs `Value` non-empty, takes `Limit`. Every other field set for a verb is an error. `Columns`: at most 8, unique non-empty names, each with 1 to 64 slot numbers greater than 0; `Filters`: at most 8, each naming a defined column; `By`, `With` name defined columns; `Patterns`: at most 100 numbers, none negative; `Limit` 0 to 1000.
- `RenderText` prints a header line `answer A3: <verb> ...` built only from the verb and the numbers (no log value), the question's values through `textsafe.Clean`, a tab-separated table with a header row, every cell cleaned, then one line each for `rows covered N`, the first and last time and span when `HasTimes`, the first and last position (`source S line L`), `cut: rows (showing R of T)` or `cut: bytes ...` when cut, `answered in N ms`, and the times line of decision 8.

- [ ] **Step 1: Write the failing tests:** `TestValidateAcceptsEveryVerbWithItsRequiredFields` (one valid question for each of the eight verbs); `TestValidateRefusesWhatTheTableForbids` (a table of at least twelve cases: a verb that is unknown, a field the verb does not take, two columns with one name, a filter that names no column, a column of 65 slots, a slot 0, 9 columns, `Buckets` 201, `Limit` 1001, `patterns` with no words, `slots` with two patterns, `where` with an empty value; each error names the field); `TestParseQuestionRefusesUnknownFieldsAndOtherFormats`; `TestCanonicalIsStableAndDoesNotEscapeHTML` (`Value: "<*>&"` stays as written; encoding twice gives the same text; `ParseQuestion(q.Canonical())` returns `q`); `TestRenderTextCleansEveryCellAndTheQuestion` (a value holding ESC `[31m`, U+202E, DEL and a newline renders with none of those bytes raw); `TestRenderTextSaysWhenAnswerWasCut`; `TestParseAnswerReturnsWhatWasEncodedAndNeverNilRows` (an answer with no rows encodes and parses back equal, `Rows` and `Columns` non-nil); `TestIDRoundTrips` (`ID(3) == "A3"`, `ParseID("A3")` and `ParseID("3")` give 3, `ParseID("A")`, `ParseID("A0")`, `ParseID("-1")` are false); in `report`: `TestWriteValueEscapesTheSameControlsAsWriteJSON` (a struct holding DEL, U+0085 and U+202E gives escapes only).
- [ ] **Step 2: Run** `go test ./internal/query ./internal/report` and see them fail.
- [ ] **Step 3: Implement** the types and functions.
- [ ] **Step 4: Run** `go test ./internal/query ./internal/report` and `just check`; commit `Add the model of a question and an answer`.

---

### Task 4a: Running the first four questions

**Files:**
- Create: `internal/store/ask.go`, `internal/store/ask_filters.go`, `internal/store/ask_basic.go`, `internal/store/ask_test.go` (package `store_test`)

**Interfaces:**
- Produces: `func (d *DB) Run(ctx context.Context, q query.Question, lim query.Limits) (query.Answer, error)`. It validates `q`; wraps `ctx` in `context.WithTimeout(ctx, lim.Time)` (no timeout when `lim.Time <= 0`); on `context.DeadlineExceeded` of its own timeout returns `query.ErrTimeLimit` and a zero `Answer`; on the caller's cancellation returns `ctx.Err()`; checks that every slot of every column and every pattern in `q` exists (`*query.UsageError` naming the number, for example `slot 99 does not exist`); sets `ElapsedMillis`; cuts the rows to `Question.Limit` when it is above 0 (it sets the cap, at most 1000 by `Validate`) and to `lim.Rows` only when `Question.Limit` is 0 (ruling R8; `slots` and `buckets` are bounded by constants and are never cut by rows), then drops trailing rows until the cells total at most `lim.Bytes` bytes, setting `Cut` and `Total`; cuts each cell at `query.MaxCellBytes` bytes on a UTF-8 boundary with a small helper `cutCell(s string) string` in `ask.go` (the store does not import the miner). It returns `ErrClosed` after `Close`.
- The records a question covers ("the record set"): `FROM records r WHERE` the `r.pattern IN (?,...)` clause when `Patterns` is set, and for each filter `EXISTS (SELECT 1 FROM slot_values f WHERE f.record = r.id AND f.slot IN (?,...) AND f.value = ?)`; every number and value is a parameter; placeholders are generated by a helper `placeholders(n int) string` in `ask_filters.go`.
- `Facts` for the verbs that cover a record set (`count`, `top`, `pairs`, `buckets`, `rows`): `Rows` = records in the set; `HasTimes`, `FirstNanos`, `LastNanos` = the least and greatest `t_nanos` among records with `t_state` resolved (`t_state = ` the integer value of `record.Resolved`, passed as a parameter); `First` and `Last` = `(source, line)` of the records with the least and greatest `id`; for an empty set (`Rows == 0`) `First` and `Last` are nil, `HasTimes` is false and the times are 0.
- Rows per verb (columns exactly): `patterns`: `pattern, records, text` for patterns whose `text` contains every word (`instr(text, ?) > 0`), ordered by `records` descending then `id`; `slots`: `slot, pos, type, values, distinct, min, max, top` from `DB.SlotStats(ctx, pattern, 5)` (`min` and `max` empty for non-numeric; `top` is `value (count)` joined by `, `); `count`: one row, column `rows`; `top`: `value, rows, share` for the values of column `By`, `rows` = `COUNT(DISTINCT record)`, ordered by rows descending then value in byte order (`ORDER BY n DESC, value`), `share` = rows divided by the set's `Facts.Rows`, formatted `%.4f`; `Total` = the number of distinct values.

- [ ] **Step 1: Write the failing tests** over `mined(t, body)` (`stats_test.go:65`) and `patternWith` (`stats_test.go:81`): `TestRunPatternsFindsPatternsByWords` (two words both required; a word that appears in no pattern gives no rows and `Total` 0); `TestRunSlotsReturnsTheStatisticsOfOnePattern`; `TestRunCountCountsThePatternsAndTheFiltersTogether` (two patterns, one filter on a column made of one slot of each pattern: the count is the sum, first and last position are the ends of the set); `TestRunTopCountsValuesOfAColumnOfSlotsOfSeveralPatterns` (a body in which the user name is in a different slot of two patterns, as in the sshd sample: one column of both slots gives the combined counts and `Total` the combined distinct values); `TestAFilterValueThatIsSQLMatchesOnlyItself` (a record whose value is `' OR 1=1 --` is found by exactly that value and a different filter value of the same shape returns no rows); `TestAColumnWithAnUnknownSlotIsAUsageErrorThatNamesTheSlot`; `TestAPatternThatDoesNotExistIsAUsageErrorThatNamesIt`; `TestRunCutsRowsAndSaysSo` (`Limits{Rows: 2}` on a column of five values: two rows, `Cut == "rows"`, `Total == 5`); `TestRunCutsBytesAndSaysSo`; `TestRunAfterCloseReturnsErrClosed`.
- [ ] **Step 2: Run** `go test ./internal/store -run 'TestRun|TestAFilter|TestAColumn|TestAPattern'` and see them fail.
- [ ] **Step 3: Implement** `Run` and the four verbs; the other four verbs return `query.UsageError{"verb not built yet"}` until Task 4b (the dispatch is a `switch` the next task completes).
- [ ] **Step 4: Run the tests and `just check`;** commit `Answer the first four questions of the design in the store`.

---

### Task 4b: The other four questions, and the time limit

**Files:**
- Create: `internal/store/ask_more.go`; modify `internal/store/ask.go` (the dispatch), `internal/store/ask_test.go`

**Interfaces:**
- Produces, rows per verb (columns exactly): `pairs`: `value, rows, distinct` where, for each value of column `By`, `rows` = `COUNT(DISTINCT a.record)` and `distinct` = `COUNT(DISTINCT b.value)` over the values of column `With` in the same records, from `slot_values a JOIN slot_values b ON b.record = a.record AND b.slot IN (...)` with `a.slot IN (...)` and the record-set conditions on `a.record`; ordered by `rows` descending then `value`; `Total` = the number of distinct values of `By`; `buckets`: `start, end, rows` over `Buckets` (default 24) equal intervals between the least and greatest resolved time of the set, the last interval closed on its right edge, records with no resolved time not counted (their number is in the footer line of `RenderText` as `no time: N`; put it in `Facts` as a field `NoTime int64 \`json:"no_time"\`` that Task 3 declares: add it there as part of this task if absent); `rows`: `id, source, line, time, text` in reading order (`ORDER BY r.id`, descending when `Last`), `text` is `raw` cut at `MaxCellBytes`, `source` is the source's name, `time` empty when unresolved; `where`: `pattern, slot, pos, type, rows, text` for every slot whose values equal `Value`, from `FROM slots s JOIN slot_values sv ON sv.slot = s.id AND sv.value = ?` (the join order lets the index `slot_values_by_slot (slot, value)` serve it) joined to `patterns`, ordered by `rows` descending then slot.
- The time limit: the statements run with the timed context, so `modernc.org/sqlite` interrupts them.

- [ ] **Step 1: Write the failing tests:** `TestRunPairsCountsRowsAndDistinctOtherValuesForEachValue` (the "names tried per address" case over two patterns: for one address, rows and distinct names equal values computed in the test from the body); `TestRunPairsRowsDoNotDoubleCountARecordWithTwoValuesInTheOtherColumn`; `TestRunBucketsCoversEveryTimedRecordOnce` (sum of buckets equals the timed records; the greatest time falls in the last bucket; `NoTime` counts the others); `TestRunRowsGivesTheFirstOrTheLastRecordsInOrderWithTheirLineNumbers` (`Last` true gives the last ones, in reading order descending, with the right `line`); `TestRunWhereFindsAValueInEverySlotThatHoldsIt` (the same address in two patterns' slots); `TestAQuestionThatRunsOutOfTimeGivesNoFiguresAndErrTimeLimit` (a database of 200,000 records written by a loop in the test with `Limits{Time: time.Millisecond}` and a `pairs` over two columns that must scan them: `errors.Is(err, query.ErrTimeLimit)`, the answer is the zero value, and the call returns within one second; if the machine is fast enough to finish in a millisecond the test grows the table until it does not, up to 2,000,000 records, and skips with a visible message past that); `TestACancelledContextReturnsItsError`.
- [ ] **Step 2: Run, implement, run** `go test ./internal/store` and `just check`.
- [ ] **Step 3: Commit** `Answer the other four questions and honour the time limit`.

---

### Task 5: `aigolog query` and `aigolog show`

**Files:**
- Create: `internal/cli/query.go`, `internal/cli/show.go`, `internal/cli/query_test.go` (package `cli`, with the harness `newHarness` of `commands_test.go:50`: `h.run(args...)`, `h.file`, `h.stdin`)
- Modify: `internal/cli/env.go` (cases `query` and `show` in the `switch` of `RunEnv` at `env.go:71`), `internal/cli/cli.go` (the `usage` text lists both commands), the usage tests that pin the text (`TestUsageTextIsPinned` in `cli_test.go`)

**Interfaces:**
- Consumes: `store.Open`, `DB.Run`, `store.SaveAnswer`, `DB.Answers`, `DB.Answer`, `query.*`, `report.WriteValue`, `summary.Build`/`summary.DefaultOptions`/`summary.Render` and `summary.RenderDigest` as `analyze` uses them (`analyze.go:230`, `:360`), `report`/`warn`/`say` helpers of `env.go`, `endedByDoubleDash` (`analyze.go:153`).
- Produces: the two commands.
  - Usage: `aigolog query --db PATH [--json] [--no-save] [--limit N] [--timeout DURATION] [--col NAME=SLOT[,SLOT...]]... [--where NAME=VALUE]... [--pattern ID[,ID...]] [--by NAME] [--with NAME] [--buckets N] [--last] VERB [WORD...]` and `aigolog query --db PATH --answer ID [--json]`. Flags and the verb may be mixed in any order and `--` ends the flags (the loop of `parseAnalyzeArgs`, `analyze.go:88-112`, is the model; write `parseQueryArgs` the same way). `patterns` takes its words as arguments; `where` takes its value as the one argument; `--where NAME=VALUE` splits on the first `=`. `--col` and `--where` are repeatable, so each is a small type that implements `flag.Value` and appends to a slice (a plain `flag.String` keeps only the last); the first positional argument is the verb and the rest are its arguments (the words of `patterns`, the value of `where`), so a word that starts with `-` needs `--` before it. With `--answer`, a verb, an argument or any question flag (`--col`, `--where`, `--pattern`, `--by`, `--with`, `--buckets`, `--last`, `--limit`) is a usage error. `--limit` sets `Question.Limit` and `--timeout` sets `Limits.Time`.
  - Exit codes: 0 answered (also when cut or not stored); 2 for flags, an invalid question (`*query.UsageError`), an unknown answer id, or a missing `--db`; 1 when the database cannot be opened (missing, incomplete, other version or layout: the message of `store.Open` through `report`), when the time limit is passed (message `the question ran out of time (5s)`) and for any other failure.
  - Output: `query.RenderText(query.ID(id), a)`, or with `--json` one document from `report.WriteValue` holding `{"id": "A3", "stored": true, "answer": {...}}`; when the save did not happen, `"stored": false` and, in text, standard error gets `aigolog: note: the answer was not stored: <reason>` (reason from `ErrLocked`, `ErrNoLock`, or the error, cleaned). The id of an answer that was not stored is `-` and is not a valid input. The text ends with `re-run: aigolog query --db <path> --answer A3` only when stored; `<path>` is the path as the user gave it, cleaned; no log value is ever in that line.
  - `--answer ID`: `DB.Answer`, `query.ParseQuestion`, `DB.Run` again with default limits, and nothing is stored; the output header says `re-run of A3`.
  - `show --db PATH`: prints the database line (`database: <path> (<size> bytes)`), the digest and the full summary as `analyze` prints them (`summary.Build` on the opened database), then `answers: N stored` and each stored answer through the same rendering (a stored answer is re-rendered from its stored JSON, not re-run), then `model exchange: none recorded`. Exit codes as above; `--json` is not offered (usage error).

- [ ] **Step 1: Write the failing tests** (`query_test.go`; build databases with `h.run("analyze", file)` and read the path from the `database:` line with `regexp.MustCompile(\`(?m)^database: (.+\.db) \(\`)` as `commands_test.go` does): `TestQueryRequiresADatabase` (exit 2); `TestQueryAnswersAndStoresByDefaultWithAnIdentifier` (`count` exits 0, prints `answer A1`, a second one prints `A2`, `show` lists both); `TestQueryNoSaveStoresNothing`; `TestQueryAnswerReRunsAStoredQuestionAndStoresNothing` (same table as the first answer, `re-run of A1`, `answers: 2 stored` unchanged); `TestQueryAnswerWithAnUnknownIdIsAUsageError`; `TestQueryJSONIsOneDocumentWithTheIdTheStoredFlagAndTheAnswer`; `TestQueryPrintsNoLogValueInTheRerunLine` (the database holds an address; no output line that starts `re-run:` contains it); `TestQueryCleansHostileValues` (a log whose value holds ESC `[31m`, a bell and U+202E: no raw control byte in text output; in `--json` none of DEL, C1 or bidi bytes raw); `TestQueryNotesThatAnAnswerWasNotStoredWhenTheLockIsHeld` (hold the lock by keeping the run open: use `store.Create`/`MarkComplete` without `Close` in the test; exit 0, the note on standard error, `stored` false); `TestQueryWithAnUnknownSlotIsExitTwoAndNamesTheSlot`; `TestQueryOnAnIncompleteDatabaseIsExitOne`; `TestQueryTimeLimitIsExitOneAndStoresNothing` (`--timeout 1ns` on a pairs question: exit 1, no row stored); `TestQueryRepeatedColAndWhereAccumulate` (two `--col` and two `--where` all reach the question); `TestQueryAnswerWithAVerbOrAQuestionFlagIsAUsageError`; `TestQueryMixesFlagsAndTheVerb` (flags before and after the verb, and `--` before a word that starts with `-`); `TestShowPrintsTheRunTheSummaryTheAnswersAndTheNoExchangeLine`; `TestShowRefusesJSON`; update the usage-text test for the two new lines.
- [ ] **Step 2: Run** `go test ./internal/cli -run 'TestQuery|TestShow'` and see them fail.
- [ ] **Step 3: Implement** both commands and the dispatch.
- [ ] **Step 4: Run** `just check`; commit `Add query and show`.

---

### Task 6: A2 and A3 through the query tools

**Files:**
- Create: `internal/cli/a3_test.go`, `internal/cli/queries_truth_test.go`

**Interfaces:**
- Consumes: `testlog.SSH`, `testlog.Web`, `testlog.Restart` and their truth structs (`SSHTruth`: `FailedLines, Addresses, Names, AcceptedLine, TopAddress, TopAttempts, TopNames, TopOnRoot, TopFirstLine, TopLastLine`; `WebTruth`: `Requests, DistinctPaths, Client, ClientRequests, ClientPaths`; `RestartTruth`: `Unit, Restarts, GaveUp`), `testlog.OpenSSH2k(t)` (skips with a notice when the sample is not fetched), the harness.
- Produces: a test helper `columnOf(t, h, db string, pattern int, typ string) int64` (runs `query slots --pattern N --json`, returns the slot number of the first slot of that type), and `patternWithText(t, h, db, words...) []int` (runs `query patterns WORD... --json`).

- [ ] **Step 1: Write `TestTheSshdSampleAnswersTheQuestionsOfA3`** (skipped without the sample; the log goes through a file and through standard input, as `ways` in `internal/ingest/truth_test.go` does; `--as-of 2026-12-31`): find the patterns of `Failed password` with `patterns Failed password`, build the column `names` from the word slots that follow `for` and `for invalid user` (found with `slots`), the column `addr` from the `ip4` slots, then assert through `query` with `--json`: `count --pattern <failed patterns>` gives 520; `top --by addr` has `Total` 23; `top --by names` has `Total` 63; `where 183.62.140.253` finds it in the `addr` slots; `count --where addr=183.62.140.253` gives 286, first line 1024, last line 1997; `pairs --by addr --with names` gives for that address `rows` 286 and `distinct` 10; `top --by names --where addr=183.62.140.253` gives `root` 276; `pairs` for `187.141.143.180` gives `rows` 80, `distinct` 28; the one `Accepted` record is `rows --pattern <accepted> --limit 1` at line 956. (The numbers are design A3 and the B2a record; if a pattern layout makes one of the columns need an extra slot, say so in the test's comment and use `slots` to find it, never a hard-coded slot number.)
- [ ] **Step 2: Write `TestGeneratedLogsGiveTheirTruthThroughTheQueryTools`** for ssh (seed 1, 3,000 lines), web (3,000) and restart (300): the same checks as `internal/ingest/truth_test.go` makes with SQL, now through `query` (ssh: `FailedLines`, `Addresses`, `Names`, `AcceptedLine`, `TopAddress`, `TopAttempts`, `TopNames`, `TopOnRoot`, `TopFirstLine`, `TopLastLine`; web: `Requests`, the client's `rows` and distinct paths through `pairs --by client --with path`, `DistinctPaths`; restart: the greatest value of the restart counter is `Restarts` by `slots` on its pattern, `Unit` appears in `where`, `GaveUp` is true exactly when a `Failed to start` pattern has records), each from a file and from standard input.
- [ ] **Step 3: Run** `just fetch-openssh && go test ./internal/cli -run 'TestTheSshd|TestGenerated' -v`, and see them pass; a figure that disagrees is a finding for the owner, not an edit of the expected value.
- [ ] **Step 4: Run `just check`; commit** `Prove A2 and A3 through the query tools`.

---

### Task 7: The first measurement on the full OpenSSH log (A6)

**Files:**
- Create: `scripts/ci/fetch-openssh-full.sh`
- Modify: `cmd/measure/main.go` (flag `-log PATH`), `cmd/measure/main_test.go`, `justfile` (recipes `fetch-openssh-full` and `measure-full`), `docs/measuring.md`, `.gitignore` (already ignores `testdata/fetched/`; check)

**Interfaces:**
- Produces: `scripts/ci/fetch-openssh-full.sh`: same shape as `scripts/ci/fetch-openssh.sh` (set -eu; download to a `.part` file; check; move; trap cleanup; a file already present with the right checksum is left alone). It downloads `https://zenodo.org/api/records/8196385/files/SSH.tar.gz/content` (4,596,666 bytes), requires the archive's SHA-256 to be `296610a35773f58b7c295e5407fc5ccdf48beb3c544fc14a6309addf904a2712` (its MD5 `a44e40b4348697dabf2bba56885e0a38` is what Zenodo's record states), extracts only the member `SSH.log` (73,417,506 bytes, 655,146 lines; `tar -xzf ... SSH.log` into a temporary folder), requires its SHA-256 to be `89eee9e36c182722f9fadc6e5617a4818ffc66109e7e222b8f9e8b78fc3aa2f3`, and moves it to `testdata/fetched/SSH_full.log`. All four values were measured on 2026-11-17 by downloading the file once.
- `cmd/measure -log PATH`: measures `aigolog analyze` on that file; `-kind`, `-lines`, `-seed`, `-bars` and `-small-lines` given together with `-log` are a usage error (exit 2); the line count is read from the file; the output says `log: <path>, <lines> lines` in place of the generated-log line; nothing else changes.

- [ ] **Step 1: Write the failing tests** in `cmd/measure/main_test.go`: `TestMeasureOfAGivenLogReportsItsLineCountAndRefusesGeneratorFlags` (a 10-line file through the stub program the neighbouring tests use: `lines: 10` in the figures, exit 0; `-log f -kind web` exits 2).
- [ ] **Step 2: Implement `-log`** and the recipes (`fetch-openssh-full: sh scripts/ci/fetch-openssh-full.sh`; `measure-full *args: go run ./cmd/measure -log testdata/fetched/SSH_full.log {{args}}`). Run the script under `dash` and `busybox sh` as B2a did for the other; run it twice (the second run says `ok (already present)`).
- [ ] **Step 3: Measure (top level, nothing else running; follow `docs/measuring.md` and the timing rules of the owner's standing instructions: two runs, the range, say what was not controlled).** `just fetch-openssh-full && just measure-full -runs 2`; the result goes into `docs/acceptance/b2b-questions.md` next to the B2a generated-log figures (655,146 lines: wall 101.72 to 107.84 s, peak 31.8 to 38.7 MiB, database 215.6 MiB for the generated log).
- [ ] **Step 4: Questions on the whole log.** Keep a database (`aigolog analyze testdata/fetched/SSH_full.log`, no `--no-keep`), then time on it, each as a stored answer printing `answered in N ms`: `patterns Failed password`, `count` with the failed-password patterns, `top --by addr`, `pairs --by addr --with names` (question 5), `buckets`, `rows --last --limit 10`, `where <the busiest address>`. A6 requires question 5 within its time limit: if it is not within 5 s on this log, say so, show the measured time, and set the provisional time limit and size cap of `query.DefaultLimits` from the measured figures (the largest answer, a safety factor of 4, the owner told in the final message); do not edit the limit silently.
- [ ] **Step 5: Spot-check against standard tools** and record both sides: `grep -c 'Failed password' testdata/fetched/SSH_full.log` (197,587 on 2026-11-17), `grep -c 'Accepted ' ...` (182), the distinct addresses with `grep 'Failed password' ... | grep -o '[0-9]\+\.[0-9]\+\.[0-9]\+\.[0-9]\+' | sort -u | wc -l`, the line of the first `Accepted `, and the figures of the same questions from `query`; the counts must be equal (the `Failed password` count equals the sum of `records` of the patterns that `patterns Failed password` finds only if no other pattern holds those words; say which). A disagreement is a finding: show it to the owner, do not adjust.
- [ ] **Step 6: Commit** `Measure the full OpenSSH log and the questions over it`.

---

### Task 8: The record, the tracker and the roadmap

**Files:**
- Modify: `docs/acceptance/b2b-questions.md` (finish it), `TODO.md`, `docs/superpowers/plans/2026-11-16-part-1-roadmap.md` (the rows "The size cap and the time limit of an answer" and "The total size limit of kept databases" get their measured values; B2b is marked built), `docs/measuring.md`, `docs/agy-capstone-ledger.md` and `docs/agy-test-audit-ledger.md` (rows added only after those disciplines ran)

- [ ] **Step 1: Finish the acceptance record**: what B2b built in order; the A3 figures and the A2 truth, each with the test that checks it; the A6 measurement (generated log from B2a next to the real log), the question timings, the spot-check table, the limits that were set and why; what the runs do not show (the Windows peak of the full log, the cold cache, a machine that is not idle, `readOnlyURI` and `fileURI` for UNC folders); the CI run ids once the branch is pushed.
- [ ] **Step 2: Update `TODO.md`:** open items B2b leaves (the digit-free web path, UNC, sub-page cut, mining CPU, the A10 tripwire of C stay), add items found while building; tick only what is done and name the part or a ruling.
- [ ] **Step 3: Run `just check`; commit** `Record B2b`. The AGY-CAPSTONE, the test audit and the push are not part of this plan's tasks: they follow the owner's standing rules once the final review is clean.

## Self-review (run when the plan was written)

- **Spec coverage:** 7.6's eight questions are Tasks 4a and 4b (patterns 1, slots 2, count 3, top 4, pairs 5, buckets 6, rows 7, where 8); "every answer gets an identifier and is stored with its question" Tasks 2 and 5; "states how many rows it covers", the derived facts, the size cap and the time limit Tasks 3, 4a, 4b; "can be re-run" Task 5 (`--answer`; `--finding` belongs to C); "a value from a log is never placed in a command line" Task 5; "the same tools are open to the user" Task 5; `show` Task 5; A2 and A3 Task 6; A6 Task 7; the first task's caps Task 1. Left to C by design: `--finding`, the exchange in `show`, Windows' words in the answer to question 2, the model-side tools.
- **Types:** `query.Question`, `Answer`, `Limits`, `Facts`, `Ref`, `UsageError`, `ErrTimeLimit` are defined in Task 3 and used with the same names in Tasks 4a, 4b and 5; `store.SaveAnswer`, `StoredAnswer`, `ErrLocked`, `ErrNoLock`, `ErrNoAnswer`, `DB.Answers`, `DB.Answer` come from Task 2; `DB.Run` from Task 4a. `Facts.NoTime` is declared in Task 3 and filled by `buckets` in Task 4b.
- **Values still provisional:** the answer limits (100 rows, 64 KiB, 5 s) until Task 7 measures them; the miner caps (8 and 64) until Task 1's measurement.
- **Review Focus:** items 1 to 5 each have a named test (`TestQueryCleansHostileValues`, `TestAFilterValueThatIsSQLMatchesOnlyItself`, `TestAColumnWithAnUnknownSlotIsAUsageErrorThatNamesTheSlot`, `TestTwoSavesAtOnceNeverGiveTheSameID`, `TestAQuestionThatRunsOutOfTimeGivesNoFiguresAndErrTimeLimit`).
- **Known weak points a reviewer should look at:** the A3 column discovery in Task 6 depends on the miner's patterns of the sample, which B2a measured (24 patterns, 63 names in two slots).
