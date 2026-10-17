package core

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func TestDesktopChoicesAndSharedSwitchLifecycle(t *testing.T) {
	setup(t)
	p, _, _ := provider.Get("cpa")
	p.Models = append(p.Models, provider.Model{ID: "claude-opus-4-6"})
	if err := provider.Update(p); err != nil {
		t.Fatal(err)
	}
	a, ok := agent.Get("claude-desktop")
	if !ok {
		t.Fatal("Claude Desktop not registered for CLI/Web/GUI")
	}
	view := View(a)
	if !reflect.DeepEqual(view.ModelRefs, []string{"cpa/claude-opus-4-6"}) || view.SignedIn || view.SignIn != "unknown" {
		t.Fatalf("Desktop choices or login status %+v", view)
	}
	for _, f := range a.Detect().Files {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("listing Desktop created/read unexpected fixture file %s: %v", f, err)
		}
	}
	r := Request{Agent: "claude-desktop", Ref: "cpa/claude-opus-4-6"}
	pv, err := PreviewUse(r)
	if err != nil || pv.Nothing || !strings.Contains(pv.Notice, "Claude-3p") || safekeep.Touched(r.Agent) {
		t.Fatalf("preview %+v, %v", pv, err)
	}
	if _, err := Use(r); err != nil {
		t.Fatal(err)
	}
	if current := Current(r.Agent); current.Ref != r.Ref || View(a).Provider != "cpa" {
		t.Fatalf("Desktop current %+v", current)
	}
	if _, err := Use(Request{Agent: r.Agent, Ref: Official}); err != nil {
		t.Fatal(err)
	}
	if Current(r.Agent).Ref != Official || safekeep.Touched(r.Agent) {
		t.Fatal("Desktop official restore left active state")
	}
	// An explicit empty list must survive JSON serialization: omitting it
	// would let the page offer unsupported protocol-compatible models.
	p.Headers = map[string]string{"X-Trace": "custom"}
	if err := provider.Update(p); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(View(a))
	if err != nil || !strings.Contains(string(encoded), `"modelRefs":[]`) {
		t.Fatalf("missing empty supported choices: %s, %v", encoded, err)
	}
	if _, err := PreviewUse(r); err == nil {
		t.Fatal("Desktop silently ignored unsupported provider headers")
	}
}
