package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmena34/capataz/internal/paths"
)

// runTrustDir marks <path> as a trusted folder in Claude Code's ~/.claude.json
// (projects.<abs-path>.hasTrustDialogAccepted = true) so a freshly-spawned
// `claude` in that directory skips the "trust this folder?" prompt. This is
// needed when dispatching agents into fresh git worktrees, whose paths Claude
// has never seen — `--dangerously-skip-permissions` does NOT bypass the
// folder-trust gate. Additive and idempotent; the rest of ~/.claude.json is
// preserved and the write is atomic (temp + rename).
func runTrustDir(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: capataz trust-dir <path>")
	}
	abs, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}

	cfgPath := paths.ClaudeConfigJSON()
	root := map[string]any{}
	mode := os.FileMode(0o600)
	if data, err := os.ReadFile(cfgPath); err == nil {
		if err := json.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parse %s: %w", cfgPath, err)
		}
		if fi, err := os.Stat(cfgPath); err == nil {
			mode = fi.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
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
	entry["hasTrustDialogAccepted"] = true

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	tmp := cfgPath + ".capataz-tmp"
	if err := os.WriteFile(tmp, out, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
		return err
	}
	fmt.Printf("trusted %s\n", abs)
	return nil
}
