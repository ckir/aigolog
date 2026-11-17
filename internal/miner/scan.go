// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package miner

import (
	"strings"
	"unicode/utf8"
)

// SlotType names the kind of a slot in a pattern and of a value found in a
// token.
type SlotType string

// The slot types. Word is a slot for free text; the others are typed values
// the scanner finds in a token.
const (
	Word SlotType = "word"
	Int  SlotType = "int"
	Dec  SlotType = "dec"
	Hex  SlotType = "hex"
	IPv4 SlotType = "ip4"
	IPv6 SlotType = "ip6"
	UUID SlotType = "uuid"
)

// Limits of the scanner.
const (
	// MaxTokens is the most tokens Scan takes from one message.
	MaxTokens = 256
	// MaxValueBytes is the longest value CutValue leaves whole.
	MaxValueBytes = 512
	// MaxTokenTextBytes is the longest Text a Token holds.
	MaxTokenTextBytes = 256
)

// Caps on the length of a value, in bytes. A longer hexadecimal run or
// decimal is not a value, and the scans of runs stop at these lengths, so a
// huge token costs no more than its length and no value is larger than a cap.
const (
	maxHexBytes     = 130 // 0x and 128 digits, or 130 digits of long hexadecimal
	maxDecimalBytes = 40
	maxIntDigits    = 18
	maxIPv6Bytes    = 45
)

// Marker returns the text that stands for a slot of type t in a pattern.
func (t SlotType) Marker() string {
	switch t {
	case Int:
		return "<int>"
	case Dec:
		return "<dec>"
	case Hex:
		return "<hex>"
	case IPv4:
		return "<ip4>"
	case IPv6:
		return "<ip6>"
	case UUID:
		return "<uuid>"
	default:
		return "<*>"
	}
}

// Value is a typed value found in a token: its type and the text as written.
type Value struct {
	Type SlotType
	Text string
}

// Token is one white-space separated word of a message. Raw is the token as
// written. Text is Raw with each typed value replaced by its marker, at most
// MaxTokenTextBytes bytes long: a marker is put in only when it fits whole, and
// masking stops at the first value whose marker does not fit, so Values holds
// exactly the values whose markers are in Text, in order, as written. The rest
// of the token after that point is copied unmasked, cut on a character
// boundary.
type Token struct {
	Raw    string
	Text   string
	Values []Value
}

// Scan splits msg on ASCII white space into at most MaxTokens tokens (text
// after the last token taken is ignored) and masks the typed values of each.
func Scan(msg string) []Token {
	var toks []Token
	i, n := 0, len(msg)
	for len(toks) < MaxTokens {
		for i < n && isSpace(msg[i]) {
			i++
		}
		if i == n {
			break
		}
		j := i
		for j < n && !isSpace(msg[j]) {
			j++
		}
		toks = append(toks, scanToken(msg[i:j]))
		i = j
	}
	return toks
}

// CutValue returns s unchanged when it is at most MaxValueBytes long;
// otherwise its first MaxValueBytes bytes, shortened to the last whole UTF-8
// sequence.
func CutValue(s string) string {
	return s[:cutPoint(s, MaxValueBytes)]
}

// cutPoint returns the largest index at most max at which s may be cut
// without splitting a UTF-8 sequence. Bytes that are not valid UTF-8 cannot
// be told apart from the middle of a sequence for more than UTFMax bytes, so
// the cut is then made at max.
func cutPoint(s string, max int) int {
	if len(s) <= max {
		return len(s)
	}
	for c := max; c > 0 && c > max-utf8.UTFMax; c-- {
		if utf8.RuneStart(s[c]) {
			return c
		}
	}
	return max
}

