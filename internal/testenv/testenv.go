// Package testenv runs a package's tests in a home of their own, so no test
// ever reads or writes the user's real config files.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Vars are the environment variables pointed into the temporary home.
var Vars = []string{
	"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
}

// Cleared are variables that would point an agent or this tool elsewhere.
var Cleared = []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "QCS_HOME"}

// Setup points the home and config folders into dir.
func Setup(dir string) {
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir)
	os.Setenv("APPDATA", filepath.Join(dir, "AppData", "Roaming"))
	os.Setenv("LOCALAPPDATA", filepath.Join(dir, "AppData", "Local"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	os.Setenv("XDG_DATA_HOME", filepath.Join(dir, ".local", "share"))
	os.Setenv("XDG_STATE_HOME", filepath.Join(dir, ".local", "state"))
	os.Setenv("XDG_CACHE_HOME", filepath.Join(dir, ".cache"))
	for _, v := range Cleared {
		os.Unsetenv(v)
	}
}

// Main is a TestMain: it runs m with a temporary home and removes it after.
func Main(m *testing.M) {
	dir, err := os.MkdirTemp("", "qcs-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		os.Exit(1)
	}
	Setup(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// Home gives a test its own fresh home, restored when the test ends.
func Home(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	for _, v := range append(append([]string{}, Vars...), Cleared...) {
		if old, ok := os.LookupEnv(v); ok {
			t.Cleanup(func() { os.Setenv(v, old) })
		} else {
			t.Cleanup(func() { os.Unsetenv(v) })
		}
	}
	Setup(dir)
	return dir
}
