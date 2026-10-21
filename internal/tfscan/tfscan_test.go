package tfscan

import (
	"reflect"
	"strings"
	"testing"
)

func scan(t *testing.T, fixture string) *Map {
	t.Helper()
	m, err := Scan("testdata/" + fixture)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRootsFor(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		changed []string
		roots   []string
		unknown bool
	}{
		{"shared module hits all callers", "basic", []string{"modules/net/main.tf"}, []string{"envs/prod", "envs/staging", "envs/ws"}, false},
		{"root file hits one root", "basic", []string{"envs/prod/main.tf"}, []string{"envs/prod"}, false},
		{"tfvars", "basic", []string{"envs/staging/terraform.tfvars"}, []string{"envs/staging"}, false},
		{"lock file", "basic", []string{"envs/prod/.terraform.lock.hcl"}, []string{"envs/prod"}, false},
		{"non terraform ignored", "basic", []string{"README.md", "src/app.go"}, nil, false},
		{"multiple files union", "basic", []string{"envs/prod/main.tf", "envs/staging/main.tf"}, []string{"envs/prod", "envs/staging"}, false},
		{"cloud block root", "cloud", []string{"main.tf"}, []string{"."}, false},
		{"remote source not unknown", "remote", []string{"main.tf"}, []string{"."}, false},
		{"shared module remote version bump", "sharedremote", []string{"modules/app/main.tf"}, []string{"envs/a", "envs/b"}, false},
		{"remote source, unrelated change", "remote", []string{"README.md"}, nil, false},
		{"stray tf unknown", "stray", []string{"scripts/orphan.tf"}, nil, true},
		{"stray does not taint roots", "stray", []string{"root/main.tf"}, []string{"root"}, false},
		{"commented module ignored", "commented", []string{"root/main.tf"}, []string{"root"}, false},
		{"new tf in unknown dir", "basic", []string{"elsewhere/new.tf"}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			roots, unknown := scan(t, c.fixture).RootsFor(c.changed)
			if !reflect.DeepEqual(roots, c.roots) || unknown != c.unknown {
				t.Fatalf("got (%v, %v), want (%v, %v)", roots, unknown, c.roots, c.unknown)
			}
		})
	}
}

func TestScanRootsAndNotes(t *testing.T) {
	m := scan(t, "basic")
	want := []string{"envs/prod", "envs/staging", "envs/ws"}
	if !reflect.DeepEqual(m.Roots, want) {
		t.Fatalf("roots = %v, want %v", m.Roots, want)
	}
	joined := strings.Join(m.Notes, "\n")
	if !strings.Contains(joined, "terraform.workspace") || !strings.Contains(joined, "Terragrunt") {
		t.Fatalf("notes missing workspace/terragrunt signals: %v", m.Notes)
	}
	if rm := scan(t, "remote"); len(rm.RemoteSources) != 1 {
		t.Fatalf("remote sources = %v", rm.RemoteSources)
	}
}
