// Copyright (c) 2026 Costas Kirgoussios
//
// This software is licensed under the PolyForm Noncommercial License 1.0.0.
// You may obtain a copy of the License at:
// https://polyformproject.org/licenses/noncommercial/1.0.0/

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNoFileGivesTheDefaults(t *testing.T) {
	got, err := Load(env(map[string]string{"AIGOLOG_CONFIG": filepath.Join(t.TempDir(), "absent.json")}))
	if err != nil || got != (Config{KeepDays: 7, MaxTotalBytes: 1 << 30}) {
		t.Errorf("got %+v, %v", got, err)
	}
	if Defaults() != got {
		t.Errorf("Defaults() = %+v", Defaults())
	}
}

func TestTheFileOverridesTheDefaultsKeyByKey(t *testing.T) {
	p := writeConfig(t, `{"keep_days": 3, "cache_dir": "/tmp/x"}`)
	got, err := Load(env(map[string]string{"AIGOLOG_CONFIG": p}))
	if err != nil || got != (Config{KeepDays: 3, MaxTotalBytes: 1 << 30, CacheDir: "/tmp/x"}) {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestEnvironmentVariablesOverrideTheFile(t *testing.T) {
	p := writeConfig(t, `{"keep_days": 3, "max_total_bytes": 100, "cache_dir": "/from/file"}`)
	got, err := Load(env(map[string]string{"AIGOLOG_CONFIG": p, "AIGOLOG_KEEP_DAYS": "9", "AIGOLOG_MAX_TOTAL_BYTES": "5000", "AIGOLOG_CACHE_DIR": "/from/env"}))
	if err != nil || got != (Config{KeepDays: 9, MaxTotalBytes: 5000, CacheDir: "/from/env"}) {
		t.Errorf("got %+v, %v", got, err)
	}
	got, _ = Load(env(map[string]string{"AIGOLOG_CONFIG": p, "AIGOLOG_KEEP_DAYS": ""}))
	if got.KeepDays != 3 {
		t.Errorf("an empty variable must not override: KeepDays = %d", got.KeepDays)
	}
}

func TestBadConfigurationIsRefusedWithAMessageThatNamesIt(t *testing.T) {
	cases := []struct{ name, body, envKey, envVal, mention string }{
		{"unknown key", `{"keep_dayz": 3}`, "", "", "keep_dayz"},
		{"wrong type", `{"keep_days": "three"}`, "", "", "keep_days"},
		{"zero days", `{"keep_days": 0}`, "", "", "keep_days"},
		{"negative size", `{"max_total_bytes": -5}`, "", "", "max_total_bytes"},
		{"not json", `keep_days = 3`, "", "", "config"},
		{"two values", `{} {}`, "", "", "config"},
		{"bad variable", `{}`, "AIGOLOG_KEEP_DAYS", "soon", "AIGOLOG_KEEP_DAYS"},
		{"bad size variable", `{}`, "AIGOLOG_MAX_TOTAL_BYTES", "0", "AIGOLOG_MAX_TOTAL_BYTES"},
	}
	for _, c := range cases {
		m := map[string]string{"AIGOLOG_CONFIG": writeConfig(t, c.body)}
		if c.envKey != "" {
			m[c.envKey] = c.envVal
		}
		_, err := Load(env(m))
		if err == nil || !strings.Contains(err.Error(), c.mention) {
			t.Errorf("%s: err = %v, want a message that names %q", c.name, err, c.mention)
		}
	}
}

func TestDefaultPathEndsInAigologConfigJSON(t *testing.T) {
	p, err := DefaultPath()
	if err != nil {
		t.Skipf("no user configuration folder here: %v", err)
	}
	if filepath.Base(p) != "config.json" || filepath.Base(filepath.Dir(p)) != "aigolog" {
		t.Errorf("DefaultPath = %q", p)
	}
}

func TestAHugeKeepDaysIsRefused(t *testing.T) {
	// A later step multiplies keep_days by 24 hours into a time.Duration.
	ok := writeConfig(t, `{"keep_days": 36500}`)
	got, err := Load(env(map[string]string{"AIGOLOG_CONFIG": ok}))
	if err != nil || got.KeepDays != 36500 {
		t.Errorf("file, 36500: got %+v, %v", got, err)
	}
	got, err = Load(env(map[string]string{"AIGOLOG_CONFIG": ok, "AIGOLOG_KEEP_DAYS": "36500"}))
	if err != nil || got.KeepDays != 36500 {
		t.Errorf("variable, 36500: got %+v, %v", got, err)
	}

	bad := writeConfig(t, `{"keep_days": 36501}`)
	_, err = Load(env(map[string]string{"AIGOLOG_CONFIG": bad}))
	if err == nil || !strings.Contains(err.Error(), "keep_days") || !strings.Contains(err.Error(), "36501") {
		t.Errorf("file, 36501: err = %v, want a message that names keep_days and 36501", err)
	}
	_, err = Load(env(map[string]string{"AIGOLOG_CONFIG": ok, "AIGOLOG_KEEP_DAYS": "36501"}))
	if err == nil || !strings.Contains(err.Error(), "AIGOLOG_KEEP_DAYS") || !strings.Contains(err.Error(), "36501") {
		t.Errorf("variable, 36501: err = %v, want a message that names AIGOLOG_KEEP_DAYS and 36501", err)
	}
}

func TestNoConfigFolderAndNoOverrideGivesTheDefaults(t *testing.T) {
	switch runtime.GOOS {
	case "windows":
		t.Skip("os.UserConfigDir reads AppData here, which this test does not clear")
	case "darwin", "ios", "plan9", "js", "wasip1":
		t.Skipf("os.UserConfigDir reads other variables on %s", runtime.GOOS)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if _, err := os.UserConfigDir(); err == nil {
		t.Skip("os.UserConfigDir still succeeds here")
	}
	got, err := Load(env(nil))
	if err != nil || got != Defaults() {
		t.Errorf("got %+v, %v, want the defaults and no error", got, err)
	}
	got, err = Load(env(map[string]string{"AIGOLOG_KEEP_DAYS": "4"}))
	if err != nil || got.KeepDays != 4 {
		t.Errorf("a variable must still apply: got %+v, %v", got, err)
	}
}
