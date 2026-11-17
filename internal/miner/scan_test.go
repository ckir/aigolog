// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package miner

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestScanMasksTypedValues(t *testing.T) {
	cases := []struct {
		in   string
		text string
		want []Value
	}{
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
	if len(cases) != 25 {
		t.Fatalf("the oracle table has %d rows, want 25", len(cases))
	}
	for _, c := range cases {
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

func TestScanNeverPanicsAndKeepsRawTokens(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []byte("0123456789abcdefABCDEFxX:.- _\t\n\x00\xc3\xa9<>[]")
	for n := 0; n < 5000; n++ {
		b := make([]byte, rng.Intn(80))
		for i := range b {
			if n%2 == 0 {
				b[i] = byte(rng.Intn(256))
			} else {
				b[i] = alphabet[rng.Intn(len(alphabet))]
			}
		}
		in := string(b)
		toks := Scan(in)
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
			if len(tok.Text) > MaxTokenTextBytes {
				t.Fatalf("Text of %q is %d bytes", tok.Raw, len(tok.Text))
			}
			for _, v := range tok.Values {
				if v.Text == "" || !strings.Contains(tok.Raw, v.Text) {
					t.Fatalf("value %q is not in token %q", v.Text, tok.Raw)
				}
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
	inputs := map[string]string{
		"digits and dots":  strings.Repeat("12.", size/3),
		"double dots":      strings.Repeat("1..2", size/4),
		"colons and hex":   strings.Repeat("a:", size/2),
		"one long number":  strings.Repeat("1", size),
		"one long decimal": "1." + strings.Repeat("1", size),
	}
	rng := rand.New(rand.NewSource(7))
	random := make([]byte, size)
	for i := range random {
		random[i] = "0123456789.:abcdef"[rng.Intn(18)]
	}
	inputs["random digits, dots and colons"] = string(random)
	for name, in := range inputs {
		start := time.Now()
		toks := Scan(in)
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s: Scan took %v, want under 2s", name, d)
		}
		if len(toks) != 1 {
			t.Errorf("%s: got %d tokens, want 1", name, len(toks))
			continue
		}
		if toks[0].Raw != in {
			t.Errorf("%s: Raw was changed", name)
		}
		for _, v := range toks[0].Values {
			if len(v.Text) > len(toks[0].Raw) {
				t.Errorf("%s: value of %d bytes in a token of %d", name, len(v.Text), len(toks[0].Raw))
			}
		}
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
