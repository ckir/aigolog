// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

//go:build windows

package main

import (
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// psapi is the library that holds GetProcessMemoryInfo. golang.org/x/sys/windows
// has no wrapper for it, so it is loaded the way winlog loads wevtapi.dll.
var psapi = windows.NewLazySystemDLL("psapi.dll")

var procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")

// processMemoryCounters is PROCESS_MEMORY_COUNTERS of psapi.h: two DWORDs and
// eight SIZE_Ts, in this order.
type processMemoryCounters struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// openPeak is called right after the program has started. It opens a handle
// to the process; the handle stays valid after the process has ended, and the
// reader reads PeakWorkingSetSize through it, then closes it. When the handle
// cannot be opened the reader says that there is no figure.
func openPeak(cmd *exec.Cmd) peakReader {
	if cmd == nil || cmd.Process == nil {
		return func(*os.ProcessState) (uint64, bool) { return 0, false }
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(cmd.Process.Pid))
	return func(*os.ProcessState) (uint64, bool) {
		if err != nil {
			return 0, false
		}
		defer windows.CloseHandle(handle)
		// LazyProc.Call panics when the library or the procedure is missing;
		// a system that lacks it has no figure.
		if procGetProcessMemoryInfo.Find() != nil {
			return 0, false
		}
		var counters processMemoryCounters
		counters.cb = uint32(unsafe.Sizeof(counters))
		r, _, _ := procGetProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.cb))
		if r == 0 || counters.PeakWorkingSetSize == 0 {
			return 0, false
		}
		return uint64(counters.PeakWorkingSetSize), true
	}
}

// resetPeakMemory is called just before the program is started. On Windows the
// peak is read from a handle to the child, which has no memory of this
// process, so there is nothing to reset and the figure can be read.
func resetPeakMemory() bool { return true }
