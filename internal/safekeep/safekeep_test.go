package safekeep

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

const codexConfig = `# Codex config, kept by hand
model = "gpt-5.5" # my favourite
model_reasoning_effort = "high"
service_tier = "fast"   # keep my Fast
approval_policy = "on-request"
notify = [
  "notify-send",
  "codex",
]

[features]
fast_mode = true
web_search_request = true

# Added by CC Switch
[model_providers.custom]
name = "custom"
base_url = "https://relay.example.com/v1"
wire_api = "responses"
experimental_bearer_token = "sk-ccswitch"

[profiles.work]
model = "o3"
model_provider = "openai"

[projects."/home/me/src/app"]
trust_level = "trusted"
`

const claudeSettings = `{
  // my Claude Code settings
  "model": "sonnet",
  "permissions": {
    "allow": ["Bash(git status)"], // trailing comma below
  },
  "env": {
    "DISABLE_TELEMETRY": "1"
  }
}
`

const claudeNoEnv = `{
  /* no env yet */
  "model": "sonnet",
  "theme": "dark"
}
`

func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
func bom(s string) string  { return "\xef\xbb\xbf" + s }

// variants is codexConfig as different tools save it.
var variants = map[string]string{
	"lf":       codexConfig,
	"crlf":     crlf(codexConfig),
	"bom+crlf": bom(crlf(codexConfig)),
	"bom":      bom(codexConfig),
}

type env struct {
	home, cfg, catalog, claude string
}

func setup(t *testing.T) env {
	t.Helper()
	h := testenv.Home(t)
	return env{
		home:    h,
		cfg:     filepath.Join(h, ".codex", "config.toml"),
		catalog: filepath.Join(appdir.Dir(), "codex", "catalog-cpa.json"),
		claude:  filepath.Join(h, ".claude", "settings.json"),
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
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

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("%s exists", path)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func providerTable(keep bool) plan.Op {
	return plan.Op{Kind: plan.SetTable, Table: "model_providers.qcs-cpa", Keep: keep, KVs: []edit.KV{
		{Path: "name", Value: "CPA"},
		{Path: "base_url", Value: "http://127.0.0.1:8317/v1"},
		{Path: "wire_api", Value: "responses"},
		{Path: "experimental_bearer_token", Value: "sk-cpa"},
	}}
}

// planA is mode D with a generated model catalog.
func planA(e env, keep bool) *plan.Plan {
	return &plan.Plan{Agent: "codex", State: map[string]string{"mode": "direct", "provider": "cpa"}, Edits: []plan.FileEdit{
		{Path: e.cfg, Format: plan.TOML, Ops: []plan.Op{
			{Kind: plan.SetTop, Key: "model_provider", Value: "qcs-cpa"},
			{Kind: plan.SetTop, Key: "model", Value: "cpa/gpt-5.5"},
			{Kind: plan.SetTop, Key: "model_catalog_json", Value: e.catalog},
			providerTable(keep),
		}},
		{Path: e.catalog, Format: plan.Raw, Ops: []plan.Op{{Kind: plan.WriteFile, Data: []byte(`{"models":[]}` + "\n")}}},
	}}
}

// planB is mode D on a model of Codex's own catalog: no catalog file.
func planB(e env, keep bool) *plan.Plan {
	return &plan.Plan{Agent: "codex", State: map[string]string{"mode": "direct", "provider": "other"}, Edits: []plan.FileEdit{
		{Path: e.cfg, Format: plan.TOML, Ops: []plan.Op{
			{Kind: plan.SetTop, Key: "model_provider", Value: "qcs-cpa"},
			{Kind: plan.SetTop, Key: "model", Value: "gpt-5.5"},
			providerTable(keep),
		}},
	}}
}

func claudePlan(e env) *plan.Plan {
	return &plan.Plan{Agent: "claude", Edits: []plan.FileEdit{{Path: e.claude, Format: plan.JSON, Ops: []plan.Op{
		{Kind: plan.SetJSON, Key: "model", Value: "opus"},
		{Kind: plan.SetJSON, Key: "env.ANTHROPIC_BASE_URL", Value: "http://127.0.0.1:8317"},
		{Kind: plan.SetJSON, Key: "env.ANTHROPIC_AUTH_TOKEN", Value: "sk-cpa"},
	}}}}
}

// Apply then Restore leaves every file byte for byte as it was, one that
// wasn't there gone again.
func TestApplyRestoreExact(t *testing.T) {
	type tc struct {
		name, orig string // orig "" with none: the file is not there
		none       bool
		path       func(env) string
		plan       func(env) *plan.Plan
	}
	var cases []tc
	for name, text := range variants {
		cases = append(cases, tc{name: "codex " + name, orig: text, path: func(e env) string { return e.cfg }, plan: func(e env) *plan.Plan { return planA(e, false) }})
	}
	cases = append(cases,
		tc{name: "codex absent", none: true, path: func(e env) string { return e.cfg }, plan: func(e env) *plan.Plan { return planA(e, false) }},
		tc{name: "claude jsonc", orig: claudeSettings, path: func(e env) string { return e.claude }, plan: claudePlan},
		tc{name: "claude crlf bom", orig: bom(crlf(claudeSettings)), path: func(e env) string { return e.claude }, plan: claudePlan},
		tc{name: "claude no env", orig: claudeNoEnv, path: func(e env) string { return e.claude }, plan: claudePlan},
		tc{name: "claude absent", none: true, path: func(e env) string { return e.claude }, plan: claudePlan},
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t)
			p := c.plan(e)
			path := c.path(e)
			if !c.none {
				write(t, path, c.orig)
			}
			must(t, Apply(p))
			if !Touched(p.Agent) {
				t.Fatal("not touched after Apply")
			}
			if got := read(t, path); got == c.orig {
				t.Fatal("Apply changed nothing")
			}
			// applying the same plan again changes nothing and keeps it exact
			must(t, Apply(p))
			rep, err := Restore(p.Agent)
			must(t, err)
			if c.none {
				absent(t, path)
			} else if got := read(t, path); got != c.orig {
				t.Fatalf("not the original:\n%q\nwant\n%q", got, c.orig)
			}
			absent(t, e.catalog)
			for _, f := range rep.Files {
				if !f.Exact || len(f.Left) > 0 {
					t.Errorf("%s: %+v", f.Path, f)
				}
			}
			if Touched(p.Agent) || State(p.Agent) != nil {
				t.Error("still recorded after Restore")
			}
		})
	}
}

