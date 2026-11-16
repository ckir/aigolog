// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

//go:build !windows

package cli

// platformParts returns the lines that version prints after the sqlite line.
// Off Windows there are none.
func platformParts() []string {
	return nil
}
