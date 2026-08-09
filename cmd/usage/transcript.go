package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// transcriptTail is how much of the end of a transcript we read. Transcripts
// grow to many megabytes on long sessions, but only the last assistant turn
// matters, so we tail-read a bounded window — cheap enough to refresh every
// couple of seconds across every open pane.
const transcriptTail = 512 * 1024

// turn is one assistant turn's raw token counts, as recorded in the transcript.
type turn struct {
	in, out, cacheRead, cacheWrite int
	model                          string
}

// contextTokens is how full the context window is after this turn: everything
// the model was sent, whether it came from cache or not. Output tokens are
// excluded — they are counted on the *next* turn's input.
func (t turn) contextTokens() int { return t.in + t.cacheRead + t.cacheWrite }

// claudeDir returns Claude Code's state directory, honoring CLAUDE_CONFIG_DIR
// (the documented override) and falling back to ~/.claude.
func claudeDir() string {
	if d := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// projectSlug renders a working directory the way Claude Code names the project
// folder that holds its transcripts: every character outside [A-Za-z0-9-] is
// replaced with '-', so /Users/me/personal-code/capataz-herdr becomes
// -Users-me-personal-code-capataz-herdr.
//
// This is a best-effort reproduction of an undocumented naming scheme, which is
// exactly why transcriptPath falls back to a glob on the session id — that path
// is slug-independent and cannot drift.
func projectSlug(cwd string) string {
	var b strings.Builder
	b.Grow(len(cwd))
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// transcriptPath resolves a pane's (cwd, sessionID) to its transcript file.
//
// Fast path: <claudeDir>/projects/<slug(cwd)>/<sessionID>.jsonl. Fallback: glob
// <claudeDir>/projects/*/<sessionID>.jsonl — a session id is globally unique, so
// this finds the transcript even when the slug rule changes or the agent's cwd
// differs from the directory Claude keyed the project under (a worktree opened
// through a symlinked path, say). Returns "" when nothing matches.
func transcriptPath(dir, cwd, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	if cwd != "" {
		p := filepath.Join(dir, "projects", projectSlug(cwd), sessionID+".jsonl")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	matches, err := filepath.Glob(filepath.Join(dir, "projects", "*", sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// tailBytes returns up to max bytes from the end of a file. The first line of
// the result may be a partial record (it is the oldest, and JSON parsing skips
// it), which is harmless — we scan backwards from the newest line.
func tailBytes(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > max {
		if _, err := f.Seek(-max, io.SeekEnd); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(f)
}

// latestTurn returns the newest main-session assistant turn in a transcript, and
// whether one was found.
//
// Two things it deliberately skips while scanning backwards:
//
//   - isSidechain records. Subagent turns are written into the *same* transcript
//     as the session that spawned them, and a subagent starts with a nearly empty
//     context. Counting one would make a session that just dispatched a subagent
//     look like it had suddenly emptied out. This matters especially under the
//     drain, which spawns subagents constantly.
//   - assistant records with no usage block at all (streaming fragments and
//     synthetic entries) — we keep looking further back rather than reporting 0.
func latestTurn(path string) (turn, bool) {
	if path == "" {
		return turn{}, false
	}
	data, err := tailBytes(path, transcriptTail)
	if err != nil {
		return turn{}, false
	}
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(line, &rec) != nil {
			continue // partial first line, or a record we don't care about
		}
		if t, _ := rec["type"].(string); t != "assistant" {
			continue
		}
		if side, _ := rec["isSidechain"].(bool); side {
			continue // a subagent's context, not this session's
		}
		tu := parseTurn(rec)
		if tu.contextTokens() == 0 {
			continue // assistant record without usage — keep looking back
		}
		return tu, true
	}
	return turn{}, false
}

// parseTurn pulls one assistant turn's token counts out of its transcript
// record. It walks generic maps rather than binding a struct so that unrelated
// schema changes elsewhere in the record can't break the read.
func parseTurn(rec map[string]any) turn {
	msg, ok := rec["message"].(map[string]any)
	if !ok {
		return turn{}
	}
	model, _ := msg["model"].(string)
	usage, ok := msg["usage"].(map[string]any)
	if !ok {
		return turn{model: model}
	}
	num := func(k string) int {
		if f, ok := usage[k].(float64); ok {
			return int(f)
		}
		return 0
	}
	return turn{
		in:         num("input_tokens"),
		out:        num("output_tokens"),
		cacheRead:  num("cache_read_input_tokens"),
		cacheWrite: num("cache_creation_input_tokens"),
		model:      model,
	}
}