// The user edits a key of their own after Apply: Restore takes ours back
// key by key and keeps their edit; one of ours they changed is left.
func TestRestoreKeyLevel(t *testing.T) {
	type tc struct {
		name, orig string
		path       func(env) string
		plan       func(env) *plan.Plan
		// user edits path; want is the expected file after Restore given
		// the original; left the slots Restore must leave
		user func(t *testing.T, path string)
		want func(orig string) string
		left []string
	}
	var cases []tc
	for name, text := range variants {
		cases = append(cases,
			tc{
				name: "codex unrelated " + name, orig: text,
				path: func(e env) string { return e.cfg }, plan: func(e env) *plan.Plan { return planA(e, false) },
				user: func(t *testing.T, p string) {
					must(t, edit.SetTOMLKey(p, "features", "web_search_request", false))
				},
				want: func(o string) string {
					return strings.Replace(o, "web_search_request = true", "web_search_request = false", 1)
				},
			},
			tc{
				name: "codex ours " + name, orig: text,
				path: func(e env) string { return e.cfg }, plan: func(e env) *plan.Plan { return planA(e, false) },
				user: func(t *testing.T, p string) {
					must(t, edit.SetTOMLTopPreserving(p, edit.KV{Path: "model", Value: "my-own"}))
				},
				want: func(o string) string {
					return strings.Replace(o, `model = "gpt-5.5" # my favourite`, `model = "my-own" # my favourite`, 1)
				},
				left: []string{"toml-top:model"},
			},
		)
	}
	cases = append(cases,
		tc{
			name: "claude unrelated", orig: claudeSettings,
			path: func(e env) string { return e.claude }, plan: claudePlan,
			user: func(t *testing.T, p string) { must(t, edit.SetJSON(p, edit.KV{Path: "theme", Value: "light"})) },
			want: func(o string) string { return strings.Replace(o, "{\n", "{\n  \"theme\": \"light\",\n", 1) },
		},
		tc{
			name: "claude no env", orig: claudeNoEnv,
			path: func(e env) string { return e.claude }, plan: claudePlan,
			user: func(t *testing.T, p string) { must(t, edit.SetJSON(p, edit.KV{Path: "theme", Value: "light"})) },
			want: func(o string) string { return strings.Replace(o, `"dark"`, `"light"`, 1) },
		},
		tc{
			name: "claude ours", orig: claudeSettings,
			path: func(e env) string { return e.claude }, plan: claudePlan,
			user: func(t *testing.T, p string) {
				must(t, edit.SetJSON(p, edit.KV{Path: "env.ANTHROPIC_BASE_URL", Value: "https://mine"}))
			},
			want: nil,
			left: []string{"json:env.ANTHROPIC_BASE_URL"},
		},
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t)
			p := c.plan(e)
			path := c.path(e)
			write(t, path, c.orig)
			must(t, Apply(p))
			c.user(t, path)
			rep, err := Restore(p.Agent)
			must(t, err)
			got := read(t, path)
			if c.want != nil {
				if want := c.want(c.orig); got != want {
					t.Fatalf("got\n%q\nwant\n%q", got, want)
				}
			}
			var left []string
			for _, f := range rep.Files {
				if f.Exact && f.Path == path {
					t.Errorf("%s put back whole though the user changed it", f.Path)
				}
				left = append(left, f.Left...)
			}
			if !slices.Equal(left, c.left) {
				t.Errorf("left %v, want %v", left, c.left)
			}
			if c.name == "claude ours" {
				for _, want := range []string{`"https://mine"`, `"model": "sonnet"`, `"DISABLE_TELEMETRY": "1"`} {
					if !strings.Contains(got, want) {
						t.Errorf("no %s in\n%s", want, got)
					}
				}
				if strings.Contains(got, "ANTHROPIC_AUTH_TOKEN") {
					t.Errorf("token left:\n%s", got)
				}
			}
			if strings.Contains(c.orig, "\r\n") && strings.Count(got, "\n") != strings.Count(got, "\r\n") {
				t.Errorf("mixed line ends: %q", got)
			}
			absent(t, e.catalog)
			if Touched(p.Agent) {
				t.Error("still touched")
			}
		})
	}
}

