package config

import (
	"encoding/json"
	"testing"
)

func TestDrainForDefaults(t *testing.T) {
	got := Default().DrainFor("/r")
	if got.BranchPrefix != "" || got.Verify != "" || got.PRCap != 3 {
		t.Fatalf("defaults = %+v", got)
	}
	if (Config{}).DrainFor("/r").PRCap != 3 {
		t.Fatal("zero config pr_cap should default to 3")
	}
}

func TestDrainForOverlay(t *testing.T) {
	c := Default()
	data := `{"drain":{"branch_prefix":"g/","verify":"go test","pr_cap":5,
	  "repos":{"/a":{"branch_prefix":"bm/","pr_cap":2},"/b":{"branch_prefix":""}}}}`
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		t.Fatal(err)
	}
	if g := c.DrainFor("/other"); g.BranchPrefix != "g/" || g.Verify != "go test" || g.PRCap != 5 {
		t.Errorf("global = %+v", g)
	}
	if a := c.DrainFor("/a/"); a.BranchPrefix != "bm/" || a.Verify != "go test" || a.PRCap != 2 {
		t.Errorf("repo /a = %+v", a)
	}
	if b := c.DrainFor("/b"); b.BranchPrefix != "" || b.PRCap != 5 {
		t.Errorf("repo /b = %+v", b)
	}
}
