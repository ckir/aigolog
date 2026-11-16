// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package store

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// nameLayout is the layout of the time in the name of a database.
const nameLayout = "20060102T150405Z"

const (
	randomLen = 8                                            // hex digits after the time
	nameLen   = len(nameLayout) + 1 + randomLen + len(dbExt) // the whole name
	dbExt     = ".db"
	lockExt   = ".lock"
)

// DatabaseName returns the file name of the database of a run created at the
// given time: the time, a dash, the random part and ".db". The time is taken
// in UTC.
func DatabaseName(created time.Time, random string) string {
	return created.UTC().Format(nameLayout) + "-" + random + dbExt
}

// ParseName returns the creation time (UTC, whole seconds) in the name of a
// database of a run. It is false for any name that DatabaseName could not have
// made: the random part must be 8 lowercase hexadecimal digits.
func ParseName(base string) (created time.Time, ok bool) {
	if len(base) != nameLen || base[len(nameLayout)] != '-' || base[nameLen-len(dbExt):] != dbExt {
		return time.Time{}, false
	}
	stamp := base[:len(nameLayout)]
	for _, c := range base[len(nameLayout)+1 : nameLen-len(dbExt)] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return time.Time{}, false
		}
	}
	t, err := time.Parse(nameLayout, stamp)
	if err != nil || t.Format(nameLayout) != stamp {
		return time.Time{}, false
	}
	return t, true
}

// randomPart returns a new random part for a name: 4 random bytes in
// lowercase hexadecimal.
func randomPart() (string, error) {
	var b [randomLen / 2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