// Switching from plan A to plan B puts back what B no longer sets:
// model_catalog_json and the catalog file, whether or not the user edited
// the file in between.
func TestSwitchRevertsDropped(t *testing.T) {
	withOld := strings.Replace(codexConfig, "approval_policy", "model_catalog_json = \"/old/catalog.json\"\napproval_policy", 1)
	for _, c := range []struct {
		name, orig string
		dirty      bool
		want       string // model_catalog_json after B, "" for none
	}{
		{"clean", codexConfig, false, ""},
		{"dirty", codexConfig, true, ""},
		{"clean crlf", crlf(codexConfig), false, ""},
		{"had one", withOld, false, `"/old/catalog.json"`},
		{"had one dirty", withOld, true, `"/old/catalog.json"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t)
			write(t, e.cfg, c.orig)
			must(t, Apply(planA(e, true)))
			if got, _ := edit.GetTOMLTopRaw(e.cfg, "model_catalog_json"); !strings.Contains(got, "catalog-cpa.json") {
				t.Fatalf("model_catalog_json %q", got)
			}
			if c.dirty {
				must(t, edit.SetTOMLKey(e.cfg, "features", "fast_mode", false))
			}
			must(t, Apply(planB(e, true)))
			got, ok := edit.GetTOMLTopRaw(e.cfg, "model_catalog_json")
			if c.want == "" && ok || c.want != "" && got != c.want {
				t.Errorf("model_catalog_json %q %v, want %q", got, ok, c.want)
			}
			absent(t, e.catalog)
			if m, _ := edit.GetTOMLTopRaw(e.cfg, "model"); m != `"gpt-5.5"` {
				t.Errorf("model %s", m)
			}
			if st := State("codex"); st["provider"] != "other" {
				t.Errorf("state %v", st)
			}
			if c.dirty {
				if v, _ := edit.GetTOMLKeyRaw(e.cfg, "features", "fast_mode"); v != "false" {
					t.Errorf("user's edit lost: fast_mode = %s", v)
				}
			}
			if !c.dirty {
				// still exact after the switch
				rep, err := Restore("codex")
				must(t, err)
				want := withKeep(t, c.orig)
				if got := read(t, e.cfg); got != want {
					t.Errorf("after Restore\n%q\nwant\n%q", got, want)
				}
				if !rep.Files[0].Exact || !slices.Equal(rep.Files[0].Kept, []string{"toml-table:model_providers.qcs-cpa"}) {
					t.Errorf("%+v", rep.Files[0])
				}
			}
		})
	}
}

// withKeep is orig with the Keep provider table added, as an exact
// Restore leaves it.
func withKeep(t *testing.T, orig string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "keep.toml")
	write(t, p, orig)
	must(t, edit.PutTOMLTableBlock(p, "model_providers.qcs-cpa", strings.Join([]string{
		"[model_providers.qcs-cpa]",
		`name = "CPA"`,
		`base_url = "http://127.0.0.1:8317/v1"`,
		`wire_api = "responses"`,
		`experimental_bearer_token = "sk-cpa"`,
	}, "\n")))
	return read(t, p)
}

// A Keep table stays through Restore, put back whole or key by key, and
// through an Apply that no longer sets it.
func TestKeepSurvives(t *testing.T) {
	for _, c := range []struct {
		name     string
		orig     string
		dirty    bool
		official bool // switch back to official (an empty plan) before Restore
	}{
		{"exact", codexConfig, false, false},
		{"exact crlf", crlf(codexConfig), false, false},
		{"key level", codexConfig, true, false},
		{"official then restore", codexConfig, false, true},
		{"official dirty", bom(crlf(codexConfig)), true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t)
			write(t, e.cfg, c.orig)
			must(t, Apply(planA(e, true)))
			if c.dirty {
				must(t, edit.SetTOMLTopPreserving(e.cfg, edit.KV{Path: "approval_policy", Value: "never"}))
			}
			if c.official {
				must(t, Apply(&plan.Plan{Agent: "codex", State: map[string]string{"mode": "official"}}))
				if _, ok := edit.GetTOMLTopRaw(e.cfg, "model_provider"); ok {
					t.Fatal("model_provider still set in official mode")
				}
				if _, ok := edit.GetTOMLTableBlock(e.cfg, "model_providers.qcs-cpa"); !ok {
					t.Fatal("Keep table gone in official mode")
				}
				if State("codex")["mode"] != "official" || !Touched("codex") {
					t.Fatal("official not recorded")
				}
			}
			rep, err := Restore("codex")
			must(t, err)
			block, ok := edit.GetTOMLTableBlock(e.cfg, "model_providers.qcs-cpa")
			if !ok || !strings.Contains(block, `experimental_bearer_token = "sk-cpa"`) {
				t.Fatalf("Keep table gone: %q", block)
			}
			want := withKeep(t, c.orig)
			if c.dirty {
				want = strings.Replace(want, `approval_policy = "on-request"`, `approval_policy = "never"`, 1)
			}
			if got := read(t, e.cfg); got != want {
				t.Errorf("got\n%q\nwant\n%q", got, want)
			}
			if !c.official && c.dirty == rep.Files[0].Exact {
				t.Errorf("exact %v with dirty %v", rep.Files[0].Exact, c.dirty)
			}
			if Touched("codex") {
				t.Error("still touched")
			}
			// a second round after the Restore still restores
			must(t, Apply(planA(e, true)))
			_, err = Restore("codex")
			must(t, err)
			if got := read(t, e.cfg); got != want {
				t.Errorf("second round\n%q\nwant\n%q", got, want)
			}
		})
	}
}

// A plan that would leave a file invalid is refused, every file as it was
// and nothing recorded.
func TestInvalidRejected(t *testing.T) {
	for _, c := range []struct {
		name string
		ops  []plan.Op
	}{
		{"raw value", []plan.Op{{Kind: plan.SetTop, Key: "model", Value: edit.Raw(`"unclosed`)}}},
		{"after a good op", []plan.Op{
			{Kind: plan.SetTop, Key: "model_provider", Value: "qcs-cpa"},
			{Kind: plan.SetTableKey, Table: "features", Key: "x", Value: edit.Raw("[1, ")},
		}},
		{"duplicate key", []plan.Op{{Kind: plan.SetTableKey, Table: "features", Key: "fast_mode.x", Value: true}}},
		{"json op on toml", []plan.Op{{Kind: plan.SetJSON, Key: "a", Value: 1}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t)
			write(t, e.cfg, crlf(codexConfig))
			write(t, e.claude, claudeSettings)
			p := &plan.Plan{Agent: "codex", Edits: []plan.FileEdit{
				{Path: e.claude, Format: plan.JSON, Ops: []plan.Op{{Kind: plan.SetJSON, Key: "model", Value: "opus"}}},
				{Path: e.catalog, Format: plan.Raw, Ops: []plan.Op{{Kind: plan.WriteFile, Data: []byte("{}")}}},
				{Path: e.cfg, Format: plan.TOML, Ops: c.ops},
			}}
			if err := Apply(p); err == nil {
				t.Fatal("no error")
			}
			if read(t, e.cfg) != crlf(codexConfig) || read(t, e.claude) != claudeSettings {
				t.Error("a file changed")
			}
			absent(t, e.catalog)
			if Touched("codex") {
				t.Error("recorded")
			}
			if b, err := os.ReadFile(stashPath()); err == nil && strings.Contains(string(b), "codex") {
				t.Errorf("stash kept originals of a refused plan:\n%s", b)
			}
		})
	}
}

