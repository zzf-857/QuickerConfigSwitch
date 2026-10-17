package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

const codexCfg = "# mine\nmodel = \"gpt-5.5\"\nservice_tier = \"fast\"\n"

func setup(t *testing.T) string {
	t.Helper()
	home := testenv.Home(t)
	cx := filepath.Join(home, ".codex")
	os.MkdirAll(cx, 0o700)
	t.Setenv("CODEX_HOME", cx)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	if err := os.WriteFile(filepath.Join(cx, "config.toml"), []byte(codexCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := provider.Add(provider.Provider{ID: "cpa", Name: "CPA", BaseURL: "http://127.0.0.1:8317/v1", APIKey: "k",
		APIs:   provider.APIs{Responses: true, Anthropic: "http://127.0.0.1:8317"},
		Models: []provider.Model{{ID: "gpt-5.5", Fast: true}, {ID: "alias", Fast: true}}}); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(cx, "config.toml")
}

func TestUsePreviewAndBack(t *testing.T) {
	cfg := setup(t)
	off := false
	r := Request{Agent: "codex", Ref: "cpa/alias", Fast: &off, Effort: "high"}
	pv, err := PreviewUse(r)
	if err != nil || pv.Nothing || !strings.Contains(pv.Diff, `model_provider = "qcs-cpa"`) {
		t.Fatalf("preview %+v %v", pv, err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != codexCfg {
		t.Fatal("preview wrote")
	}
	if _, err := Use(r); err != nil {
		t.Fatal(err)
	}
	v := View(mustAgent(t, "codex"))
	if !v.Managed || v.Provider != "cpa" || v.Model != "alias" || v.Fast != "false" || v.Effort != "high" {
		t.Fatalf("view %+v", v)
	}
	if c := Current("codex"); c.Ref != "cpa/alias" || c.Fast == nil || *c.Fast || c.Effort != "high" {
		t.Fatalf("current %+v", c)
	}
	if _, err := Use(Request{Agent: "codex", Ref: Official}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); !strings.HasPrefix(string(b), codexCfg) {
		t.Fatalf("not restored:\n%s", b)
	}
	if pv, _ := PreviewUse(Request{Agent: "codex", Ref: Official}); !pv.Nothing {
		t.Fatal("nothing left to restore")
	}
	if _, err := PreviewUse(Request{Agent: "nope", Ref: Official}); err == nil {
		t.Fatal("unknown agent")
	}
}

func TestProfiles(t *testing.T) {
	cfg := setup(t)
	if _, err := Use(Request{Agent: "codex", Ref: "cpa/gpt-5.5"}); err != nil {
		t.Fatal(err)
	}
	p, err := SaveProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range p.Agents {
		if r.Agent == "codex" && r.Ref == "cpa/gpt-5.5" {
			found = true
		}
	}
	if !found {
		t.Fatalf("profile %+v", p)
	}
	// the built-in official profile switches everything back
	steps, err := ApplyProfile(Official)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.Error != "" {
			t.Fatalf("%s: %s", s.Agent, s.Error)
		}
	}
	if b, _ := os.ReadFile(cfg); !strings.HasPrefix(string(b), codexCfg) || strings.Contains(string(b), "model_provider =") {
		t.Fatalf("official profile didn't restore:\n%s", b)
	}
	pre, err := PreviewProfile("work")
	if err != nil || len(pre) == 0 {
		t.Fatal(err)
	}
	if _, err := ApplyProfile("work"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); !strings.Contains(string(b), `model_provider = "qcs-cpa"`) {
		t.Fatalf("work profile not applied:\n%s", b)
	}
	ps, _ := Profiles()
	if len(ps) != 2 || ps[0].Name != Official || ps[1].Name != "work" {
		t.Fatalf("profiles %+v", ps)
	}
	if _, err := SaveProfile(Official); err == nil {
		t.Fatal("official is built in")
	}
	if err := DeleteProfile("work"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteProfile("work"); err == nil {
		t.Fatal("deleted twice")
	}
}

func mustAgent(t *testing.T, id string) agentT {
	a, err := lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type agentT = agent.Agent
