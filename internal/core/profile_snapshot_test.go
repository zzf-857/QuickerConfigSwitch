package core

import (
	"os"
	"testing"
)

func TestApplyProfileUsesInspectedSnapshot(t *testing.T) {
	cfg := setup(t)
	original, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p := Profile{Name: "work", Agents: []Request{{Agent: "claude", Ref: Official}}}
	if err := PutProfile(p); err != nil {
		t.Fatal(err)
	}
	snapshot, err := findProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	// Another window changes the named profile after the caller reviewed it.
	p.Agents = []Request{{Agent: "codex", Ref: "cpa/alias"}}
	if err := PutProfile(p); err != nil {
		t.Fatal(err)
	}
	steps := ApplyProfileSnapshot(snapshot)
	if len(steps) != 1 || steps[0].Agent != "claude" || steps[0].Error != "" {
		t.Fatalf("steps: %+v", steps)
	}
	if after, _ := os.ReadFile(cfg); string(after) != string(original) {
		t.Fatal("reloaded later profile and changed Codex")
	}
}
