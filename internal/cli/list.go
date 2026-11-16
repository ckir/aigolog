// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"fmt"
	"strconv"

	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/store"
	"github.com/ckir/aigolog/internal/textsafe"
)

// list prints one line for each database that is kept.
func list(env Env, args []string) int {
	_, dir, code, ok := noArguments(env, "list", args)
	if !ok {
		return code
	}
	infos, err := store.List(dir)
	if err != nil {
		report(env.Stderr, err.Error())
		return exitcode.Failure
	}
	if len(infos) == 0 {
		fmt.Fprintf(env.Stdout, "no databases are kept in %s\n", textsafe.Clean(dir))
		return exitcode.OK
	}
	for _, in := range infos {
		records, version := strconv.FormatInt(in.Records, 10), in.Version
		if in.State == "running" {
			records, version = "-", "-"
		}
		// Each field is cleaned, and Clean escapes a tab, so the line has six
		// fields whatever the path holds.
		fmt.Fprintf(env.Stdout, "%s\t%d\t%s\t%s\t%s\t%s\n",
			textsafe.Clean(in.Path), in.Size, in.Created.UTC().Format("2006-01-02T15:04:05Z"),
			textsafe.Clean(in.State), records, textsafe.Clean(version))
	}
	return exitcode.OK
}
