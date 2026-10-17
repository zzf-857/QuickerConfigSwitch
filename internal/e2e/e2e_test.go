// Package e2e checks the whole path a switch takes — adapter plan, safekeep
// apply, the agent's config as the agent reads it, and the way back — on
// realistic configs in a temporary home.
package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/agent/claude"
	"github.com/zzf-857/quickerconfigswitch/internal/agent/codex"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// A Codex signed in to ChatGPT, with /fast already on, a CC Switch table and
// a profile: what a real user has.
const codexConfig = "\ufeff# my codex config\r\n" +
	"model = \"gpt-5.5\"\r\n" +
	"service_tier = \"fast\"   # /fast\r\n" +
	"model_reasoning_effort = \"high\"\r\n" +
	"\r\n" +
	"[features]\r\n" +
	"fast_mode = true\r\n" +
	"\r\n" +
	"# CC Switch's provider\r\n" +
	"[model_providers.custom]\r\n" +
	"name = \"relay\"\r\n" +
	"base_url = \"https://relay.example/v1\"\r\n" +
	"\r\n" +
	"[profiles.deep]\r\n" +
	"model_reasoning_effort = \"xhigh\"\r\n"

const authJSON = `{"auth_mode":"chatgpt","tokens":{"access_token":"SECRET-ACCESS","refresh_token":"SECRET-REFRESH"}}`

