// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

//go:build windows

package winlog

import (
	"errors"
	"slices"
	"strings"
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

// TestAvailableNamesTheFirstMissingProcedure: the finder fails for the third
// name; the error names it, does not name the fourth, and the finder was not
// asked about the fourth.
func TestAvailableNamesTheFirstMissingProcedure(t *testing.T) {
	names := []string{"First", "Second", "Third", "Fourth"}
	var asked []string
	find := func(name string) error {
		asked = append(asked, name)
		if name == "Third" {
			return errors.New("not found")
		}
		return nil
	}
	err := available(find, names)
	if err == nil {
		t.Fatal("available = nil, want an error")
	}
	if !strings.Contains(err.Error(), "Third") {
		t.Errorf("error %q does not name Third", err)
	}
	if strings.Contains(err.Error(), "Fourth") {
		t.Errorf("error %q names Fourth", err)
	}
	if !slices.Equal(asked, names[:3]) {
		t.Errorf("finder was asked %q, want %q", asked, names[:3])
	}
}

func TestAvailableReturnsNilWhenEveryProcedureIsFound(t *testing.T) {
	names := []string{"First", "Second", "Third"}
	calls := 0
	find := func(string) error {
		calls++
		return nil
	}
	if err := available(find, names); err != nil {
		t.Fatalf("available = %v, want nil", err)
	}
	if calls != len(names) {
		t.Errorf("finder called %d times, want %d", calls, len(names))
	}
}

func TestAvailableWrapsTheFindersError(t *testing.T) {
	sentinel := errors.New("sentinel")
	err := available(func(string) error { return sentinel }, []string{"Only"})
	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is(%v, sentinel) = false, want true", err)
	}
}
