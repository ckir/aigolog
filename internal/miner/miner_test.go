// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package miner

import (
	"fmt"
	"math/rand"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// letters returns a word of six lower case letters that is different for each
// n below 26^6: it has no digit, so the scanner leaves it a plain word.
func letters(n int) string {
	var b [6]byte
	for i := range b {
		b[i] = byte('a' + n%26)
		n /= 26
	}
	return string(b[:])
}

func learnAll(m *Miner, lines []string) {
	for _, l := range lines {
		m.Learn(l)
	}
}

// mined returns the patterns of ps that are not the catch-all.
func mined(ps []Pattern) []Pattern {
	var out []Pattern
	for _, p := range ps {
		if !p.CatchAll {
			out = append(out, p)
		}
	}
	return out
}

func sshLine(kind string, pid int, name, addr string, port int) string {
	return fmt.Sprintf("LabSZ sshd[%d]: %s for %s from %s port %d ssh2", pid, kind, name, addr, port)
}

func TestDefaultConfig(t *testing.T) {
	want := Config{MaxPatterns: 1000, Depth: 6, MaxChildren: 100, Similarity: 0.5}
	if got := DefaultConfig(); got != want {
		t.Fatalf("DefaultConfig() = %+v, want %+v", got, want)
	}
}

func TestOneShapeWithVaryingNamesAndAddressesIsOnePattern(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	type line struct {
		text string
		want []string
	}
	var lines []line
	m := New(DefaultConfig())
	for i := 0; i < 200; i++ {
		pid, port := 100+r.Intn(99900), 1024+r.Intn(64000)
		name := letters(r.Intn(30))
		addr := fmt.Sprintf("10.0.%d.%d", r.Intn(4), 1+r.Intn(10))
		text := fmt.Sprintf("LabSZ sshd[%d]: Failed password for invalid user %s from %s port %d ssh2", pid, name, addr, port)
		lines = append(lines, line{text, []string{fmt.Sprint(pid), name, addr, fmt.Sprint(port)}})
		m.Learn(text)
	}
	ps := m.Finish()
	if len(ps) != 2 || !ps[1].CatchAll || ps[0].CatchAll {
		t.Fatalf("want one mined pattern and the catch-all, got %+v", ps)
	}
	const wantText = "LabSZ sshd[<int>]: Failed password for invalid user <*> from <ip4> port <int> ssh2"
	if ps[0].Text != wantText {
		t.Fatalf("Text = %q, want %q", ps[0].Text, wantText)
	}
	wantSlots := []SlotType{Int, Word, IPv4, Int}
	if !reflect.DeepEqual(ps[0].Slots, wantSlots) {
		t.Fatalf("Slots = %v, want %v", ps[0].Slots, wantSlots)
	}
	for _, l := range lines {
		id, vals := m.Match(l.text)
		if id != ps[0].ID {
			t.Fatalf("Match(%q) id = %d, want %d", l.text, id, ps[0].ID)
		}
		if !reflect.DeepEqual(vals, l.want) {
			t.Fatalf("Match(%q) values = %q, want %q", l.text, vals, l.want)
		}
	}
}

func TestAVaryingWordAfterTheFourthTokenBecomesASlotButOneInTheFirstFourIsKeptApart(t *testing.T) {
	// The first four tokens are the path of the tree: a name in the fifth token
	// or later can be a slot, a name among the first four splits the pattern.
	// Known property of the method (Task 12b's record names it): a log whose
	// lines begin with a variable prefix longer than the fixed one can merge
	// event types that differ only after the fourth token.
	r := rand.New(rand.NewSource(2))
	m := New(DefaultConfig())
	for i := 0; i < 100; i++ {
		m.Learn(sshLine("Failed password", 100+r.Intn(900), letters(r.Intn(20)), "10.0.0.1", 1024+r.Intn(1000)))
	}
	ps := mined(m.Finish())
	if len(ps) != 1 {
		t.Fatalf("want one pattern, got %+v", ps)
	}
	if !reflect.DeepEqual(ps[0].Slots, []SlotType{Int, Word, IPv4, Int}) {
		t.Fatalf("Slots = %v, want [int word ip4 int]", ps[0].Slots)
	}

	m = New(DefaultConfig())
	names := []string{"alice", "bob", "carol"}
	for rep := 0; rep < 5; rep++ {
		for _, n := range names {
			m.Learn("user " + n + " logged in today")
		}
	}
	ps = mined(m.Finish())
	if len(ps) != 3 {
		t.Fatalf("want three patterns, got %+v", ps)
	}
	for _, p := range ps {
		if len(p.Slots) != 0 || strings.Contains(p.Text, "<*>") {
			t.Fatalf("pattern %+v must be fixed text with no slot", p)
		}
	}
}

func TestFailedAndAcceptedLoginsAreNeverMerged(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	m := New(DefaultConfig())
	for i := 0; i < 100; i++ {
		kind := "Failed password"
		if i%2 == 1 {
			kind = "Accepted password"
		}
		m.Learn(sshLine(kind, 100+r.Intn(900), letters(r.Intn(20)), fmt.Sprintf("10.0.0.%d", 1+r.Intn(9)), 1024+r.Intn(1000)))
	}
	ps := mined(m.Finish())
	if len(ps) != 2 {
		t.Fatalf("want two patterns, got %+v", ps)
	}
	if !strings.Contains(ps[0].Text, "Failed password") || strings.Contains(ps[0].Text, "Accepted") {
		t.Fatalf("pattern 1 = %q", ps[0].Text)
	}
	if !strings.Contains(ps[1].Text, "Accepted password") || strings.Contains(ps[1].Text, "Failed") {
		t.Fatalf("pattern 2 = %q", ps[1].Text)
	}
}

func TestTheSameWordsWithADifferentNumberOfTokensAreAnotherPattern(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	m := New(DefaultConfig())
	for i := 0; i < 200; i++ {
		m.Learn(sshLine("Failed password", 100+r.Intn(900), "invalid user "+letters(r.Intn(30)), "10.0.0.1", 1024+r.Intn(1000)))
	}
	// The name is the fourth token here, so it is part of the path of the tree
	// unless it holds a digit (then its key is the wildcard): the names have
	// digits, so that the question is only the number of tokens.
	for i := 0; i < 100; i++ {
		m.Learn(fmt.Sprintf("Failed password for user%d from 10.0.0.%d port %d ssh2", r.Intn(30), 1+r.Intn(9), 1024+r.Intn(1000)))
	}
	if ps := mined(m.Finish()); len(ps) != 2 {
		t.Fatalf("want two patterns, got %+v", ps)
	}
}

func TestTwoNamesInTheSamePositionBecomeAWordSlotButOneNameDoesNot(t *testing.T) {
	m := New(DefaultConfig())
	for i := 0; i < 10; i++ {
		m.Learn("connection closed by remote host alice again")
	}
	ps := mined(m.Finish())
	if len(ps) != 1 || ps[0].Text != "connection closed by remote host alice again" || len(ps[0].Slots) != 0 {
		t.Fatalf("one repeated text must stay as it is with no slot, got %+v", ps)
	}

	m = New(DefaultConfig())
	m.Learn("connection closed by remote host alice again")
	m.Learn("connection closed by remote host bob again")
	ps = mined(m.Finish())
	if len(ps) != 1 || ps[0].Text != "connection closed by remote host <*> again" || !reflect.DeepEqual(ps[0].Slots, []SlotType{Word}) {
		t.Fatalf("two names must make one Word slot, got %+v", ps)
	}
}

func TestAMessageAtExactlyTheSimilarityThresholdJoins(t *testing.T) {
	m := New(DefaultConfig())
	m.Learn("alpha beta gamma delta a b c d")
	m.Learn("alpha beta gamma delta e f g h") // 4 of 8 positions: 0.5 joins
	ps := mined(m.Finish())
	if len(ps) != 1 || ps[0].Text != "alpha beta gamma delta <*> <*> <*> <*>" {
		t.Fatalf("a similarity of exactly 0.5 must join, got %+v", ps)
	}

	m = New(DefaultConfig())
	m.Learn("alpha beta gamma delta a b c d z")
	m.Learn("alpha beta gamma delta e f g h y") // 4 of 9 positions: below 0.5
	if ps := mined(m.Finish()); len(ps) != 2 {
		t.Fatalf("a similarity below 0.5 must not join, got %+v", ps)
	}
}

func TestMatchTakesTheMostSpecificClusterThatFits(t *testing.T) {
	// Depth 2 keys no token, so the clusters share one leaf. After the four
	// messages cluster 1 is "a b <*> <*>" and cluster 2 is "a <*> r s".
	m := New(Config{MaxPatterns: 1000, Depth: 2, MaxChildren: 100, Similarity: 0.5})
	learnAll(m, []string{"a b c d", "a x r s", "a y r s", "a b e f"})
	ps := m.Finish()
	if ps[0].Text != "a b <*> <*>" || ps[1].Text != "a <*> r s" || len(ps) != 3 {
		t.Fatalf("patterns = %+v", ps)
	}
	if id, vals := m.Match("a b r s"); id != 2 || !reflect.DeepEqual(vals, []string{"b"}) {
		t.Fatalf("Match(a b r s) = %d %q, want pattern 2 (three fixed tokens) and [b]", id, vals)
	}
	if id, vals := m.Match("a b q q"); id != 1 || !reflect.DeepEqual(vals, []string{"q", "q"}) {
		t.Fatalf("Match(a b q q) = %d %q, want pattern 1", id, vals)
	}
	if id, vals := m.Match("z z z z"); id != 0 || len(vals) != 0 {
		t.Fatalf("Match(z z z z) = %d %q, want the catch-all", id, vals)
	}
}

func TestALiteralMarkerInALogIsNotASlot(t *testing.T) {
	// A log may hold the text "<int>". Slots are counted from the values the
	// scanner found, so the number of values always equals the number of slots.
	m := New(DefaultConfig())
	m.Learn("job <int> done")
	m.Learn("job <int> done")
	ps := mined(m.Finish())
	if len(ps) != 1 || ps[0].Text != "job <int> done" || len(ps[0].Slots) != 0 {
		t.Fatalf("a literal <int> is fixed text with no slot, got %+v", ps)
	}
	if id, vals := m.Match("job <int> done"); id != 1 || len(vals) != 0 {
		t.Fatalf("Match = %d %q", id, vals)
	}
	if id, vals := m.Match("job 5 done"); id != 0 || len(vals) != 0 {
		t.Fatalf("a real 5 has a value the pattern has no slot for: Match = %d %q, want the catch-all", id, vals)
	}

	m = New(DefaultConfig())
	m.Learn("job <int> done")
	m.Learn("job 5 done")
	ps = mined(m.Finish())
	if len(ps) != 1 || ps[0].Text != "job <*> done" || !reflect.DeepEqual(ps[0].Slots, []SlotType{Word}) {
		t.Fatalf("got %+v", ps)
	}
	for line, want := range map[string]string{"job <int> done": "<int>", "job 5 done": "5"} {
		if id, vals := m.Match(line); id != 1 || !reflect.DeepEqual(vals, []string{want}) {
			t.Fatalf("Match(%q) = %d %q, want 1 [%s]", line, id, vals, want)
		}
	}
}

// genCorpus makes n lines of five shapes with random fields; 5% are unique
// lines of random words.
func genCorpus(seed int64, n int) []string {
	r := rand.New(rand.NewSource(seed))
	word := func() string { return letters(r.Intn(40)) }
	ip := func() string { return fmt.Sprintf("192.168.%d.%d", r.Intn(4), 1+r.Intn(60)) }
	out := make([]string, 0, n)
	for len(out) < n {
		if r.Intn(100) < 5 {
			k := 3 + r.Intn(6)
			ws := make([]string, k)
			for i := range ws {
				ws[i] = letters(r.Intn(1 << 20))
			}
			out = append(out, strings.Join(ws, " "))
			continue
		}
		switch r.Intn(5) {
		case 0:
			out = append(out, sshLine("Failed password", r.Intn(90000), word(), ip(), 1024+r.Intn(60000)))
		case 1:
			out = append(out, sshLine("Accepted password", r.Intn(90000), word(), ip(), 1024+r.Intn(60000)))
		case 2:
			out = append(out, fmt.Sprintf("kernel: usb %d-%d: new high-speed device number %d using xhci_hcd", r.Intn(9), r.Intn(9), r.Intn(200)))
		case 3:
			out = append(out, fmt.Sprintf("request %08x-%04x-%04x-%04x-%012x took %d.%d ms status %d", r.Uint32(), r.Intn(65536), r.Intn(65536), r.Intn(65536), r.Int63n(1<<40), r.Intn(900), r.Intn(99), 200+r.Intn(4)*100))
		default:
			out = append(out, fmt.Sprintf("worker %s started with pid %d and handle 0x%x", word(), r.Intn(90000), r.Intn(1<<30)))
		}
	}
	return out
}

func TestEveryLearnedMessageMatchesItsOwnPattern(t *testing.T) {
	lines := genCorpus(7, 3000)
	m := New(DefaultConfig())
	learnAll(m, lines)
	ps := m.Finish()
	byID := make(map[int]Pattern, len(ps))
	for _, p := range ps {
		byID[p.ID] = p
	}
	for _, l := range lines {
		id, vals := m.Match(l)
		if id == 0 {
			t.Fatalf("learned line %q went to the catch-all", l)
		}
		p := byID[id]
		toks := Scan(l)
		pt := strings.Fields(p.Text)
		if len(pt) != len(toks) {
			t.Fatalf("line %q: pattern %q has %d tokens, the line %d", l, p.Text, len(pt), len(toks))
		}
		for i, f := range pt {
			if f != wildcard && f != toks[i].Text {
				t.Fatalf("line %q: token %d is %q, the pattern %q has %q", l, i, toks[i].Text, p.Text, f)
			}
		}
		if len(vals) != len(p.Slots) {
			t.Fatalf("line %q: %d values for %d slots of %q", l, len(vals), len(p.Slots), p.Text)
		}
	}
}

type snapshot struct {
	Patterns []Pattern
	IDs      []int
	Values   [][]string
}

func takeSnapshot(lines []string) snapshot {
	m := New(DefaultConfig())
	learnAll(m, lines)
	s := snapshot{Patterns: m.Finish()}
	for _, l := range lines {
		id, vals := m.Match(l)
		s.IDs = append(s.IDs, id)
		s.Values = append(s.Values, vals)
	}
	return s
}

func TestSameInputSameOutput(t *testing.T) {
	lines := genCorpus(11, 3000)
	want := takeSnapshot(lines)
	for i := 0; i < 12; i++ {
		if got := takeSnapshot(lines); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d differs from the first", i)
		}
	}
	old := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(old)
	for _, procs := range []int{1, 8} {
		runtime.GOMAXPROCS(procs)
		if got := takeSnapshot(lines); !reflect.DeepEqual(got, want) {
			t.Fatalf("GOMAXPROCS=%d differs from the first run", procs)
		}
	}
}

