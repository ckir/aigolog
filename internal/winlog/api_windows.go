// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

//go:build windows

package winlog

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// wevtapi is the library that holds the Windows Event Log API.
var wevtapi = windows.NewLazySystemDLL("wevtapi.dll")

// procedures lists every procedure of wevtapi.dll that the reader will call.
var procedures = []string{
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

// Available reports whether wevtapi.dll loads and holds every procedure the
// reader will call. It finds the procedures and calls none of them. The error
// names the first procedure that is missing.
func Available() error {
	for _, name := range procedures {
		if err := wevtapi.NewProc(name).Find(); err != nil {
			return fmt.Errorf("wevtapi.dll: %s: %w", name, err)
		}
	}
	return nil
}
