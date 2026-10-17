package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// fixture is a config.toml as people have them: a BOM, comments, a
// [features] table, a CC Switch provider, profiles and MCP servers.
const fixture = "\xef\xbb\xbf# my Codex config\n" +
	`model = "gpt-5.5" # default
service_tier = "fast"
approval_policy = "on-request"

[features]
fast_mode = true

[model_providers.custom]
name = "custom"
base_url = "https://relay.example/v1"
wire_api = "responses"

[profiles.work]
model = "gpt-5.5"

[mcp_servers.docs]
command = "docs-mcp"
`

func setup(t *testing.T, config string, models ...provider.Model) string {
	t.Helper()
	home := testenv.Home(t)
	if config != "" {
		writeFile(t, ConfigPath(), config)
	}
	if err := provider.Add(provider.Provider{
		ID: "cpa", Name: "CPA", BaseURL: "http://127.0.0.1:8317/v1", APIKey: "sk-secret-cpa",
		APIs: provider.APIs{Responses: true}, Models: models,
	}); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ptr(b bool) *bool { return &b }

func mustPlan(t *testing.T, tg agent.Target) *plan.Plan {
	t.Helper()
	p, err := New().Plan(tg)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range p.Edits {
		if filepath.Base(e.Path) == "auth.json" {
			t.Fatal("plan touches auth.json")
		}
	}
	return p
}

func configEdit(t *testing.T, p *plan.Plan) plan.FileEdit {
	t.Helper()
	for _, e := range p.Edits {
		if e.Path == ConfigPath() {
			if e.Format != plan.TOML {
				t.Fatalf("config format %q", e.Format)
			}
			return e
		}
	}
	t.Fatal("no config.toml edit")
	return plan.FileEdit{}
}

func catalogEdit(p *plan.Plan) (plan.FileEdit, bool) {
	for _, e := range p.Edits {
		if e.Path == CatalogPath("cpa") {
			return e, true
		}
	}
	return plan.FileEdit{}, false
}

func wantTable(extra ...edit.KV) plan.Op {
	kvs := []edit.KV{
		{Path: "name", Value: "CPA"},
		{Path: "base_url", Value: "http://127.0.0.1:8317/v1"},
		{Path: "wire_api", Value: "responses"},
		{Path: "experimental_bearer_token", Value: "sk-secret-cpa"},
	}
	return plan.Op{Kind: plan.SetTable, Table: "model_providers.qcs-cpa", KVs: append(kvs, extra...), Keep: true, RestoreKeys: []string{"experimental_bearer_token", "http_headers"}}
}

func TestRegistered(t *testing.T) {
	a, ok := agent.Get("codex")
	if !ok || a.Name() != "Codex" {
		t.Fatalf("registry: %v %v", a, ok)
	}
	if got := a.Modes(); !reflect.DeepEqual(got, []agent.Mode{agent.Official, agent.Direct, agent.Relay}) {
		t.Fatalf("modes %v", got)
	}
}

// (a) a model of Codex's bundled catalog, which offers Fast there: no catalog.
func TestPlanBundledModelNoCatalog(t *testing.T) {
	setup(t, fixture, provider.Model{ID: "gpt-5.5", Fast: true})
	p := mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5", Fast: ptr(true)})
	if len(p.Edits) != 1 {
		t.Fatalf("edits: %+v", p.Edits)
	}
	want := []plan.Op{
		{Kind: plan.SetTop, Key: "model_provider", Value: "qcs-cpa"},
		{Kind: plan.SetTop, Key: "model", Value: "gpt-5.5"},
		{Kind: plan.SetTop, Key: "service_tier", Value: "fast"},
		wantTable(),
	}
	if got := configEdit(t, p).Ops; !reflect.DeepEqual(got, want) {
		t.Fatalf("ops\n got %+v\nwant %+v", got, want)
	}
	if want := map[string]string{"mode": "direct", "provider": "cpa", "model": "gpt-5.5"}; !reflect.DeepEqual(p.State, want) {
		t.Fatalf("state %v", p.State)
	}
	if p.Agent != "codex" || !strings.Contains(p.Notice, "重启 Codex 桌面端") || !strings.Contains(p.Notice, "auth.json") {
		t.Fatalf("notice %q", p.Notice)
	}
}

// (b) a model Codex doesn't know: a catalog with Fast, named by model_catalog_json.
func TestPlanCustomModelCatalog(t *testing.T) {
	setup(t, fixture,
		provider.Model{ID: "kimi-k3", Name: "Kimi K3", Fast: true, Efforts: []string{"low", "high"}},
		provider.Model{ID: "gpt-5.5", Fast: true})
	p := mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "kimi-k3", Effort: "high"})
	if len(p.Edits) != 2 || p.Edits[0].Path != CatalogPath("cpa") {
		t.Fatalf("edits (catalog first): %+v", p.Edits)
	}
	cat, _ := catalogEdit(p)
	if cat.Format != plan.Raw || len(cat.Ops) != 1 || cat.Ops[0].Kind != plan.WriteFile {
		t.Fatalf("catalog edit %+v", cat)
	}
	var f struct {
		Models []struct {
			Slug  string `json:"slug"`
			Tiers []struct {
				ID string `json:"id"`
			} `json:"service_tiers"`
		} `json:"models"`
	}
	if err := json.Unmarshal(cat.Ops[0].Data, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Models) != 2 || f.Models[0].Slug != "kimi-k3" || f.Models[1].Slug != "gpt-5.5" {
		t.Fatalf("catalog models %+v", f.Models)
	}
	for _, m := range f.Models {
		if len(m.Tiers) == 0 || m.Tiers[0].ID != "priority" {
			t.Fatalf("%s has no priority tier: %+v", m.Slug, m.Tiers)
		}
	}
	if !filepath.IsAbs(CatalogPath("cpa")) || CatalogPath("cpa") != mustAbs(t, appdir.Path("codex", "catalog-cpa.json")) {
		t.Fatalf("catalog path %s", CatalogPath("cpa"))
	}
	want := []plan.Op{
		{Kind: plan.SetTop, Key: "model_provider", Value: "qcs-cpa"},
		{Kind: plan.SetTop, Key: "model", Value: "kimi-k3"},
		{Kind: plan.SetTop, Key: "model_catalog_json", Value: CatalogPath("cpa")},
		{Kind: plan.SetTop, Key: "model_reasoning_effort", Value: "high"},
		wantTable(),
	}
	if got := configEdit(t, p).Ops; !reflect.DeepEqual(got, want) {
		t.Fatalf("ops\n got %+v\nwant %+v", got, want)
	}
}