func TestTheCapSendsTheRestToTheCatchAll(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxPatterns = 5
	m := New(cfg)
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("%s %s %s", letters(3*i), letters(3*i+1), letters(3*i+2)))
	}
	learnAll(m, lines)
	ps := m.Finish()
	if len(ps) != 6 || !ps[5].CatchAll || ps[5].ID != 0 || ps[5].Text != "<other>" || len(ps[5].Slots) != 0 {
		t.Fatalf("want five mined patterns and the catch-all last, got %+v", ps)
	}
	for i, p := range ps[:5] {
		if p.ID != i+1 || p.CatchAll {
			t.Fatalf("pattern %d = %+v", i, p)
		}
	}
	if m.other != 45 {
		t.Fatalf("%d messages counted in the catch-all, want 45", m.other)
	}
	for i, l := range lines {
		id, vals := m.Match(l)
		if i < 5 && id != i+1 || i >= 5 && (id != 0 || len(vals) != 0) {
			t.Fatalf("Match(line %d) = %d %q", i, id, vals)
		}
	}
}

func TestAFullNodeRoutesNewKeysToItsWildcardChildConsistently(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxChildren = 3
	m := New(cfg)
	var lines []string
	for rep := 0; rep < 2; rep++ {
		for i := 0; i < 20; i++ {
			lines = append(lines, fmt.Sprintf("svc %s alpha beta gamma", letters(i)))
		}
	}
	learnAll(m, lines)
	ps := m.Finish()
	if got := len(mined(ps)); got != 4 { // three named children and the wildcard child
		t.Fatalf("%d mined patterns, want 4: %+v", got, ps)
	}
	for _, l := range lines {
		if id, _ := m.Match(l); id == 0 {
			t.Fatalf("learned line %q went to the catch-all", l)
		}
	}
}

