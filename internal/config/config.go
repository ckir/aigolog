// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

// Package config reads the configuration of aigolog: an optional JSON file
// and four environment variables. It prints nothing and keeps no state.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	// DefaultKeepDays is how many days of databases are kept.
	DefaultKeepDays = 7

	// DefaultMaxTotalBytes is the size, in bytes, that the kept databases may
	// take together. The figure is provisional: it is replaced after B2
	// measures a database next to its log.
	DefaultMaxTotalBytes int64 = 1 << 30
)

// maxKeepDays is the largest keep_days accepted (about 100 years). A later
// step multiplies it by 24 hours into a time.Duration, which would wrap for
// huge values.
const maxKeepDays = 36500

// Config is the configuration of aigolog.
type Config struct {
	// KeepDays is how many days of databases are kept; at least 1.
	KeepDays int
	// MaxTotalBytes is the size the kept databases may take together; at least 1.
	MaxTotalBytes int64
	// CacheDir is the folder of the databases; empty means the default folder.
	CacheDir string
}

// Defaults returns the configuration used when nothing says otherwise.
func Defaults() Config {
	return Config{KeepDays: DefaultKeepDays, MaxTotalBytes: DefaultMaxTotalBytes}
}

// DefaultPath returns where the configuration file is looked for when
// AIGOLOG_CONFIG is not set: the user configuration folder, then
// aigolog/config.json. It creates nothing.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the user configuration folder: %w", err)
	}
	return filepath.Join(dir, "aigolog", "config.json"), nil
}

// fileConfig is the JSON file. A pointer tells a key that is left out from a
// key that is set, so a left-out key keeps its default.
type fileConfig struct {
	KeepDays      *int    `json:"keep_days"`
	MaxTotalBytes *int64  `json:"max_total_bytes"`
	CacheDir      *string `json:"cache_dir"`
}

// Load returns the configuration: the defaults, then the file named by
// AIGOLOG_CONFIG (or the file at DefaultPath when it is not set), then
// AIGOLOG_KEEP_DAYS, AIGOLOG_MAX_TOTAL_BYTES and AIGOLOG_CACHE_DIR, each of
// which replaces the value before it when it is set and not empty. A missing
// file, or a path under something that is not a folder, is not an error (a
// missing file named by AIGOLOG_CONFIG gives the defaults; the caller may want
// to tell the user). lookupEnv reads the environment; Load never calls
// os.Getenv. A nil lookupEnv means an empty environment (the file at
// DefaultPath is still read).
func Load(lookupEnv func(string) (string, bool)) (Config, error) {
	if lookupEnv == nil {
		lookupEnv = func(string) (string, bool) { return "", false }
	}
	get := func(k string) (string, bool) {
		v, ok := lookupEnv(k)
		return v, ok && v != ""
	}

	cfg := Defaults()

	path, ok := get("AIGOLOG_CONFIG")
	if !ok {
		// A machine with no configuration folder just has no file; the
		// environment variables still apply.
		if p, err := DefaultPath(); err == nil {
			path = p
		}
	}
	if path != "" {
		if err := readFile(path, &cfg); err != nil {
			return Config{}, err
		}
	}

	if v, ok := get("AIGOLOG_KEEP_DAYS"); ok {
		n, err := envInt("AIGOLOG_KEEP_DAYS", v, maxKeepDays)
		if err != nil {
			return Config{}, err
		}
		cfg.KeepDays = int(n)
	}
	if v, ok := get("AIGOLOG_MAX_TOTAL_BYTES"); ok {
		n, err := envInt("AIGOLOG_MAX_TOTAL_BYTES", v, math.MaxInt64)
		if err != nil {
			return Config{}, err
		}
		cfg.MaxTotalBytes = n
	}
	if v, ok := get("AIGOLOG_CACHE_DIR"); ok {
		cfg.CacheDir = v
	}
	return cfg, nil
}

// readFile applies the file at path to cfg. A missing file, or a path under
// something that is not a folder, changes nothing.
func readFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading the configuration file %s: %w", path, err)
	}

	// Windows editors and PowerShell 5.1 write a UTF-8 byte-order mark; drop
	// one, before the empty check, so a file of only a mark reads as empty.
	// UTF-16 is not supported and stays an error.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f fileConfig
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the configuration file %s: it is empty, it must hold one JSON object", path)
		}
		return fmt.Errorf("the configuration file %s: %w", path, err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("the configuration file %s: unexpected data after the first JSON value", path)
	}

	if f.KeepDays != nil {
		if *f.KeepDays < 1 || *f.KeepDays > maxKeepDays {
			return fmt.Errorf("the configuration file %s: keep_days is %d, it must be a whole number from 1 to %d", path, *f.KeepDays, maxKeepDays)
		}
		cfg.KeepDays = *f.KeepDays
	}
	if f.MaxTotalBytes != nil {
		if *f.MaxTotalBytes < 1 {
			return fmt.Errorf("the configuration file %s: max_total_bytes is %d, it must be a whole number of at least 1", path, *f.MaxTotalBytes)
		}
		cfg.MaxTotalBytes = *f.MaxTotalBytes
	}
	if f.CacheDir != nil {
		cfg.CacheDir = *f.CacheDir
	}
	return nil
}

// envInt reads a whole number from 1 to upper out of the variable name.
func envInt(name, value string, upper int64) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	switch {
	case err != nil && errors.Is(err, strconv.ErrRange) && n > 0:
		// Too large for an int64 (ParseInt clamps n to the largest value).
		return 0, fmt.Errorf("%s is %q, it must be a whole number of at least 1 and at most %d", name, value, upper)
	case err != nil || n < 1:
		return 0, fmt.Errorf("%s is %q, it must be a whole number of at least 1", name, value)
	case n > upper:
		return 0, fmt.Errorf("%s is %q, it must be a whole number of at most %d", name, value, upper)
	}
	return n, nil
}
