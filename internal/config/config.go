// Package config loads capataz's user config from ~/.config/capataz/config.json.
// The file is optional — missing or partial files fall back to sane defaults.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/bmena34/capataz/internal/paths"
)

// Config is capataz's user-tunable settings.
type Config struct {
	Usage   Usage   `json:"usage"`
	Context Context `json:"context"`
	Drain   Drain   `json:"drain"`
}

// DefaultStallMinutes is how long a working agent may go without changing a
// file in its worktree before the drain notifies the operator.
const DefaultStallMinutes = 15

// Drain holds `capataz drain` settings: global values plus optional per-repo
// overrides keyed by absolute repo root.
type Drain struct {
	// BranchPrefix is prepended to dispatched branch names. Empty by default,
	// so branches look like <feature>-NN-slug.
	BranchPrefix string `json:"branch_prefix"`
	// StallMinutes is how many minutes a working agent may go without a file
	// change in its worktree before the operator is notified (0 = default 15).
	StallMinutes int `json:"stall_minutes"`
	// Repos maps an absolute repo root to overrides for that repo.
	Repos map[string]DrainRepo `json:"repos"`
}

// DrainRepo is a per-repo override. Pointer fields distinguish "unset" (inherit
// the global value) from an explicit empty/zero value.
type DrainRepo struct {
	BranchPrefix *string `json:"branch_prefix"`
	StallMinutes *int    `json:"stall_minutes"`
}

// DrainSettings is the effective drain configuration for one repo.
type DrainSettings struct {
	BranchPrefix string
	StallMinutes int
}

// DrainFor resolves the effective drain settings for repoRoot: a per-repo entry
// overrides the global value field by field; stall_minutes falls back to its
// default when unset or non-positive.
func (c Config) DrainFor(repoRoot string) DrainSettings {
	s := DrainSettings{BranchPrefix: c.Drain.BranchPrefix, StallMinutes: c.Drain.StallMinutes}
	r, ok := c.Drain.Repos[filepath.Clean(repoRoot)]
	if !ok {
		r, ok = c.Drain.Repos[repoRoot]
	}
	if ok {
		if r.BranchPrefix != nil {
			s.BranchPrefix = *r.BranchPrefix
		}
		if r.StallMinutes != nil {
			s.StallMinutes = *r.StallMinutes
		}
	}
	if s.StallMinutes <= 0 {
		s.StallMinutes = DefaultStallMinutes
	}
	return s
}

// Context tunes the context-window gauge. The transcript records only the bare
// model id (never the [1m] beta marker), so a 1M session can't be auto-detected;
// Limits lets you pin a window size per model.
type Context struct {
	// Limits maps a model id (matched exactly or as a substring of the reported,
	// lowercased model) to a window size ("1m", "200k", or a raw number). Empty
	// by default; unmatched models fall back to the built-in table.
	Limits map[string]string `json:"limits"`
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
	// PlanType overrides auto-detection: "pro" shows session/weekly rate-limit bars;
	// "api" shows an enterprise/per-token label instead. Leave blank to auto-detect.
	PlanType string `json:"plan_type"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Drain: Drain{StallMinutes: DefaultStallMinutes},
		Usage: Usage{
			Enabled:         true,
			URL:             "https://api.anthropic.com/api/oauth/usage",
			KeychainService: "Claude Code-credentials",
		},
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
