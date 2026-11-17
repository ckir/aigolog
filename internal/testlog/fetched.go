// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package testlog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// sshdSHA256 is the checksum of the fetched sshd sample (LogHub's OpenSSH_2k),
// in hexadecimal.
const sshdSHA256 = "1e4912727fa88245113d41b16a0cd25ceadba7f931e1c406542885b91254264f"

// sshdNotice is the text a test is skipped with when the sample is absent.
const sshdNotice = `the sshd sample is not fetched: run "just fetch-openssh"`

// OpenSSH2k returns the sshd sample, testdata/fetched/OpenSSH_2k.log under the
// folder that holds go.mod, found by walking up from the working directory. A
// test is skipped, with a notice that says how to fetch the file, when the
// file is absent, and fails when its checksum is not the one recorded.
func OpenSSH2k(t testing.TB) []byte {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("finding the working directory: %v", err)
		return nil
	}
	root, ok := findRoot(wd)
	if !ok {
		t.Fatalf("no go.mod in %s or above it: cannot find the folder of the sshd sample", wd)
		return nil
	}
	return readFetched(t, root, sshdSHA256)
}

// findRoot walks up from start to the first folder that holds a go.mod.
func findRoot(start string) (string, bool) {
	dir := start
	for {
		if st, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !st.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// readFetched reads the sample of the repository at root and checks it against
// want, a checksum in hexadecimal.
func readFetched(t testing.TB, root, want string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "testdata", "fetched", "OpenSSH_2k.log"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip(sshdNotice)
		return nil
	}
	if err != nil {
		t.Fatalf("reading the sshd sample: %v", err)
		return nil
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("the sshd sample has the checksum %s, want %s: fetch it again with \"just fetch-openssh\"", got, want)
		return nil
	}
	return data
}
