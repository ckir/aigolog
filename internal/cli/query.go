// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/query"
	reports "github.com/ckir/aigolog/internal/report"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/textsafe"
)

const queryUsage = `Usage: aigolog query --db PATH [--json] [--no-save] [--limit N] [--timeout DURATION] [--col NAME=SLOT[,SLOT...]]... [--where NAME=VALUE]... [--pattern ID[,ID...]] [--by NAME] [--with NAME] [--buckets N] [--last] VERB [WORD...]
       aigolog query --db PATH --answer ID [--json]`

// appender is a flag that may be given more than once; each value is read by
// parse and the results are kept in the order given. A plain string flag would
// keep only the last.
type appender[T any] struct {
	parse func(string) (T, error)
	items []T
}

func (a *appender[T]) String() string { return "" }

func (a *appender[T]) Set(s string) error {
	v, err := a.parse(s)
	if err != nil {
		return err
	}
	a.items = append(a.items, v)
	return nil
}

// splitOnce splits s at the first "=".
func splitOnce(s string) (name, value string, ok bool) {
	return strings.Cut(s, "=")
}

// parseColumn reads NAME=SLOT[,SLOT...]: the slots are positive integers.
func parseColumn(s string) (query.Column, error) {
	name, list, ok := splitOnce(s)
	if !ok || name == "" {
		return query.Column{}, errors.New("want NAME=SLOT[,SLOT...]")
	}
	c := query.Column{Name: name}
	for _, field := range strings.Split(list, ",") {
		n, err := strconv.ParseInt(field, 10, 64)
		if err != nil || n < 1 {
			return query.Column{}, errors.New("the slots of a column are whole numbers above 0, separated by commas")
		}
		c.Slots = append(c.Slots, n)
	}
	return c, nil
}

// parseFilter reads NAME=VALUE, splitting at the first "=".
func parseFilter(s string) (query.Filter, error) {
	name, value, ok := splitOnce(s)
	if !ok || name == "" {
		return query.Filter{}, errors.New("want NAME=VALUE")
	}
	return query.Filter{Column: name, Value: value}, nil
}

// parsePatterns reads ID[,ID...]: whole numbers, 0 and above.
func parsePatterns(s string) ([]int64, error) {
	var out []int64
	for _, field := range strings.Split(s, ",") {
		n, err := strconv.ParseInt(field, 10, 64)
		if err != nil || n < 0 {
			return nil, errors.New("the patterns are whole numbers, separated by commas")
		}
		out = append(out, n)
	}
	return out, nil
}

// takesNoArguments says whether v is a verb that has no words or value.
func takesNoArguments(v query.Verb) bool {
	switch v {
	case query.Slots, query.Count, query.Top, query.Pairs, query.Buckets, query.Rows:
		return true
	}
	return false
}

// queryArgs is what the command line of query says.
type queryArgs struct {
	db     string
	json   bool
	noSave bool
	// answer is the number of the stored answer to run again; it is 0 when the
	// command asks a question.
	answer   int64
	question query.Question
	limits   query.Limits
}

