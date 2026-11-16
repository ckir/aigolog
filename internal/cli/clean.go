// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"fmt"

	"github.com/ckir/aigolog/internal/exitcode"
	"github.com/ckir/aigolog/internal/store"
)

// clean removes every database that no run is using.
func clean(env Env, args []string) int {
	_, dir, code, ok := noArguments(env, "clean", args)
	if !ok {
		return code
	}
	res, err := store.Sweep(dir, store.Policy{All: true, Now: env.Now()})
	var bytes int64
	for _, in := range res.Removed {
		bytes += in.Size
	}
	fmt.Fprintf(env.Stdout, "removed %d databases, %d bytes; running %d\n", len(res.Removed), bytes, len(res.Running))
	code = exitcode.OK
	for _, e := range splitErrors(err) {
		report(env.Stderr, e.Error())
		code = exitcode.Failure
	}
	return code
}
