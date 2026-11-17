// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package cli

import (
	"errors"
	"slices"
	"testing"
)

func swapEventLogAvailable(t *testing.T, f func() error) {
	t.Helper()
	old := eventLogAvailable
	t.Cleanup(func() { eventLogAvailable = old })
	eventLogAvailable = f
}

func TestPlatformPartsSaysTheEventLogAPIIsMissingAndWhy(t *testing.T) {
	swapEventLogAvailable(t, func() error { return errors.New("boom") })
	got := platformParts()
	want := []string{"eventlog api missing: boom"}
	if !slices.Equal(got, want) {
		t.Errorf("platformParts() = %q, want %q", got, want)
	}
}

func TestPlatformPartsSaysTheEventLogAPIIsOK(t *testing.T) {
	swapEventLogAvailable(t, func() error { return nil })
	got := platformParts()
	want := []string{"eventlog api ok"}
	if !slices.Equal(got, want) {
		t.Errorf("platformParts() = %q, want %q", got, want)
	}
}
