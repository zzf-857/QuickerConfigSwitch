// Package appdir says where QuickerConfigSwitch keeps its own files and where
// each agent keeps its config, on every system.
package appdir

import (
	"os"
	"path/filepath"
	"runtime"
)

// Name is the folder name on macOS and Windows; Linux uses its lowercase.
const Name = "QuickerConfigSwitch"

// Home is the user's home folder.
func Home() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

// Dir is this tool's data folder: QCS_HOME when set, else
// ~/Library/Application Support/QuickerConfigSwitch on macOS,
// %APPDATA%\QuickerConfigSwitch on Windows and
// $XDG_CONFIG_HOME/quickerconfigswitch (~/.config/...) elsewhere.
func Dir() string {
	if d := os.Getenv("QCS_HOME"); d != "" {
		return d
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(Home(), "Library", "Application Support", Name)
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, Name)
		}
		return filepath.Join(Home(), "AppData", "Roaming", Name)
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "quickerconfigswitch")
	}
	return filepath.Join(Home(), ".config", "quickerconfigswitch")
}

// Path joins parts under Dir.
func Path(parts ...string) string { return filepath.Join(append([]string{Dir()}, parts...)...) }

// StateDir holds stash.json, applied.json and the lock.
func StateDir() string { return Path("state") }

// BackupDir holds snapshots and rolling backups.
func BackupDir() string { return Path("backups") }

// CodexHome is Codex's folder: CODEX_HOME, else ~/.codex.
func CodexHome() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	return filepath.Join(Home(), ".codex")
}

// ClaudeHome is Claude Code's folder: CLAUDE_CONFIG_DIR, else ~/.claude.
func ClaudeHome() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(Home(), ".claude")
}
