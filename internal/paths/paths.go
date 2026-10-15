// Package paths centralizes the on-disk locations capataz uses.
package paths

import (
	"os"
	"path/filepath"
)

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

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

// ClaudeSettings is the user's global Claude Code settings file.
func ClaudeSettings() string { return filepath.Join(home(), ".claude", "settings.json") }

// ClaudeConfigJSON is Claude Code's main per-user state file (~/.claude.json),
// which records, among much else, per-project folder-trust under
// projects.<abs-dir>.hasTrustDialogAccepted.
func ClaudeConfigJSON() string { return filepath.Join(home(), ".claude.json") }

// ClaudeSkillsDir is where Claude Code loads user skills from. capataz installs
// its native skills here.
func ClaudeSkillsDir() string { return filepath.Join(home(), ".claude", "skills") }

// ClaudeAgentsDir is where Claude Code loads user subagent definitions from.
// capataz installs its native per-role agent defs here.
func ClaudeAgentsDir() string { return filepath.Join(home(), ".claude", "agents") }

// LegacyPlist is the launchd agent the old tmux build installed for its
// daemon; install and uninstall remove it.
func LegacyPlist() string {
	return filepath.Join(home(), "Library", "LaunchAgents", "dev.capataz.plist")
}
