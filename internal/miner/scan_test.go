// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package miner

import (
	"math/rand"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type maskCase struct {
	in   string
	text string
	want []Value
}

// oracleCases are the 25 rows of the oracle table, unedited.
var oracleCases = []maskCase{
	{"root", "root", nil},
	{"22", "<int>", []Value{{Int, "22"}}},
	{"sshd[24206]:", "sshd[<int>]:", []Value{{Int, "24206"}}},
	{"3.14", "<dec>", []Value{{Dec, "3.14"}}},
	{"183.62.140.253", "<ip4>", []Value{{IPv4, "183.62.140.253"}}},
	{"10.0.0.1:22", "<ip4>:<int>", []Value{{IPv4, "10.0.0.1"}, {Int, "22"}}},
	// A group over 255 is not IPv4; "256.1" is not a decimal because ".1" follows it.
	{"256.1.1.1", "<int>.1.1.1", []Value{{Int, "256"}}},
	{"1.2.3.4.5", "<int>.2.3.4.5", []Value{{Int, "1"}}},
	{"fe80::1", "<ip6>", []Value{{IPv6, "fe80::1"}}},
	{"2001:db8:0:0:0:0:0:1", "<ip6>", []Value{{IPv6, "2001:db8:0:0:0:0:0:1"}}},
	{"08:15:22", "<int>:<int>:<int>", []Value{{Int, "08"}, {Int, "15"}, {Int, "22"}}},
	{"0x1f", "<hex>", []Value{{Hex, "0x1f"}}},
	{"deadbeef01", "<hex>", []Value{{Hex, "deadbeef01"}}},
	{"deadbeef", "deadbeef", nil}, // no digit
	{"12345678", "<int>", []Value{{Int, "12345678"}}},
	{"550e8400-e29b-41d4-a716-446655440000", "<uuid>", []Value{{UUID, "550e8400-e29b-41d4-a716-446655440000"}}},
	{"user123", "user123", nil}, // a digit after a letter
	{"22ms", "22ms", nil},       // a letter right after
	{"ssh2", "ssh2", nil},
	{"v1.2.3", "v1.2.3", nil},
	{"-5", "-<int>", []Value{{Int, "5"}}},
	{"abc-5", "abc-<int>", []Value{{Int, "5"}}},
	{"1234567890123456789", "1234567890123456789", nil}, // 19 digits
	{"héllo5", "héllo5", nil},                           // 0x80 and above are word bytes
	{"<int>", "<int>", nil},                             // a literal marker is plain text
}

// ipv6Cases are the rows added by ruling R7 (an IPv6 candidate may not start
// after a colon, may not be followed by '.' and a digit or by a longer chain,
// and leaves a single trailing colon out).
var ipv6Cases = []maskCase{
	{"fe80::1:", "<ip6>:", []Value{{IPv6, "fe80::1"}}},
	{"::ffff:1.2.3.4", "::ffff:<ip4>", []Value{{IPv4, "1.2.3.4"}}},
	{"a:b:c:d:e:f:1:2:3", "a:b:c:d:e:f:<int>:<int>:<int>", []Value{{Int, "1"}, {Int, "2"}, {Int, "3"}}},
	{"deadbeef::1", "deadbeef::<int>", []Value{{Int, "1"}}},
	{"1:2:3:4:5:6:7:8:9::", "<int>:<int>:<int>:<int>:<int>:<int>:<int>:<int>:<int>::",
		[]Value{{Int, "1"}, {Int, "2"}, {Int, "3"}, {Int, "4"}, {Int, "5"}, {Int, "6"}, {Int, "7"}, {Int, "8"}, {Int, "9"}}},
}

func TestScanMasksTypedValues(t *testing.T) {
	if len(oracleCases) != 25 {
		t.Fatalf("the oracle table has %d rows, want 25", len(oracleCases))
	}
	if len(ipv6Cases) != 5 {
		t.Fatalf("the IPv6 table has %d rows, want 5", len(ipv6Cases))
	}
	for _, c := range append(append([]maskCase{}, oracleCases...), ipv6Cases...) {
		toks := Scan(c.in)
		if len(toks) != 1 {
			t.Errorf("Scan(%q) gave %d tokens, want 1", c.in, len(toks))
			continue
		}
		got := toks[0]
		if got.Raw != c.in {
			t.Errorf("Scan(%q).Raw = %q", c.in, got.Raw)
		}
		if got.Text != c.text {
			t.Errorf("Scan(%q).Text = %q, want %q", c.in, got.Text, c.text)
		}
		if len(got.Values) != len(c.want) || (len(c.want) > 0 && !reflect.DeepEqual(got.Values, c.want)) {
			t.Errorf("Scan(%q).Values = %v, want %v", c.in, got.Values, c.want)
		}
	}
}

func TestSlotTypeMarker(t *testing.T) {
	want := map[SlotType]string{
		Word: "<*>", Int: "<int>", Dec: "<dec>", Hex: "<hex>",
		IPv4: "<ip4>", IPv6: "<ip6>", UUID: "<uuid>",
	}
	names := map[SlotType]string{
		Word: "word", Int: "int", Dec: "dec", Hex: "hex",
		IPv4: "ip4", IPv6: "ip6", UUID: "uuid",
	}
	for typ, marker := range want {
		if typ.Marker() != marker {
			t.Errorf("%q.Marker() = %q, want %q", string(typ), typ.Marker(), marker)
		}
		if string(typ) != names[typ] {
			t.Errorf("SlotType %q, want %q", string(typ), names[typ])
		}
	}
}

func TestScanSplitsOnWhiteSpaceAndKeepsAtMost256Tokens(t *testing.T) {
	words := make([]string, 300)
	for i := range words {
		words[i] = "w"
	}
	if got := len(Scan(strings.Join(words, " "))); got != MaxTokens || MaxTokens != 256 {
		t.Errorf("300 words gave %d tokens (MaxTokens %d), want 256", got, MaxTokens)
	}
	var raws []string
	for _, tok := range Scan("  a \t b\nc ") {
		raws = append(raws, tok.Raw)
	}
	if !reflect.DeepEqual(raws, []string{"a", "b", "c"}) {
		t.Errorf(`Scan("  a \t b\nc ") = %q`, raws)
	}
	for _, sep := range []string{" ", "\t", "\n", "\r", "\v", "\f"} {
		if got := Scan("x" + sep + "y"); len(got) != 2 {
			t.Errorf("separator %q gave %d tokens, want 2", sep, len(got))
		}
	}
	for _, in := range []string{"", "   "} {
		if got := Scan(in); len(got) != 0 {
			t.Errorf("Scan(%q) gave %d tokens, want none", in, len(got))
		}
	}
}

// The reference masker below is written from the rules, slowly and with
// regular expressions, and shares no code with the scanner. The scanner must
// agree with it on every token it is given.

var (
	reUUID    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reIPv4    = regexp.MustCompile(`^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}`)
	reHex0x   = regexp.MustCompile(`^0[xX][0-9a-fA-F]+`)
	reDec     = regexp.MustCompile(`^[0-9]+\.[0-9]+`)
	reLongHex = regexp.MustCompile(`^[0-9a-fA-F]+`)
	reInt     = regexp.MustCompile(`^[0-9]+`)
	reGroup   = regexp.MustCompile(`^[0-9a-fA-F]{1,4}$`)
)

func refWord(b byte) bool {
	return b >= 0x80 || b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func refDigit(b byte) bool { return b >= '0' && b <= '9' }

func refHex(b byte) bool {
	return refDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// refEnds: a value may end at e when no word byte follows.
func refEnds(raw string, e int) bool { return e == len(raw) || !refWord(raw[e]) }

func refDotDigit(raw string, e int) bool {
	return e+1 < len(raw) && raw[e] == '.' && refDigit(raw[e+1])
}

func refValid6(c string) bool {
	if strings.Count(c, ":") < 2 || strings.Contains(c, ":::") {
		return false
	}
	if strings.Contains(c, "::") {
		if strings.Count(c, "::") != 1 {
			return false
		}
		n := 0
		for _, side := range strings.SplitN(c, "::", 2) {
			if side == "" {
				continue
			}
			for _, g := range strings.Split(side, ":") {
				if !reGroup.MatchString(g) {
					return false
				}
				n++
			}
		}
		return n >= 1 && n <= 7
	}
	parts := strings.Split(c, ":")
	if len(parts) != 8 {
		return false
	}
	for _, g := range parts {
		if !reGroup.MatchString(g) {
			return false
		}
	}
	return true
}

// refMatch returns the type and length of the typed value that starts at i,
// or length 0.
func refMatch(raw string, i int) (SlotType, int) {
	if i > 0 {
		if refWord(raw[i-1]) || (raw[i-1] == '.' && i >= 2 && refWord(raw[i-2])) {
			return Word, 0
		}
	}
	rest := raw[i:]
	if m := reUUID.FindString(rest); m != "" && refEnds(raw, i+len(m)) {
		return UUID, len(m)
	}
	if i == 0 || raw[i-1] != ':' {
		top := 45
		if len(rest) < top {
			top = len(rest)
		}
		for l := top; l >= 2; l-- {
			e := i + l
			if !refValid6(rest[:l]) || !refEnds(raw, e) || refDotDigit(raw, e) {
				continue
			}
			if e+1 < len(raw) && raw[e] == ':' && (refHex(raw[e+1]) || raw[e+1] == ':') {
				continue
			}
			return IPv6, l
		}
	}
	if m := reIPv4.FindString(rest); m != "" && refEnds(raw, i+len(m)) && !refDotDigit(raw, i+len(m)) {
		ok := true
		for _, g := range strings.Split(m, ".") {
			if len(g) == 3 && g > "255" {
				ok = false
			}
		}
		if ok {
			return IPv4, len(m)
		}
	}
	if m := reHex0x.FindString(rest); m != "" && len(m) <= 130 && refEnds(raw, i+len(m)) {
		return Hex, len(m)
	}
	if m := reDec.FindString(rest); m != "" && len(m) <= 40 && refEnds(raw, i+len(m)) && !refDotDigit(raw, i+len(m)) {
		return Dec, len(m)
	}
	if m := reLongHex.FindString(rest); len(m) >= 8 && len(m) <= 130 && refEnds(raw, i+len(m)) &&
		strings.ContainsAny(m, "0123456789") && strings.ContainsAny(m, "abcdefABCDEF") {
		return Hex, len(m)
	}
	if m := reInt.FindString(rest); m != "" && len(m) <= 18 && refEnds(raw, i+len(m)) {
		return Int, len(m)
	}
	return Word, 0
}

// refCut returns the length at which s is cut to at most room bytes without
// splitting a UTF-8 sequence; with no sequence start in the last UTFMax
// bytes it cuts at room.
func refCut(s string, room int) int {
	if len(s) <= room {
		return len(s)
	}
	for n := room; n > 0 && room-n < utf8.UTFMax; n-- {
		if utf8.RuneStart(s[n]) {
			return n
		}
	}
	return room
}

// refMask masks one token by the rules, including the rule that masking
// stops at the first marker that does not fit in MaxTokenTextBytes.
func refMask(raw string) (string, []Value) {
	var text strings.Builder
	var vals []Value
	copied := 0
	for i := 0; i < len(raw); i++ {
		if i < copied {
			continue
		}
		typ, n := refMatch(raw, i)
		if n == 0 {
			continue
		}
		if text.Len()+(i-copied)+len(typ.Marker()) > MaxTokenTextBytes {
			break
		}
		text.WriteString(raw[copied:i])
		text.WriteString(typ.Marker())
		vals = append(vals, Value{Type: typ, Text: raw[i : i+n]})
		copied = i + n
	}
	tail := raw[copied:]
	text.WriteString(tail[:refCut(tail, MaxTokenTextBytes-text.Len())])
	return text.String(), vals
}

func TestTheReferenceMaskerAgreesWithTheOracleTable(t *testing.T) {
	for _, c := range append(append([]maskCase{}, oracleCases...), ipv6Cases...) {
		text, vals := refMask(c.in)
		if text != c.text || len(vals) != len(c.want) || (len(vals) > 0 && !reflect.DeepEqual(vals, c.want)) {
			t.Errorf("reference of %q = %q %v, table says %q %v", c.in, text, vals, c.text, c.want)
		}
	}
}

// checkBounds checks what Scan guarantees of every token whatever its input.
func checkBounds(t *testing.T, name string, tok Token) {
	t.Helper()
	if len(tok.Text) > MaxTokenTextBytes {
		t.Errorf("%s: Text is %d bytes, want at most %d", name, len(tok.Text), MaxTokenTextBytes)
	}
	if !strings.Contains(tok.Raw, "<") && strings.Count(tok.Text, "<") != len(tok.Values) {
		t.Errorf("%s: %d markers in Text for %d Values", name, strings.Count(tok.Text, "<"), len(tok.Values))
	}
	for _, v := range tok.Values {
		max := map[SlotType]int{Hex: 130, Dec: 40, Int: 18, IPv4: 15, IPv6: 45, UUID: 36}[v.Type]
		if max == 0 || v.Text == "" || len(v.Text) > max {
			t.Errorf("%s: value %s of %d bytes, want 1..%d", name, v.Type, len(v.Text), max)
			return
		}
	}
}

func TestScanNeverPanicsAndKeepsRawTokens(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []byte("0123456789abcdefABCDEFxX:.- _\t\n\x00\xc3\xa9<>[]")
	pieces := []string{"12", "3.14", "10.0.0.1", "fe80::1", "::1", "0x1f", "deadbeef01", "1-", "-", ":", ".", "a", "é",
		"550e8400-e29b-41d4-a716-446655440000", "1:2:3:4:5:6:7:8", "x", "999", "::ffff:1.2.3.4", "ab:cd"}
	for n := 0; n < 5000; n++ {
		var b []byte
		switch n % 3 {
		case 0:
			b = make([]byte, rng.Intn(80))
			for i := range b {
				b[i] = byte(rng.Intn(256))
			}
		case 1:
			b = make([]byte, rng.Intn(80))
			for i := range b {
				b[i] = alphabet[rng.Intn(len(alphabet))]
			}
		default: // long tokens built from value-rich pieces, to reach the 256-byte limit
			for k := rng.Intn(150); k >= 0; k-- {
				b = append(b, pieces[rng.Intn(len(pieces))]...)
			}
		}
		in := string(b)
		toks := Scan(in)
		if again := Scan(in); !reflect.DeepEqual(toks, again) {
			t.Fatalf("Scan(%q) is not deterministic", in)
		}
		if len(toks) > MaxTokens {
			t.Fatalf("input %q gave %d tokens", in, len(toks))
		}
		for _, tok := range toks {
			if tok.Raw == "" || !strings.Contains(in, tok.Raw) {
				t.Fatalf("Raw %q is not a substring of %q", tok.Raw, in)
			}
			if strings.ContainsAny(tok.Raw, " \t\n\r\v\f") {
				t.Fatalf("Raw %q holds white space", tok.Raw)
			}
			checkBounds(t, tok.Raw, tok)
			wantText, wantVals := refMask(tok.Raw)
			if tok.Text != wantText || len(tok.Values) != len(wantVals) || (len(wantVals) > 0 && !reflect.DeepEqual(tok.Values, wantVals)) {
				t.Fatalf("Scan of %q = %q %v, reference says %q %v", tok.Raw, tok.Text, tok.Values, wantText, wantVals)
			}
		}
	}
}

func TestATokenLongerThanTheLimitHasCutTextAndWholeRaw(t *testing.T) {
	long := strings.Repeat("w", 1000)
	toks := Scan(long)
	if len(toks) != 1 {
		t.Fatalf("got %d tokens, want 1", len(toks))
	}
	if len(toks[0].Text) > MaxTokenTextBytes || len(toks[0].Text) == 0 {
		t.Errorf("len(Text) = %d, want 1..%d", len(toks[0].Text), MaxTokenTextBytes)
	}
	if toks[0].Raw != long {
		t.Errorf("Raw was cut: %d bytes, want 1000", len(toks[0].Raw))
	}
	// A cut that would fall inside a character falls before it, also when
	// values were masked first.
	odd := Scan("12 " + strings.Repeat("é", 300))
	if got := odd[1].Text; len(got) > MaxTokenTextBytes || !utf8.ValidString(got) {
		t.Errorf("Text of a long é token: %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
	masked := Scan("7" + strings.Repeat("-é", 200))
	if got := masked[0].Text; len(got) > MaxTokenTextBytes || !utf8.ValidString(got) || !strings.HasPrefix(got, "<int>-") {
		t.Errorf("Text of a masked long token: %d bytes, valid=%v, %q", len(got), utf8.ValidString(got), got)
	}
}

func TestScanOfAMegabyteTokenIsLinear(t *testing.T) {
	const size = 1 << 20
	rng := rand.New(rand.NewSource(7))
	random := make([]byte, size)
	for i := range random {
		random[i] = "0123456789.:abcdef"[rng.Intn(18)]
	}
	inputs := []struct{ name, in string }{
		{"digits and dots", strings.Repeat("12.", size/3)},
		{"double dots", strings.Repeat("1..2", size/4)},
		{"colons and hex", strings.Repeat("a:", size/2)},
		{"colon groups", strings.Repeat("1:2:3:4:5:6:7:8-", size/16)},
		{"one long number", strings.Repeat("1", size)},
		{"one long decimal", "1." + strings.Repeat("1", size)},
		{"one long hex", "1" + strings.Repeat("a", size-1)},
		{"alternating hex", strings.Repeat("a1", size/2)},
		{"one long 0x", "0x" + strings.Repeat("f", size)},
		{"a value every two bytes", strings.Repeat("1-", size/2)},
		{"random digits, dots and colons", string(random)},
	}
	for _, c := range inputs {
		done := make(chan []Token, 1)
		go func(in string) { done <- Scan(in) }(c.in)
		var toks []Token
		select {
		case toks = <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: Scan did not return in 5s, want it linear", c.name)
		}
		if len(toks) != 1 {
			t.Errorf("%s: got %d tokens, want 1", c.name, len(toks))
			continue
		}
		if toks[0].Raw != c.in {
			t.Errorf("%s: Raw was changed", c.name)
		}
		checkBounds(t, c.name, toks[0])
	}
}

func TestScanOfAValueRichTokenKeepsFewValuesAndLittleMemory(t *testing.T) {
	in := strings.Repeat("1-", 1<<19)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	toks := Scan(in)
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 64<<10 {
		t.Errorf("Scan of a 1 MiB token allocated %d bytes, want at most 65536", got)
	}
	tok := toks[0]
	if len(tok.Values) > 51 {
		t.Errorf("%d values in one token, want at most 51 (256 bytes of Text, 5 for the shortest marker)", len(tok.Values))
	}
	if n := strings.Count(tok.Text, "<"); n != len(tok.Values) {
		t.Errorf("%d markers in Text for %d Values", n, len(tok.Values))
	}
	if len(tok.Raw) != len(in) {
		t.Errorf("Raw was cut to %d bytes", len(tok.Raw))
	}
	runtime.KeepAlive(toks)
}

func TestMaskingStopsAtTheFirstMarkerThatDoesNotFit(t *testing.T) {
	for n := 240; n <= 256; n++ {
		in := strings.Repeat("-", n) + "1.2.3.4"
		tok := Scan(in)[0]
		checkBounds(t, in, tok)
		fits := n+len("<ip4>") <= MaxTokenTextBytes
		if fits && (len(tok.Values) != 1 || !strings.HasSuffix(tok.Text, "<ip4>")) {
			t.Errorf("%d dashes: the marker fits but Text ends %q with %d values", n, tok.Text[len(tok.Text)-6:], len(tok.Values))
		}
		if !fits && (len(tok.Values) != 0 || strings.Contains(tok.Text, "<")) {
			t.Errorf("%d dashes: the marker does not fit but Text has %d values and %q", n, len(tok.Values), tok.Text[n-3:])
		}
		if tok.Raw != in {
			t.Errorf("%d dashes: Raw was changed", n)
		}
	}
	// Plain bytes that straddle the limit are cut on a character boundary.
	tok := Scan(strings.Repeat("é", 200) + "7")[0]
	if len(tok.Text) > MaxTokenTextBytes || !utf8.ValidString(tok.Text) {
		t.Errorf("a cut inside é: %d bytes, valid=%v", len(tok.Text), utf8.ValidString(tok.Text))
	}
}

func TestValuesAreBoundedByTheirCaps(t *testing.T) {
	ones := func(n int) string { return strings.Repeat("1", n) }
	hexOf := func(n int) string { return "1" + strings.Repeat("a", n-1) } // a digit and letters
	type row struct {
		name, in string
		text     string
		typ      SlotType // Word when there is no value
		n        int      // length of the value
	}
	rows := []row{
		{"decimal of 40 bytes", ones(5) + "." + ones(34), "<dec>", Dec, 40},
		// 41 bytes is not a decimal; the whole number before the '.' is tried.
		{"decimal of 41 bytes", ones(5) + "." + ones(35), "<int>." + ones(35), Int, 5},
		{"long hex of 128 bytes", hexOf(128), "<hex>", Hex, 128},
		{"long hex of 129 bytes", hexOf(129), "<hex>", Hex, 129},
		{"long hex of 130 bytes", hexOf(130), "<hex>", Hex, 130},
		{"long hex of 131 bytes", hexOf(131), hexOf(131), Word, 0},
		// 0x and 128 digits is 130 bytes, the longest hexadecimal value.
		{"0x and 128 digits", "0x" + hexOf(128), "<hex>", Hex, 130},
		{"0x and 129 digits", "0x" + hexOf(129), "0x" + hexOf(129), Word, 0},
		{"0x and 130 digits", "0x" + hexOf(130), "0x" + hexOf(130), Word, 0},
		{"0x and 131 digits", "0x" + hexOf(131), "0x" + hexOf(131), Word, 0},
	}
	for _, r := range rows {
		tok := Scan(r.in)[0]
		if tok.Text != r.text {
			t.Errorf("%s: Text = %.60q (%d bytes), want %.60q", r.name, tok.Text, len(tok.Text), r.text)
		}
		if r.typ == Word {
			if len(tok.Values) != 0 {
				t.Errorf("%s: %d values, want none", r.name, len(tok.Values))
			}
		} else if len(tok.Values) != 1 || tok.Values[0].Type != r.typ || len(tok.Values[0].Text) != r.n {
			t.Errorf("%s: values %.80v, want one %s of %d bytes", r.name, tok.Values, r.typ, r.n)
		}
	}
	// A megabyte of each: every value is within its cap.
	for _, in := range []string{hexOf(1 << 20), "1." + ones(1<<20), "0x" + hexOf(1<<20)} {
		for _, v := range Scan(in)[0].Values {
			if len(v.Text) > 130 {
				t.Errorf("a %d-byte %s value from a token of %d bytes", len(v.Text), v.Type, len(in))
			}
		}
	}
	if got := Scan("1." + ones(1<<20))[0]; !strings.HasPrefix(got.Text, "<int>.") || len(got.Values) != 1 || got.Values[0].Text != "1" {
		t.Errorf(`"1." and a megabyte of ones gives %.20q %v`, got.Text, got.Values)
	}
}

func TestCutValueKeepsAWholeUTF8SequenceAndShortValuesAlone(t *testing.T) {
	if MaxValueBytes != 512 {
		t.Fatalf("MaxValueBytes = %d, want 512", MaxValueBytes)
	}
	if got := CutValue("0123456789"); got != "0123456789" {
		t.Errorf("a 10-byte value was changed to %q", got)
	}
	exact := strings.Repeat("é", 256)
	if len(exact) != 512 || CutValue(exact) != exact {
		t.Errorf("exactly 512 bytes was changed")
	}
	for _, prefix := range []string{"", "a", "ab", "abc"} {
		in := prefix + strings.Repeat("é", 300)
		got := CutValue(in)
		if len(got) > MaxValueBytes || !utf8.ValidString(got) || !strings.HasPrefix(in, got) {
			t.Errorf("prefix %q: cut gave %d bytes, valid=%v", prefix, len(got), utf8.ValidString(got))
		}
		if len(got) < MaxValueBytes-1 {
			t.Errorf("prefix %q: cut gave only %d bytes", prefix, len(got))
		}
	}
	// 513 bytes of one-byte characters is cut to exactly 512.
	if got := CutValue(strings.Repeat("a", 513)); len(got) != 512 {
		t.Errorf("513 ASCII bytes gave %d", len(got))
	}
	// Four-byte characters: the cut falls inside one.
	four := "x" + strings.Repeat("\U0001F600", 200)
	if got := CutValue(four); len(got) > MaxValueBytes || !utf8.ValidString(got) {
		t.Errorf("four-byte characters: %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
}