// Preview shows the change and writes nothing anywhere.
func TestPreview(t *testing.T) {
	e := setup(t)
	write(t, e.cfg, crlf(codexConfig))
	d, err := Preview(planA(e, true))
	must(t, err)
	for _, want := range []string{
		"--- a/" + strings.TrimLeft(filepath.ToSlash(e.cfg), "/"),
		"+++ b/" + strings.TrimLeft(filepath.ToSlash(e.cfg), "/"),
		"-model = \"gpt-5.5\" # my favourite\n+model = \"cpa/gpt-5.5\" # my favourite\n",
		"+model_provider = \"qcs-cpa\"\n",
		"+[model_providers.qcs-cpa]\n",
		"--- /dev/null\n+++ b/" + strings.TrimLeft(filepath.ToSlash(e.catalog), "/") + "\n@@ -0,0 +1,1 @@\n+{\"models\":[]}\n",
		"@@ -1,",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("no %q in\n%s", want, d)
		}
	}
	if read(t, e.cfg) != crlf(codexConfig) {
		t.Error("config changed")
	}
	absent(t, e.catalog)
	absent(t, appdir.StateDir())
	absent(t, appdir.BackupDir())

	// after an Apply, Preview of the same plan is empty, RestorePreview
	// the way back, and neither writes
	must(t, Apply(planA(e, true)))
	stash, applied := read(t, stashPath()), read(t, appliedPath())
	baks := backups(t, "codex", e.cfg)
	if d, err := Preview(planA(e, true)); err != nil || d != "" {
		t.Errorf("same plan again: %q %v", d, err)
	}
	after := read(t, e.cfg)
	d, err = RestorePreview("codex")
	must(t, err)
	for _, want := range []string{"-model_provider = \"qcs-cpa\"\n", "+model = \"gpt-5.5\" # my favourite\n", "+++ /dev/null"} {
		if !strings.Contains(d, want) {
			t.Errorf("no %q in\n%s", want, d)
		}
	}
	if strings.Contains(d, "-[model_providers.qcs-cpa]") {
		t.Errorf("restore preview drops the Keep table:\n%s", d)
	}
	if read(t, e.cfg) != after || read(t, stashPath()) != stash || read(t, appliedPath()) != applied || backups(t, "codex", e.cfg) != baks {
		t.Error("a preview wrote something")
	}
	if !Touched("codex") {
		t.Error("RestorePreview forgot the agent")
	}
}

