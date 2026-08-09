package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/me/personal-code/capataz-herdr", "-Users-me-personal-code-capataz-herdr"},
		{"/repo/.scratch", "-repo--scratch"},
		{"/repo/my_dir", "-repo-my-dir"},
		{"relative/path", "relative-path"},
		{"", ""},
	}
	for _, c := range cases {
		if got := projectSlug(c.in); got != c.want {
			t.Errorf("projectSlug(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

// writeTranscript creates <dir>/projects/<slug>/<session>.jsonl containing the
// given lines, and returns the transcript path.
func writeTranscript(t *testing.T, dir, slug, session string, lines ...string) string {
	t.Helper()
	pdir := filepath.Join(dir, "projects", slug)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pdir, session+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTranscriptPath_SlugFastPath(t *testing.T) {
	dir := t.TempDir()
	cwd := "/Users/me/code/proj"
	want := writeTranscript(t, dir, projectSlug(cwd), "sess-1", `{"type":"assistant"}`)

	if got := transcriptPath(dir, cwd, "sess-1"); got != want {
		t.Errorf("transcriptPath: got %q, want %q", got, want)
	}
}

// When the cwd doesn't slugify to the directory Claude actually used (a
// symlinked worktree path, or a change to the undocumented naming rule), the
// session id alone must still find the file.
func TestTranscriptPath_GlobFallback(t *testing.T) {
	dir := t.TempDir()
	want := writeTranscript(t, dir, "-some-other-slug", "sess-2", `{"type":"assistant"}`)

	if got := transcriptPath(dir, "/completely/different/cwd", "sess-2"); got != want {
		t.Errorf("transcriptPath: got %q, want %q", got, want)
	}
}

func TestTranscriptPath_Missing(t *testing.T) {
	dir := t.TempDir()
	if got := transcriptPath(dir, "/repo", "nope"); got != "" {
		t.Errorf("transcriptPath: got %q, want empty", got)
	}
	if got := transcriptPath(dir, "/repo", ""); got != "" {
		t.Errorf("transcriptPath with empty session: got %q, want empty", got)
	}
}

func TestLatestTurn_PicksNewestAssistantTurn(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "slug", "s",
		`{"type":"user","message":{"role":"user"}}`,
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":10,"cache_read_input_tokens":1000,"cache_creation_input_tokens":100,"output_tokens":50}}}`,
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":5,"cache_read_input_tokens":9000,"cache_creation_input_tokens":995,"output_tokens":70}}}`,
	)

	turn, ok := latestTurn(path)
	if !ok {
		t.Fatal("expected a turn")
	}
	if got := turn.contextTokens(); got != 10_000 {
		t.Errorf("contextTokens: got %d, want 10000", got)
	}
	if turn.model != "claude-opus-5" {
		t.Errorf("model: got %q", turn.model)
	}
	if turn.out != 70 {
		t.Errorf("output tokens: got %d, want 70", turn.out)
	}
}

// A subagent's turns land in the same transcript as the session that spawned it,
// but start from a nearly empty context. Counting one would make a session that
// just dispatched a subagent look like it had suddenly emptied out — which is
// the common case under the drain.
func TestLatestTurn_SkipsSidechainTurns(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "slug", "s",
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":0,"cache_read_input_tokens":150000,"cache_creation_input_tokens":0}}}`,
		`{"type":"assistant","isSidechain":true,"message":{"model":"claude-opus-5","usage":{"input_tokens":0,"cache_read_input_tokens":3000,"cache_creation_input_tokens":0}}}`,
	)

	turn, ok := latestTurn(path)
	if !ok {
		t.Fatal("expected a turn")
	}
	if got := turn.contextTokens(); got != 150_000 {
		t.Errorf("contextTokens: got %d, want 150000 (subagent turn leaked in)", got)
	}
}

func TestLatestTurn_SkipsRecordsWithoutUsage(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "slug", "s",
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":1,"cache_read_input_tokens":4999,"cache_creation_input_tokens":0}}}`,
		`{"type":"assistant","message":{"model":"claude-opus-5"}}`,
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{}}}`,
	)

	turn, ok := latestTurn(path)
	if !ok {
		t.Fatal("expected a turn")
	}
	if got := turn.contextTokens(); got != 5_000 {
		t.Errorf("contextTokens: got %d, want 5000", got)
	}
}

func TestLatestTurn_NoAssistantTurns(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "slug", "s",
		`{"type":"user","message":{"role":"user"}}`,
		`{"type":"mode"}`,
	)
	if _, ok := latestTurn(path); ok {
		t.Error("expected no turn from a transcript with no assistant records")
	}
}

func TestLatestTurn_ToleratesGarbageLines(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "slug", "s",
		`{"type":"assistant","message":{"usage":{"input_tokens":2,"cache_read_input_tokens":2998}}}`,
		`{ truncated json`,
		``,
	)
	turn, ok := latestTurn(path)
	if !ok {
		t.Fatal("expected a turn despite the trailing garbage line")
	}
	if got := turn.contextTokens(); got != 3_000 {
		t.Errorf("contextTokens: got %d, want 3000", got)
	}
}

func TestLatestTurn_MissingFile(t *testing.T) {
	if _, ok := latestTurn(filepath.Join(t.TempDir(), "nope.jsonl")); ok {
		t.Error("expected no turn for a missing file")
	}
	if _, ok := latestTurn(""); ok {
		t.Error("expected no turn for an empty path")
	}
}

// Transcripts reach many megabytes; only the tail is read. A record beyond the
// tail window must not be found, and the partial line the window starts on must
// not break parsing of the good records after it.
func TestLatestTurn_TailWindow(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "projects", "slug")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(pdir, "s.jsonl")

	var b strings.Builder
	b.WriteString(`{"type":"assistant","message":{"usage":{"input_tokens":999999}}}` + "\n")
	// Pad well past the tail window so the record above is unreachable.
	padding := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 1024) + `"}}`
	for b.Len() < transcriptTail+64*1024 {
		b.WriteString(padding + "\n")
	}
	b.WriteString(`{"type":"assistant","message":{"usage":{"input_tokens":7,"cache_read_input_tokens":1993}}}` + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	turn, ok := latestTurn(path)
	if !ok {
		t.Fatal("expected a turn")
	}
	if got := turn.contextTokens(); got != 2_000 {
		t.Errorf("contextTokens: got %d, want 2000", got)
	}
}
