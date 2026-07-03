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
	Usage    Usage    `json:"usage"`
	Sidebar  Sidebar  `json:"sidebar"`
	Review   Review   `json:"review"`
	Dispatch Dispatch `json:"dispatch"`
	Parallel Parallel `json:"parallel"`
}

// Parallel controls the hands-off drain (the dashboard's p key).
type Parallel struct {
	// MaxConcurrency caps how many issues the drain orchestrator works at once —
	// i.e. the most capataz-work subagents running concurrently in a single wave.
	// Overlapping/over-cap issues just fall to the next wave. Default 3; keeps one
	// orchestrator context and the machine from being swamped by a wide wave.
	MaxConcurrency int `json:"max_concurrency"`
}

// Dispatch controls how capataz launches the sessions it dispatches to do
// automated work (the hands-off drain orchestrator, the R/S reviews, and the
// fix-conflict resolver — any session capataz starts with an opening prompt).
type Dispatch struct {
	// SkipPermissions runs those dispatched sessions with
	// `claude --dangerously-skip-permissions` so they don't stall forever on
	// Claude Code's interactive permission prompt (a dispatched agent reads the
	// backlog at an absolute path outside its cwd, claims, edits, and commits —
	// each of which would otherwise prompt, and nobody is there to approve).
	// It applies ONLY to prompted/dispatched sessions, never to the interactive
	// session you open by hand (prefix+n). Default true. Set it false to
	// supervise dispatched sessions and approve their tools yourself; the
	// tradeoff is that an unattended drain will then freeze on the first prompt.
	SkipPermissions bool `json:"skip_permissions"`
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
		Sidebar:  Sidebar{Width: 40},
		Dispatch: Dispatch{SkipPermissions: true},
		Parallel: Parallel{MaxConcurrency: 3},
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
	if c.Parallel.MaxConcurrency <= 0 {
		c.Parallel.MaxConcurrency = d.Parallel.MaxConcurrency
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