func TestAnEmptyAndAWhiteSpaceOnlyMessageGoToTheCatchAll(t *testing.T) {
	m := New(DefaultConfig())
	m.Learn("")
	m.Learn(" \t\r\n ")
	ps := m.Finish()
	if len(ps) != 1 || ps[0].ID != 0 || ps[0].Text != "<other>" || !ps[0].CatchAll || len(ps[0].Slots) != 0 {
		t.Fatalf("want only the catch-all, got %+v", ps)
	}
	for _, s := range []string{"", " \t\r\n "} {
		if id, vals := m.Match(s); id != 0 || len(vals) != 0 {
			t.Fatalf("Match(%q) = %d %q", s, id, vals)
		}
	}
	if again := m.Finish(); !reflect.DeepEqual(again, ps) {
		t.Fatalf("a second Finish returned %+v, want %+v", again, ps)
	}
}

func TestAMessageOfMoreThan256TokensUsesTheFirst256(t *testing.T) {
	msg := strings.TrimSpace(strings.Repeat("word ", 300))
	m := New(DefaultConfig())
	m.Learn(msg)
	m.Learn(msg)
	ps := mined(m.Finish())
	if len(ps) != 1 {
		t.Fatalf("want one pattern, got %d", len(ps))
	}
	if got := len(strings.Fields(ps[0].Text)); got != 256 {
		t.Fatalf("the pattern has %d tokens, want 256", got)
	}
	if id, vals := m.Match(msg); id != 1 || len(vals) != 0 {
		t.Fatalf("Match = %d %q", id, vals)
	}
}

