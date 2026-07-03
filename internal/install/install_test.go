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
