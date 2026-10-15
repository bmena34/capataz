// Package install wires capataz into Claude Code (hooks in settings.json) and
// macOS (a launchd agent for the daemon). Both operations back up settings.json
// first and are fully reversible via Uninstall.
package install

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmena34/capataz/internal/config"
	"github.com/bmena34/capataz/internal/paths"
)

// hookSpec describes one Claude Code hook event we register.
type hookSpec struct {
	event   string // Claude Code hook event name
	matcher string // matcher filter (may be "")
	kind    string // capataz hook kind argument
}

// specs is the full set of hooks capataz installs.
func specs() []hookSpec {
	return []hookSpec{
		{"Notification", "permission_prompt", "permission_prompt"},
		{"Notification", "idle_prompt", "idle_prompt"},
		{"Stop", "", "stop"},
		{"StopFailure", "", "stopfailure"},
		{"SessionStart", "", "session_start"},
		{"SessionEnd", "", "session_end"},
		{"UserPromptSubmit", "", "working"},
		{"PreToolUse", "", "working"},
		{"SubagentStop", "", "subagent_stop"},
	}
}

// marker identifies hook commands that belong to capataz, so Uninstall can
// remove only ours and re-installs don't duplicate.
const marker = "capataz hook"

// Install wires up hooks, the native skills, the per-role agent defs, and the
// launchd agent. skills is the embedded skills tree (rooted at the skill
// directories) and agents is the embedded agents tree (rooted at the agent
// directories); a nil FS skips that installation step.
func Install(skills, agents fs.FS) error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}

	if err := wireSettings(bin); err != nil {
		return fmt.Errorf("wire settings.json: %w", err)
	}
	fmt.Printf("✓ hooks merged into %s (backup at %s.bak)\n", paths.ClaudeSettings(), paths.ClaudeSettings())

	if err := config.WriteDefault(); err == nil {
		fmt.Printf("✓ config at %s\n", paths.ConfigFile())
	}

	if err := installLaunchd(bin); err != nil {
		return fmt.Errorf("install launchd agent: %w", err)
	}
	fmt.Printf("✓ daemon installed and started via launchd (%s)\n", paths.LaunchdLabel())

	if p, err := ensureScratchIgnored(); err != nil {
		fmt.Printf("! could not add %s to the global git excludes file (continuing): %v\n", scratchPattern, err)
	} else {
		fmt.Printf("✓ %s ignored globally via %s\n", scratchPattern, p)
	}

	// Skills are a convenience layer, not core wiring — install them last and
	// never let a copy failure abort the install: by here hooks + config + the
	// daemon are already up, and aborting would leave settings.json pointing at a
	// socket with no listener. Warn and continue instead.
	if skills != nil {
		installed, err := installSkills(skills)
		if err != nil {
			fmt.Printf("! some skills could not be installed (continuing): %v\n", err)
		}
		if len(installed) > 0 {
			fmt.Printf("✓ skills installed to %s (%s)\n", paths.ClaudeSkillsDir(), strings.Join(installed, ", "))
		}
	}

	// Agents are a convenience layer too — same never-abort posture as skills.
	if agents != nil {
		installed, err := installAgents(agents)
		if err != nil {
			fmt.Printf("! some agents could not be installed (continuing): %v\n", err)
		}
		if len(installed) > 0 {
			fmt.Printf("✓ agents installed to %s (%s)\n", paths.ClaudeAgentsDir(), strings.Join(installed, ", "))
		}
	}

	fmt.Println("\nDone. New Claude Code sessions in tmux will appear in `capataz`.")
	fmt.Println("Note: hooks load at session start, so restart any sessions already running.")
	return nil
}

// Uninstall removes capataz hooks, native skills, per-role agent defs, and the
// launchd agent. skills and agents are the embedded trees, used to know which
// directories are ours to remove; a nil FS skips that removal step.
func Uninstall(skills, agents fs.FS) error {
	if err := unwireSettings(); err != nil {
		return fmt.Errorf("unwire settings.json: %w", err)
	}
	fmt.Printf("✓ capataz hooks removed from %s\n", paths.ClaudeSettings())

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

	if err := uninstallLaunchd(); err != nil {
		return fmt.Errorf("remove launchd agent: %w", err)
	}
	fmt.Println("✓ launchd agent removed and stopped")
	fmt.Printf("\nData kept at %s (delete it manually if you want a clean slate).\n", paths.Dir())
	return nil
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

func wireSettings(bin string) error {
	settings, err := readSettings()
	if err != nil {
		return err
	}
	if err := backupSettings(); err != nil {
		return err
	}

	hooks := asMap(settings["hooks"])

	// Group specs by event first. Several specs can share one event (e.g. two
	// Notification matchers), so we must strip our prior entries ONCE per event
	// and then append all of that event's specs — otherwise later specs clobber
	// earlier ones for the same event.
	byEvent := map[string][]hookSpec{}
	order := []string{}
	for _, sp := range specs() {
		if _, seen := byEvent[sp.event]; !seen {
			order = append(order, sp.event)
		}
		byEvent[sp.event] = append(byEvent[sp.event], sp)
	}

	for _, event := range order {
		groups := stripOurs(asSlice(hooks[event]))
		for _, sp := range byEvent[event] {
			entry := map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": fmt.Sprintf("%s hook %s", bin, sp.kind),
					},
				},
			}
			if sp.matcher != "" {
				entry["matcher"] = sp.matcher
			}
			groups = append(groups, entry)
		}
		hooks[event] = groups
	}
	settings["hooks"] = hooks

	return writeSettings(settings)
}

