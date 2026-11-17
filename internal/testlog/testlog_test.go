// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package testlog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// generators are the three logs, by the name of their kind.
var generators = []struct {
	kind string
	make func(seed int64, lines int) []byte
}{
	{"ssh", func(seed int64, lines int) []byte { b, _ := SSH(seed, lines); return b }},
	{"restart", func(seed int64, lines int) []byte { b, _ := Restart(seed, lines); return b }},
	{"web", func(seed int64, lines int) []byte { b, _ := Web(seed, lines); return b }},
}

// splitLines returns the lines of data, without their line ends. It fails the
// test when data does not end in a line end.
func splitLines(t *testing.T, data []byte) []string {
	t.Helper()
	if len(data) == 0 {
		return nil
	}
	if data[len(data)-1] != '\n' {
		t.Fatalf("the log does not end with a line feed: %q", data[max(0, len(data)-20):])
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestEachGeneratorGivesTheSameBytesForTheSameSeedAndOtherBytesForAnotherSeed(t *testing.T) {
	for _, g := range generators {
		t.Run(g.kind, func(t *testing.T) {
			a, b := g.make(42, 500), g.make(42, 500)
			if !bytes.Equal(a, b) {
				t.Error("the same seed gave other bytes")
			}
			if c := g.make(43, 500); bytes.Equal(a, c) {
				t.Error("the same bytes for another seed")
			}
			// The prefix property is not promised, but the number of lines is.
			if d := g.make(42, 501); bytes.Equal(a, d) {
				t.Error("the same bytes for another number of lines")
			}
		})
	}
}

func TestEachGeneratorGivesExactlyTheRequestedNumberOfLines(t *testing.T) {
	for _, g := range generators {
		for _, n := range []int{0, 1, 2, 3, 5, 7, 10, 100, 1000} {
			t.Run(fmt.Sprintf("%s/%d", g.kind, n), func(t *testing.T) {
				data := g.make(5, n)
				if got := bytes.Count(data, []byte{'\n'}); got != n {
					t.Fatalf("%d line feeds, want %d", got, n)
				}
				if bytes.IndexByte(data, '\r') >= 0 {
					t.Error("the log holds a carriage return")
				}
				for i, l := range splitLines(t, data) {
					if strings.TrimSpace(l) == "" || l != strings.TrimRight(l, " ") || l[0] == ' ' {
						t.Fatalf("line %d is empty or has white space at an end: %q", i+1, l)
					}
				}
			})
		}
		if data := g.make(5, -3); len(data) != 0 {
			t.Errorf("%s: a negative number of lines gave %d bytes", g.kind, len(data))
		}
	}
}

// sshFacts counts the truth of an ssh log from its bytes, with strings and
// maps only. It shares no code with the generator.
func sshFacts(t *testing.T, data []byte) SSHTruth {
	t.Helper()
	type who struct {
		attempts, root, first, last int
		names                       map[string]bool
	}
	var want SSHTruth
	byAddr := map[string]*who{}
	allNames := map[string]bool{}
	for i, l := range splitLines(t, data) {
		no := i + 1
		if strings.Contains(l, "Accepted ") {
			if want.AcceptedLine != 0 {
				t.Fatalf("two lines hold Accepted: %d and %d", want.AcceptedLine, no)
			}
			want.AcceptedLine = no
		}
		_, rest, found := strings.Cut(l, "Failed password for ")
		if !found {
			continue
		}
		want.FailedLines++
		rest = strings.TrimPrefix(rest, "invalid user ")
		name, rest, ok := strings.Cut(rest, " from ")
		if !ok {
			t.Fatalf("line %d has no from: %q", no, l)
		}
		addr := strings.Fields(rest)[0]
		w := byAddr[addr]
		if w == nil {
			w = &who{first: no, names: map[string]bool{}}
			byAddr[addr] = w
		}
		w.attempts++
		w.last = no
		w.names[name] = true
		allNames[name] = true
		if name == "root" {
			w.root++
		}
	}
	want.Addresses, want.Names = len(byAddr), len(allNames)
	for addr, w := range byAddr {
		if w.attempts > want.TopAttempts || (w.attempts == want.TopAttempts && addr < want.TopAddress) {
			want.TopAddress, want.TopAttempts = addr, w.attempts
			want.TopNames, want.TopOnRoot, want.TopFirstLine, want.TopLastLine = len(w.names), w.root, w.first, w.last
		}
	}
	return want
}

func TestSSHTruthEqualsAnIndependentCount(t *testing.T) {
	for _, tc := range []struct {
		seed  int64
		lines int
	}{{1, 3000}, {2, 3000}, {99, 700}, {3, 1}, {3, 2}, {3, 3}, {4, 10}, {5, 0}} {
		t.Run(fmt.Sprintf("seed%d/%d", tc.seed, tc.lines), func(t *testing.T) {
			data, got := SSH(tc.seed, tc.lines)
			want := sshFacts(t, data)
			if got != want {
				t.Fatalf("truth = %+v\n count = %+v", got, want)
			}
			if tc.lines >= 2 && want.AcceptedLine != tc.lines/2 {
				t.Errorf("the Accepted line is %d, want line %d", want.AcceptedLine, tc.lines/2)
			}
			if tc.lines < 2 && want.AcceptedLine != 0 {
				t.Errorf("a log of %d lines holds an Accepted line", tc.lines)
			}
		})
	}
}

func TestSSHLogHasTheShapesOfTheSampleAndATopAttacker(t *testing.T) {
	data, truth := SSH(1, 3000)
	lines := splitLines(t, data)
	for _, l := range lines {
		if !strings.Contains(l, " LabSZ sshd[") {
			t.Fatalf("a line is not in the shape of the sample: %q", l)
		}
	}
	for _, shape := range []string{
		"Failed password for invalid user ", "Failed password for root from ", "Invalid user ",
		"Received disconnect from ", ": 11: Bye Bye [preauth]", "pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=",
	} {
		if !strings.Contains(string(data), shape) {
			t.Errorf("no line holds %q", shape)
		}
	}
	if !strings.HasPrefix(lines[0], "Dec 10 07:07:38 LabSZ sshd[") {
		t.Errorf("the first line is %q", lines[0])
	}
	if truth.Addresses < 15 || truth.Addresses > 23 {
		t.Errorf("%d addresses, want the three attackers and about 20 more", truth.Addresses)
	}
	if truth.TopNames < 20 || truth.TopOnRoot == 0 || truth.TopAttempts < 100 {
		t.Errorf("the top attacker is %+v: want many names, some on root, many attempts", truth)
	}
	for _, l := range lines {
		for _, a := range strings.Fields(l) {
			if ip := strings.TrimSuffix(strings.TrimPrefix(a, "rhost="), ":"); strings.Count(ip, ".") == 3 {
				if !strings.HasPrefix(ip, "203.0.113.") && !strings.HasPrefix(ip, "198.51.100.") && !strings.HasPrefix(ip, "192.0.2.") {
					t.Fatalf("an address outside the documentation ranges: %q", l)
				}
			}
		}
	}
}

func TestRestartTruthEqualsAnIndependentCount(t *testing.T) {
	var gaveUp, looping int
	for seed := int64(1); seed <= 12; seed++ {
		for _, n := range []int{1000, 40, 7, 3, 1} {
			t.Run(fmt.Sprintf("seed%d/%d", seed, n), func(t *testing.T) {
				data, got := Restart(seed, n)
				lines := splitLines(t, data)
				var unit []string
				want := RestartTruth{Unit: "app-worker.service"}
				for _, l := range lines {
					if !strings.Contains(l, "app-worker.service") {
						continue
					}
					unit = append(unit, l)
					if strings.Contains(l, "Scheduled restart job") {
						want.Restarts++
						if !strings.HasSuffix(l, "restart counter is at "+strconv.Itoa(want.Restarts)+".") {
							t.Fatalf("restart %d has the line %q", want.Restarts, l)
						}
					}
					if strings.Contains(l, "Failed to start app-worker.service") {
						want.GaveUp = true
					}
				}
				if got != want {
					t.Fatalf("truth = %+v, count = %+v", got, want)
				}
				if n == 1000 {
					if want.GaveUp {
						gaveUp++
						if !strings.Contains(unit[len(unit)-1], "Failed to start") || !strings.Contains(unit[len(unit)-2], "Start request repeated too quickly") {
							t.Errorf("the log gave up, but its last unit lines are %q", unit[len(unit)-2:])
						}
					} else {
						looping++
						if !strings.HasSuffix(unit[len(unit)-1], "Started app-worker.service.") {
							t.Errorf("the log is still looping, but its last unit line is %q", unit[len(unit)-1])
						}
						if strings.Contains(string(data), "Start request repeated too quickly") {
							t.Error("the log is still looping, but holds the line of a unit that gave up")
						}
					}
					for _, other := range []string{" CRON[", "dbus-daemon[", "systemd-logind["} {
						if !strings.Contains(string(data), other) {
							t.Errorf("no line of %q", other)
						}
					}
					if want.Restarts < 50 {
						t.Errorf("%d restarts in 1000 lines", want.Restarts)
					}
				}
			})
		}
	}
	if gaveUp == 0 || looping == 0 {
		t.Errorf("over 12 seeds %d logs gave up and %d were still looping: want both", gaveUp, looping)
	}
}

// webFacts counts the truth of a web log from its bytes, with strings and
// maps only.
func webFacts(t *testing.T, data []byte) WebTruth {
	t.Helper()
	type who struct {
		n     int
		paths map[string]bool
	}
	var want WebTruth
	clients := map[string]*who{}
	paths := map[string]bool{}
	for i, l := range splitLines(t, data) {
		f := strings.Fields(l)
		if len(f) != 8 || f[1] != "+0000" || f[3] != `"GET` || f[5] != `HTTP/1.1"` {
			t.Fatalf("line %d is not in the shape of the common log format: %q", i+1, l)
		}
		want.Requests++
		w := clients[f[2]]
		if w == nil {
			w = &who{paths: map[string]bool{}}
			clients[f[2]] = w
		}
		w.n++
		w.paths[f[4]] = true
		paths[f[4]] = true
	}
	want.DistinctPaths = len(paths)
	for c, w := range clients {
		if w.n > want.ClientRequests || (w.n == want.ClientRequests && c < want.Client) {
			want.Client, want.ClientRequests, want.ClientPaths = c, w.n, len(w.paths)
		}
	}
	return want
}

func TestWebTruthEqualsAnIndependentCount(t *testing.T) {
	for _, tc := range []struct {
		seed  int64
		lines int
	}{{1, 3000}, {2, 3000}, {9, 400}, {3, 1}, {3, 2}, {4, 10}, {5, 0}} {
		t.Run(fmt.Sprintf("seed%d/%d", tc.seed, tc.lines), func(t *testing.T) {
			data, got := Web(tc.seed, tc.lines)
			want := webFacts(t, data)
			if got != want {
				t.Fatalf("truth = %+v\n count = %+v", got, want)
			}
			if tc.lines == 3000 {
				if !strings.HasPrefix(want.Client, "203.0.113.") || want.ClientPaths < 500 || want.ClientRequests < 1000 {
					t.Errorf("the top client is %+v: want the probing client, with many requests and many paths", want)
				}
				if want.DistinctPaths-want.ClientPaths > 20 {
					t.Errorf("the other clients use %d paths, want a few", want.DistinctPaths-want.ClientPaths)
				}
			}
		})
	}
}

// fakeT stands for a *testing.T that a test of the helper can survive: Skip
// and Fatalf record what they were given and end the goroutine, as the real
// ones do.
type fakeT struct {
	testing.TB
	skipped, failed bool
	text            string
}

func (f *fakeT) Helper() {}

func (f *fakeT) Skip(args ...any) {
	f.skipped, f.text = true, fmt.Sprint(args...)
	runtime.Goexit()
}

func (f *fakeT) Fatalf(format string, args ...any) {
	f.failed, f.text = true, fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// runFake runs fn in its own goroutine, so that Goexit ends only that.
func runFake(fn func(f *fakeT)) *fakeT {
	f := &fakeT{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(f)
	}()
	<-done
	return f
}

// fetchedRoot makes a folder with a go.mod, and the sample when body is not
// nil, and returns it.
func fetchedRoot(t *testing.T, body []byte) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body != nil {
		dir := filepath.Join(root, "testdata", "fetched")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "OpenSSH_2k.log"), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestTheSampleChecksumIsTheOneOfTheBrief(t *testing.T) {
	if sshdSHA256 != "1e4912727fa88245113d41b16a0cd25ceadba7f931e1c406542885b91254264f" {
		t.Errorf("the checksum is %s", sshdSHA256)
	}
}

func TestAnAbsentSampleSkipsTheTestWithTheNotice(t *testing.T) {
	root := fetchedRoot(t, nil)
	f := runFake(func(f *fakeT) { readFetched(f, root, sshdSHA256) })
	if !f.skipped || f.failed {
		t.Fatalf("skipped = %v, failed = %v", f.skipped, f.failed)
	}
	if want := `the sshd sample is not fetched: run "just fetch-openssh"`; f.text != want {
		t.Errorf("the notice is %q, want %q", f.text, want)
	}
}

func TestAPresentSampleIsReturnedWhenItsChecksumMatches(t *testing.T) {
	body := []byte("Dec 10 06:55:46 LabSZ sshd[24200]: reverse mapping checking getaddrinfo failed\n")
	sum := sha256.Sum256(body)
	root := fetchedRoot(t, body)
	var got []byte
	f := runFake(func(f *fakeT) { got = readFetched(f, root, hex.EncodeToString(sum[:])) })
	if f.skipped || f.failed {
		t.Fatalf("skipped = %v, failed = %v, %q", f.skipped, f.failed, f.text)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("got %q", got)
	}
}

func TestAPresentSampleWithAnotherChecksumFailsTheTest(t *testing.T) {
	root := fetchedRoot(t, []byte("not the sample\n"))
	f := runFake(func(f *fakeT) { readFetched(f, root, sshdSHA256) })
	if f.skipped || !f.failed {
		t.Fatalf("skipped = %v, failed = %v, want the test to fail", f.skipped, f.failed)
	}
	if !strings.Contains(f.text, "checksum") || !strings.Contains(f.text, sshdSHA256) {
		t.Errorf("the failure does not name the checksum it wanted: %q", f.text)
	}
}

func TestTheRootIsFoundByWalkingUpToGoMod(t *testing.T) {
	root := fetchedRoot(t, nil)
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{root, deep} {
		if got, ok := findRoot(from); !ok || got != root {
			t.Errorf("findRoot(%q) = %q, %v; want %q", from, got, ok, root)
		}
	}
	if got, ok := findRoot(t.TempDir()); ok {
		t.Errorf("a folder with no go.mod above it gave the root %q", got)
	}
}

// The real sample is read from the repository the test runs in: skipped, with
// the notice, when it was not fetched.
func TestTheFetchedSampleOfTheRepositoryIsReadWhenItIsThere(t *testing.T) {
	data := OpenSSH2k(t)
	// The facts measured on the sample: 520 lines hold Failed password, and
	// line 956 holds Accepted. (The checksum pins the bytes; these say that the
	// sample is the one the tests of the miner expect.)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	failed := 0
	for _, l := range lines {
		if strings.Contains(l, "Failed password") {
			failed++
		}
	}
	if failed != 520 {
		t.Errorf("%d lines hold Failed password, want 520", failed)
	}
	if len(lines) < 956 || !strings.Contains(lines[955], "Accepted ") {
		t.Errorf("line 956 does not hold Accepted")
	}
}
