package tfscan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

const top = "testdata/basic"

// fakeRunner answers commands keyed on "dir|name args…"; unknown commands fail.
type fakeRunner map[string]string

func (f fakeRunner) Run(dir, name string, args ...string) (string, error) {
	key := dir + "|" + name + " " + strings.Join(args, " ")
	if out, ok := f[key]; ok {
		if out == "ERR:notfound" {
			return "", fmt.Errorf("%s: %w", name, exec.ErrNotFound)
		}
		return out, nil
	}
	return "", fmt.Errorf("unexpected command %q", key)
}

// baseFake is a checkout on branch "feat" changing changed (uncommitted), with
// no open PRs, no sibling worktrees, and nothing new on origin/main.
func baseFake(changed string) fakeRunner {
	return fakeRunner{
		".|git rev-parse --show-toplevel":                                                top + "\n",
		top + "|git rev-parse --verify --quiet origin/main^{commit}":                     "abc\n",
		top + "|git merge-base origin/main HEAD":                                         "mb\n",
		top + "|git diff --name-only mb":                                                 changed,
		top + "|git ls-files --others --exclude-standard":                                "",
		top + "|git rev-parse --abbrev-ref HEAD":                                         "feat\n",
		top + "|gh pr list --state open --limit 200 --json number,url,headRefName,files": "[]",
		top + "|git worktree list --porcelain":                                           "worktree " + top + "\nHEAD aaa\nbranch refs/heads/feat\n",
		top + "|git diff --name-only mb..origin/main":                                    "",
	}
}

func check(t *testing.T, f fakeRunner, args ...string) (int, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(f, ".", append([]string{"check"}, args...), &out, &errb)
	if code == 2 {
		t.Fatalf("error exit: %s", errb.String())
	}
	return code, out.String()
}

