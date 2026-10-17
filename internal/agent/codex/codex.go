// Package codex is the adapter for OpenAI's Codex (the CLI and the Codex
// app), which keeps its settings in <CODEX_HOME>/config.toml.
//
// Modes:
//   - Official: Codex's own built-in "openai" provider and its ChatGPT or
//     API-key sign-in in auth.json. Nothing of ours in config.toml.
//   - Direct: `model_provider = "qcs-<id>"`, `model`, a
//     [model_providers.qcs-<id>] table speaking Responses with the
//     provider's key as experimental_bearer_token, and, when Codex's bundled
//     catalog wouldn't list the models with the wanted Fast tier, a
//     generated `model_catalog_json`.
//   - Relay: only `openai_base_url`, sign-in kept.
//
// What Codex does that this follows (openai/codex codex-rs):
//   - core/src/config/mod.rs: the provider named by model_provider is looked
//     up in the built-in providers merged with [model_providers.*]; a name
//     found in neither fails the whole config ("Model provider `X` not
//     found"). The built-ins (model-provider-info built_in_model_providers)
//     are openai, amazon-bedrock, amazon-bedrock-runtime, ollama, lmstudio;
//     a configured table never overrides openai.
//   - Threads keep the provider they were started on and the app lists only
//     the current provider's, so the qcs table is marked Keep: Restore
//     leaves it so those threads can be reopened.
//   - model_catalog_json is read when the config loads (load_catalog_json):
//     a missing or empty file fails the load, and it replaces the bundled
//     catalog entirely, so every model of the provider goes in it.
//   - /fast needs features.fast_mode (on by default) and a "priority"
//     service tier on the model's catalog entry; service_tier = "fast" (or
//     "priority") is the user's choice of it.
//
// auth.json is never written and its values are never kept or shown: only
// whether a ChatGPT token or an API key is there is looked at.
package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/desktopapp"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func init() { agent.Register(New()) }

// ID is the agent's id.
const ID = "codex"

// TablePrefix starts the id of every provider table this tool writes.
const TablePrefix = "qcs-"

// Codex is the adapter.
type Codex struct{}

// New returns the Codex adapter.
func New() *Codex { return &Codex{} }

func (*Codex) ID() string   { return ID }
func (*Codex) Name() string { return "Codex" }

// Modes includes the foreground local relay managed by the CLI.
func (*Codex) Modes() []agent.Mode { return []agent.Mode{agent.Official, agent.Direct, agent.Relay} }

// ConfigPath is Codex's config.toml.
func ConfigPath() string { return filepath.Join(appdir.CodexHome(), "config.toml") }

// AuthPath is Codex's auth.json, which this tool never writes.
func AuthPath() string { return filepath.Join(appdir.CodexHome(), "auth.json") }

// CatalogPath is the absolute path of the model catalog generated for a
// provider; Codex wants model_catalog_json absolute.
func CatalogPath(providerID string) string {
	p := appdir.Path("codex", "catalog-"+providerID+".json")
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// TableID is the model provider id written for a provider.
func TableID(providerID string) string { return TablePrefix + providerID }

// builtin are the providers Codex has without a table
// (codex-rs/model-provider-info/src/lib.rs built_in_model_providers).
var builtin = map[string]bool{
	"openai": true, "amazon-bedrock": true, "amazon-bedrock-runtime": true,
	"ollama": true, "lmstudio": true,
}

// ccSwitch matches the provider tables CC Switch writes into Codex's config:
// "custom", and "cc-switch", "cc-switch-2"… from its older versions (as in
// yetone/magpie internal/agent/codex.go, MIT).
var ccSwitch = regexp.MustCompile(`^(custom|cc-switch(-[0-9]+)?)$`)

// Detect looks for Codex and its sign-in.
func (c *Codex) Detect() agent.Detection {
	home := appdir.CodexHome()
	d := agent.Detection{Dir: home, Files: []string{ConfigPath()}}
	if fi, err := os.Stat(home); err == nil && fi.IsDir() {
		d.Installed = true
	} else if _, err := exec.LookPath("codex"); err == nil {
		d.Installed = true
	} else if desktopInstalled(ID) {
		d.Installed = true
	}
	if cats, _ := filepath.Glob(CatalogPath("*")); len(cats) > 0 {
		sort.Strings(cats)
		d.Files = append(d.Files, cats...)
	}
	d.SignedIn, d.SignIn = signIn()
	return d
}

var desktopInstalled = desktopapp.Installed

// signIn says what auth.json holds, by shape only. With
// cli_auth_credentials_store = "keyring" Codex keeps the sign-in in the
// system keyring, which isn't looked into: unknown, "keyring". With "auto"
// it uses the keyring when there is one, so no auth.json reads the same.
func signIn() (bool, string) {
	store, _ := edit.GetTOMLTop(ConfigPath(), "cli_auth_credentials_store")
	if store == "keyring" {
		return false, "keyring"
	}
	if kind := authKind(AuthPath()); kind != "" {
		return true, kind
	}
	if store == "auto" {
		return false, "keyring"
	}
	return false, ""
}

// authKind is "chatgpt" when auth.json has a non-empty tokens.access_token,
// else "apikey" when OPENAI_API_KEY is a non-empty string, else "". The
// file is read directly (not through edit.Read, which memoizes contents)
// and nothing of it outlives this call.
func authKind(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	defer clear(b)
	var top map[string]json.RawMessage
	if json.Unmarshal(edit.TrimBOM(b), &top) != nil {
		return ""
	}
	nonEmpty := func(raw json.RawMessage) bool {
		var s string
		return json.Unmarshal(raw, &s) == nil && s != ""
	}
	var tokens map[string]json.RawMessage
	if t, ok := top["tokens"]; ok && json.Unmarshal(t, &tokens) == nil && nonEmpty(tokens["access_token"]) {
		return "chatgpt"
	}
	if nonEmpty(top["OPENAI_API_KEY"]) {
		return "apikey"
	}
	return ""
}

// config is config.toml read: the text (without a BOM) and, when it
// parses, its decoded tree. A missing file is an empty config.
type config struct {
	path   string
	exists bool
	doc    map[string]any
	err    error // parse error
}

func readConfig(path string) (config, error) {
	c := config{path: path}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			c.doc = map[string]any{}
			return c, nil
		}
		return c, err
	}
	c.exists = true
	raw, err := edit.Read(path)
	if err != nil {
		return c, err
	}
	doc := map[string]any{}
	if err := toml.Unmarshal(raw, &doc); err != nil {
		c.err = err
		return c, nil
	}
	c.doc = doc
	return c, nil
}

