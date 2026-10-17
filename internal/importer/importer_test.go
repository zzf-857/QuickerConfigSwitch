package importer

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

const ccCodexCfg = `model_provider = "custom"
model = "gpt-5.1"

[model_providers.custom]
name = "custom"
base_url = "https://relay.example.com/v1/"
wire_api = "responses"
`

func makeDB(t *testing.T, path string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE providers (id TEXT, app_type TEXT, name TEXT, settings_config TEXT, website_url TEXT, category TEXT, sort_index INTEGER, created_at INTEGER)`); err != nil {
		t.Fatal(err)
	}
	ins := func(id, app, name, cfg string) {
		if _, err := db.Exec(`INSERT INTO providers (id, app_type, name, settings_config, sort_index, created_at) VALUES (?,?,?,?,0,1)`, id, app, name, cfg); err != nil {
			t.Fatal(err)
		}
	}
	ins("1", "claude", "我的中转", `{"env":{"ANTHROPIC_BASE_URL":"https://relay.example.com","ANTHROPIC_AUTH_TOKEN":"sk-cc-secret","ANTHROPIC_MODEL":"claude-sonnet-4","ANTHROPIC_DEFAULT_HAIKU_MODEL":"claude-haiku"}}`)
	ins("2", "codex", "我的中转", `{"auth":{"OPENAI_API_KEY":"sk-cc-secret"},"config":`+jsonStr(ccCodexCfg)+`}`)
	ins("3", "claude", "Official", `{"env":{}}`)
	ins("4", "codex", "Official", `{"auth":{},"config":""}`)
	ins("5", "claude", "qcs-mine", `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:1","ANTHROPIC_AUTH_TOKEN":"x"}}`)
	ins("6", "gemini", "G", `{"env":{"GEMINI_API_KEY":"k"}}`)
}

func jsonStr(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return `"` + s + `"`
}

func find(cs []Candidate, source, sub string) *Candidate {
	for i := range cs {
		if cs[i].Source == source && strings.Contains(cs[i].Origin, sub) {
			return &cs[i]
		}
	}
	return nil
}