func mustAbs(t *testing.T, p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPlanCatalogKeepsUserFields(t *testing.T) {
	setup(t, "", provider.Model{ID: "kimi-k3", Fast: true})
	writeFile(t, CatalogPath("cpa"), `{"models":[{"slug":"kimi-k3","my_note":"hand-added"}]}`)
	p := mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "kimi-k3"})
	cat, ok := catalogEdit(p)
	if !ok || !strings.Contains(string(cat.Ops[0].Data), `"my_note": "hand-added"`) {
		t.Fatalf("user field dropped: %s", cat.Ops[0].Data)
	}
}

// (c) Fast is tri-state: true sets service_tier, false removes it, nil leaves it.
func TestPlanFastTriState(t *testing.T) {
	setup(t, fixture, provider.Model{ID: "gpt-5.5", Fast: true})
	tierOps := func(fast *bool) []plan.Op {
		var out []plan.Op
		for _, op := range configEdit(t, mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5", Fast: fast})).Ops {
			if op.Key == "service_tier" {
				out = append(out, op)
			}
		}
		return out
	}
	if got := tierOps(ptr(true)); !reflect.DeepEqual(got, []plan.Op{{Kind: plan.SetTop, Key: "service_tier", Value: "fast"}}) {
		t.Fatalf("fast: %+v", got)
	}
	if got := tierOps(ptr(false)); !reflect.DeepEqual(got, []plan.Op{{Kind: plan.DelTop, Key: "service_tier"}}) {
		t.Fatalf("no fast: %+v", got)
	}
	if got := tierOps(nil); got != nil {
		t.Fatalf("nil touched service_tier: %+v", got)
	}
	// Fast=false on a bundled GPT model that offers Fast needs a catalog
	// without it; nil keeps the model's own Fast (bundled, no catalog)
	off := mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5", Fast: ptr(false)})
	cat, ok := catalogEdit(off)
	if !ok || !strings.Contains(string(cat.Ops[0].Data), `"service_tiers": []`) || strings.Contains(string(cat.Ops[0].Data), `"id": "priority"`) {
		t.Fatalf("Fast=false catalog: %v", ok)
	}
	if _, ok := catalogEdit(mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})); ok {
		t.Fatal("nil Fast on bundled Fast model wrote a catalog")
	}
}

