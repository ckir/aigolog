// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

//go:build windows

package winlog

import (
	"slices"
	"testing"
)

func TestEventLogAPIIsReachable(t *testing.T) {
	if err := Available(); err != nil {
		t.Fatal(err)
	}
}

// TestEventLogProceduresAreTheNineTheReaderNeeds pins the list that
// TestEventLogAPIIsReachable walks: an emptied or shortened list would make
// Available succeed without looking for anything.
func TestEventLogProceduresAreTheNineTheReaderNeeds(t *testing.T) {
	want := []string{
		"EvtQuery",
		"EvtNext",
		"EvtRender",
		"EvtClose",
		"EvtFormatMessage",
		"EvtOpenPublisherMetadata",
		"EvtOpenEventMetadataEnum",
		"EvtNextEventMetadata",
		"EvtGetEventMetadataProperty",
	}
	if !slices.Equal(procedures, want) {
		t.Errorf("procedures = %q, want %q", procedures, want)
	}
}
