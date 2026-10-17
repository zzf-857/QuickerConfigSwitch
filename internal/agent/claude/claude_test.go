package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func setup(t *testing.T, settings string) {
	t.Helper()
	testenv.Home(t)
	managedSettingsPath = filepath.Join(t.TempDir(), "managed-settings.json")
	if settings != "" {
		must(t, os.MkdirAll(appdir.ClaudeHome(), 0o755))
		must(t, os.WriteFile(settingsPath(), []byte(settings), 0o644))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func addProv(t *testing.T, id, anth string, models ...string) {
	t.Helper()
	p := provider.Provider{ID: id, Name: id, BaseURL: "http://127.0.0.1:8317/v1", APIKey: "sk-test", APIs: provider.APIs{Chat: true, Anthropic: anth}}
	for _, m := range models {
		p.Models = append(p.Models, provider.Model{ID: m})
	}
	must(t, provider.Add(p))
}

func ops(p *plan.Plan) map[string]plan.Op {
	m := map[string]plan.Op{}
	for _, o := range p.Edits[0].Ops {
		m[string(o.Kind)+" "+o.Key] = o
	}
	return m
}

func TestRegistered(t *testing.T) {
	a, ok := agent.Get("claude")
	if !ok || a.Name() != "Claude Code" {
		t.Fatal("not registered")
	}
}

func TestPlanDirect(t *testing.T) {
	setup(t, "")
	addProv(t, "cpa", "http://127.0.0.1:8317", "claude-opus-4.6", "gpt-5")
	p, err := New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "claude-opus-4.6"})
	must(t, err)
	if len(p.Edits) != 1 || p.Edits[0].Format != plan.JSON || p.Edits[0].Path != settingsPath() {
		t.Fatalf("edits: %+v", p.Edits)
	}
	o := ops(p)
	want := map[string]string{
		"env.ANTHROPIC_BASE_URL":             "http://127.0.0.1:8317",
		"env.ANTHROPIC_AUTH_TOKEN":           "sk-test",
		"env.ANTHROPIC_DEFAULT_OPUS_MODEL":   "claude-opus-4-6",
		"env.ANTHROPIC_DEFAULT_SONNET_MODEL": "claude-opus-4-6",
		"env.ANTHROPIC_DEFAULT_HAIKU_MODEL":  "claude-opus-4-6",
		"model":                              "claude-opus-4-6",
	}
	for k, v := range want {
		if got := o["set-json "+k].Value; got != v {
			t.Errorf("%s = %v, want %s", k, got, v)
		}
	}
	if len(p.Edits[0].Ops) != len(want) {
		t.Errorf("unexpected ops: %+v", p.Edits[0].Ops)
	}
	if p.State["mode"] != "direct" || p.State["provider"] != "cpa" || p.State["model"] != "claude-opus-4.6" {
		t.Errorf("state: %v", p.State)
	}
	if !strings.Contains(p.Notice, "重启 Claude Code") {
		t.Errorf("notice: %q", p.Notice)
	}
}

func TestSpelled(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-4.6":      "claude-opus-4-6",
		"claude-sonnet-4.5":    "claude-sonnet-4-5",
		"claude-opus-4-6":      "claude-opus-4-6",
		"gpt-5.1":              "gpt-5.1",
		"deepseek-v3.2":        "deepseek-v3.2",
		"claude-haiku-4.5[1m]": "claude-haiku-4-5[1m]",
	} {
		if got := Spelled(in); got != want {
			t.Errorf("Spelled(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlanErrors(t *testing.T) {
	setup(t, "")
	addProv(t, "noanth", "", "m1")
	addProv(t, "cpa", "http://x", "m1")
	addProv(t, "free", "http://y")
	c := New()
	if _, err := c.Plan(agent.Target{Mode: agent.Direct, Provider: "noanth", Model: "m1"}); err == nil || !strings.Contains(err.Error(), "Anthropic") {
		t.Errorf("no anthropic: %v", err)
	}
	if _, err := c.Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "nope"}); err == nil {
		t.Error("unknown model accepted")
	}
	if _, err := c.Plan(agent.Target{Mode: agent.Direct, Provider: "free", Model: "anything"}); err != nil {
		t.Errorf("empty model list should accept any: %v", err)
	}
	if _, err := c.Plan(agent.Target{Mode: agent.Direct, Provider: "ghost", Model: "m1"}); err == nil {
		t.Error("unknown provider accepted")
	}
	if _, err := c.Plan(agent.Target{Mode: agent.Official}); err == nil || !strings.Contains(err.Error(), "safekeep.Restore") {
		t.Errorf("official: %v", err)
	}
}

