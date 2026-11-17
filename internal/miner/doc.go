// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package miner turns the text of a log record into a pattern with typed
// slots. Its scanner splits a record into tokens and masks the typed values in
// them (whole numbers, decimals, hexadecimal, IPv4, IPv6 and UUID), and the
// clustering that groups the results into patterns builds on it.
//
// The scanner is bounded: at most MaxTokens tokens of a message, at most
// MaxTokenTextBytes of text in a token, at most MaxTokenValues typed values in
// a token and at most MaxMessageValues in a message. Past a cap the rest is
// text, so a pattern has at most MaxMessageValues typed slots and one Word slot
// for each wildcard token, however many numbers the log holds.
package miner
