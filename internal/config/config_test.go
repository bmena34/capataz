package config

import "testing"

// TestDefaultModels pins the per-role model defaults so a refactor can't silently
// change which model a dispatched role runs on.
func TestDefaultModels(t *testing.T) {
	m := Default().Models
	cases := map[string]string{
		"implementer":  m.Implementer,
		"reviewer":     m.Reviewer,
		"security":     m.Security,
		"conflict":     m.Conflict,
		"orchestrator": m.Orchestrator,
	}
	want := map[string]string{
		"implementer":  "sonnet",
		"reviewer":     "opus",
		"security":     "opus",
		"conflict":     "opus",
		"orchestrator": "default",
	}
	for role, got := range cases {
		if got != want[role] {
			t.Errorf("Default().Models.%s = %q, want %q", role, got, want[role])
		}
	}
}

// TestModelsResolve covers the tolerant alias resolution: recognized aliases pass
// through, default/empty/unknown collapse to "" (inherit).
func TestModelsResolve(t *testing.T) {
	var mm Models
	cases := []struct {
		in, want string
	}{
		{"opus", "opus"},
		{"sonnet", "sonnet"},
		{"fable", "fable"},
		{"haiku", "haiku"},
		{"default", ""},   // inherit
		{"", ""},          // inherit
		{"gpt-4", ""},     // unknown → inherit, not a bad --model
		{"OPUS", ""},      // case-sensitive: unknown → inherit
		{"sonnet ", ""},   // stray whitespace → unknown → inherit
	}
	for _, c := range cases {
		if got := mm.Resolve(c.in); got != c.want {
			t.Errorf("Resolve(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
