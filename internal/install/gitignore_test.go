package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendIgnoreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "git", "ignore")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("*.swp"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := appendIgnore(path, scratchPattern); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(path)
	if n := strings.Count(string(got), scratchPattern); n != 1 {
		t.Fatalf("want one %s line, got %d:\n%s", scratchPattern, n, got)
	}
	if !strings.HasPrefix(string(got), "*.swp\n") {
		t.Fatalf("existing content mangled:\n%s", got)
	}
}

func TestAppendIgnoreRespectsEquivalentPattern(t *testing.T) {
	for _, existing := range []string{".scratch", ".scratch/", "/.scratch/", "**/.scratch/"} {
		path := filepath.Join(t.TempDir(), "ignore")
		if err := os.WriteFile(path, []byte(existing+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := appendIgnore(path, scratchPattern); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); string(got) != existing+"\n" {
			t.Errorf("%q: file changed to %q", existing, got)
		}
	}
}

func TestAppendIgnoreCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "ignore")
	if err := appendIgnore(path, scratchPattern); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "\n"+scratchPattern+"\n") {
		t.Fatalf("pattern missing:\n%s", got)
	}
}