func unwireSettings() error {
	settings, err := readSettings()
	if err != nil {
		return err
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		return nil // nothing to do
	}
	if err := backupSettings(); err != nil {
		return err
	}
	for event, v := range hooks {
		groups := stripOurs(asSlice(v))
		if len(groups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = groups
		}
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	return writeSettings(settings)
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

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`

func installLaunchd(bin string) error {
	if err := os.MkdirAll(filepath.Dir(paths.Plist()), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	plist := fmt.Sprintf(plistTemplate, paths.LaunchdLabel(), bin, daemonPath(), paths.LogFile(), paths.LogFile())
	if err := os.WriteFile(paths.Plist(), []byte(plist), 0o644); err != nil {
		return err
	}
	return bootstrapLaunchd()
}

// daemonPath returns a PATH for the launchd daemon that includes the user's
// current PATH plus common Homebrew locations, so the daemon can find tmux
// (launchd's default PATH omits /opt/homebrew/bin and /usr/local/bin).
func daemonPath() string {
	p := os.Getenv("PATH")
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin"} {
		if !strings.Contains(p, dir) {
			p = dir + ":" + p
		}
	}
	if strings.TrimSpace(strings.Trim(p, ":")) == "" {
		p = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	}
	return p
}

func uninstallLaunchd() error {
	_ = bootoutLaunchd()
	if err := os.Remove(paths.Plist()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func launchctlTarget() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

// bootstrapLaunchd (re)loads the agent, cleanly replacing any running instance.
func bootstrapLaunchd() error {
	target := launchctlTarget()
	_ = exec.Command("launchctl", "bootout", target, paths.Plist()).Run()
	if out, err := exec.Command("launchctl", "bootstrap", target, paths.Plist()).CombinedOutput(); err != nil {
		// Fall back to the older load syntax if bootstrap is unavailable.
		_ = exec.Command("launchctl", "unload", paths.Plist()).Run()
		if out2, err2 := exec.Command("launchctl", "load", "-w", paths.Plist()).CombinedOutput(); err2 != nil {
			return fmt.Errorf("launchctl bootstrap: %s / load: %s", strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
		}
	}
	// Give the daemon a moment to bind its socket so callers report accurately.
	for i := 0; i < 20; i++ {
		if socketUp() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// bootoutLaunchd stops the agent. It is tolerant of the agent not being loaded.
func bootoutLaunchd() error {
	target := launchctlTarget()
	if out, err := exec.Command("launchctl", "bootout", target, paths.Plist()).CombinedOutput(); err != nil {
		// bootout errors when the agent isn't loaded; treat that as success.
		if !socketUp() {
			return nil
		}
		_ = exec.Command("launchctl", "unload", paths.Plist()).Run()
		if socketUp() {
			return fmt.Errorf("launchctl bootout: %s", strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func plistExists() bool {
	_, err := os.Stat(paths.Plist())
	return err == nil
}

// socketUp reports whether the daemon is currently accepting connections.
func socketUp() bool {
	c, err := net.DialTimeout("unix", paths.Sock(), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// Start loads the launchd agent (starting the daemon).
func Start() error {
	if !plistExists() {
		return fmt.Errorf("daemon not installed — run `capataz install` first")
	}
	if err := bootstrapLaunchd(); err != nil {
		return err
	}
	fmt.Println("✓ daemon started")
	return nil
}

// Stop unloads the launchd agent (stopping the daemon, no respawn).
func Stop() error {
	if !plistExists() {
		return fmt.Errorf("daemon not installed — nothing to stop")
	}
	if err := bootoutLaunchd(); err != nil {
		return err
	}
	fmt.Println("✓ daemon stopped (stays down until `capataz start` or next login)")
	return nil
}

// Restart bounces the daemon.
func Restart() error {
	if !plistExists() {
		return fmt.Errorf("daemon not installed — run `capataz install` first")
	}
	if err := bootstrapLaunchd(); err != nil {
		return err
	}
	fmt.Println("✓ daemon restarted")
	return nil
}

// Status prints whether the agent is installed and the daemon is running.
func Status() error {
	if plistExists() {
		fmt.Println("agent:  installed")
	} else {
		fmt.Println("agent:  not installed (run `capataz install`)")
	}
	if socketUp() {
		fmt.Printf("daemon: running (socket %s)\n", paths.Sock())
	} else {
		fmt.Println("daemon: not running")
	}
	return nil
}