// parseQueryArgs reads the command line of query. Flags may come before,
// between or after the verb and its arguments, and -- ends them. When it
// returns ok false it has said why, and code is the exit code of the command.
func parseQueryArgs(env Env, args []string) (a queryArgs, code int, ok bool) {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	// The flag package would print its messages as they are, and they quote
	// what the user typed; they are printed here, through textsafe.Clean.
	fs.SetOutput(io.Discard)
	db := fs.String("db", "", "the database to ask")
	fs.BoolVar(&a.json, "json", false, "print the answer as one JSON document")
	fs.BoolVar(&a.noSave, "no-save", false, "do not store the answer")
	limit := fs.Int("limit", 0, "the most rows to show")
	timeout := fs.Duration("timeout", 0, "the time the question may take")
	cols := &appender[query.Column]{parse: parseColumn}
	fs.Var(cols, "col", "NAME=SLOT[,SLOT...]: a column made of slots (repeatable)")
	filters := &appender[query.Filter]{parse: parseFilter}
	fs.Var(filters, "where", "NAME=VALUE: keep the records whose column is the value (repeatable)")
	patterns := &appender[[]int64]{parse: parsePatterns}
	fs.Var(patterns, "pattern", "ID[,ID...]: the patterns whose records are used")
	by := fs.String("by", "", "the column counted")
	with := fs.String("with", "", "the column whose distinct values are counted")
	buckets := fs.Int("buckets", 0, "the number of buckets")
	last := fs.Bool("last", false, "the last records, not the first")
	answerID := fs.String("answer", "", "the id of a stored answer to run again")

	fail := func(msg string) (queryArgs, int, bool) {
		report(env.Stderr, msg)
		fmt.Fprintln(env.Stderr, queryUsage)
		return a, exitcode.Usage, false
	}

	var positional []string
	rest := args
	for {
		err := fs.Parse(rest)
		if err != nil {
			return fail(err.Error())
		}
		consumed := rest[:len(rest)-fs.NArg()]
		rest = fs.Args()
		if endedByDoubleDash(fs, consumed) {
			positional = append(positional, rest...)
			break
		}
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}

	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	a.db = *db
	if a.db == "" {
		return fail("query needs --db PATH, the database to ask")
	}
	a.limits = query.DefaultLimits()
	if given["timeout"] {
		if *timeout <= 0 {
			return fail("--timeout must be above 0")
		}
		a.limits.Time = *timeout
	}

	if given["answer"] {
		for _, name := range []string{"col", "where", "pattern", "by", "with", "buckets", "last", "limit"} {
			if given[name] {
				return fail("--answer runs a stored question again; --" + name + " belongs to a new question")
			}
		}
		if len(positional) > 0 {
			return fail("--answer runs a stored question again; it takes no verb and no arguments")
		}
		n, valid := query.ParseID(*answerID)
		if !valid {
			return fail(fmt.Sprintf("%q is not the id of an answer: want A1, A2, ...", *answerID))
		}
		a.answer = n
		return a, exitcode.OK, true
	}

	if len(positional) == 0 {
		return fail("query needs a verb: patterns, slots, count, top, pairs, buckets, rows or where")
	}
	q := query.Question{
		Format:  1,
		Verb:    query.Verb(positional[0]),
		Columns: cols.items,
		Filters: filters.items,
		By:      *by,
		With:    *with,
		Buckets: *buckets,
		Last:    *last,
		Limit:   *limit,
	}
	for _, p := range patterns.items {
		q.Patterns = append(q.Patterns, p...)
	}
	words := positional[1:]
	switch {
	case q.Verb == query.Patterns:
		q.Words = words
	case q.Verb == query.Where:
		if len(words) != 1 {
			return fail("the verb where takes one value")
		}
		q.Value = words[0]
	case takesNoArguments(q.Verb):
		if len(words) > 0 {
			return fail(fmt.Sprintf("the verb %s takes no arguments", q.Verb))
		}
	}
	if err := q.Validate(); err != nil {
		return fail(err.Error())
	}
	a.question = q
	return a, exitcode.OK, true
}

// queryReply is the document that query --json prints.
type queryReply struct {
	ID     string       `json:"id"`
	Stored bool         `json:"stored"`
	Rerun  bool         `json:"rerun,omitempty"`
	Answer query.Answer `json:"answer"`
}

