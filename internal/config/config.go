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
	Context  Context  `json:"context"`
	Resume   Resume   `json:"resume"`
	Models   Models   `json:"models"`
}

// Resume controls retention of ended sessions so they survive a tmux/machine
// restart and can be resumed later.
type Resume struct {
	// MaxRetained caps how many ended sessions the daemon keeps persisted (the
	// most-recent by last_change). Older ended rows are trimmed. Default 20.
	MaxRetained int `json:"max_retained"`
}

// Models routes a model alias to each dispatched agent role, so heavier reasoning
// goes where it earns its cost (reviews/security/conflicts on opus) and routine
// implementation stays cheap (sonnet). The recognized aliases are the fixed set
// opus/sonnet/fable/haiku/default; "default" (or empty, or any unrecognized value)
// means "don't pass a model" — the dispatched claude/subagent inherits its own
// default. Parsing is deliberately tolerant: an unknown alias is treated as
// inherit rather than rejected, so a typo degrades gracefully instead of crashing.
//
// The recognized roles are:
//
//   - implementer  — the capataz-work implementer (single-issue dispatch, drain
//     subagents). Default sonnet.
//   - reviewer     — the fresh-context correctness review (R). Default opus.
//   - security     — the fresh-context OWASP security review (S). Default opus.
//   - conflict     — the capataz-fix-conflict resolver. Default opus.
//   - orchestrator — the session that hands work to subagents (single-issue
//     orchestration + drain). Default "default" (inherit).
type Models struct {
	Implementer  string `json:"implementer"`
	Reviewer     string `json:"reviewer"`
	Security     string `json:"security"`
	Conflict     string `json:"conflict"`
	Orchestrator string `json:"orchestrator"`
}

// Aliases is the fixed set of recognized model aliases. "default" means inherit
// (pass no --model / no per-subagent model).
var modelAliases = map[string]bool{
	"opus":    true,
	"sonnet":  true,
	"fable":   true,
	"haiku":   true,
	"default": true,
}

// Resolve normalizes an alias to a value safe to thread into a dispatch: a
// recognized alias other than "default" is returned as-is; "default", empty, or
// any unrecognized value collapses to "" (inherit — pass no model). Tolerant by
// design so a typo in config.json degrades to inherit rather than breaking a
// dispatch.
func (Models) Resolve(alias string) string {
	if alias == "" || alias == "default" {
		return ""
	}
	if modelAliases[alias] {
		return alias
	}
	return "" // unknown alias: inherit rather than pass a bad --model
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
		Resume:   Resume{MaxRetained: 20},
		Models: Models{
			Implementer:  "sonnet",
			Reviewer:     "opus",
			Security:     "opus",
			Conflict:     "opus",
			Orchestrator: "default", // inherit
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
	if c.Usage.PollSeconds <= 0 {
		c.Usage.PollSeconds = d.Usage.PollSeconds
	}
	if c.Sidebar.Width <= 0 {
		c.Sidebar.Width = d.Sidebar.Width
	}
	if c.Parallel.MaxConcurrency <= 0 {
		c.Parallel.MaxConcurrency = d.Parallel.MaxConcurrency
	}
	if c.Resume.MaxRetained <= 0 {
		c.Resume.MaxRetained = d.Resume.MaxRetained
	}
	// A partial "models" block (some roles set, others omitted) unmarshals the
	// absent roles to "", which would silently drop their default. Re-seed each
	// empty role from the defaults so an unspecified role keeps its built-in
	// model. A role explicitly set to "default" stays "default" (inherit) — only
	// the empty (absent) case falls back here.
	if c.Models.Implementer == "" {
		c.Models.Implementer = d.Models.Implementer
	}
	if c.Models.Reviewer == "" {
		c.Models.Reviewer = d.Models.Reviewer
	}
	if c.Models.Security == "" {
		c.Models.Security = d.Models.Security
	}
	if c.Models.Conflict == "" {
		c.Models.Conflict = d.Models.Conflict
	}
	if c.Models.Orchestrator == "" {
		c.Models.Orchestrator = d.Models.Orchestrator
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