func backups(t *testing.T, agent, path string) int {
	es, _ := os.ReadDir(backupDir(agent, path))
	n := 0
	for _, e := range es {
		if strings.HasSuffix(e.Name(), ".bak") {
			n++
		}
	}
	return n
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name string
		do   func(t *testing.T, e env)
		want []Drift
	}{
		{"nothing", func(*testing.T, env) {}, nil},
		{"unrelated edit", func(t *testing.T, e env) {
			must(t, edit.SetTOMLKey(e.cfg, "features", "fast_mode", false))
		}, nil},
		{"changed", func(t *testing.T, e env) {
			must(t, edit.SetTOMLTopPreserving(e.cfg, edit.KV{Path: "model", Value: "gpt-6"}))
		}, []Drift{{Kind: Changed, Slot: "toml-top:model", Want: `"cpa/gpt-5.5"`, Got: `"gpt-6"`}}},
		{"removed", func(t *testing.T, e env) {
			must(t, edit.DelTOMLTop(e.cfg, "model_provider"))
		}, []Drift{{Kind: Removed, Slot: "toml-top:model_provider", Want: `"qcs-cpa"`}}},
		{"replaced", func(t *testing.T, e env) {
			write(t, e.cfg, "model_provider = \"custom\"\nmodel = \"gpt-5\"\n\n[model_providers.custom]\nname = \"custom\"\n")
		}, []Drift{{Kind: Replaced}}},
		{"missing", func(t *testing.T, e env) { must(t, os.Remove(e.catalog)) }, []Drift{{Kind: Missing}}},
		{"catalog changed", func(t *testing.T, e env) { write(t, e.catalog, "{}") }, []Drift{{Kind: Changed, Slot: "file"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t)
			write(t, e.cfg, codexConfig)
			must(t, Apply(planA(e, true)))
			c.do(t, e)
			got := Check("codex")
			if len(got) != len(c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
			for i, w := range c.want {
				g := got[i]
				if g.Kind != w.Kind || g.Slot != w.Slot || (w.Want != "" && g.Want != w.Want) || (w.Got != "" && g.Got != w.Got) {
					t.Errorf("got %+v, want %+v", g, w)
				}
			}
		})
	}
}

