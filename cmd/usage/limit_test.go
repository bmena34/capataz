package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTokenCount(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"1m", 1_000_000},
		{"1M", 1_000_000},
		{"200k", 200_000},
		{"1.5m", 1_500_000},
		{" 250K ", 250_000},
		{"1000000", 1_000_000},
		{"", 0},
		{"abc", 0},
		{"0", 0},
		{"-5k", 0},
	}
	for _, c := range cases {
		if got := parseTokenCount(c.in); got != c.want {
			t.Errorf("parseTokenCount(%q): got %d, want %d", c.in, got, c.want)
		}
	}
}

func TestBaseLimit_Precedence(t *testing.T) {
	cases := []struct {
		name     string
		resolver *limitResolver
		model    string
		want     int
	}{
		{
			name:     "default when nothing matches",
			resolver: &limitResolver{},
			model:    "claude-sonnet-4-6",
			want:     200_000,
		},
		{
			name:     "built-in table for 1M-only models",
			resolver: &limitResolver{},
			model:    "claude-opus-4-8",
			want:     1_000_000,
		},
		{
			name:     "dual-variant models are NOT in the table",
			resolver: &limitResolver{},
			model:    "claude-opus-5",
			want:     200_000,
		},
		{
			name:     "config pin overrides the default",
			resolver: &limitResolver{limits: map[string]string{"claude-opus-5": "1m"}},
			model:    "claude-opus-5",
			want:     1_000_000,
		},
		{
			name: "longest matching config key wins",
			resolver: &limitResolver{limits: map[string]string{
				"claude":        "300k",
				"claude-opus-5": "1m",
			}},
			model: "claude-opus-5",
			want:  1_000_000,
		},
		{
			name:     "config pin beats the built-in table",
			resolver: &limitResolver{limits: map[string]string{"claude-opus-4-8": "500k"}},
			model:    "claude-opus-4-8",
			want:     500_000,
		},
		{
			name:     "explicit override beats everything",
			resolver: &limitResolver{override: 123_000, limits: map[string]string{"claude-opus-5": "1m"}},
			model:    "claude-opus-5",
			want:     123_000,
		},
		{
			name:     "1m hint in the model id",
			resolver: &limitResolver{},
			model:    "claude-opus-5[1m]",
			want:     1_000_000,
		},
		{
			name:     "unknown model falls back to the default",
			resolver: &limitResolver{},
			model:    "",
			want:     200_000,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.resolver.baseLimit(c.model); got != c.want {
				t.Errorf("baseLimit(%q): got %d, want %d", c.model, got, c.want)
			}
		})
	}
}

// The transcript records a bare model id with no [1m] marker, so a 1M session on
// a dual-variant model is indistinguishable from a 200k one — until it exceeds
// 200k tokens, which proves the window is bigger. The gauge must widen rather
// than render an impossible >100% bar.
func TestLimitFor_ObservedActsAsFloor(t *testing.T) {
	r := &limitResolver{}
	cases := []struct {
		name     string
		observed int
		want     int
	}{
		{"within the default window", 150_000, 200_000},
		{"exactly at the window", 200_000, 200_000},
		{"past the window widens to 1M", 260_000, 1_000_000},
		{"past every known tier scales to observed", 1_400_000, 1_400_000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.limitFor("claude-opus-5", c.observed)
			if got != c.want {
				t.Errorf("limitFor(observed=%d): got %d, want %d", c.observed, got, c.want)
			}
			if pct := pctOf(c.observed, got); pct > 100 {
				t.Errorf("gauge exceeds 100%%: %d%%", pct)
			}
		})
	}
}

func TestNewLimitResolver_ReadsCapatazConfig(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "capataz")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"usage":{"enabled":true},"context":{"limits":{"claude-opus-5":"1m"}}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("CAPATAZ_CONTEXT_LIMIT", "")

	r := newLimitResolver("")
	if got := r.baseLimit("claude-opus-5"); got != 1_000_000 {
		t.Errorf("baseLimit: got %d, want 1000000 (config pin not applied)", got)
	}
}

func TestNewLimitResolver_MissingConfigIsNotFatal(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no capataz/config.json inside
	t.Setenv("CAPATAZ_CONTEXT_LIMIT", "")

	r := newLimitResolver("")
	if got := r.baseLimit("claude-sonnet-4-6"); got != defaultLimit {
		t.Errorf("baseLimit: got %d, want %d", got, defaultLimit)
	}
}

func TestNewLimitResolver_EnvOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CAPATAZ_CONTEXT_LIMIT", "750k")

	r := newLimitResolver("")
	if got := r.baseLimit("claude-opus-4-8"); got != 750_000 {
		t.Errorf("baseLimit: got %d, want 750000", got)
	}
	// An explicit flag still beats the environment.
	if got := newLimitResolver("300k").baseLimit("claude-opus-4-8"); got != 300_000 {
		t.Errorf("flag override: got %d, want 300000", got)
	}
}
