// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package testlog

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// RestartTruth is what a restart log holds, counted by the generator from the
// lines it wrote: the unit that restarts, how many times systemd scheduled a
// restart of it (the last counter), and whether the log ends with the unit
// given up ("Failed to start" was written) or still looping.
type RestartTruth struct {
	Unit     string
	Restarts int
	GaveUp   bool
}

// restartUnit is the unit of the loop.
const restartUnit = "app-worker.service"

// restartHost is the host of every line. It has no digit, so that it is a
// fixed token of every pattern.
const restartHost = "buildhost"

// restartStart is the time of the first line; the lines have no year.
var restartStart = time.Date(2026, time.November, 16, 8, 15, 22, 0, time.UTC)

// The steps of the script of the unit.
const (
	stepStarted = iota
	stepExited
	stepFailedResult
	stepScheduled
	stepRepeated
	stepFailedStart
)

// restartScript returns the steps of the unit, in order: cycles of started,
// exited, failed, scheduled, then the end of the log, cut to at most limit
// steps.
func restartScript(cycles int, gaveUp bool, limit int) []int {
	var steps []int
	for range cycles {
		steps = append(steps, stepStarted, stepExited, stepFailedResult, stepScheduled)
	}
	if gaveUp {
		steps = append(steps, stepRepeated, stepFailedStart)
	} else {
		steps = append(steps, stepStarted)
	}
	return steps[:min(limit, len(steps))]
}

// Restart makes the log of a unit that restarts in a loop, mixed with lines of
// other services (cron, dbus, systemd-logind), and its truth. The unit's lines
// are about three fifths of the log. The log ends, for the unit, with a last
// "Started" (still looping) or with "Start request repeated too quickly" and
// "Failed to start" (gave up), as the seed says; a log too short for its end is
// cut.
func Restart(seed int64, lines int) ([]byte, RestartTruth) {
	truth := RestartTruth{Unit: restartUnit}
	if lines <= 0 {
		return nil, truth
	}
	rng := rand.New(rand.NewSource(seed))
	gaveUp := rng.Intn(2) == 0
	tail := 1
	if gaveUp {
		tail = 2
	}
	cycles := max(0, (lines*3/5-tail)/4)
	script := restartScript(cycles, gaveUp, lines)
	isUnit := make([]bool, lines)
	for _, i := range rng.Perm(lines)[:len(script)] {
		isUnit[i] = true
	}

	var b strings.Builder
	now := restartStart
	next, counter, session := 0, 0, 0
	for no := 1; no <= lines; no++ {
		if no > 1 {
			now = advance(now, rng.Intn(5))
		}
		fmt.Fprintf(&b, "%s %s ", now.Format(syslogLayout), restartHost)
		if isUnit[no-1] {
			step := script[next]
			next++
			switch step {
			case stepStarted:
				fmt.Fprintf(&b, "systemd[1]: Started %s.\n", restartUnit)
			case stepExited:
				fmt.Fprintf(&b, "systemd[1]: %s: Main process exited, code=exited, status=1/FAILURE\n", restartUnit)
			case stepFailedResult:
				fmt.Fprintf(&b, "systemd[1]: %s: Failed with result 'exit-code'.\n", restartUnit)
			case stepScheduled:
				counter++
				truth.Restarts = counter
				fmt.Fprintf(&b, "systemd[1]: %s: Scheduled restart job, restart counter is at %d.\n", restartUnit, counter)
			case stepRepeated:
				fmt.Fprintf(&b, "systemd[1]: %s: Start request repeated too quickly.\n", restartUnit)
			case stepFailedStart:
				truth.GaveUp = true
				fmt.Fprintf(&b, "systemd[1]: Failed to start %s.\n", restartUnit)
			}
			continue
		}
		switch rng.Intn(7) {
		case 0:
			fmt.Fprintf(&b, "CRON[%d]: pam_unix(cron:session): session opened for user root by (uid=0)\n", 1000+rng.Intn(9000))
		case 1:
			fmt.Fprintf(&b, "CRON[%d]: pam_unix(cron:session): session closed for user root\n", 1000+rng.Intn(9000))
		case 2:
			fmt.Fprintf(&b, "dbus-daemon[612]: [system] Successfully activated service 'org.freedesktop.systemd1'\n")
		case 3:
			fmt.Fprintf(&b, "dbus-daemon[612]: [system] Activating via systemd: service name='org.freedesktop.hostname1' unit='dbus-org.freedesktop.hostname1.service'\n")
		case 4:
			session += 1 + rng.Intn(3)
			fmt.Fprintf(&b, "systemd-logind[701]: New session %d of user %s.\n", session, [...]string{"alice", "bob", "carol"}[rng.Intn(3)])
		case 5:
			fmt.Fprintf(&b, "systemd-logind[701]: Session %d logged out. Waiting for processes to exit.\n", 1+rng.Intn(session+1))
		default:
			fmt.Fprintf(&b, "systemd-logind[701]: Removed session %d.\n", 1+rng.Intn(session+1))
		}
	}
	return []byte(b.String()), truth
}
