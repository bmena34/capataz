package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmena34/capataz/internal/paths"
)

// TrustDir marks dir as folder-trust-accepted in Claude Code's ~/.claude.json
// (projects.<abs-dir>.hasTrustDialogAccepted = true) so a claude session spawned
// in a brand-new directory — e.g. a parallel-dispatch git worktree — doesn't
// stall on the interactive "Is this a project you trust?" gate before it can run
// its opening prompt.
//
// It's a careful read-modify-write: the whole config is decoded into a generic
// map and only the one key is added (every other field is preserved), written
// atomically via a temp file + rename, after a one-time backup. It's a no-op
// when dir is already trusted. Best effort — Claude Code also writes this file,
// so a concurrent write could race; callers treat a failure as non-fatal (worst
// case the trust prompt shows, i.e. the pre-fix behavior).
func TrustDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("empty dir")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	path := paths.ClaudeConfigJSON()

	root := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("~/.claude.json is not valid JSON: %w", err)
		}
	case !os.IsNotExist(err):
		return err
	}

	projects, _ := root["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		root["projects"] = projects
	}
	entry, _ := projects[abs].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		projects[abs] = entry
	}
	if t, ok := entry["hasTrustDialogAccepted"].(bool); ok && t {
		return nil // already trusted — don't rewrite the file
	}
	entry["hasTrustDialogAccepted"] = true

	// One-time backup of the original before the first rewrite.
	if len(data) > 0 {
		if bak := path + ".capataz.bak"; !fileExists(bak) {
			_ = os.WriteFile(bak, data, 0o600)
		}
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	// Atomic replace: write a temp beside the file, then rename over it.
	tmp := path + ".capataz.tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