func isSpace(b byte) bool {
	return b == ' ' || (b >= '\t' && b <= '\r') // tab, newline, vertical tab, form feed, carriage return
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isHexLetter(b byte) bool { return (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') }

func isHexDigit(b byte) bool { return isDigit(b) || isHexLetter(b) }

// isWord reports whether b is a word byte: an ASCII letter, a digit, an
// underscore or any byte of 0x80 or more.
func isWord(b byte) bool {
	return isDigit(b) || (b|0x20 >= 'a' && b|0x20 <= 'z') || b == '_' || b >= 0x80
}

// scanToken masks the typed values of one token.
func scanToken(raw string) Token {
	tok := Token{Raw: raw}
	var sb strings.Builder
	copied := 0 // raw[:copied] has been written to sb
	for i := 0; i < len(raw); {
		end, typ := 0, Word
		if canStart(raw, i) {
			end, typ = matchAt(raw, i)
		}
		if typ == Word {
			i++
			continue
		}
		marker := typ.Marker()
		if sb.Len()+(i-copied)+len(marker) > MaxTokenTextBytes {
			break // masking stops here; the rest is copied as it is
		}
		if tok.Values == nil {
			sb.Grow(MaxTokenTextBytes)
		}
		sb.WriteString(raw[copied:i])
		sb.WriteString(marker)
		tok.Values = append(tok.Values, Value{Type: typ, Text: raw[i:end]})
		copied = end
		i = end
	}
	if tok.Values == nil {
		tok.Text = raw[:cutPoint(raw, MaxTokenTextBytes)]
		return tok
	}
	tail := raw[copied:]
	sb.WriteString(tail[:cutPoint(tail, MaxTokenTextBytes-sb.Len())])
	tok.Text = sb.String()
	return tok
}

// canStart reports whether a typed value may start at byte i of s: at the
// start, or after a byte that is not a word byte and is not a '.' that itself
// follows a word byte.
func canStart(s string, i int) bool {
	c := s[i]
	if !isHexDigit(c) && c != ':' {
		return false
	}
	if i == 0 {
		return true
	}
	p := s[i-1]
	if isWord(p) {
		return false
	}
	return !(p == '.' && i >= 2 && isWord(s[i-2]))
}

// matchAt tries the typed values in order at byte i and returns the end of
// the first that matches, or Word when none does.
func matchAt(s string, i int) (int, SlotType) {
	if e := matchUUID(s, i); e > 0 {
		return e, UUID
	}
	if e := matchIPv6(s, i); e > 0 {
		return e, IPv6
	}
	if e := matchIPv4(s, i); e > 0 {
		return e, IPv4
	}
	if e := matchHex(s, i); e > 0 {
		return e, Hex
	}
	if e := matchDecimal(s, i); e > 0 {
		return e, Dec
	}
	if e := matchLongHex(s, i); e > 0 {
		return e, Hex
	}
	if e := matchInt(s, i); e > 0 {
		return e, Int
	}
	return 0, Word
}

// endsValue reports whether a value may end at e: at the end of s or before a
// byte that is not a word byte.
func endsValue(s string, e int) bool {
	return e == len(s) || !isWord(s[e])
}

// limit returns the smaller of len(s) and i+n: the end of a scan that looks at
// no more than n bytes from i.
func limit(s string, i, n int) int {
	if n > len(s)-i {
		return len(s)
	}
	return i + n
}

// digitsFrom returns the index after the run of digits that starts at i,
// looking at no byte at or after hi.
func digitsFrom(s string, i, hi int) int {
	for i < hi && isDigit(s[i]) {
		i++
	}
	return i
}

// hexFrom returns the index after the run of hexadecimal digits at i, looking
// at no byte at or after hi.
func hexFrom(s string, i, hi int) int {
	for i < hi && isHexDigit(s[i]) {
		i++
	}
	return i
}

// matchUUID matches 8-4-4-4-12 hexadecimal digits joined by '-'.
func matchUUID(s string, i int) int {
	const length = 36
	if len(s)-i < length {
		return 0
	}
	for k := 0; k < length; k++ {
		c := s[i+k]
		if k == 8 || k == 13 || k == 18 || k == 23 {
			if c != '-' {
				return 0
			}
		} else if !isHexDigit(c) {
			return 0
		}
	}
	if !endsValue(s, i+length) {
		return 0
	}
	return i + length
}

// matchIPv6 matches groups of 1 to 4 hexadecimal digits separated by ':',
// with one "::" (and at least one group, at most seven) or exactly eight
// groups, in at most 45 bytes. It may not start right after a ':', may not be
// followed by '.' and a digit, and may not be followed by a ':' that is itself
// followed by a hexadecimal digit or a second ':' (a longer chain is not an
// address). The longest candidate that meets these wins, so a single trailing
// ':' is left out.
func matchIPv6(s string, i int) int {
	if i > 0 && s[i-1] == ':' {
		return 0
	}
	hi := limit(s, i, maxIPv6Bytes+1)
	runEnd := i
	for runEnd < hi && (isHexDigit(s[runEnd]) || s[runEnd] == ':') {
		runEnd++
	}
	// A candidate ends at the end of the run or before a ':'; anywhere else
	// a hexadecimal digit follows, which is a word byte.
	for e := runEnd; e >= i+2; e-- {
		if e != runEnd && s[e] != ':' {
			continue
		}
		if e-i > maxIPv6Bytes || !endsValue(s, e) || followedByDotDigit(s, e) {
			continue
		}
		if e+1 < len(s) && s[e] == ':' && (isHexDigit(s[e+1]) || s[e+1] == ':') {
			continue
		}
		if validIPv6(s[i:e]) {
			return e
		}
	}
	return 0
}

// validIPv6 reports whether run is an IPv6 address by the rules of matchIPv6.
func validIPv6(run string) bool {
	dbl := strings.Index(run, "::")
	if dbl < 0 {
		n, ok := countGroups(run)
		return ok && n == 8
	}
	if strings.Contains(run[dbl+1:], "::") {
		return false // a second "::", or ":::"
	}
	left, lok := countGroups(run[:dbl])
	right, rok := countGroups(run[dbl+2:])
	return lok && rok && left+right >= 1 && left+right <= 7
}

// countGroups counts the groups of a run of 1 to 4 hexadecimal digits
// separated by single colons; the empty string has none. ok is false when a
// group is empty or longer than four digits.
func countGroups(s string) (n int, ok bool) {
	if s == "" {
		return 0, true
	}
	size := 0
	n = 1
	for k := 0; k < len(s); k++ {
		if s[k] == ':' {
			if size == 0 {
				return 0, false
			}
			n++
			size = 0
			continue
		}
		size++
		if size > 4 {
			return 0, false
		}
	}
	return n, size > 0
}

// matchIPv4 matches four groups of 1 to 3 digits, each at most 255, separated
// by '.', not followed by '.' and a digit.
func matchIPv4(s string, i int) int {
	p := i
	for g := 0; g < 4; g++ {
		if g > 0 {
			if p >= len(s) || s[p] != '.' {
				return 0
			}
			p++
		}
		q := digitsFrom(s, p, limit(s, p, 4))
		if q == p || q-p > 3 {
			return 0
		}
		v := 0
		for k := p; k < q; k++ {
			v = v*10 + int(s[k]-'0')
		}
		if v > 255 {
			return 0
		}
		p = q
	}
	if !endsValue(s, p) || followedByDotDigit(s, p) {
		return 0
	}
	return p
}

// followedByDotDigit reports whether s continues at p with '.' and a digit.
func followedByDotDigit(s string, p int) bool {
	return p+1 < len(s) && s[p] == '.' && isDigit(s[p+1])
}

// matchHex matches "0x" or "0X" and one or more hexadecimal digits.
func matchHex(s string, i int) int {
	if i+2 >= len(s) || s[i] != '0' || (s[i+1] != 'x' && s[i+1] != 'X') {
		return 0
	}
	e := hexFrom(s, i+2, limit(s, i, maxHexBytes+1))
	if e == i+2 || e-i > maxHexBytes || !endsValue(s, e) {
		return 0
	}
	return e
}

// matchDecimal matches digits, '.', digits, not followed by '.' and a digit.
func matchDecimal(s string, i int) int {
	hi := limit(s, i, maxDecimalBytes+1)
	p := digitsFrom(s, i, hi)
	if p == i || p >= hi || s[p] != '.' {
		return 0
	}
	e := digitsFrom(s, p+1, hi)
	if e == p+1 || e-i > maxDecimalBytes || !endsValue(s, e) || followedByDotDigit(s, e) {
		return 0
	}
	return e
}

// matchLongHex matches eight or more hexadecimal digits with at least one
// digit and at least one of a-f.
func matchLongHex(s string, i int) int {
	e := hexFrom(s, i, limit(s, i, maxHexBytes+1))
	if e-i < 8 || e-i > maxHexBytes || !endsValue(s, e) {
		return 0
	}
	digits, letters := false, false
	for k := i; k < e; k++ {
		if isDigit(s[k]) {
			digits = true
		} else {
			letters = true
		}
		if digits && letters {
			return e
		}
	}
	return 0
}

// matchInt matches one to 18 digits.
func matchInt(s string, i int) int {
	e := digitsFrom(s, i, limit(s, i, maxIntDigits+1))
	if e == i || e-i > maxIntDigits || !endsValue(s, e) {
		return 0
	}
	return e
}