// (d) refusals.
func TestPlanRefusals(t *testing.T) {
	setup(t, fixture, provider.Model{ID: "gpt-5.5", Fast: true, Efforts: []string{"low", "high"}})
	if err := provider.Add(provider.Provider{ID: "chat", Name: "Chat only", BaseURL: "http://x.example/v1", APIs: provider.APIs{Chat: true}}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]agent.Target{
		"no responses":  {Mode: agent.Direct, Provider: "chat", Model: "m"},
		"unknown model": {Mode: agent.Direct, Provider: "cpa", Model: "gpt-9"},
		"no model":      {Mode: agent.Direct, Provider: "cpa"},
		"no provider":   {Mode: agent.Direct, Provider: "nope", Model: "gpt-5.5"},
		"bad effort":    {Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5", Effort: "max"},
		"relay":         {Mode: agent.Relay, Provider: "cpa", Model: "gpt-5.5"},
		"official":      {Mode: agent.Official},
	}
	for name, tg := range cases {
		if p, err := New().Plan(tg); err == nil {
			t.Errorf("%s: planned %+v", name, p)
		}
	}
	writeFile(t, ConfigPath(), "model = \"gpt-5.5\"\n[features\nfast_mode = true\n")
	if _, err := New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"}); err == nil || !strings.Contains(err.Error(), "not valid TOML") {
		t.Errorf("broken config: %v", err)
	}
}

// A provider with no model list takes any model, and its catalog is that one.
func TestPlanNoModelList(t *testing.T) {
	setup(t, "")
	p := mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "vendor/thing", Fast: ptr(true)})
	cat, ok := catalogEdit(p)
	if !ok || !strings.Contains(string(cat.Ops[0].Data), `"slug": "vendor/thing"`) || !strings.Contains(string(cat.Ops[0].Data), `"priority"`) {
		t.Fatalf("catalog %v", ok)
	}
}

// Adding a provider and using it before fetching models must not disable the
// original Fast choice. Explicit capability/selection changes still win.
func TestPlanUnlistedFastDefaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		model  string
		models []provider.Model
		fast   *bool
		want   bool
	}{
		{name: "bundled GPT", model: "gpt-5.5", want: true},
		{name: "vendor GPT", model: "vendor/gpt-5.5", want: true},
		{name: "o series", model: "o3", want: true},
		{name: "unknown", model: "other-model"},
		{name: "explicit off", model: "gpt-5.5", fast: ptr(false)},
		{name: "explicit on", model: "other-model", fast: ptr(true), want: true},
		{name: "stored off", model: "gpt-5.5", models: []provider.Model{{ID: "gpt-5.5"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup(t, fixture, tc.models...)
			p := mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: tc.model, Fast: tc.fast})
			cat, custom := catalogEdit(p)
			if custom {
				hasFast := strings.Contains(string(cat.Ops[0].Data), `"id": "priority"`)
				if hasFast != tc.want {
					t.Fatalf("catalog Fast = %v, want %v", hasFast, tc.want)
				}
			} else if !tc.want {
				t.Fatal("non-Fast model did not get a catalog override")
			}
			if tc.fast == nil {
				for _, op := range configEdit(t, p).Ops {
					if op.Key == "service_tier" {
						t.Fatal("implicit model changed original Fast selection")
					}
				}
			}
			if got, err := os.ReadFile(ConfigPath()); err != nil || string(got) != fixture {
				t.Fatalf("planning changed original config: %v", err)
			}
		})
	}
}

// (e)+(f) the table is Keep, merged (not Whole), with headers inline.
func TestPlanTableKeepMerge(t *testing.T) {
	setup(t, fixture+`
[model_providers.qcs-cpa]
name = "CPA"
base_url = "http://127.0.0.1:8317/v1"
wire_api = "responses"
request_max_retries = 9 # mine
`, provider.Model{ID: "gpt-5.5", Fast: true})
	if err := provider.Update(provider.Provider{
		ID: "cpa", Name: "CPA", BaseURL: "http://127.0.0.1:8317/v1", APIKey: "sk-secret-cpa",
		APIs: provider.APIs{Responses: true}, Models: []provider.Model{{ID: "gpt-5.5", Fast: true}},
		Headers: map[string]string{"X-B": "2", "X-A": "1"},
	}); err != nil {
		t.Fatal(err)
	}
	p := mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})
	ops := configEdit(t, p).Ops
	tbl := ops[len(ops)-1]
	want := wantTable(edit.KV{Path: "http_headers", Value: edit.Inline{{Path: "X-A", Value: "1"}, {Path: "X-B", Value: "2"}}})
	if !reflect.DeepEqual(tbl, want) || tbl.Whole || !tbl.Keep {
		t.Fatalf("table op\n got %+v\nwant %+v", tbl, want)
	}
	for _, op := range ops {
		if strings.HasPrefix(op.Table, "model_providers.custom") || op.Table == "features" || strings.HasPrefix(op.Table, "profiles") || op.Key == "openai_base_url" {
			t.Fatalf("touched %+v", op)
		}
	}
}

