// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package exitcode holds the process exit codes aigolog returns.
package exitcode

const (
	// OK is returned when the work was done.
	OK = 0
	// Failure is returned when the work failed.
	Failure = 1
	// Usage is returned when the command line was wrong.
	Usage = 2
	// ModelFailed is returned when a model was configured and failed.
	ModelFailed = 3
	// LogRefused is returned when Windows refused to give up a log.
	LogRefused = 4
	// StorageUnavailable is returned when the store cannot be written.
	StorageUnavailable = 5
)