func TestPlanDeletions(t *testing.T) {
	setup(t, `{"env":{"ANTHROPIC_MODEL":"x","ANTHROPIC_API_KEY":"k","CLAUDE_CODE_USE_BEDROCK":"1","CLAUDE_CODE_USE_VERTEX":"0","CLAUDE_CODE_USE_FOUNDRY":"true"}}`)
	addProv(t, "cpa", "http://x", "m1")
	p, err := New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "m1"})
	must(t, err)
	o := ops(p)
	for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_FOUNDRY"} {
		if _, ok := o["del-json env."+k]; !ok {
			t.Errorf("missing del %s", k)
		}
	}
	if _, ok := o["del-json env.CLAUDE_CODE_USE_VERTEX"]; ok {
		t.Error("switch that is off should stay")
	}

	setup(t, `{"env":{"FOO":"1"}}`)
	addProv(t, "cpa", "http://x", "m1")
	p, _ = New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "m1"})
	for _, op := range p.Edits[0].Ops {
		if op.Kind == plan.DelJSON {
			t.Errorf("unexpected delete %s", op.Key)
		}
	}
}

func TestCurrent(t *testing.T) {
	setup(t, "")
	c := New()
	st, err := c.Current()
	must(t, err)
	if st.Mode != agent.Official {
		t.Errorf("no file: %+v", st)
	}

	addProv(t, "cpa", "http://127.0.0.1:8317", "claude-opus-4.6")
	must(t, os.MkdirAll(appdir.ClaudeHome(), 0o755))
	must(t, os.WriteFile(settingsPath(), []byte(`{"model":"claude-opus-4-6","env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:8317/"}}`), 0o644))
	st, _ = c.Current()
	if st.Mode != agent.Direct || st.Provider != "cpa" || st.Model != "claude-opus-4.6" {
		t.Errorf("managed: %+v", st)
	}

	must(t, os.WriteFile(settingsPath(), []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://other.example"}}`), 0o644))
	st, _ = c.Current()
	if st.Mode != agent.Direct || st.Provider != "" || st.Detail != "custom endpoint https://other.example (not managed by this tool)" {
		t.Errorf("unknown: %+v", st)
	}
}

func TestDetect(t *testing.T) {
	setup(t, "")
	t.Setenv("PATH", "")
	d := New().Detect()
	if d.Installed || d.SignedIn {
		t.Errorf("empty: %+v", d)
	}
	must(t, os.MkdirAll(appdir.ClaudeHome(), 0o755))
	must(t, os.WriteFile(filepath.Join(appdir.ClaudeHome(), ".credentials.json"), []byte("{}"), 0o600))
	d = New().Detect()
	if !d.Installed || !d.SignedIn || d.SignIn != "claudeai" || len(d.Files) != 1 {
		t.Errorf("present: %+v", d)
	}
}

func TestDoctor(t *testing.T) {
	setup(t, "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	for _, c := range cloudSwitches {
		t.Setenv(c, "")
	}
	if ps := New().Doctor(); len(ps) != 0 {
		t.Fatalf("clean: %+v", ps)
	}

	must(t, os.WriteFile(managedSettingsPath, []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://corp"}}`), 0o644))
	t.Setenv("ANTHROPIC_API_KEY", "k")
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "on")
	must(t, os.MkdirAll(appdir.ClaudeHome(), 0o755))
	must(t, os.WriteFile(settingsPath(), []byte(`{"env":{"CLAUDE_CODE_USE_BEDROCK":"yes"}}`), 0o644))
	must(t, os.WriteFile(filepath.Join(appdir.Home(), ".claude.json"), []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://z"}}`), 0o644))
	var all []string
	for _, p := range New().Doctor() {
		if p.Level != "warn" {
			t.Errorf("level %q", p.Level)
		}
		all = append(all, p.Message)
	}
	joined := strings.Join(all, "\n")
	for _, s := range []string{"managed settings", "ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK", ".claude.json"} {
		if !strings.Contains(joined, s) {
			t.Errorf("missing warning about %s in:\n%s", s, joined)
		}
	}
	if len(all) != 5 {
		t.Errorf("got %d warnings:\n%s", len(all), joined)
	}
}
