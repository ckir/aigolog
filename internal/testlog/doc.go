// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package testlog makes logs with a known truth, for tests and for
// measurements: an ssh log in the shapes of the LogHub sample, the log of a
// unit that restarts in a loop, and a web log in the common log format. Each
// generator takes a seed and a number of lines and returns the bytes and the
// truth about them.
//
// The logs are made by a seeded math/rand and a fixed clock, with no global
// state and no map iterated into an output, so the same arguments give the same
// bytes on every system. Lines end with a line feed. The truth is counted by the
// generator from the lines it writes, as it writes them; a test counts it
// again from the bytes, with code of its own.
//
// OpenSSH2k reads the real sshd sample, which is fetched and not committed.
package testlog

import "time"

// layouts of the time stamps the generators write.
const (
	syslogLayout = "Jan _2 15:04:05"
	clfLayout    = "02/Jan/2006:15:04:05 -0700"
)

// advance returns t moved on by n seconds; the caller draws n from its
// generator.
func advance(t time.Time, n int) time.Time {
	return t.Add(time.Duration(n) * time.Second)
}
