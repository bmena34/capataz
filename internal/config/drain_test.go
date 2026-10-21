package config

import (
	"encoding/json"
	"testing"
)

func TestDrainForDefaults(t *testing.T) {
	got := Default().DrainFor("/r")
	if got.BranchPrefix != "" || got.StallMinutes != 15 {
		t.Fatalf("defaults = %+v", got)
	}
	if (Config{}).DrainFor("/r").StallMinutes != 15 {
		t.Fatal("zero config stall_minutes should default to 15")
	}
}

func TestDrainForOverlay(t *testing.T) {
	c := Default()
	data := `{"drain":{"branch_prefix":"g/","stall_minutes":20,
	  "repos":{"/a":{"branch_prefix":"bm/","stall_minutes":5},"/b":{"branch_prefix":"","stall_minutes":0}}}}`
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		t.Fatal(err)
	}
	if g := c.DrainFor("/other"); g.BranchPrefix != "g/" || g.StallMinutes != 20 {
		t.Errorf("global = %+v", g)
	}
	if a := c.DrainFor("/a/"); a.BranchPrefix != "bm/" || a.StallMinutes != 5 {
		t.Errorf("repo /a = %+v", a)
	}
	if b := c.DrainFor("/b"); b.BranchPrefix != "" || b.StallMinutes != 15 {
		t.Errorf("repo /b = %+v", b)
	}
}
