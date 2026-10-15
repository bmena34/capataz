package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// scratchPattern is the backlog dir capataz keeps in every repo. It is ignored
// via the user's global git excludes file rather than each repo's .gitignore,
// so the backlog is never tracked, committed, or pushed — and no repo has to
// carry a capataz-specific line in a file that gets pushed.
const scratchPattern = ".scratch/"

// globalExcludesFile is the file git reads as core.excludesFile: the configured
// value if set, else git's default ($XDG_CONFIG_HOME/git/ignore or
// ~/.config/git/ignore).
func globalExcludesFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if out, err := exec.Command("git", "config", "--global", "--get", "core.excludesFile").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			if p == "~" || strings.HasPrefix(p, "~/") {
				p = filepath.Join(home, p[1:])
			}
			return p, nil
		}
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "git", "ignore"), nil
	}
	return filepath.Join(home, ".config", "git", "ignore"), nil
}

// ensureScratchIgnored appends scratchPattern to the global excludes file if no
// equivalent pattern is already there. Idempotent. Returns the file path.
func ensureScratchIgnored() (string, error) {
	path, err := globalExcludesFile()
	if err != nil {
		return "", err
	}
	return path, appendIgnore(path, scratchPattern)
}

func appendIgnore(path, pattern string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	bare := strings.Trim(pattern, "/")
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Trim(strings.TrimPrefix(strings.TrimSpace(line), "**/"), "/") == bare {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("# capataz issue backlog — local only, never commit\n")
	b.WriteString(pattern + "\n")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(b.String())
	return err
}
