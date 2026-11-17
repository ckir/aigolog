// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

//go:build !windows

package main

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// openPeak is called right after the program has started. The peak memory of
// a finished process is in the resource usage that the system hands to Wait,
// so nothing is opened here and the reader needs nothing but the state.
func openPeak(*exec.Cmd) peakReader {
	return func(state *os.ProcessState) (uint64, bool) {
		if state == nil {
			return 0, false
		}
		usage, ok := state.SysUsage().(*syscall.Rusage)
		if !ok || usage == nil {
			return 0, false
		}
		// Maxrss is in kibibytes on Linux and the other Unix systems and in
		// bytes on darwin; rusageBytes knows which.
		return rusageBytes(int64(usage.Maxrss), runtime.GOOS)
	}
}

// resetPeakMemory is called just before the program is started, and reports
// whether the figure that openPeak reads will be the program's own.
//
// On Linux it will not be unless this is done. Go starts a child with a
// vfork-style clone that shares this process's memory until the exec, and the
// kernel keeps the larger of the old and the new memory's high-water marks as
// the child's maximum resident size (measured: a process that had held 300
// MiB and freed it got 310,224 KiB for /bin/true). This command holds the
// generated log in memory before it starts the program, so without the reset
// every figure would be at least the size of the log.
//
// Writing 5 to /proc/self/clear_refs sets the high-water mark to the present
// resident size. The caller has freed the log and returned the memory to the
// system first. The result is checked in /proc/self/status: if the mark is
// still well above the resident size the reset did not work (an old kernel, a
// restricted container) and the answer is false, so the report says "not
// measured" and never gives a floored figure.
//
// On the other Unix systems no such sharing is known and nothing is done; this
// has not been verified there.
func resetPeakMemory() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	f, err := os.OpenFile("/proc/self/clear_refs", os.O_WRONLY, 0)
	if err != nil {
		return false
	}
	_, werr := f.WriteString("5")
	cerr := f.Close()
	if werr != nil || cerr != nil {
		return false
	}
	hwm, ok1 := statusKiB("VmHWM")
	rss, ok2 := statusKiB("VmRSS")
	// 8 MiB of room for what this process allocates between the two reads.
	return ok1 && ok2 && hwm <= rss+8*1024
}

// statusKiB reads a size, in kibibytes, from /proc/self/status.
func statusKiB(key string) (int64, bool) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		rest, found := strings.CutPrefix(sc.Text(), key+":")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 2 || fields[1] != "kB" {
			return 0, false
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		return n, err == nil
	}
	return 0, false
}