func TestScanAll(t *testing.T) {
	home := testenv.Home(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codexhome"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claudehome"))
	db := filepath.Join(home, ".cc-switch", "cc-switch.db")
	makeDB(t, db)
	write(t, filepath.Join(home, "codexhome", "config.toml"), `model_provider = "custom"
model = "gpt-5"

[model_providers.custom]
name = "Custom Relay"
base_url = "https://relay.example.com/v1"
experimental_bearer_token = "sk-cc-secret"

[model_providers.qcs-x]
base_url = "http://127.0.0.1:9/v1"

[model_providers.fromenv]
base_url = "https://env.example.com/v1"
env_key = "MY_KEY"

[model_providers.nourl]
name = "none"
`)
	write(t, filepath.Join(home, "claudehome", "settings.json"), `{
  // comment
  "env": {"ANTHROPIC_BASE_URL": "https://claude.example.org/", "ANTHROPIC_API_KEY": "sk-claude", "ANTHROPIC_MODEL": "m1"}
}`)
	// an existing provider with the CC Switch relay's URL and key
	if err := provider.Add(provider.Provider{ID: "mine", Name: "Mine", BaseURL: "https://RELAY.example.com/v1", APIKey: "sk-cc-secret", APIs: provider.APIs{Responses: true}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(db)
	st1, _ := os.Stat(db)

	cs, errs := Scan()
	if len(errs) != 0 {
		t.Fatalf("errs: %v", errs)
	}
	after, _ := os.ReadFile(db)
	st2, _ := os.Stat(db)
	if string(before) != string(after) || !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("cc-switch.db was modified")
	}

	// CC Switch: claude + codex merged into one provider with both APIs
	cc := find(cs, "cc-switch", "我的中转")
	if cc == nil {
		t.Fatalf("no cc-switch candidate: %+v", cs)
	}
	p := cc.Provider
	if p.APIs.Anthropic != "https://relay.example.com" || !p.APIs.Responses || p.BaseURL != "https://relay.example.com/v1/" || p.APIKey != "sk-cc-secret" {
		t.Errorf("merged provider: %+v", p)
	}
	if len(p.Models) != 3 {
		t.Errorf("models: %+v", p.Models)
	}
	if m, ok := p.Model("gpt-5.1"); !ok || !m.Fast {
		t.Errorf("gpt model fast: %+v", p.Models)
	}
	if m, _ := p.Model("claude-sonnet-4"); m.Fast {
		t.Error("claude model should not be fast")
	}
	if cc.Exists != "mine" {
		t.Errorf("Exists = %q", cc.Exists)
	}
	n := 0
	for _, c := range cs {
		if c.Source == "cc-switch" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("cc-switch candidates = %d, want 1 (official/qcs/gemini skipped)", n)
	}

	cx := find(cs, "codex", "custom")
	if cx == nil || cx.Provider.Name != "Custom Relay" || cx.Provider.APIKey != "sk-cc-secret" || !cx.Provider.APIs.Responses || len(cx.Provider.Models) != 1 || cx.Provider.Models[0].ID != "gpt-5" {
		t.Errorf("codex custom: %+v", cx)
	}
	if cx != nil && cx.Exists != "mine" {
		t.Errorf("codex Exists = %q", cx.Exists)
	}
	fe := find(cs, "codex", "fromenv")
	if fe == nil || fe.Provider.APIKey != "" || fe.Note != "key comes from env var MY_KEY" || len(fe.Provider.Models) != 0 {
		t.Errorf("fromenv: %+v", fe)
	}
	if find(cs, "codex", "qcs-x") != nil || find(cs, "codex", "nourl") != nil {
		t.Error("qcs-x / nourl should be skipped")
	}

	cl := find(cs, "claude", "settings.json")
	if cl == nil || cl.Provider.APIs.Anthropic != "https://claude.example.org/" || cl.Provider.APIKey != "sk-claude" || len(cl.Provider.Models) != 1 || cl.Exists != "" {
		t.Errorf("claude: %+v", cl)
	}

	ids := map[string]bool{}
	for _, c := range cs {
		if err := provider.ValidID(c.Provider.ID); err != nil {
			t.Error(err)
		}
		if ids[c.Provider.ID] || c.Provider.ID == "mine" {
			t.Errorf("duplicate id %q", c.Provider.ID)
		}
		ids[c.Provider.ID] = true
	}

	// Add keeps the suggested id, or takes a given one
	if _, err := Add(*fe, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(*fe, ""); err == nil {
		t.Error("second Add under the same id should fail")
	}
	if _, err := Add(*fe, "other"); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyConfigJSON(t *testing.T) {
	home := testenv.Home(t)
	write(t, filepath.Join(home, ".cc-switch", "config.json"), `{
  "claude": {"providers": {"a": {"name": "Legacy", "settingsConfig": {"env": {"ANTHROPIC_BASE_URL": "https://legacy.example.net", "ANTHROPIC_API_KEY": "k1"}}}}},
  "codex": {"providers": {"b": {"name": "LegacyX", "settingsConfig": {"auth": {"OPENAI_API_KEY": "k2"}, "config": "model_provider = \"p\"\nmodel = \"o3\"\n[model_providers.p]\nbase_url = \"https://x.example.net/v1\"\n"}}}}
}`)
	cs, errs := Scan()
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(cs) != 2 {
		t.Fatalf("got %+v", cs)
	}
	if cs[0].Provider.ID != "legacy" || cs[1].Provider.ID != "legacyx" {
		t.Errorf("ids: %q %q", cs[0].Provider.ID, cs[1].Provider.ID)
	}
	if m := cs[1].Provider.Models[0]; m.ID != "o3" || !m.Fast {
		t.Errorf("model %+v", m)
	}
}

func TestBrokenDBStillReturnsOthers(t *testing.T) {
	home := testenv.Home(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, "ch"))
	write(t, filepath.Join(home, ".cc-switch", "cc-switch.db"), "this is not a sqlite database at all, just garbage text padding padding padding padding")
	write(t, filepath.Join(home, "ch", "config.toml"), "[model_providers.a]\nbase_url = \"https://a.example.com/v1\"\n")
	cs, errs := Scan()
	if len(errs) != 1 {
		t.Fatalf("errs = %v", errs)
	}
	if len(cs) != 1 || cs[0].Source != "codex" {
		t.Fatalf("cands = %+v", cs)
	}
}

func TestIDsAndNothing(t *testing.T) {
	testenv.Home(t)
	if cs, errs := Scan(); len(cs) != 0 || len(errs) != 0 {
		t.Fatalf("empty home: %v %v", cs, errs)
	}
	taken := map[string]bool{"abc": true, "abc-2": true}
	if got := uniqueID("abc", taken); got != "abc-3" {
		t.Error(got)
	}
	long := strings.Repeat("a", 40)
	id := uniqueID(baseID(long, "", ""), map[string]bool{strings.Repeat("a", 32): true})
	if len(id) > 32 || provider.ValidID(id) != nil {
		t.Error(id)
	}
	if got := baseID("123 x", "", ""); got != "p-123-x" {
		t.Error(got)
	}
	if got := baseID("我的", "https://api.foo.com:8080/v1", ""); got != "foo-com" {
		t.Error(got)
	}
}
