package codex

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/codexcat"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

// Plan computes the edits for t. It is the whole set of changes: what an
// earlier plan wrote and this one leaves out (model_catalog_json,
// service_tier, model_reasoning_effort) is put back by the executor.
//
// Direct mode never touches auth.json, [features], [desktop], [profiles.*],
// [mcp_servers.*], CC Switch's provider tables or openai_base_url.
// Relay mode changes only openai_base_url; see relayPlan.
func (c *Codex) Plan(t agent.Target) (*plan.Plan, error) {
	switch t.Mode {
	case agent.Direct:
	case agent.Relay:
		return c.relayPlan(t)
	case agent.Official:
		return nil, errors.New("codex: official is a restore, not a plan")
	default:
		return nil, fmt.Errorf("codex: unknown mode %q", t.Mode)
	}
	p, ok, err := provider.Get(t.Provider)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("codex: no provider %q", t.Provider)
	}
	if !p.APIs.Responses {
		return nil, fmt.Errorf("codex: provider %s does not speak the Responses API, the only one Codex uses", p.ID)
	}
	if p.BaseURL == "" {
		return nil, fmt.Errorf("codex: provider %s has no base URL", p.ID)
	}
	if t.Model == "" {
		return nil, errors.New("codex: no model given")
	}
	if strings.ContainsAny(t.Model, " \t\r\n") {
		return nil, fmt.Errorf("codex: invalid model id %q", t.Model)
	}
	chosen, listed := p.Model(t.Model)
	if len(p.Models) > 0 && !listed {
		return nil, fmt.Errorf("codex: provider %s has no model %q", p.ID, t.Model)
	}
	if !listed {
		chosen = provider.Model{ID: t.Model, Fast: provider.DefaultFast(t.Model)}
	}
	if t.Effort != "" {
		if strings.ContainsAny(t.Effort, " \t\r\n") {
			return nil, fmt.Errorf("codex: invalid reasoning effort %q", t.Effort)
		}
		if len(chosen.Efforts) > 0 && !slices.Contains(chosen.Efforts, t.Effort) {
			return nil, fmt.Errorf("codex: model %s offers efforts %s, not %q", chosen.ID, strings.Join(chosen.Efforts, ", "), t.Effort)
		}
	}

	path := ConfigPath()
	cfg, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	if cfg.err != nil {
		return nil, fmt.Errorf("codex: %s is not valid TOML, fix it first: %w", path, cfg.err)
	}
	table := TableID(p.ID)
	if _, there := cfg.providers()[table]; there {
		// spelled some other way than the header SetTable writes ([model_providers."qcs-x"],
		// an inline table, dotted keys): writing ours would define it twice
		names, err := edit.TOMLTables(path)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(names, "model_providers."+table) {
			return nil, fmt.Errorf("codex: %s defines model_providers.%s in a form this tool doesn't edit; write it as a [model_providers.%s] table or remove it", path, table, table)
		}
	}
	if err := checkDirectAuth(table, cfg.providers()[table], p); err != nil {
		return nil, err
	}

	var top []plan.Op
	set := func(k string, v any) { top = append(top, plan.Op{Kind: plan.SetTop, Key: k, Value: v}) }
	set("model_provider", table)
	set("model", chosen.ID)

	var edits []plan.FileEdit
	models := catalogModels(p, chosen.ID, t.Fast)
	// A pre-existing custom catalog replaces Codex's bundled list too. Point
	// to our provider catalog instead of silently keeping incompatible model
	// capabilities; safekeep restores the original pointer, never that file.
	existingCatalog, _ := cfg.str("model_catalog_json")
	// A provider's explicit list is authoritative even when every ID is a
	// bundled GPT. Otherwise Codex shows its entire built-in list, including
	// models this CPA does not serve. Keep the full native entries in our list.
	if len(p.Models) > 0 || codexcat.NeedsCatalog(models) || existingCatalog != "" {
		cp := CatalogPath(p.ID)
		next, err := codexcat.Catalog(models, appdir.CodexHome())
		if err != nil {
			return nil, fmt.Errorf("codex: model catalog: %w", err)
		}
		cur, err := edit.Read(cp)
		if err != nil {
			return nil, err
		}
		edits = append(edits, plan.FileEdit{Path: cp, Format: plan.Raw, Ops: []plan.Op{
			{Kind: plan.WriteFile, Data: codexcat.Keep(cur, next)},
		}})
		set("model_catalog_json", cp)
	}
	if t.Fast != nil {
		if *t.Fast {
			set("service_tier", "fast")
		} else {
			top = append(top, plan.Op{Kind: plan.DelTop, Key: "service_tier"})
		}
	}
	if t.Effort != "" {
		set("model_reasoning_effort", t.Effort)
	}
	if err := checkDirectProfile(cfg, top); err != nil {
		return nil, err
	}
	top = append(top, plan.Op{
		Kind: plan.SetTable, Table: "model_providers." + table, KVs: tableKVs(p), Keep: true,
		RestoreKeys: []string{"experimental_bearer_token", "http_headers"},
	})
	// the catalog first: config.toml names it
	edits = append(edits, plan.FileEdit{Path: path, Format: plan.TOML, Ops: top})

	return &plan.Plan{
		Agent:  ID,
		Edits:  edits,
		Notice: notice(),
		State:  map[string]string{"mode": string(agent.Direct), "provider": p.ID, "model": chosen.ID},
	}, nil
}

