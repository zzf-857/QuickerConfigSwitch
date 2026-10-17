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

// ErrRelayUnavailable is Plan's answer for Relay until the relay exists.
var ErrRelayUnavailable = errors.New("codex: relay mode is not available yet")

// Plan computes the edits for t. It is the whole set of changes: what an
// earlier plan wrote and this one leaves out (model_catalog_json,
// service_tier, model_reasoning_effort) is put back by the executor.
//
// Never touched: auth.json, [features], [desktop], [profiles.*],
// [mcp_servers.*], CC Switch's provider tables, openai_base_url.
func (c *Codex) Plan(t agent.Target) (*plan.Plan, error) {
	switch t.Mode {
	case agent.Direct:
	case agent.Relay:
		return nil, ErrRelayUnavailable
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
		chosen = provider.Model{ID: t.Model}
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

	var top []plan.Op
	set := func(k string, v any) { top = append(top, plan.Op{Kind: plan.SetTop, Key: k, Value: v}) }
	set("model_provider", table)
	set("model", chosen.ID)

	var edits []plan.FileEdit
	models := catalogModels(p, chosen.ID, t.Fast)
	if codexcat.NeedsCatalog(models) {
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
	top = append(top, plan.Op{Kind: plan.SetTable, Table: "model_providers." + table, KVs: tableKVs(p), Keep: true})
	// the catalog first: config.toml names it
	edits = append(edits, plan.FileEdit{Path: path, Format: plan.TOML, Ops: top})

	return &plan.Plan{
		Agent:  ID,
		Edits:  edits,
		Notice: notice(),
		State:  map[string]string{"mode": string(agent.Direct), "provider": p.ID, "model": chosen.ID},
	}, nil
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
		src = []provider.Model{{ID: chosen}}
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
	return "Restart the Codex app, or start a new Codex CLI session, for the change to take effect. " +
		"In Direct mode the Codex app lists only the threads started on this provider and runs without its ChatGPT features " +
		"(remote control, cloud tasks, …); switching back to official restores them. auth.json is left untouched."
}