func TestPlanRefusesOtherSpelling(t *testing.T) {
	setup(t, "[model_providers]\nqcs-cpa = { name = \"x\", base_url = \"http://y/v1\" }\n", provider.Model{ID: "gpt-5.5", Fast: true})
	if _, err := New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"}); err == nil {
		t.Fatal("planned over an inline qcs table")
	}
}

func TestPlanNoKeyNoToken(t *testing.T) {
	setup(t, "")
	if err := provider.Update(provider.Provider{ID: "cpa", Name: "CPA", BaseURL: "http://127.0.0.1:8317/v1", APIs: provider.APIs{Responses: true}}); err != nil {
		t.Fatal(err)
	}
	ops := configEdit(t, mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})).Ops
	for _, kv := range ops[len(ops)-1].KVs {
		if kv.Path == "experimental_bearer_token" {
			t.Fatal("token written without a key")
		}
	}
}

func TestCurrent(t *testing.T) {
	cases := []struct {
		name, config string
		want         agent.Status
	}{
		{"absent", "", agent.Status{Mode: agent.Official}},
		{"official", fixture, agent.Status{Mode: agent.Official, Model: "gpt-5.5"}},
		{"explicit openai", "model_provider = \"openai\"\n", agent.Status{Mode: agent.Official}},
		{"base url", "\xef\xbb\xbfopenai_base_url = \"http://127.0.0.1:17321/backend-api/codex\"\n",
			agent.Status{Mode: agent.Official, Detail: "openai_base_url = http://127.0.0.1:17321/backend-api/codex（自定义地址）"}},
		{"qcs", "model_provider = \"qcs-cpa\"\nmodel = \"kimi-k3\"\n\n[model_providers.qcs-cpa]\nname = \"CPA\"\nbase_url = \"http://h/v1\"\n",
			agent.Status{Mode: agent.Direct, Provider: "cpa", Model: "kimi-k3", Detail: "provider CPA（qcs-cpa）"}},
		{"cc switch", strings.Replace(fixture, "service_tier", "model_provider = \"custom\"\nservice_tier", 1),
			agent.Status{Mode: agent.Direct, Model: "gpt-5.5", Detail: "provider custom set by another tool (base_url https://relay.example/v1)"}},
		{"broken", "model_provider = \"qcs-cpa\"\nmodel = \"m\"\n[oops\n", agent.Status{Mode: agent.Direct, Provider: "cpa", Model: "m"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testenv.Home(t)
			if c.config != "" {
				writeFile(t, ConfigPath(), c.config)
			}
			got, err := New().Current()
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func doctor(t *testing.T, config string) []agent.Problem {
	t.Helper()
	testenv.Home(t)
	t.Setenv("OPENAI_BASE_URL", "")
	if config != "" {
		writeFile(t, ConfigPath(), config)
	}
	return New().Doctor()
}

func has(ps []agent.Problem, level, sub string) bool {
	for _, p := range ps {
		if p.Level == level && strings.Contains(p.Message, sub) {
			return true
		}
	}
	return false
}

func TestDoctor(t *testing.T) {
	if ps := doctor(t, "model_provider = \"openai\"\n[features]\nfast_mode = true\n"); len(ps) != 0 {
		t.Errorf("clean config: %+v", ps)
	}
	if ps := doctor(t, ""); len(ps) != 0 {
		t.Errorf("no config: %+v", ps)
	}
	ps := doctor(t, "model_provider = \"qcs-gone\"\n")
	if !has(ps, "error", "no [model_providers.qcs-gone] table") {
		t.Errorf("missing table: %+v", ps)
	}
	for _, b := range []string{"ollama", "lmstudio", "amazon-bedrock", "amazon-bedrock-runtime", "openai"} {
		if ps := doctor(t, fmt.Sprintf("model_provider = %q\n", b)); len(ps) != 0 {
			t.Errorf("builtin %s: %+v", b, ps)
		}
	}
	if ps := doctor(t, "model = \"x\"\n[broken\n"); !has(ps, "warn", "not valid TOML") {
		t.Errorf("broken: %+v", ps)
	}
	if ps := doctor(t, "model_catalog_json = \"/nowhere/catalog.json\"\n"); !has(ps, "error", "/nowhere/catalog.json") {
		t.Errorf("catalog: %+v", ps)
	}
	ok := doctor(t, "")
	writeFile(t, CatalogPath("cpa"), `{"models":[]}`)
	writeFile(t, ConfigPath(), fmt.Sprintf("model_catalog_json = %q\n", CatalogPath("cpa")))
	if ps := New().Doctor(); len(ps) != 0 || len(ok) != 0 {
		t.Errorf("present catalog: %+v", ps)
	}
	if ps := doctor(t, "[features]\nfast_mode = false\n"); !has(ps, "warn", "fast_mode = false") {
		t.Errorf("fast_mode: %+v", ps)
	}
	ps = doctor(t, "profile = \"work\"\n[profiles.work]\nmodel_provider = \"custom\"\n[profiles.home]\nmodel_provider = \"openai\"\n[profiles.plain]\nmodel = \"x\"\n[model_providers.custom]\nname = \"c\"\n")
	if !has(ps, "warn", `profile "work" is active`) || !has(ps, "warn", `profile "home" sets model_provider`) || has(ps, "warn", `"plain"`) {
		t.Errorf("profiles: %+v", ps)
	}
	if !has(ps, "info", "CC Switch provider tables found (custom)") {
		t.Errorf("cc switch: %+v", ps)
	}
	if ps := doctor(t, "[model_providers.cc-switch-2]\nname = \"c\"\n[model_providers.cc-switch-official]\nname = \"o\"\n"); !has(ps, "info", "(cc-switch-2)") {
		t.Errorf("cc switch n: %+v", ps)
	}
	testenv.Home(t)
	t.Setenv("OPENAI_BASE_URL", "http://env.example/v1")
	if ps := New().Doctor(); !has(ps, "info", "OPENAI_BASE_URL") {
		t.Errorf("env: %+v", ps)
	}
}

const secret = "eyJsecret-token-value"

func TestDetect(t *testing.T) {
	home := testenv.Home(t)
	t.Setenv("PATH", t.TempDir())
	if d := New().Detect(); d.Installed || d.SignedIn || d.SignIn != "" || d.Dir != filepath.Join(home, ".codex") {
		t.Fatalf("nothing there: %+v", d)
	}
	cases := []struct {
		name, auth, config string
		signed             bool
		kind               string
	}{
		{"chatgpt", `{"OPENAI_API_KEY":null,"tokens":{"id_token":"x","access_token":"` + secret + `","refresh_token":"` + secret + `"}}`, "", true, "chatgpt"},
		{"chatgpt bom", "\xef\xbb\xbf" + `{"tokens":{"access_token":"` + secret + `"}}`, "", true, "chatgpt"},
		{"apikey", `{"OPENAI_API_KEY":"sk-` + secret + `","tokens":null}`, "", true, "apikey"},
		{"empty token", `{"OPENAI_API_KEY":"","tokens":{"access_token":""}}`, "", false, ""},
		{"not json", `{"tokens":`, "", false, ""},
		{"keyring", `{"tokens":{"access_token":"` + secret + `"}}`, "cli_auth_credentials_store = \"keyring\"\n", false, "keyring"},
		{"auto without file", "", "cli_auth_credentials_store = \"auto\"\n", false, "keyring"},
		{"auto with file", `{"OPENAI_API_KEY":"k"}`, "cli_auth_credentials_store = \"auto\"\n", true, "apikey"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testenv.Home(t)
			os.MkdirAll(appdir.CodexHome(), 0o700)
			if c.auth != "" {
				writeFile(t, AuthPath(), c.auth)
			}
			if c.config != "" {
				writeFile(t, ConfigPath(), c.config)
			}
			d := New().Detect()
			if !d.Installed || d.SignedIn != c.signed || d.SignIn != c.kind {
				t.Fatalf("got installed=%v signedIn=%v signIn=%q", d.Installed, d.SignedIn, d.SignIn)
			}
			if strings.Contains(fmt.Sprintf("%#v", d), "secret") {
				t.Fatal("detection carries a token")
			}
		})
	}
	// CODEX_HOME is followed, and the catalogs are listed among the files
	testenv.Home(t)
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	writeFile(t, CatalogPath("cpa"), "{}")
	d := New().Detect()
	if !d.Installed || d.Dir != dir || !reflect.DeepEqual(d.Files, []string{filepath.Join(dir, "config.toml"), CatalogPath("cpa")}) {
		t.Fatalf("CODEX_HOME: %+v", d)
	}
}
