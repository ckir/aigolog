// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

//go:build windows

package cli

import "github.com/ckir/aigolog/internal/winlog"

// platformParts returns the lines that version prints after the sqlite line.
func platformParts() []string {
	if err := winlog.Available(); err != nil {
		return []string{"eventlog api missing: " + err.Error()}
	}
	return []string{"eventlog api ok"}
}