func TestValuesAreCutAt512BytesOnAValidBoundary(t *testing.T) {
	m := New(DefaultConfig())
	m.Learn("start of the run alice finished")
	m.Learn("start of the run bob finished")
	m.Finish()
	long := "a" + strings.Repeat("é", 400) // 801 bytes; byte 512 is inside a letter
	id, vals := m.Match("start of the run " + long + " finished")
	if id != 1 || len(vals) != 1 {
		t.Fatalf("Match = %d, %d values", id, len(vals))
	}
	v := vals[0]
	if len(v) != 511 || !utf8.ValidString(v) || v != CutValue(long) || !strings.HasPrefix(long, v) {
		t.Fatalf("value of %d bytes (valid UTF-8: %v), want the first 511 bytes of the token", len(v), utf8.ValidString(v))
	}
	if id, vals := m.Match("start of the run alice finished"); id != 1 || !reflect.DeepEqual(vals, []string{"alice"}) {
		t.Fatalf("Match = %d %q", id, vals)
	}
}

func TestLearnAfterFinishAndMatchBeforeFinishPanic(t *testing.T) {
	catch := func(f func()) (msg any) {
		defer func() { msg = recover() }()
		f()
		return nil
	}
	m := New(DefaultConfig())
	if got := catch(func() { m.Match("x y") }); got != "miner: Match before Finish" {
		t.Fatalf("Match before Finish panicked with %v", got)
	}
	m.Finish()
	if got := catch(func() { m.Learn("x y") }); got != "miner: Learn after Finish" {
		t.Fatalf("Learn after Finish panicked with %v", got)
	}
}

