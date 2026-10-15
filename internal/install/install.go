// Package install sets capataz up for Claude Code under herdr: it copies the
// shipped skills and agent defs into ~/.claude, writes a default config, and
// ignores the .scratch backlog globally. It also removes what the old tmux
// build installed (capataz hooks in settings.json and the launchd daemon).
package install

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bmena34/capataz/internal/config"
	"github.com/bmena34/capataz/internal/paths"
)

// marker identifies hook commands the old tmux build registered, so they can
// be removed without touching anyone else's hooks.
const marker = "capataz hook"

// Install lays down the skills, agent defs, default config, and the global
// .scratch ignore, then removes any leftover tmux-era wiring. skills and agents
// are the embedded trees (rooted at the skill/agent directories); a nil FS
// skips that step.
func Install(skills, agents fs.FS) error {
	if err := config.WriteDefault(); err == nil {
		fmt.Printf("✓ config at %s\n", paths.ConfigFile())
	}

	if p, err := ensureScratchIgnored(); err != nil {
		fmt.Printf("! could not add %s to the global git excludes file (continuing): %v\n", scratchPattern, err)
	} else {
		fmt.Printf("✓ %s ignored globally via %s\n", scratchPattern, p)
	}

	if skills != nil {
		installed, err := installSkills(skills)
		if err != nil {
			fmt.Printf("! some skills could not be installed (continuing): %v\n", err)
		}
		if len(installed) > 0 {
			fmt.Printf("✓ skills installed to %s (%s)\n", paths.ClaudeSkillsDir(), strings.Join(installed, ", "))
		}
	}

	if agents != nil {
		installed, err := installAgents(agents)
		if err != nil {
			fmt.Printf("! some agents could not be installed (continuing): %v\n", err)
		}
		if len(installed) > 0 {
			fmt.Printf("✓ agents installed to %s (%s)\n", paths.ClaudeAgentsDir(), strings.Join(installed, ", "))
		}
	}

	removeLegacy()
	return nil
}

// Uninstall removes the skills and agent defs capataz installed, plus any
// leftover tmux-era wiring. The config and the global .scratch ignore stay, so
// an existing backlog is never exposed to a commit.
func Uninstall(skills, agents fs.FS) error {
	if skills != nil {
		if removed, err := uninstallSkills(skills); err != nil {
			fmt.Printf("! could not remove skills: %v\n", err)
		} else if len(removed) > 0 {
			fmt.Printf("✓ skills removed from %s (%s)\n", paths.ClaudeSkillsDir(), strings.Join(removed, ", "))
		}
	}

	if agents != nil {
		if removed, err := uninstallAgents(agents); err != nil {
			fmt.Printf("! could not remove agents: %v\n", err)
		} else if len(removed) > 0 {
			fmt.Printf("✓ agents removed from %s (%s)\n", paths.ClaudeAgentsDir(), strings.Join(removed, ", "))
		}
	}

	removeLegacy()
	return nil
}

// removeLegacy strips the tmux build's hooks from settings.json and unloads
// and deletes its launchd agent. Each step is a no-op when nothing is there.
func removeLegacy() {
	if removed, err := unwireSettings(); err != nil {
		fmt.Printf("! could not remove old capataz hooks from %s: %v\n", paths.ClaudeSettings(), err)
	} else if removed {
		fmt.Printf("✓ old capataz hooks removed from %s (backup at %s.bak)\n", paths.ClaudeSettings(), paths.ClaudeSettings())
	}

	plist := paths.LegacyPlist()
	if _, err := os.Stat(plist); err != nil {
		return
	}
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d", os.Getuid()), plist).Run()
	if err := os.Remove(plist); err != nil {
		fmt.Printf("! could not remove old launchd agent %s: %v\n", plist, err)
		return
	}
	fmt.Println("✓ old capataz launchd daemon stopped and removed")
}

// installSkills copies each top-level skill directory in the embedded tree into
// ~/.claude/skills/<name>, overwriting our own prior copies. It returns the
// names successfully installed and the first error encountered (if any) — a
// failure on one skill does not stop the others. Only capataz's own skill
// directories are touched; any other user skills are left alone.
func installSkills(skills fs.FS) ([]string, error) {
	names, err := skillNames(skills)
	if err != nil {
		return nil, err
	}
	dest := paths.ClaudeSkillsDir()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	var installed []string
	var firstErr error
	for _, name := range names {
		if err := installSkill(skills, name, dest); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		installed = append(installed, name)
	}
	return installed, firstErr
}

