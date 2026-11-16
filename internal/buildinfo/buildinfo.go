// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package buildinfo holds facts about the build of the program.
package buildinfo

// Version is the version of the program. It is a variable so that
// `just build` can set it at build time.
var Version = "0.1.0-dev"
