// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package record defines a record: one entry of a log, whatever kind of log it
// came from. The types here are plain data. Reader is the plug point for kinds
// of log: each kind supplies a Reader that hands out its records one by one.
package record

// StampState says what a reader found when it looked for a time in a record.
type StampState int

const (
	// NoStamp means the reader found no time in the record.
	NoStamp StampState = iota
	// Resolved means Stamp.Nanos holds the time.
	Resolved
	// YearPending means the stamp has no year: Month, Day and NanoOfDay hold
	// it, and Nanos is not meaningful until the year is worked out.
	YearPending
)

// Stamp is the time of a record, as the log states it.
type Stamp struct {
	// State says which of the other fields mean something.
	State StampState
	// Nanos is nanoseconds since 1970. For a stamp with a zone it is the real
	// instant. For a stamp with no zone it is the wall-clock reading carried
	// as if it were UTC.
	Nanos int64
	// Exact is true when the log states a zone or the stamp is a count since
	// 1970.
	Exact bool
	// Zone is the zone text exactly as written (+02:00, Z, -0700); it is empty
	// when the log states none.
	Zone string
	// Month and Day (each from 1) and NanoOfDay (nanoseconds since midnight,
	// fraction included) hold a YearPending stamp.
	Month, Day int
	NanoOfDay  int64
	// Len is the number of bytes at the start of the record's first line that
	// the stamp covers, a priority tag (<34>) and a bracket before it
	// included. Raw[Len:] is the message. It is 0 when State is NoStamp.
	Len int
}

// Record is one entry of a log.
type Record struct {
	// Source is the number of the source in the run, from 1, set by the caller
	// of the reader.
	Source int
	// Line is the number of the record's first line, from 1.
	Line int64
	// Offset and Length are the record's position and size, in bytes. For a
	// UTF-8 input they are positions in the original input, and a byte-order
	// mark counts. For a UTF-16 input they are positions in the text decoded
	// to UTF-8, and the mark does not count. The Bytes method of the text
	// reader always counts the bytes of the original input.
	Offset, Length int64
	// Stamp is the time of the record.
	Stamp Stamp
	// Raw is the record's text.
	Raw string
}

// Reader hands out the records of one source.
type Reader interface {
	// Next returns the next record, and io.EOF after the last one.
	Next() (Record, error)
}
