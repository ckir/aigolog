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

// SSHTruth is what an ssh log holds, counted by the generator from the lines
// it wrote. The counts are those of the lines that hold "Failed password", in
// both of its shapes. The top address is the one with the most such lines (the
// smallest address when two have as many).
type SSHTruth struct {
	// FailedLines is the number of lines that hold "Failed password"; Addresses
	// and Names are the numbers of distinct addresses and distinct names in them.
	FailedLines, Addresses, Names int
	// AcceptedLine is the number, from 1, of the line that holds "Accepted ", or
	// 0 when the log has none (it has none when it has fewer than 2 lines).
	AcceptedLine int
	TopAddress   string
	// TopAttempts is the number of lines of the top address, TopNames the
	// distinct names it tried, TopOnRoot its lines on root, and TopFirstLine and
	// TopLastLine the numbers, from 1, of its first and last line.
	TopAttempts, TopNames, TopOnRoot, TopFirstLine, TopLastLine int
}

// sshHost is the host of every line, as in the LogHub sample; with the program
// it takes the first two tokens of the line, so the words that tell the shapes
// apart are the third and the fourth.
const sshHost = "LabSZ"

// sshStart is the time of the first line, as in the sample; the lines have no
// year.
var sshStart = time.Date(2026, time.December, 10, 7, 7, 38, 0, time.UTC)

// sshNames are the names of the attackers. The attacker with many names tries
// the first 40, the one with a few the last two, so that the names of the whole
// log are more than the names of the top attacker.
var sshNames = []string{
	"root", "admin", "administrator", "test", "guest", "user", "oracle", "postgres",
	"mysql", "ftp", "ftpuser", "git", "svn", "jenkins", "tomcat", "nagios",
	"zabbix", "ubuntu", "debian", "pi", "support", "info", "webmaster", "www",
	"apache", "nginx", "backup", "mail", "postfix", "hadoop", "kafka", "redis",
	"elastic", "docker", "vagrant", "deploy", "ansible", "puppet", "student", "teacher",
	"demo", "default", "operator", "service", "sysadmin", "dev", "developer", "samba",
}

// sshActor is an address and the names it tries.
type sshActor struct {
	addr  string
	names []string
}

// sshTally is what the generator counts about one address.
type sshTally struct {
	attempts, root, first, last int
	names                       map[string]bool
}

// sshActors returns the three attackers (many names, a few, in between) and the
// background addresses, all in the documentation ranges.
func sshActors(rng *rand.Rand) (attackers, background []sshActor) {
	octets := rng.Perm(254) // 0..253, distinct: the last byte is octet+1
	attackers = []sshActor{
		{"203.0.113." + fmt.Sprint(octets[0]+1), sshNames[:40]},
		{"203.0.113." + fmt.Sprint(octets[1]+1), sshNames[46:]},
		{"203.0.113." + fmt.Sprint(octets[2]+1), sshNames[4:16]},
	}
	bgNames := sshNames[:6:6]
	for i, n := range rng.Perm(254)[:20] {
		net := "198.51.100."
		if i%2 == 1 {
			net = "192.0.2."
		}
		background = append(background, sshActor{net + fmt.Sprint(n+1), bgNames})
	}
	return attackers, background
}

// SSH makes an ssh log of lines lines and its truth: syslog lines of sshd in
// the shapes of the LogHub sample, from three attackers and about 20
// background addresses, with one accepted login at line lines/2.
func SSH(seed int64, lines int) ([]byte, SSHTruth) {
	var truth SSHTruth
	if lines <= 0 {
		return nil, truth
	}
	rng := rand.New(rand.NewSource(seed))
	attackers, background := sshActors(rng)
	if lines >= 2 {
		truth.AcceptedLine = lines / 2
	}
	tallies := map[string]*sshTally{}
	allNames := map[string]bool{}
	var b strings.Builder
	now := sshStart
	for no := 1; no <= lines; no++ {
		if no > 1 {
			now = advance(now, rng.Intn(3))
		}
		fmt.Fprintf(&b, "%s %s sshd[%d]: ", now.Format(syslogLayout), sshHost, 20000+rng.Intn(9000))
		if no == truth.AcceptedLine {
			fmt.Fprintf(&b, "Accepted password for alice from %s port %d ssh2\n", background[0].addr, 1024+rng.Intn(60000))
			continue
		}
		var who sshActor
		switch r := rng.Intn(100); {
		case r < 30:
			who = attackers[0]
		case r < 42:
			who = attackers[1]
		case r < 60:
			who = attackers[2]
		default:
			who = background[rng.Intn(len(background))]
		}
		switch k := rng.Intn(100); {
		case k < 50: // Failed password, in its two shapes
			name := who.names[rng.Intn(len(who.names))]
			port := 1024 + rng.Intn(60000)
			if rng.Intn(100) < 40 {
				fmt.Fprintf(&b, "Failed password for %s from %s port %d ssh2\n", name, who.addr, port)
			} else {
				fmt.Fprintf(&b, "Failed password for invalid user %s from %s port %d ssh2\n", name, who.addr, port)
			}
			truth.FailedLines++
			t := tallies[who.addr]
			if t == nil {
				t = &sshTally{first: no, names: map[string]bool{}}
				tallies[who.addr] = t
			}
			t.attempts++
			t.last = no
			t.names[name] = true
			allNames[name] = true
			if name == "root" {
				t.root++
			}
		case k < 65:
			fmt.Fprintf(&b, "Invalid user %s from %s\n", who.names[rng.Intn(len(who.names))], who.addr)
		case k < 80:
			fmt.Fprintf(&b, "Received disconnect from %s: 11: Bye Bye [preauth]\n", who.addr)
		default:
			fmt.Fprintf(&b, "pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=%s\n", who.addr)
		}
	}
	truth.Addresses, truth.Names = len(tallies), len(allNames)
	// The map is read for a maximum with a rule that does not depend on the
	// order it is read in.
	for addr, t := range tallies {
		if t.attempts > truth.TopAttempts || (t.attempts == truth.TopAttempts && addr < truth.TopAddress) {
			truth.TopAddress, truth.TopAttempts = addr, t.attempts
			truth.TopNames, truth.TopOnRoot, truth.TopFirstLine, truth.TopLastLine = len(t.names), t.root, t.first, t.last
		}
	}
	return []byte(b.String()), truth
}