// installSkill copies one skill into a temp directory and then atomically swaps
// it into place. The destructive step (removing the old copy) happens only
// after the copy has fully succeeded, so a failed or partial copy leaves any
// previously-installed version of the skill intact.
func installSkill(skills fs.FS, name, dest string) error {
	target := filepath.Join(dest, name)
	tmp := target + ".capataz-tmp"
	_ = os.RemoveAll(tmp) // clear any leftover from a prior interrupted install
	if err := copyTree(skills, name, tmp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

// uninstallSkills removes the skill directories capataz installed. It returns
// the names actually present and removed.
func uninstallSkills(skills fs.FS) ([]string, error) {
	names, err := skillNames(skills)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, name := range names {
		target := filepath.Join(paths.ClaudeSkillsDir(), name)
		if _, err := os.Stat(target); err == nil {
			if err := os.RemoveAll(target); err != nil {
				return removed, err
			}
			removed = append(removed, name)
		}
	}
	return removed, nil
}

// installAgents copies each top-level agent directory in the embedded tree into
// ~/.claude/agents/<name>, overwriting our own prior copies. Same partial-
// success contract as installSkills: a failure on one agent does not stop the
// others, and only capataz's own agent directories are touched — any other user
// agents are left alone. It reuses installSkill (a generic copy-then-swap).
func installAgents(agents fs.FS) ([]string, error) {
	names, err := agentNames(agents)
	if err != nil {
		return nil, err
	}
	dest := paths.ClaudeAgentsDir()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	var installed []string
	var firstErr error
	for _, name := range names {
		if err := installSkill(agents, name, dest); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		installed = append(installed, name)
	}
	return installed, firstErr
}

// uninstallAgents removes the agent directories capataz installed. It returns
// the names actually present and removed.
func uninstallAgents(agents fs.FS) ([]string, error) {
	names, err := agentNames(agents)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, name := range names {
		target := filepath.Join(paths.ClaudeAgentsDir(), name)
		if _, err := os.Stat(target); err == nil {
			if err := os.RemoveAll(target); err != nil {
				return removed, err
			}
			removed = append(removed, name)
		}
	}
	return removed, nil
}

// agentNames lists the top-level agent directory names in the embedded tree.
func agentNames(agents fs.FS) ([]string, error) {
	return skillNames(agents)
}

// skillNames lists the top-level skill directory names in the embedded tree.
func skillNames(skills fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(skills, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// copyTree copies the sub-tree rooted at src within srcFS to the on-disk dstDir.
func copyTree(srcFS fs.FS, src, dstDir string) error {
	return fs.WalkDir(srcFS, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, err := fs.ReadFile(srcFS, p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
}

// unwireSettings removes the old capataz hook groups from settings.json,
// reporting whether anything was removed. The file is only rewritten (after a
// backup) when it actually contained capataz hooks.
func unwireSettings() (bool, error) {
	settings, err := readSettings()
	if err != nil {
		return false, err
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return false, nil
	}
	changed := false
	for event, v := range hooks {
		before := asSlice(v)
		groups := stripOurs(before)
		if len(groups) == len(before) {
			continue
		}
		changed = true
		if len(groups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = groups
		}
	}
	if !changed {
		return false, nil
	}
	if err := backupSettings(); err != nil {
		return false, err
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	return true, writeSettings(settings)
}

// stripOurs returns hook groups that are NOT ours (command contains the marker).
func stripOurs(groups []any) []any {
	out := make([]any, 0, len(groups))
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		if !ok {
			out = append(out, g)
			continue
		}
		if groupIsOurs(gm) {
			continue
		}
		out = append(out, g)
	}
	return out
}

func groupIsOurs(group map[string]any) bool {
	for _, h := range asSlice(group["hooks"]) {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, marker) {
			return true
		}
	}
	return false
}

func readSettings() (map[string]any, error) {
	data, err := os.ReadFile(paths.ClaudeSettings())
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("settings.json is not valid JSON: %w", err)
	}
	return m, nil
}

func writeSettings(m map[string]any) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(paths.ClaudeSettings(), data, 0o644)
}

// backupSettings preserves the pristine, pre-capataz settings.json. It only
// writes the backup if one doesn't already exist, so re-running install never
// clobbers the original with an already-modified copy.
func backupSettings() error {
	bak := paths.ClaudeSettings() + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return nil // backup already exists; leave the original untouched
	}
	data, err := os.ReadFile(paths.ClaudeSettings())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(bak, data, 0o644)
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
