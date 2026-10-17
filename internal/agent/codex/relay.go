package codex

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

// relayPlan changes only the built-in provider's address. The listener
// owner applies it after showing its preview and restores it on exit.
func (c *Codex) relayPlan(t agent.Target) (*plan.Plan, error) {
	u, err := url.Parse(t.RelayURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.Path != "/backend-api/codex" || u.RawPath != "" {
		return nil, errors.New("codex: relay needs a listening loopback URL; use qcs use codex <provider>/<model> --relay")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("codex: invalid relay port")
	}
	if t.Fast != nil || t.Effort != "" {
		return nil, errors.New("codex: relay leaves Fast and reasoning effort to Codex; do not use --fast, --no-fast or --effort")
	}
	p, ok, err := provider.Get(t.Provider)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("codex: no provider %q", t.Provider)
	}
	if !p.APIs.Responses || p.BaseURL == "" {
		return nil, fmt.Errorf("codex: provider %s needs a Responses API base URL", p.ID)
	}
	if t.Model == "" || strings.ContainsAny(t.Model, " \t\r\n") {
		return nil, fmt.Errorf("codex: invalid model id %q", t.Model)
	}
	if _, listed := p.Model(t.Model); len(p.Models) > 0 && !listed {
		return nil, fmt.Errorf("codex: provider %s has no model %q", p.ID, t.Model)
	}
	cfg, err := readConfig(ConfigPath())
	if err != nil {
		return nil, err
	}
	if cfg.err != nil {
		return nil, fmt.Errorf("codex: config is not valid TOML: %w", cfg.err)
	}
	for _, key := range []string{"model_provider", "model_catalog_json", "profile", "openai_base_url"} {
		if value, exists := cfg.doc[key]; exists {
			if _, ok := value.(string); !ok {
				return nil, fmt.Errorf("codex: %s must be a string", key)
			}
		}
	}
	if mp, _ := cfg.str("model_provider"); mp != "" && mp != "openai" {
		return nil, errors.New("codex: relay requires the official openai provider; switch back to official first")
	}
	if cat, _ := cfg.str("model_catalog_json"); cat != "" {
		return nil, errors.New("codex: model_catalog_json overrides the relay's model list; remove that override before using relay")
	}
	if active, _ := cfg.str("profile"); active != "" {
		profiles, _ := cfg.doc["profiles"].(map[string]any)
		profile, _ := profiles[active].(map[string]any)
		for _, key := range []string{"model_provider", "openai_base_url", "model_catalog_json"} {
			if _, set := profile[key]; set {
				return nil, fmt.Errorf("codex: active profile %q overrides %s; remove that override before using relay", active, key)
			}
		}
	}
	return &plan.Plan{
		Agent: ID,
		Edits: []plan.FileEdit{{Path: ConfigPath(), Format: plan.TOML, Ops: []plan.Op{
			{Kind: plan.SetTop, Key: "openai_base_url", Value: t.RelayURL},
		}}},
		State: map[string]string{
			"mode": string(agent.Relay), "provider": p.ID, "model": t.Model,
			"relay_url": t.RelayURL, "relay_all": strconv.FormatBool(t.RelayAll),
		},
		Notice: "保持此进程运行，并重启 Codex 桌面端及仍在运行的共享 app-server，再打开新 CLI 会话。" +
			"在 Codex 模型选择器中选择 " + p.ID + "/" + t.Model + "；Fast 和推理强度仍由 Codex 控制。" +
			"停止路由后自动还原地址，需再次重启 Codex。auth.json 没有被修改。",
	}, nil
}