func TestRollingBackups(t *testing.T) {
	e := setup(t)
	write(t, e.cfg, codexConfig)
	for i := range 25 {
		p := planA(e, true)
		p.Edits[0].Ops[1].Value = "model-" + string(rune('a'+i))
		must(t, Apply(p))
	}
	if n := backups(t, "codex", e.cfg); n != KeepBackups {
		t.Errorf("%d backups, want %d", n, KeepBackups)
	}
	es, _ := os.ReadDir(backupDir("codex", e.cfg))
	originals := 0
	for _, f := range es {
		if strings.HasPrefix(f.Name(), "original-") {
			originals++
			if read(t, filepath.Join(backupDir("codex", e.cfg), f.Name())) != codexConfig {
				t.Error("snapshot is not the original")
			}
		}
	}
	if originals != 1 {
		t.Errorf("%d original snapshots", originals)
	}
	// the catalog, written the same each time, has one backup of it
	if es, _ := os.ReadDir(backupDir("codex", e.catalog)); len(es) != 2 || !strings.HasSuffix(es[1].Name(), ".absent") {
		t.Errorf("catalog snapshot and backups: %v", es)
	}
	_, err := Restore("codex")
	must(t, err)
	if read(t, e.cfg) != withKeep(t, codexConfig) {
		t.Error("not restored after many applies")
	}
}

// A merging SetTable keeps keys the user added to our table and drops one
// a later plan no longer sets.
func TestSetTableMerge(t *testing.T) {
	e := setup(t)
	write(t, e.cfg, codexConfig)
	must(t, Apply(planA(e, true)))
	must(t, edit.SetTOMLKey(e.cfg, "model_providers.qcs-cpa", "request_max_retries", 4))
	p := planB(e, true)
	p.Edits[0].Ops[2].KVs = p.Edits[0].Ops[2].KVs[:3] // no token
	must(t, Apply(p))
	block, _ := edit.GetTOMLTableBlock(e.cfg, "model_providers.qcs-cpa")
	if !strings.Contains(block, "request_max_retries = 4") || strings.Contains(block, "experimental_bearer_token") {
		t.Errorf("table:\n%s", block)
	}
}

// The stash keeps keys and is only for this user.
func TestStashMode(t *testing.T) {
	e := setup(t)
	write(t, e.claude, claudeSettings)
	must(t, Apply(claudePlan(e)))
	st, err := os.Stat(stashPath())
	must(t, err)
	if st.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Errorf("stash mode %v", st.Mode().Perm())
	}
	// a stash that doesn't parse stops a change rather than being lost
	write(t, stashPath(), "{broken")
	if err := Apply(claudePlan(e)); err == nil {
		t.Error("applied over an unreadable stash")
	}
	if read(t, stashPath()) != "{broken" {
		t.Error("unreadable stash written over")
	}
}

func TestUnifiedDiff(t *testing.T) {
	a := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"
	b := "1\n2\nthree\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n"
	want := "--- a/x\n+++ b/x\n" +
		"@@ -1,6 +1,6 @@\n 1\n 2\n-3\n+three\n 4\n 5\n 6\n" +
		"@@ -10,3 +10,4 @@\n 10\n 11\n 12\n+13\n"
	if got := unifiedDiff("/x", []byte(a), []byte(b), true, true); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := unifiedDiff("/x", []byte(a), []byte(a), true, true); got != "" {
		t.Errorf("no change: %q", got)
	}
	if got := unifiedDiff("/x", []byte(a), nil, true, false); !strings.Contains(got, "+++ /dev/null\n@@ -1,12 +0,0 @@\n-1\n") {
		t.Errorf("removed: %s", got)
	}
}
