// Package config loads capataz's user config from ~/.config/capataz/config.json.
// The file is optional — missing or partial files fall back to sane defaults.
package config

import (
	"encoding/json"
	"os"

	"github.com/bmena34/capataz/internal/paths"
)

// Config is capataz's user-tunable settings.
type Config struct {
	Usage   Usage   `json:"usage"`
	Sidebar Sidebar `json:"sidebar"`
	Review  Review  `json:"review"`
}

// Review controls the optional review steps in the issue workflow.
type Review struct {
	// SanityCheck, when true, asks the capataz-work skill to run a quick
	// sanity-check subagent over its diff before resolving an issue — a guard
	// against an agent fooling itself, not a full review (that's the R key).
	SanityCheck bool `json:"sanity_check"`
}

// Usage controls the live plan-usage gauge. NOTE: it reads the Claude Code OAuth
// token from your Keychain and calls an UNOFFICIAL Anthropic endpoint (the same
// data `claude`'s /usage shows). That endpoint isn't documented and can change
// or break — hence url/keychain_service are configurable, and enabled lets you
// turn the whole thing off.
type Usage struct {
	Enabled         bool   `json:"enabled"`
	URL             string `json:"url"`
	KeychainService string `json:"keychain_service"`
	PollSeconds     int    `json:"poll_seconds"`
	// PlanType overrides auto-detection: "pro" shows session/weekly rate-limit bars;
	// "api" shows an enterprise/per-token label instead. Leave blank to auto-detect.
	PlanType string `json:"plan_type"`
}

// Sidebar controls the dashboard sidebar pane.
type Sidebar struct {
	Width int `json:"width"` // columns for the dashboard pane on attach / new
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Usage: Usage{
			Enabled:         true,
			URL:             "https://api.anthropic.com/api/oauth/usage",
			KeychainService: "Claude Code-credentials",
			PollSeconds:     300,
		},
		Sidebar: Sidebar{Width: 40},
	}
}

// Load returns the defaults overlaid with config.json when present. Absent or
// invalid files (or zeroed required fields) fall back to defaults.
func Load() Config {
	c := Default()
	if data, err := os.ReadFile(paths.ConfigFile()); err == nil {
		_ = json.Unmarshal(data, &c) // partial overlay; ignore parse errors
	}
	d := Default()
	if c.Usage.URL == "" {
		c.Usage.URL = d.Usage.URL
	}
	if c.Usage.KeychainService == "" {
		c.Usage.KeychainService = d.Usage.KeychainService
	}
	if c.Usage.PollSeconds <= 0 {
		c.Usage.PollSeconds = d.Usage.PollSeconds
	}
	if c.Sidebar.Width <= 0 {
		c.Sidebar.Width = d.Sidebar.Width
	}
	return c
}

// WriteDefault writes a default config file if none exists yet, for
// discoverability. No-op when the file is already present.
func WriteDefault() error {
	if _, err := os.Stat(paths.ConfigFile()); err == nil {
		return nil
	}
	if err := os.MkdirAll(paths.ConfigDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(paths.ConfigFile(), data, 0o644)
}
