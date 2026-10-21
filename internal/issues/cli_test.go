package issues

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn and returns what it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	ferr := fn()
	w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	return string(out), ferr
}

const askBase = "# X\n\nStatus: claimed\nType: task\nBlocked by: None\nTouches: a.go\n\n## What to build\n\nStuff.\n"

// headerLines returns every line above the first "## " section except Status,
// so tests can check ask/answer touch nothing else in the header.
func headerLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "## ") {
			break
		}
		if !strings.HasPrefix(l, "Status:") {
			out = append(out, l)
		}
	}
	return out
}

func TestAskAnswerRoundTrip(t *testing.T) {
	repo := t.TempDir()
	path := writeIssue(t, repo, "f", "01-x.md", askBase)
	before := strings.Join(headerLines(t, path), "\n")

	if _, err := captureStdout(t, func() error { return askCLI(path, "Which table?") }); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if got := claimStatus(t, path); got != StatusAsking {
		t.Fatalf("after ask status = %q, want %q", got, StatusAsking)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "## Comments\n\n"+questionMarker+" Which table?") {
		t.Errorf("question not under ## Comments:\n%s", data)
	}
	if _, ok, _ := LatestAnswer(path); ok {
		t.Errorf("LatestAnswer found an answer before one was given")
	}

	out, err := captureStdout(t, func() error { return answerCLI(path, "  the users table ") })
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if out != "the users table\n" {
		t.Errorf("answer stdout = %q, want the answer text alone", out)
	}
	if got := claimStatus(t, path); got != StatusClaimed {
		t.Errorf("after answer status = %q, want %q", got, StatusClaimed)
	}
	if got, ok, err := LatestAnswer(path); err != nil || !ok || got != "the users table" {
		t.Errorf("LatestAnswer = %q, %v, %v", got, ok, err)
	}
	if after := strings.Join(headerLines(t, path), "\n"); after != before {
		t.Errorf("non-Status header lines changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// A second round: LatestAnswer returns the newest answer.
	captureStdout(t, func() error { return askCLI(path, "And the column?") })
	captureStdout(t, func() error { return answerCLI(path, "email") })
	if got, _, _ := LatestAnswer(path); got != "email" {
		t.Errorf("LatestAnswer after second round = %q, want %q", got, "email")
	}
}

func TestAnswerRefusesWhenNotAsking(t *testing.T) {
	repo := t.TempDir()
	path := writeIssue(t, repo, "f", "01-x.md", askBase)
	before, _ := os.ReadFile(path)

	_, err := captureStdout(t, func() error { return answerCLI(path, "nope") })
	if err == nil || !strings.Contains(err.Error(), "not asking: claimed") {
		t.Fatalf("answer on claimed issue: err = %v, want refusal naming the status", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Errorf("refused answer changed the file:\n%s", after)
	}
}

func TestAskingExcludedFromWaves(t *testing.T) {
	repo := t.TempDir()
	path := writeIssue(t, repo, "f", "01-x.md", askBase)
	writeIssue(t, repo, "f", "02-y.md", "# Y\n\nStatus: ready-for-agent\nTouches: b.go\n")
	captureStdout(t, func() error { return askCLI(path, "q?") })

	t.Chdir(repo)
	out, err := captureStdout(t, func() error { return wavesJSON("f", nil) })
	if err != nil {
		t.Fatalf("waves: %v", err)
	}
	if strings.Contains(out, "01-x.md") || !strings.Contains(out, "02-y.md") {
		t.Errorf("waves should list 02 but not the asking 01:\n%s", out)
	}
}
