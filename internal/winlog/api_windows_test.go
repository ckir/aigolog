// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

//go:build windows

package winlog

import "testing"

func TestEventLogAPIIsReachable(t *testing.T) {
	if err := Available(); err != nil {
		t.Fatal(err)
	}
}