func setup(t *testing.T) (codexHome, claudeHome string) {
	t.Helper()
	home := testenv.Home(t)
	codexHome = filepath.Join(home, ".codex")
	claudeHome = filepath.Join(home, ".claude")
	for _, d := range []string{codexHome, claudeHome} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	if err := provider.Add(provider.Provider{
		ID: "cpa", Name: "CPA", BaseURL: "http://127.0.0.1:8317/v1", APIKey: "sk-cpa",
		APIs: provider.APIs{Responses: true, Chat: true, Anthropic: "http://127.0.0.1:8317"},
		Models: []provider.Model{
			{ID: "gpt-5.5", Fast: true, Efforts: []string{"low", "medium", "high", "xhigh"}},
			{ID: "cpa-gpt-alias", Fast: true, Efforts: []string{"low", "high"}},
			{ID: "claude-opus-4.6"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return codexHome, claudeHome
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func use(t *testing.T, a agent.Agent, tg agent.Target) {
	t.Helper()
	p, err := a.Plan(tg)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := safekeep.Apply(p); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

// codexReads decodes config.toml as Codex would (BOM aside).
func codexReads(t *testing.T, path string) map[string]any {
	t.Helper()
	b := bytes.TrimPrefix([]byte(read(t, path)), []byte("\ufeff"))
	var m map[string]any
	if err := toml.Unmarshal(b, &m); err != nil {
		t.Fatalf("Codex couldn't load config.toml: %v\n%s", err, b)
	}
	return m
}

func fastTier(t *testing.T, catalog, slug string) bool {
	t.Helper()
	var c struct {
		Models []struct {
			Slug  string `json:"slug"`
			Tiers []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"service_tiers"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(read(t, catalog)), &c); err != nil {
		t.Fatal(err)
	}
	for _, m := range c.Models {
		if m.Slug == slug {
			for _, tr := range m.Tiers {
				if tr.ID == "priority" && tr.Name == "Fast" {
					return true
				}
			}
			return false
		}
	}
	t.Fatalf("%s not in catalog", slug)
	return false
}

// The user's story: official Codex with /fast → CPA keeping Fast → another
// CPA model → back to official, byte for byte, sign-in never touched.
func TestCodexOfficialToCPAAndBack(t *testing.T) {
	codexHome, _ := setup(t)
	cfg := filepath.Join(codexHome, "config.toml")
	auth := filepath.Join(codexHome, "auth.json")
	write(t, cfg, codexConfig)
	write(t, auth, authJSON)
	cx, _ := agent.Get("codex")

	// 1. a model Codex knows: its bundled entry already offers Fast
	use(t, cx, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})
	m := codexReads(t, cfg)
	if m["model_provider"] != "qcs-cpa" || m["model"] != "gpt-5.5" {
		t.Fatalf("not on CPA: %v", m)
	}
	if m["service_tier"] != "fast" {
		t.Fatalf("the user's /fast choice was lost: %v", m["service_tier"])
	}
	prov := m["model_providers"].(map[string]any)
	if _, ok := prov["custom"]; !ok {
		t.Fatal("CC Switch's table was touched")
	}
	tbl := prov["qcs-cpa"].(map[string]any)
	if tbl["base_url"] != "http://127.0.0.1:8317/v1" || tbl["wire_api"] != "responses" || tbl["experimental_bearer_token"] != "sk-cpa" {
		t.Fatalf("provider table: %v", tbl)
	}
	if _, ok := m["profiles"]; !ok {
		t.Fatal("profiles lost")
	}

	// 2. a model Codex doesn't know: a catalog that still offers Fast
	use(t, cx, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "cpa-gpt-alias"})
	m = codexReads(t, cfg)
	cat, _ := m["model_catalog_json"].(string)
	if cat == "" || !filepath.IsAbs(cat) {
		t.Fatalf("model_catalog_json = %q", cat)
	}
	if !fastTier(t, cat, "cpa-gpt-alias") || !fastTier(t, cat, "gpt-5.5") {
		t.Fatal("Fast not offered in the catalog")
	}

	// 3. back to official: exactly the file the user had
	if _, err := safekeep.Restore("codex"); err != nil {
		t.Fatal(err)
	}
	got := read(t, cfg)
	m = codexReads(t, cfg)
	if _, on := m["model_provider"]; on {
		t.Fatal("still on a provider")
	}
	// the provider table stays for threads started on it; the rest is the
	// original byte for byte
	if !bytes.HasPrefix([]byte(got), []byte(codexConfig)) {
		t.Fatalf("original not restored:\n%q", got)
	}
	if _, ok := m["model_providers"].(map[string]any)["qcs-cpa"]; !ok {
		t.Fatal("qcs-cpa table should stay for its threads")
	}
	if read(t, auth) != authJSON {
		t.Fatal("auth.json changed")
	}
	if safekeep.Touched("codex") {
		t.Fatal("still marked as managed")
	}
}

// Edits the user (or Codex's own /model) makes while on CPA survive the way
// back; only what this tool set is undone.
func TestCodexRestoreKeepsTheUsersLaterEdits(t *testing.T) {
	codexHome, _ := setup(t)
	cfg := filepath.Join(codexHome, "config.toml")
	write(t, cfg, codexConfig)
	cx, _ := agent.Get("codex")
	use(t, cx, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})

	b := read(t, cfg) + "\r\n[mcp_servers.docs]\r\ncommand = \"docs-mcp\"\r\n"
	write(t, cfg, b)
	if _, err := safekeep.Restore("codex"); err != nil {
		t.Fatal(err)
	}
	m := codexReads(t, cfg)
	if _, on := m["model_provider"]; on {
		t.Fatal("still on a provider")
	}
	if m["model"] != "gpt-5.5" || m["service_tier"] != "fast" || m["model_reasoning_effort"] != "high" {
		t.Fatalf("original values not back: %v", m)
	}
	if _, ok := m["mcp_servers"]; !ok {
		t.Fatal("the user's later edit was lost")
	}
}

// Fast switched off for one switch and on again leaves the user's own
// choice in place once restored.
func TestCodexFastOffThenRestore(t *testing.T) {
	codexHome, _ := setup(t)
	cfg := filepath.Join(codexHome, "config.toml")
	write(t, cfg, codexConfig)
	cx, _ := agent.Get("codex")
	off := false
	use(t, cx, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5", Fast: &off})
	if _, on := codexReads(t, cfg)["service_tier"]; on {
		t.Fatal("service_tier should be off")
	}
	if _, err := safekeep.Restore("codex"); err != nil {
		t.Fatal(err)
	}
	if codexReads(t, cfg)["service_tier"] != "fast" {
		t.Fatal("the user's /fast not restored")
	}
}

// A Codex with no config at all gets one, and loses it again.
func TestCodexFromNothing(t *testing.T) {
	codexHome, _ := setup(t)
	cfg := filepath.Join(codexHome, "config.toml")
	cx, _ := agent.Get("codex")
	use(t, cx, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "cpa-gpt-alias"})
	codexReads(t, cfg)
	if _, err := safekeep.Restore("codex"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(cfg); err == nil {
		// only the kept provider table may remain
		var m map[string]any
		if toml.Unmarshal(b, &m) != nil || len(m) != 1 || m["model_providers"] == nil {
			t.Fatalf("leftovers after restore:\n%s", b)
		}
	}
}

const claudeSettings = `{
  // my settings
  "theme": "dark",
  "model": "opus",
  "env": {
    "DISABLE_TELEMETRY": "1",
    "ANTHROPIC_MODEL": "claude-opus-5-5"
  },
}
`

func TestClaudeToCPAAndBack(t *testing.T) {
	_, claudeHome := setup(t)
	st := filepath.Join(claudeHome, "settings.json")
	creds := filepath.Join(claudeHome, ".credentials.json")
	write(t, st, claudeSettings)
	write(t, creds, `{"claudeAiOauth":{"accessToken":"SECRET"}}`)
	cl, _ := agent.Get("claude")
	use(t, cl, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "claude-opus-4.6"})

	s := read(t, st)
	for _, want := range []string{`"ANTHROPIC_BASE_URL": "http://127.0.0.1:8317"`, `"claude-opus-4.6"`, `"theme": "dark"`, `// my settings`, `"DISABLE_TELEMETRY"`} {
		if !bytes.Contains([]byte(s), []byte(want)) {
			t.Fatalf("settings.json lacks %s:\n%s", want, s)
		}
	}
	if bytes.Contains([]byte(s), []byte(`"ANTHROPIC_MODEL"`)) {
		t.Fatal("ANTHROPIC_MODEL outranks the pick and should be out while switched")
	}
	if st, _ := cl.Current(); st.Provider != "cpa" || st.Model != "claude-opus-4.6" {
		t.Fatalf("Current = %+v", st)
	}
	if _, err := safekeep.Restore("claude"); err != nil {
		t.Fatal(err)
	}
	if read(t, st) != claudeSettings {
		t.Fatalf("settings.json not restored:\n%s", read(t, st))
	}
	if read(t, creds) != `{"claudeAiOauth":{"accessToken":"SECRET"}}` {
		t.Fatal("credentials changed")
	}
}

// Both adapters are registered for the CLI and GUI.
func TestRegistered(t *testing.T) {
	_ = codex.New
	_ = claude.New
	for _, id := range []string{"codex", "claude"} {
		if _, ok := agent.Get(id); !ok {
			t.Fatalf("%s not registered", id)
		}
	}
}
