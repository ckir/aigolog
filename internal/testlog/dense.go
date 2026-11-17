// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package testlog

import (
	"math/rand"
	"strconv"
	"strings"
)

// denseTokens is the number of tokens of a line of the dense log.
const denseTokens = 256

// denseWord writes k in base 26 with the letters a to z only, so it holds no
// digit.
func denseWord(k int) string {
	if k == 0 {
		return "a"
	}
	var b []byte
	for ; k > 0; k /= 26 {
		b = append(b, byte('a'+k%26))
	}
	return string(b)
}

// Dense makes a log of lines of 256 tokens: the word dense, two words that
// hold no digit (each pair of them is a cluster, 300 in all), the word values,
// so that the first four tokens, which the miner's tree takes as its path, hold
// no digit, and 252 tokens of 30 to 42 integers of 1 to 6 digits joined by
// commas. It has no time stamp.
// It is the shape that made the miner's memory and the database largest before
// the miner capped the values it takes from a token and from a message.
func Dense(seed int64, lines int) []byte {
	if lines <= 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(seed))
	var sb strings.Builder
	for i := 0; i < lines; i++ {
		sb.WriteString("dense ")
		sb.WriteString(denseWord(i % 3))
		sb.WriteByte(' ')
		sb.WriteString(denseWord(i / 3 % 100))
		sb.WriteString(" values")
		for t := 4; t < denseTokens; t++ {
			sb.WriteByte(' ')
			for n := 30 + rng.Intn(13); n > 0; n-- {
				// 1 to 6 digits: a number from 1 to 999999 with no leading zero.
				sb.WriteString(strconv.Itoa(1 + rng.Intn(pow10(1+rng.Intn(6))-1)))
				if n > 1 {
					sb.WriteByte(',')
				}
			}
		}
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}

// pow10 returns 10 to the power n.
func pow10(n int) int {
	p := 1
	for ; n > 0; n-- {
		p *= 10
	}
	return p
}
