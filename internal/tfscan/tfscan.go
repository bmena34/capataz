// Package tfscan maps changed files to the Terraform root modules (states)
// they would affect, using a lightweight text scan (no HCL dependency).
//
// Heuristics and limits:
//   - A root is a directory whose *.tf declare a backend "<type>" block or a
//     cloud block. Comments are stripped before matching.
//   - Only local module sources ("./" or "../") form edges. Any other source
//     (registry, git, http, or a local path escaping the repo) marks the map as
//     having unknown scope, and RootsFor then reports unknown for any
//     Terraform-relevant change. This is deliberately conservative.
//   - Modules are matched by directory only; nested directories are not
//     implicitly part of a module. A .tf in a directory that is neither a root
//     nor a called module is unknown.
//   - Workspaces and Terragrunt are reported in Notes, not modeled.
package tfscan

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Map is the scanned module graph. All paths are slash-separated and relative
// to the repo root; the repo root itself is ".".
type Map struct {
	// Roots are directories that declare a backend or cloud block.
	Roots []string
	// Notes are human-readable signals (workspaces, Terragrunt) not modeled.
	Notes []string
	// RemoteSources lists "dir: source" for each non-local module source.
	RemoteSources []string

	tfDirs  map[string]bool            // every dir containing a *.tf
	callers map[string]map[string]bool // module dir -> calling dirs
	roots   map[string]bool
}

var (
	backendRe   = regexp.MustCompile(`(?m)^\s*backend\s+"[^"]+"\s*\{`)
	cloudRe     = regexp.MustCompile(`(?m)^\s*cloud\s*\{`)
	moduleRe    = regexp.MustCompile(`(?m)^\s*module\s+"[^"]+"\s*\{`)
	sourceRe    = regexp.MustCompile(`(?m)^\s*source\s*=\s*"([^"]*)"`)
	workspaceRe = regexp.MustCompile(`\bterraform\.workspace\b`)
	skipDirs    = map[string]bool{".git": true, ".terraform": true, "node_modules": true, ".scratch": true, ".claude": true}
)

// Scan walks repoRoot and builds the module graph.
func Scan(repoRoot string) (*Map, error) {
	m := &Map{
		tfDirs:  map[string]bool{},
		callers: map[string]map[string]bool{},
		roots:   map[string]bool{},
	}
	workspace := map[string]bool{}
	err := filepath.WalkDir(repoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != repoRoot && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(repoRoot, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		dir := path.Dir(rel)
		if d.Name() == "terragrunt.hcl" {
			m.Notes = append(m.Notes, "terragrunt.hcl present in "+dir+": Terragrunt not modeled")
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".tf") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		src := stripComments(string(b))
		m.tfDirs[dir] = true
		if backendRe.MatchString(src) || cloudRe.MatchString(src) {
			m.roots[dir] = true
		}
		if workspaceRe.MatchString(src) {
			workspace[dir] = true
		}
		for _, loc := range moduleRe.FindAllStringIndex(src, -1) {
			sm := sourceRe.FindStringSubmatch(blockBody(src, loc[1]))
			if sm == nil {
				continue
			}
			s := sm[1]
			if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
				target := path.Join(dir, s)
				if target == ".." || strings.HasPrefix(target, "../") {
					m.RemoteSources = append(m.RemoteSources, dir+": "+s)
					continue
				}
				if m.callers[target] == nil {
					m.callers[target] = map[string]bool{}
				}
				m.callers[target][dir] = true
			} else {
				m.RemoteSources = append(m.RemoteSources, dir+": "+s)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for r := range m.roots {
		m.Roots = append(m.Roots, r)
	}
	sort.Strings(m.Roots)
	sort.Strings(m.RemoteSources)
	for d := range workspace {
		for _, r := range m.rootsOf(d) {
			m.Notes = append(m.Notes, "terraform.workspace used in "+d+" (root "+r+"): workspaces not modeled")
		}
	}
	sort.Strings(m.Notes)
	return m, nil
}

// rootsOf returns the roots reachable from dir via caller edges (including dir
// itself when it is a root), sorted.
func (m *Map) rootsOf(dir string) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(string)
	walk = func(d string) {
		if seen[d] {
			return
		}
		seen[d] = true
		if m.roots[d] {
			out = append(out, d)
		}
		for c := range m.callers[d] {
			walk(c)
		}
	}
	walk(dir)
	sort.Strings(out)
	return out
}

// RootsFor maps repo-relative changed files to affected roots. unknown is true
// when the change cannot be confidently scoped: a .tf file outside any root or
// known module, a module no root uses, or any remote module source in the repo
// (when a Terraform-relevant file changed).
func (m *Map) RootsFor(changedFiles []string) (roots []string, unknown bool) {
	set := map[string]bool{}
	relevant := false
	for _, f := range changedFiles {
		f = path.Clean(filepath.ToSlash(f))
		base := path.Base(f)
		isTF := strings.HasSuffix(base, ".tf")
		if !isTF && !strings.HasSuffix(base, ".tfvars") && base != ".terraform.lock.hcl" {
			continue
		}
		relevant = true
		dir := path.Dir(f)
		if !m.roots[dir] && len(m.callers[dir]) == 0 {
			if isTF || m.tfDirs[dir] {
				unknown = true
			}
			continue
		}
		rs := m.rootsOf(dir)
		if len(rs) == 0 {
			unknown = true // module only used by non-root callers
		}
		for _, r := range rs {
			set[r] = true
		}
	}
	if relevant && len(m.RemoteSources) > 0 {
		unknown = true
	}
	for r := range set {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	return roots, unknown
}

// stripComments removes #, // and /* */ comments, leaving string literals intact.
func stripComments(s string) string {
	var b strings.Builder
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			b.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			} else if c == '"' || c == '\n' {
				inStr = false
			}
		case c == '"':
			inStr = true
			b.WriteByte(c)
		case c == '#' || (c == '/' && i+1 < len(s) && s[i+1] == '/'):
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if i < len(s) {
				b.WriteByte('\n')
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				if s[i] == '\n' {
					b.WriteByte('\n')
				}
				i++
			}
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// blockBody returns the text from start (just after an opening brace) to the
// matching closing brace, honoring string literals.
func blockBody(s string, start int) string {
	depth, inStr := 1, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\\' {
				i++
			} else if c == '"' || c == '\n' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start:i]
			}
		}
	}
	return s[start:]
}