// checkDirectProfile follows Magpie's legacy in-file profile check (MIT,
// Copyright (c) 2026 yetone), internal/agent/codex.go: Check at
// 7660399a121af67db4efd56ecb6bd4fb04dc53f1; see third_party/magpie/LICENSE. A profile
// value wins over the top level. Preserve those profiles and reject a switch
// they would override. Newer Codex --profile files are separate config layers;
// this check only covers the profile selected in the config being edited.
func checkDirectProfile(cfg config, top []plan.Op) error {
	raw, exists := cfg.doc["profile"]
	if !exists {
		return nil
	}
	active, ok := raw.(string)
	if !ok {
		return errors.New("codex: profile must be a string")
	}
	if active == "" {
		return nil
	}
	profiles, _ := cfg.doc["profiles"].(map[string]any)
	profile, _ := profiles[active].(map[string]any)
	conflict := func(key string) error {
		return fmt.Errorf("codex: active legacy profile %q overrides %s; remove that override or select the default config before switching", active, key)
	}
	catalogEdited := false
	for _, op := range top {
		if op.Kind != plan.SetTop && op.Kind != plan.DelTop {
			continue
		}
		if op.Key == "model_catalog_json" {
			catalogEdited = true
		}
		value, exists := profile[op.Key]
		if !exists {
			continue
		}
		got, valid := value.(string)
		if valid && op.Kind == plan.SetTop && got == op.Value {
			continue
		}
		if valid && op.Key == "service_tier" {
			// Codex accepts priority as the request spelling of Fast and
			// default as the explicit choice of its ordinary service tier.
			if op.Kind == plan.SetTop && op.Value == "fast" && got == "priority" ||
				op.Kind == plan.DelTop && (got == "" || got == "default") {
				continue
			}
		}
		return conflict(op.Key)
	}
	if !catalogEdited {
		// A profile catalog also replaces the built-in model list when the
		// plan itself needs no generated catalog; its Fast data may differ.
		if value, exists := profile["model_catalog_json"]; exists {
			if catalog, valid := value.(string); !valid || catalog != "" {
				return conflict("model_catalog_json")
			}
		}
	}
	return nil
}

// tableKVs is the [model_providers.qcs-<id>] table. The key goes in
// experimental_bearer_token rather than env_key: the Codex app started from
// the Dock or Start menu has no shell environment.
func tableKVs(p provider.Provider) []edit.KV {
	name := p.Name
	if name == "" {
		name = p.ID
	}
	kvs := []edit.KV{
		{Path: "name", Value: name},
		{Path: "base_url", Value: p.BaseURL},
		{Path: "wire_api", Value: "responses"},
	}
	if p.APIKey != "" {
		kvs = append(kvs, edit.KV{Path: "experimental_bearer_token", Value: p.APIKey})
	}
	if len(p.Headers) > 0 {
		keys := make([]string, 0, len(p.Headers))
		for k := range p.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		in := make(edit.Inline, 0, len(keys))
		for _, k := range keys {
			in = append(in, edit.KV{Path: k, Value: p.Headers[k]})
		}
		kvs = append(kvs, edit.KV{Path: "http_headers", Value: in})
	}
	return kvs
}

// catalogModels is every model of p for the catalog (model_catalog_json
// replaces Codex's own list, so all of them go in), or just the chosen one
// when p lists none. fast, when set, overrides the chosen model's Fast.
func catalogModels(p provider.Provider, chosen string, fast *bool) []codexcat.Model {
	src := p.Models
	if len(src) == 0 {
		src = []provider.Model{{ID: chosen, Fast: provider.DefaultFast(chosen)}}
	}
	out := make([]codexcat.Model, 0, len(src))
	for _, m := range src {
		f := m.Fast
		if m.ID == chosen && fast != nil {
			f = *fast
		}
		out = append(out, codexcat.Model{Slug: m.ID, Name: m.Name, Efforts: m.Efforts, Fast: f, Context: m.Context, Images: m.Images})
	}
	return out
}

func notice() string {
	return "直连使用 provider 的认证；CPA 可在上游使用官方订阅，本机无需 ChatGPT 登录即可使用模型目录和 Fast。" +
		"重启 Codex 桌面端及仍在运行的共享 app-server，再打开新的 Codex CLI 会话。" +
		"桌面会话按 provider 区分，远程控制、云任务等本机 ChatGPT 登录功能另受客户端限制。" +
		"auth.json 没有被修改；切回官方恢复原配置。"
}
