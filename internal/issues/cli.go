package issues

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Run is the `capataz issue …` subcommand: the small CLI the capataz-work skill
// calls to claim and resolve issues so their Status stays consistent no matter
// which agent (or human) touches the file. The markdown file is the source of
// truth; these commands just edit it.
//
//	capataz issue claim   <path>              set Status: claimed
//	capataz issue resolve <path> [--answer …] set Status: resolved (+ optional answer)
//	capataz issue status  <path> <status>     set Status: <status>
//	capataz issue list    <repo>              print the backlog for a repo
//	capataz issue new     <feature> <title> [--body … --type … --repo …]
func Run(args []string) error {
	if len(args) == 0 {
		return usageErr()
	}
	switch args[0] {
	case "new":
		if len(args) < 3 {
			return usageErr()
		}
		return create(args[1], args[2], args[3:])
	case "claim":
		if len(args) < 2 {
			return usageErr()
		}
		return setStatusCLI(args[1], "claimed")
	case "resolve":
		if len(args) < 2 {
			return usageErr()
		}
		answer := flagValue(args[2:], "--answer")
		if err := setStatusCLI(args[1], "resolved"); err != nil {
			return err
		}
		if answer != "" {
			if err := appendSection(args[1], "Answer", answer); err != nil {
				return err
			}
		}
		return nil
	case "status":
		if len(args) < 3 {
			return usageErr()
		}
		return setStatusCLI(args[1], args[2])
	case "list":
		repo := "."
		if len(args) > 1 {
			repo = args[1]
		}
		return list(repo)
	default:
		return usageErr()
	}
}

func setStatusCLI(path, status string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("issue file not found: %s", path)
	}
	if err := SetStatus(path, status); err != nil {
		return err
	}
	fmt.Printf("%s → Status: %s\n", path, status)
	return nil
}

// create writes a new ready-for-agent issue at .scratch/<feature>/issues/NN-slug.md,
// numbered one past the highest existing issue in that feature. Used by the
// capataz-review skill to file review findings back into the backlog.
func create(feature, title string, flags []string) error {
	repo := flagValue(flags, "--repo")
	if repo == "" {
		repo = "."
	}
	typ := flagValue(flags, "--type")
	if typ == "" {
		typ = "task"
	}
	body := flagValue(flags, "--body")
	if body == "" {
		body = "<describe the end-to-end change>"
	}

	dir := filepath.Join(repo, scratchDir, feature, "issues")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Serialize number allocation so two concurrent `issue new` calls (e.g. a
	// review session filing findings while /to-issues runs) can't grab the same
	// number and produce duplicate-numbered files.
	unlock := lockFeature(dir)
	defer unlock()

	num := nextNum(dir)
	slug := slugify(title)
	path := filepath.Join(dir, fmt.Sprintf("%02d-%s.md", num, slug))
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("issue already exists: %s", path)
	}
	content := fmt.Sprintf(`# %s

Status: ready-for-agent
Type: %s
Blocked by: None

## What to build

%s

## Acceptance criteria

- [ ]
`, title, typ, body)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// lockFeature serializes `issue new` within one feature's issues/ directory via
// an O_EXCL lock file, returning a release func. Best-effort and deadlock-proof:
// it steals a lock older than a few seconds (a crashed creator) and, if it still
// can't acquire, proceeds unlocked rather than blocking forever. The lock file
// is not a .md and is ignored by Scan/nextNum.
func lockFeature(dir string) func() {
	lock := filepath.Join(dir, ".capataz-new.lock")
	for i := 0; i < 100; i++ {
		if f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			f.Close()
			return func() { os.Remove(lock) }
		}
		if fi, err := os.Stat(lock); err == nil && time.Since(fi.ModTime()) > 5*time.Second {
			os.Remove(lock) // steal a stale lock left by a crashed creator
			continue
		}
		time.Sleep(5 * time.Millisecond)
	}
	return func() {} // gave up waiting; proceed unlocked (degrade, don't hang)
}

// nextNum returns one past the highest NN prefix among the issue files in dir
// (1 when the directory is empty or absent).
func nextNum(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 1
	}
	max := 0
	for _, e := range entries {
		if m := fileRe.FindStringSubmatch(e.Name()); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > max {
				max = n
			}
		}
	}
	return max + 1
}

// slugify turns a title into a filename-safe kebab slug.
func slugify(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 50 {
		slug = strings.Trim(slug[:50], "-")
	}
	if slug == "" {
		slug = "issue"
	}
	return slug
}

// appendSection appends "## <heading>\n\n<body>" to a file, adding a leading
// blank line so it doesn't run into whatever came before.
func appendSection(path, heading, body string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := strings.TrimRight(string(data), "\n")
	text += fmt.Sprintf("\n\n## %s\n\n%s\n", heading, body)
	return os.WriteFile(path, []byte(text), 0o644)
}

// list prints the backlog for a repo, grouped by feature — a plain-text view
// for scripts and debugging (the dashboard has its own renderer).
func list(repo string) error {
	feats, err := Scan(repo)
	if err != nil {
		return err
	}
	if len(feats) == 0 {
		fmt.Println("no issues found under", repo+"/.scratch")
		return nil
	}
	for _, f := range feats {
		fmt.Printf("%s/\n", f.Slug)
		for _, iss := range f.Issues {
			marks := iss.Status
			if marks == "" {
				marks = "—"
			}
			if iss.Blocked {
				marks += " (blocked)"
			}
			num := iss.Num
			if num == "" {
				num = "--"
			}
			fmt.Printf("  %s  %-16s  %s\n", num, marks, iss.Title)
		}
	}
	return nil
}

// flagValue returns the value following name in args (e.g. --answer "text"), or
// "" if the flag is absent. Also accepts --answer=text.
func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"=")
		}
	}
	return ""
}

func usageErr() error {
	return fmt.Errorf("usage: capataz issue <claim|resolve|status|list> <path> [args]")
}