// str is a top-level string key.
func (c config) str(key string) (string, bool) {
	s, ok := c.doc[key].(string)
	return s, ok
}

// providers is the [model_providers] tree, by id.
func (c config) providers() map[string]map[string]any {
	out := map[string]map[string]any{}
	mp, _ := c.doc["model_providers"].(map[string]any)
	for id, v := range mp {
		if t, ok := v.(map[string]any); ok {
			out[id] = t
		}
	}
	return out
}

func (c config) ccSwitchTables() []string {
	var out []string
	for id := range c.providers() {
		if ccSwitch.MatchString(id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Current reads config.toml to say where Codex points.
func (c *Codex) Current() (agent.Status, error) {
	path := ConfigPath()
	cfg, err := readConfig(path)
	if err != nil {
		return agent.Status{}, err
	}
	// a file TOML refuses is still read line by line for the top keys
	top := func(key string) string {
		if cfg.err == nil {
			s, _ := cfg.str(key)
			return s
		}
		s, _ := edit.GetTOMLTop(path, key)
		return s
	}
	mp, model := top("model_provider"), top("model")
	switch {
	case mp == "" || mp == "openai":
		st := agent.Status{Mode: agent.Official, Model: model}
		if u := top("openai_base_url"); u != "" {
			s := safekeep.State(ID)
			if s["mode"] == string(agent.Relay) && s["relay_url"] == u {
				st.Mode, st.Provider = agent.Relay, s["provider"]
				st.Detail = "本地路由 " + u + "；需保持路由进程运行"
				return st, nil
			}
			st.Detail = fmt.Sprintf("openai_base_url = %s（自定义地址）", u)
		}
		return st, nil
	case strings.HasPrefix(mp, TablePrefix):
		id := strings.TrimPrefix(mp, TablePrefix)
		st := agent.Status{Mode: agent.Direct, Provider: id, Model: model}
		if t, ok := cfg.providers()[mp]; ok {
			name, _ := t["name"].(string)
			if name == "" {
				name = id
			}
			st.Detail = fmt.Sprintf("provider %s（%s）", name, mp)
		} else if cfg.err == nil {
			st.Detail = fmt.Sprintf("model_provider %s 没有对应的 [model_providers.%s] 表", mp, mp)
		}
		return st, nil
	case builtin[mp]:
		return agent.Status{Mode: agent.Direct, Model: model, Detail: fmt.Sprintf("Codex 内置 provider %s", mp)}, nil
	}
	detail := fmt.Sprintf("provider %s set by another tool", mp)
	if t, ok := cfg.providers()[mp]; ok {
		if u, _ := t["base_url"].(string); u != "" {
			detail += fmt.Sprintf(" (base_url %s)", u)
		}
	}
	return agent.Status{Mode: agent.Direct, Model: model, Detail: detail}, nil
}
