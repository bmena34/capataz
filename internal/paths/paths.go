// Package paths centralizes the on-disk locations capataz uses.
package paths

import (
	"os"
	"path/filepath"
)

const launchdLabel = "dev.capataz"

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// Dir is the capataz data directory (~/.capataz).
func Dir() string { return filepath.Join(home(), ".capataz") }

// ConfigDir is the user config directory (~/.config/capataz, honoring
// $XDG_CONFIG_HOME).
func ConfigDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "capataz")
	}
	return filepath.Join(home(), ".config", "capataz")
}

// ConfigFile is the user config file (~/.config/capataz/config.json).
func ConfigFile() string { return filepath.Join(ConfigDir(), "config.json") }

// WorktreesDir holds the linked git worktrees capataz creates for parallel
// dispatch (~/.capataz/worktrees), namespaced per repo underneath.
func WorktreesDir() string { return filepath.Join(Dir(), "worktrees") }

// Sock is the Unix socket the daemon listens on.
func Sock() string { return filepath.Join(Dir(), "capataz.sock") }

// DB is the SQLite history database.
func DB() string { return filepath.Join(Dir(), "history.db") }

// LogFile is where the launchd-managed daemon writes stdout/stderr.
func LogFile() string { return filepath.Join(Dir(), "daemon.log") }

// AppLog is the shared application log written by all components.
func AppLog() string { return filepath.Join(Dir(), "capataz.log") }

// PidDir holds one marker file per running dashboard (named by PID) so
// `capataz quit` can signal every instance, not just the most recent one.
func PidDir() string { return filepath.Join(Dir(), "pids") }

// ClaudeSettings is the user's global Claude Code settings file.
func ClaudeSettings() string { return filepath.Join(home(), ".claude", "settings.json") }

// ClaudeConfigJSON is Claude Code's main per-user state file (~/.claude.json),
// which records, among much else, per-project folder-trust under
// projects.<abs-dir>.hasTrustDialogAccepted.
func ClaudeConfigJSON() string { return filepath.Join(home(), ".claude.json") }

// ClaudeSkillsDir is where Claude Code loads user skills from. capataz installs
// its native skills here.
func ClaudeSkillsDir() string { return filepath.Join(home(), ".claude", "skills") }

// Plist is the launchd agent plist path.
func Plist() string {
	return filepath.Join(home(), "Library", "LaunchAgents", launchdLabel+".plist")
}

// LaunchdLabel is the launchd job label.
func LaunchdLabel() string { return launchdLabel }

// Ensure creates the data directory if needed.
func Ensure() error { return os.MkdirAll(Dir(), 0o755) }
