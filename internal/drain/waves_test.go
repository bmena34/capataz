package drain

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Wave-parsing tests
// ---------------------------------------------------------------------------

func TestParseWaves_ValidJSON(t *testing.T) {
	raw := []byte(`{
		"feature": "herdr-port",
		"waves": [
			[
				{"number":"01","path":"/repo/.scratch/herdr-port/issues/01-foo.md","feature":"herdr-port","status":"ready-for-agent","branch":"capataz/herdr-port-01-foo"},
				{"number":"02","path":"/repo/.scratch/herdr-port/issues/02-bar.md","feature":"herdr-port","status":"ready-for-agent","branch":"capataz/herdr-port-02-bar"}
			],
			[
				{"number":"03","path":"/repo/.scratch/herdr-port/issues/03-baz.md","feature":"herdr-port","status":"ready-for-agent","branch":"capataz/herdr-port-03-baz"}
			]
		]
	}`)

	out, err := parseWaves(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Feature != "herdr-port" {
		t.Errorf("feature: got %q, want %q", out.Feature, "herdr-port")
	}
	if len(out.Waves) != 2 {
		t.Fatalf("waves: got %d, want 2", len(out.Waves))
	}
	if len(out.Waves[0]) != 2 {
		t.Errorf("wave[0] length: got %d, want 2", len(out.Waves[0]))
	}
	if len(out.Waves[1]) != 1 {
		t.Errorf("wave[1] length: got %d, want 1", len(out.Waves[1]))
	}
	if out.Waves[0][0].Branch != "capataz/herdr-port-01-foo" {
		t.Errorf("branch: got %q", out.Waves[0][0].Branch)
	}
}

func TestParseWaves_EmptyWaves(t *testing.T) {
	raw := []byte(`{"feature":"myfeature","waves":[]}`)
	out, err := parseWaves(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Waves) != 0 {
		t.Errorf("expected empty waves, got %d", len(out.Waves))
	}
}

func TestParseWaves_MissingFeature(t *testing.T) {
	raw := []byte(`{"waves":[]}`)
	_, err := parseWaves(raw)
	if err == nil {
		t.Fatal("expected error for missing feature, got nil")
	}
	if !strings.Contains(err.Error(), "missing feature") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseWaves_InvalidJSON(t *testing.T) {
	_, err := parseWaves([]byte(`not-json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// ---------------------------------------------------------------------------
// dispatchable (concurrency cap) tests
// ---------------------------------------------------------------------------

func makeWave(count int) []WaveIssue {
	out := make([]WaveIssue, count)
	for i := range out {
		out[i] = WaveIssue{
			Number:  strings.Repeat("0", 2-len(string(rune('0'+i)))) + string(rune('0'+i+1)),
			Path:    "/repo/.scratch/f/issues/01-foo.md",
			Feature: "f",
			Status:  "ready-for-agent",
			Branch:  "capataz/f-01-foo",
		}
	}
	return out
}

func TestDispatchable_NoCap(t *testing.T) {
	waves := [][]WaveIssue{makeWave(5)}
	got := dispatchable(waves, 0)
	if len(got) != 5 {
		t.Errorf("cap=0: got %d, want 5", len(got))
	}
}

func TestDispatchable_Cap(t *testing.T) {
	waves := [][]WaveIssue{makeWave(5)}
	got := dispatchable(waves, 3)
	if len(got) != 3 {
		t.Errorf("cap=3: got %d, want 3", len(got))
	}
}

func TestDispatchable_CapLargerThanWave(t *testing.T) {
	waves := [][]WaveIssue{makeWave(2)}
	got := dispatchable(waves, 10)
	if len(got) != 2 {
		t.Errorf("cap>wave: got %d, want 2", len(got))
	}
}

func TestDispatchable_EmptyWaves(t *testing.T) {
	got := dispatchable(nil, 3)
	if got != nil {
		t.Errorf("empty waves: expected nil, got %v", got)
	}
}

func TestDispatchable_SkipsEmptyPath(t *testing.T) {
	waves := [][]WaveIssue{{
		{Number: "01", Path: "", Branch: "b1"}, // bad — no path
		{Number: "02", Path: "/p", Branch: "b2"},
		{Number: "03", Path: "/p2", Branch: ""}, // bad — no branch
		{Number: "04", Path: "/p3", Branch: "b4"},
	}}
	got := dispatchable(waves, 0)
	if len(got) != 2 {
		t.Errorf("skip bad: got %d, want 2", len(got))
	}
}
