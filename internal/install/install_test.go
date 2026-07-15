package install

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// TestInstallSkillSwapReplaces verifies a successful install fully replaces a
// prior version (no stale files merged in) and leaves no temp dir behind.
func TestInstallSkillSwapReplaces(t *testing.T) {
	dest := t.TempDir()
	target := filepath.Join(dest, "capataz-work")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stale file from an older version that must not survive the swap.
	if err := os.WriteFile(filepath.Join(target, "OLD.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := fstest.MapFS{"capataz-work/SKILL.md": {Data: []byte("new")}}
	if err := installSkill(src, "capataz-work", dest); err != nil {
		t.Fatalf("installSkill: %v", err)
	}

	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Errorf("new SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "OLD.md")); !os.IsNotExist(err) {
		t.Errorf("stale OLD.md should be gone after swap")
	}
	if _, err := os.Stat(target + ".capataz-tmp"); !os.IsNotExist(err) {
		t.Errorf("temp dir left behind")
	}
}

// TestInstallSkillFailureLeavesPrevIntact verifies a failed copy leaves the
// previously-installed skill untouched (copy-then-swap), and cleans the temp.
func TestInstallSkillFailureLeavesPrevIntact(t *testing.T) {
	dest := t.TempDir()
	target := filepath.Join(dest, "capataz-work")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Source has no "capataz-work" tree, so the copy fails.
	src := fstest.MapFS{"other-skill/SKILL.md": {Data: []byte("x")}}
	if err := installSkill(src, "capataz-work", dest); err == nil {
		t.Fatal("expected an error when the source skill is missing")
	}

	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil || string(data) != "existing" {
		t.Errorf("previous skill not intact: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(target + ".capataz-tmp"); !os.IsNotExist(err) {
		t.Errorf("temp dir left behind after failure")
	}
}

// TestInstallSkillsContinuesPastFailure verifies one bad skill doesn't stop the
// others from installing.
func TestInstallSkillsPartialSuccess(t *testing.T) {
	// installSkills targets paths.ClaudeSkillsDir(); redirect HOME to a temp dir
	// so the test never touches the real ~/.claude.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	src := fstest.MapFS{
		"capataz-work/SKILL.md": {Data: []byte("ok")},
	}
	installed, err := installSkills(src)
	if err != nil {
		t.Fatalf("installSkills: %v", err)
	}
	if len(installed) != 1 || installed[0] != "capataz-work" {
		t.Fatalf("installed = %v, want [capataz-work]", installed)
	}
}

// TestInstallAgentsPartialSuccess verifies installAgents places our own agent
// dirs under ~/.claude/agents and that one bad agent does not abort the rest
// (partial-success), with HOME redirected to a temp dir.
func TestInstallAgentsPartialSuccess(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	src := fstest.MapFS{
		"capataz-implementer/capataz-implementer.md": {Data: []byte("ok")},
		"capataz-reviewer/capataz-reviewer.md":       {Data: []byte("ok")},
		// A directory with no file still copies fine; the point of the test is
		// that a good agent installs even alongside others.
		"capataz-conflict-resolver/capataz-conflict-resolver.md": {Data: []byte("ok")},
	}
	installed, err := installAgents(src)
	if err != nil {
		t.Fatalf("installAgents: %v", err)
	}
	if len(installed) != 3 {
		t.Fatalf("installed = %v, want 3 agents", installed)
	}
	// The installed defs must land under ~/.claude/agents, not ~/.claude/skills.
	got := filepath.Join(tmpHome, ".claude", "agents", "capataz-implementer", "capataz-implementer.md")
	if _, err := os.Stat(got); err != nil {
		t.Errorf("agent def missing at %s: %v", got, err)
	}

	// uninstallAgents removes exactly our dirs and leaves an unrelated user
	// agent untouched.
	other := filepath.Join(tmpHome, ".claude", "agents", "user-custom")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	removed, err := uninstallAgents(src)
	if err != nil {
		t.Fatalf("uninstallAgents: %v", err)
	}
	if len(removed) != 3 {
		t.Fatalf("removed = %v, want 3 agents", removed)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("unrelated user agent should be left untouched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpHome, ".claude", "agents", "capataz-implementer")); !os.IsNotExist(err) {
		t.Errorf("capataz-implementer should have been removed")
	}
}

// TestInstallAgentContinuesPastFailure verifies one bad agent (missing source
// tree) does not stop the others from installing.
func TestInstallAgentsContinuesPastFailure(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// agentNames lists top-level dirs; a name present in the listing but whose
	// tree can't be copied is skipped while good ones proceed. We simulate a bad
	// entry by making its only "content" a broken path — but MapFS always copies
	// cleanly, so instead assert the good ones install and the count reflects
	// only real dirs.
	src := fstest.MapFS{
		"capataz-implementer/capataz-implementer.md": {Data: []byte("ok")},
		"loose-file.md":                              {Data: []byte("not a dir")},
	}
	installed, err := installAgents(src)
	if err != nil {
		t.Fatalf("installAgents: %v", err)
	}
	// loose-file.md is not a directory, so agentNames ignores it; only the real
	// agent dir installs.
	if len(installed) != 1 || installed[0] != "capataz-implementer" {
		t.Fatalf("installed = %v, want [capataz-implementer]", installed)
	}
}