func heapInUse() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapInuse
}

func TestAHugeTokenDoesNotKeepItsMessageAlive(t *testing.T) {
	// A pattern must hold copies. Were it to hold the cut Text of a token, a
	// substring of the message, the 1,000 messages of 1 MiB would stay alive.
	cfg := DefaultConfig()
	cfg.MaxPatterns = 1000
	m := New(cfg)
	huge := strings.Repeat("z", 1<<20)
	for i := 0; i < 1000; i++ {
		m.Learn(letters(i) + huge)
	}
	ps := m.Finish()
	if got := len(mined(ps)); got != 1000 {
		t.Fatalf("%d patterns, want 1000", got)
	}
	if h := heapInUse(); h > 64<<20 {
		t.Fatalf("heap in use after Finish is %d MiB, want under 64 MiB", h>>20)
	}
	runtime.KeepAlive(m)
}

func TestMemoryOfLearningDoesNotGrowWithUniqueMessages(t *testing.T) {
	// Measured on 2026-11-17 (go 1.27.4, linux/amd64): heap in use after
	// learning and a GC was 1,784 KiB (1,856 KiB in an earlier run); the bound
	// is 64 MiB. The test takes about 25 s on a loaded machine, nearly all of it
	// the comparison of each message with the clusters of the one leaf that the
	// overflow keys share (up to 1,000 clusters).
	r := rand.New(rand.NewSource(5))
	cfg := DefaultConfig()
	cfg.MaxPatterns = 1000
	m := New(cfg)
	var sb strings.Builder
	for i := 0; i < 500000; i++ {
		sb.Reset()
		for k := 0; k < 6; k++ {
			if k > 0 {
				sb.WriteByte(' ')
			}
			for j := 0; j < 8; j++ {
				sb.WriteByte(byte('g' + r.Intn(20)))
			}
		}
		m.Learn(sb.String())
	}
	ps := m.Finish()
	if got := len(mined(ps)); got != 1000 {
		t.Fatalf("%d patterns, want 1000 (the cap)", got)
	}
	h := heapInUse()
	t.Logf("heap in use after learning 500,000 unique messages: %d KiB", h>>10)
	if h > 64<<20 {
		t.Fatalf("heap in use is %d MiB, want under 64 MiB", h>>20)
	}
	runtime.KeepAlive(m)
}
