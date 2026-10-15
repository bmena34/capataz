package usageview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// defaultLimit is the context window assumed when nothing else resolves.
const defaultLimit = 200_000

// modelWindows is the built-in fallback table of context-window sizes, keyed by
// a substring of the lowercased model id. It only lists models Claude Code
// offers *exclusively* in a 1M-context form, because that is the only case where
// the bare id in a transcript is unambiguous.
//
// A model that ships in both a 200k and a 1M variant under one id (Opus 5 and
// Sonnet 4.6 both do — "Opus 5" vs "Opus 5 (1M context)") is deliberately NOT
// listed: transcripts record the bare id with no [1m] marker, so seeding it here
// would overstate every 200k session. Pin those per-model in config instead:
//
//	~/.config/capataz/config.json
//	{ "context": { "limits": { "claude-opus-5": "1m" } } }
//
// which is the same key capataz's own gauge reads, so one setting serves both.
var modelWindows = map[string]int{
	"claude-opus-4-8": 1_000_000,
	"claude-fable-5":  1_000_000,
}

// limitResolver resolves a model id to its context-window size. Construct it
// once per process with newLimitResolver: it caches the user's config so the
// refresh loop doesn't re-read the file on every tick.
type limitResolver struct {
	override int               // CAPATAZ_CONTEXT_LIMIT or -limit; 0 when unset
	limits   map[string]string // context.limits from capataz's config
}

// capatazConfig is the subset of ~/.config/capataz/config.json we read. The
// shape mirrors capataz's own config.Context so the two tools stay in sync.
type capatazConfig struct {
	Context struct {
		Limits map[string]string `json:"limits"`
	} `json:"context"`
}

// capatazConfigPath mirrors capataz's own resolution: $XDG_CONFIG_HOME/capataz,
// else ~/.config/capataz.
func capatazConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if strings.TrimSpace(dir) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "capataz", "config.json")
}

// newLimitResolver builds a resolver from an explicit override (the -limit flag,
// falling back to CAPATAZ_CONTEXT_LIMIT) plus capataz's config file. A missing or
// malformed config is not an error — it just means no per-model pins.
func newLimitResolver(override string) *limitResolver {
	r := &limitResolver{}
	if override == "" {
		override = os.Getenv("CAPATAZ_CONTEXT_LIMIT")
	}
	r.override = parseTokenCount(override)

	if path := capatazConfigPath(); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var cfg capatazConfig
			if json.Unmarshal(data, &cfg) == nil {
				r.limits = cfg.Context.Limits
			}
		}
	}
	return r
}

// limitFor returns the context window to scale a session's gauge against.
//
// Order of preference:
//  1. the explicit override (-limit / CAPATAZ_CONTEXT_LIMIT), applied to every session;
//  2. a per-model pin in capataz's config.context.limits, resolved by
//     most-specific match — the longest key that equals or is contained in the
//     model id wins, so "claude-opus-5" beats a broad "claude". (Map iteration
//     order is randomized in Go, so picking the longest match rather than the
//     first visited is what makes this deterministic.)
//  3. the built-in modelWindows table;
//  4. a "1m" hint in the model id, should one ever appear there;
//  5. the 200k default.
//
// Finally, observed is used only as a floor: a session whose measured context
// already exceeds the resolved window proves the window is bigger than we
// thought (the user is on a 1M variant we had no way to detect), so we widen to
// the next known tier rather than rendering an impossible 143%-full bar.
func (r *limitResolver) limitFor(model string, observed int) int {
	limit := r.baseLimit(model)
	if observed > limit {
		if observed <= 1_000_000 {
			return 1_000_000
		}
		return observed // beyond every tier we know — scale to what we can see
	}
	return limit
}

func (r *limitResolver) baseLimit(model string) int {
	if r.override > 0 {
		return r.override
	}
	lower := strings.ToLower(model)

	bestLen, bestVal := -1, 0
	for key, val := range r.limits {
		k := strings.ToLower(strings.TrimSpace(key))
		if k == "" {
			continue
		}
		if k == lower || strings.Contains(lower, k) {
			if n := parseTokenCount(val); n > 0 && len(k) > bestLen {
				bestLen, bestVal = len(k), n
			}
		}
	}
	if bestLen >= 0 {
		return bestVal
	}

	for key, n := range modelWindows {
		if strings.Contains(lower, key) {
			return n
		}
	}
	if strings.Contains(lower, "1m") {
		return 1_000_000
	}
	return defaultLimit
}

// parseTokenCount parses values like "1m", "200k", or "1000000". Returns 0 for
// anything it can't read, which every caller treats as "unset".
func parseTokenCount(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}
	mult := 1
	switch {
	case strings.HasSuffix(s, "m"):
		mult, s = 1_000_000, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "k"):
		mult, s = 1_000, strings.TrimSuffix(s, "k")
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 {
		return 0
	}
	return int(f * float64(mult))
}
