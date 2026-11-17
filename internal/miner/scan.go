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
// written. Text is Raw with each typed value replaced by its marker, then cut
// to at most MaxTokenTextBytes bytes on a character boundary. Values are the
// typed values, in order, as written.
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
		if tok.Values == nil {
			sb.Grow(len(raw))
		}
		sb.WriteString(raw[copied:i])
		sb.WriteString(typ.Marker())
		tok.Values = append(tok.Values, Value{Type: typ, Text: raw[i:end]})
		copied = end
		i = end
	}
	text := raw
	if tok.Values != nil {
		sb.WriteString(raw[copied:])
		text = sb.String()
	}
	tok.Text = text[:cutPoint(text, MaxTokenTextBytes)]
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

// digitsFrom returns the index after the run of digits that starts at i.
func digitsFrom(s string, i int) int {
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i
}

// hexFrom returns the index after the run of hexadecimal digits at i.
func hexFrom(s string, i int) int {
	for i < len(s) && isHexDigit(s[i]) {
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
// with one "::" (and at least one group) or exactly eight groups, in at most
// 45 bytes.
func matchIPv6(s string, i int) int {
	const maxLen = 45
	e := i
	for e < len(s) && (isHexDigit(s[e]) || s[e] == ':') && e-i <= maxLen {
		e++
	}
	if e-i > maxLen || !endsValue(s, e) {
		return 0
	}
	run := s[i:e]
	dbl := strings.Index(run, "::")
	if dbl < 0 {
		if n, ok := countGroups(run); !ok || n != 8 {
			return 0
		}
		return e
	}
	if strings.Contains(run[dbl+1:], "::") {
		return 0 // a second "::", or ":::"
	}
	left, lok := countGroups(run[:dbl])
	right, rok := countGroups(run[dbl+2:])
	if !lok || !rok || left+right < 1 {
		return 0
	}
	return e
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
		q := digitsFrom(s, p)
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
	e := hexFrom(s, i+2)
	if e == i+2 || !endsValue(s, e) {
		return 0
	}
	return e
}

// matchDecimal matches digits, '.', digits, not followed by '.' and a digit.
func matchDecimal(s string, i int) int {
	p := digitsFrom(s, i)
	if p == i || p >= len(s) || s[p] != '.' {
		return 0
	}
	e := digitsFrom(s, p+1)
	if e == p+1 || !endsValue(s, e) || followedByDotDigit(s, e) {
		return 0
	}
	return e
}

// matchLongHex matches eight or more hexadecimal digits with at least one
// digit and at least one of a-f.
func matchLongHex(s string, i int) int {
	e := hexFrom(s, i)
	if e-i < 8 || !endsValue(s, e) {
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
	e := digitsFrom(s, i)
	if e == i || e-i > 18 || !endsValue(s, e) {
		return 0
	}
	return e
}