// answerJSON is the form in which an answer is stored: its JSON on one line,
// with no HTML escaping, so that query.ParseAnswer reads it back.
func answerJSON(a query.Answer) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(a); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// queryCommand asks a question of a kept database, or runs a stored one again.
func queryCommand(env Env, args []string) int {
	a, code, ok := parseQueryArgs(env, args)
	if !ok {
		return code
	}
	ctx := env.Context
	if ctx == nil {
		var stop func()
		ctx, stop = interruptContext(env.Stderr)
		defer stop()
	}
	db, err := store.Open(ctx, a.db)
	if err != nil {
		report(env.Stderr, err.Error())
		return exitcode.Failure
	}
	// The database is opened for reading only. It is closed before the answer
	// is stored, which is a write through a connection of its own.
	closed := false
	closeDB := func() {
		if !closed {
			closed = true
			db.Close()
		}
	}
	defer closeDB()

	q, rerunOf := a.question, int64(0)
	if a.answer != 0 {
		rerunOf = a.answer
		stored, err := db.Answer(ctx, a.answer)
		if errors.Is(err, store.ErrNoAnswer) {
			report(env.Stderr, fmt.Sprintf("the database has no answer %s", query.ID(a.answer)))
			return exitcode.Usage
		}
		if err != nil {
			report(env.Stderr, err.Error())
			return exitcode.Failure
		}
		q, err = query.ParseQuestion(stored.Question)
		if err != nil {
			return questionFailed(env, ctx, err, a.limits, "the stored question of "+query.ID(a.answer)+" cannot be read: ")
		}
	}

	ans, err := db.Run(ctx, q, a.limits)
	if err != nil {
		return questionFailed(env, ctx, err, a.limits, "")
	}

	id, idText, stored := int64(0), "-", false
	if rerunOf != 0 {
		id, idText = rerunOf, query.ID(rerunOf)
	} else if !a.noSave {
		id, stored = saveAnswer(env, ctx, db, closeDB, q, ans)
		if stored {
			idText = query.ID(id)
		}
	}

	var out bytes.Buffer
	if a.json {
		err = reports.WriteValue(&out, queryReply{ID: idText, Stored: stored, Rerun: rerunOf != 0, Answer: ans})
	} else {
		if rerunOf != 0 {
			fmt.Fprintf(&out, "re-run of %s\n", idText)
		}
		out.WriteString(query.RenderText(idText, ans))
		if stored {
			// Only the path as it was typed and the id: no value from a log.
			fmt.Fprintf(&out, "re-run: aigolog query --db %s --answer %s\n", textsafe.Clean(a.db), idText)
		}
	}
	if err == nil {
		_, err = env.Stdout.Write(out.Bytes())
	}
	if err != nil {
		report(env.Stderr, "the answer could not be written: "+err.Error())
		return exitcode.Failure
	}
	return exitcode.OK
}

// questionFailed says why a question did not get an answer and returns the exit
// code: 2 for a question that is wrong, 1 for the others. prefix is put before
// the message of a question that is wrong.
func questionFailed(env Env, ctx context.Context, err error, lim query.Limits, prefix string) int {
	var ue *query.UsageError
	switch {
	case errors.As(err, &ue):
		report(env.Stderr, prefix+ue.Error())
		return exitcode.Usage
	case errors.Is(err, query.ErrTimeLimit):
		report(env.Stderr, fmt.Sprintf("the question ran out of time (%s)", lim.Time))
	case ctx.Err() != nil:
		report(env.Stderr, "interrupted")
	default:
		report(env.Stderr, err.Error())
	}
	return exitcode.Failure
}

// saveAnswer stores the answer with its question and returns its id. When it
// cannot, the answer is still printed: it says why on standard error and
// returns stored false.
func saveAnswer(env Env, ctx context.Context, db *store.DB, closeDB func(), q query.Question, ans query.Answer) (id int64, stored bool) {
	text, err := answerJSON(ans)
	if err == nil {
		closeDB()
		id, err = store.SaveAnswer(ctx, db.Path(), q.Canonical(), text, env.Now())
	}
	if err == nil {
		return id, true
	}
	reason := err.Error()
	switch {
	case errors.Is(err, store.ErrLocked):
		reason = "the database is in use by another process"
	case errors.Is(err, store.ErrNoLock):
		reason = "the database has no lock file"
	}
	report(env.Stderr, "note: the answer was not stored: "+reason)
	return 0, false
}
