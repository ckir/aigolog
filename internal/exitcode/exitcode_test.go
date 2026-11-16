// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package exitcode

import "testing"

func TestCodesAreTheOnesTheRoadmapFixes(t *testing.T) {
	got := []int{OK, Failure, Usage, ModelFailed, LogRefused, StorageUnavailable}
	for want, code := range got {
		if code != want {
			t.Errorf("code at position %d = %d, want %d", want, code, want)
		}
	}
}