func TestCheckNoTerraformChanges(t *testing.T) {
	code, out := check(t, baseFake("README.md\n"))
	if code != 0 || !strings.Contains(out, "no Terraform changes") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckClean(t *testing.T) {
	code, out := check(t, baseFake("envs/prod/main.tf\n"))
	if code != 0 || !strings.Contains(out, "clean") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckUntrackedCounts(t *testing.T) {
	f := baseFake("")
	f[top+"|git ls-files --others --exclude-standard"] = "elsewhere/new.tf\n"
	code, out := check(t, f)
	if code != 1 || !strings.Contains(out, KindUnknown) {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckOpenPR(t *testing.T) {
	f := baseFake("modules/net/main.tf\n")
	f[top+"|gh pr list --state open --limit 200 --json number,url,headRefName,files"] = `[
	  {"number":7,"url":"https://gh/pr/7","headRefName":"other","files":[{"path":"envs/staging/main.tf"}]},
	  {"number":8,"url":"https://gh/pr/8","headRefName":"feat","files":[{"path":"envs/prod/main.tf"}]},
	  {"number":9,"url":"https://gh/pr/9","headRefName":"docs","files":[{"path":"README.md"}]}
	]`
	code, out := check(t, f)
	if code != 1 || !strings.Contains(out, "https://gh/pr/7") || !strings.Contains(out, "envs/staging") {
		t.Fatalf("shared module should overlap PR 7: code=%d out=%q", code, out)
	}
	if strings.Contains(out, "pr/8") || strings.Contains(out, "pr/9") {
		t.Fatalf("own branch / non-tf PR reported: %q", out)
	}
}

func TestCheckTruncatedPRFiles(t *testing.T) {
	f := baseFake("envs/prod/main.tf\n")
	var files []string
	for i := range prFileLimit {
		files = append(files, fmt.Sprintf(`{"path":"src/f%d.go"}`, i))
	}
	f[top+"|gh pr list --state open --limit 200 --json number,url,headRefName,files"] =
		`[{"number":3,"url":"https://gh/pr/3","headRefName":"big","files":[` + strings.Join(files, ",") + `]}]`
	f[top+"|gh pr diff 3 --name-only"] = "src/f1.go\nenvs/prod/main.tf\n"
	code, out := check(t, f)
	if code != 1 || !strings.Contains(out, "https://gh/pr/3") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckGhMissing(t *testing.T) {
	f := baseFake("envs/prod/main.tf\n")
	f[top+"|gh pr list --state open --limit 200 --json number,url,headRefName,files"] = "ERR:notfound"
	code, out := check(t, f)
	if code != 1 || !strings.Contains(out, "open PRs not checked") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckSiblingWorktree(t *testing.T) {
	f := baseFake("envs/prod/main.tf\n")
	f[top+"|git worktree list --porcelain"] = "worktree " + top + "\nHEAD aaa\nbranch refs/heads/feat\n\n" +
		"worktree /wt/sib\nHEAD bbb\nbranch refs/heads/capataz/x-02-net\n\n" +
		"worktree /wt/other\nHEAD ccc\nbranch refs/heads/capataz/x-03-docs\n"
	f["/wt/sib|git merge-base origin/main bbb"] = "mb2\n"
	f["/wt/sib|git diff --name-only mb2"] = "modules/net/main.tf\n"
	f["/wt/sib|git ls-files --others --exclude-standard"] = ""
	f["/wt/other|git merge-base origin/main ccc"] = "mb3\n"
	f["/wt/other|git diff --name-only mb3"] = "README.md\n"
	f["/wt/other|git ls-files --others --exclude-standard"] = ""
	code, out := check(t, f)
	if code != 1 || !strings.Contains(out, "capataz/x-02-net") || strings.Contains(out, "x-03-docs") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckStalePlan(t *testing.T) {
	f := baseFake("envs/staging/main.tf\n")
	f[top+"|git diff --name-only mb..origin/main"] = "modules/net/main.tf\n"
	code, out := check(t, f)
	if code != 1 || !strings.Contains(out, KindStale) || !strings.Contains(out, "envs/staging") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckBaseFallback(t *testing.T) {
	f := baseFake("README.md\n")
	delete(f, top+"|git rev-parse --verify --quiet origin/main^{commit}")
	f[top+"|git rev-parse --verify --quiet main^{commit}"] = "abc\n"
	f[top+"|git merge-base main HEAD"] = "mb\n"
	code, out := check(t, f)
	if code != 0 || !strings.Contains(out, "vs main") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestCheckNoBaseIsError(t *testing.T) {
	f := baseFake("")
	var out, errb bytes.Buffer
	if code := run(f, ".", []string{"check", "--base", "nope"}, &out, &errb); code != 2 {
		t.Fatalf("code=%d", code)
	}
}

func TestCheckJSON(t *testing.T) {
	f := baseFake("envs/prod/main.tf\nmodules/net/main.tf\n")
	f[top+"|gh pr list --state open --limit 200 --json number,url,headRefName,files"] =
		`[{"number":7,"url":"https://gh/pr/7","headRefName":"other","files":[{"path":"envs/prod/terraform.tfvars"}]}]`
	code, out := check(t, f, "--json")
	if code != 1 {
		t.Fatalf("code=%d", code)
	}
	want := `{
  "base": "origin/main",
  "changed": [
    "envs/prod/main.tf",
    "modules/net/main.tf"
  ],
  "roots": [
    "envs/prod",
    "envs/staging",
    "envs/ws"
  ],
  "notes": [
    "terraform.workspace used in envs/ws (root envs/ws): workspaces not modeled",
    "terragrunt.hcl present in envs/ws: Terragrunt not modeled"
  ],
  "findings": [
    {
      "kind": "open-pr",
      "root": "envs/prod",
      "pr": 7,
      "url": "https://gh/pr/7",
      "branch": "other",
      "message": "PR #7 (other) also changes envs/prod: https://gh/pr/7"
    }
  ]
}
`
	if out != want {
		t.Fatalf("json mismatch:\n%s", out)
	}
	var rep Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatal(err)
	}
}

func TestCheckJSONNoChanges(t *testing.T) {
	_, out := check(t, baseFake(""), "--json")
	want := "{\n  \"base\": \"origin/main\",\n  \"changed\": [],\n  \"roots\": [],\n  \"notes\": [],\n  \"findings\": []\n}\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
}
