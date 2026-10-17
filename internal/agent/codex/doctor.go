package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
)

// Doctor lists what keeps Codex from loading its config or from working as
// switched.
func (c *Codex) Doctor() []agent.Problem {
	var out []agent.Problem
	add := func(level, format string, a ...any) {
		out = append(out, agent.Problem{Level: level, Message: fmt.Sprintf(format, a...)})
	}
	// Current Codex no longer reads OPENAI_BASE_URL from its environment
	// (openai_base_url in config.toml replaced it); older releases did.
	if u := os.Getenv("OPENAI_BASE_URL"); u != "" {
		add("info", "OPENAI_BASE_URL is set in the environment (%s); older Codex releases send official requests there, current ones use openai_base_url in config.toml", u)
	}
	path := ConfigPath()
	cfg, err := readConfig(path)
	if err != nil {
		add("warn", "can't read %s: %v", path, err)
		return out
	}
	if cfg.err != nil {
		add("warn", "%s is not valid TOML (%v); Codex can't load it and this tool won't edit it", path, cfg.err)
		return out
	}
	if problem := loopbackProxyProblem(cfg); problem != nil {
		out = append(out, *problem)
	}
	provs := cfg.providers()
	if mp, _ := cfg.str("model_provider"); mp != "" && !builtin[mp] {
		if _, ok := provs[mp]; !ok {
			add("error", "model_provider = %q but config.toml has no [model_providers.%s] table: Codex fails to load its config (\"Model provider `%s` not found\")", mp, mp, mp)
		}
	}
	if cat, ok := cfg.str("model_catalog_json"); ok && cat != "" {
		p := cat
		if !filepath.IsAbs(p) {
			p = filepath.Join(appdir.CodexHome(), p)
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			// codex-rs core/src/config load_catalog_json reads it as the config loads
			add("error", "model_catalog_json points at %s, which doesn't exist: Codex fails to load its config", cat)
		}
	}
	if f, _ := cfg.doc["features"].(map[string]any); f != nil {
		if on, ok := f["fast_mode"].(bool); ok && !on {
			add("warn", "[features] fast_mode = false hides Fast (/fast) for every model")
		}
	}
	if ps, _ := cfg.doc["profiles"].(map[string]any); ps != nil {
		active, _ := cfg.str("profile")
		names := make([]string, 0, len(ps))
		for n := range ps {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			pr, _ := ps[n].(map[string]any)
			mp, ok := pr["model_provider"].(string)
			if !ok {
				continue
			}
			if n == active {
				add("warn", "profile %q is active (profile = %q) and sets model_provider = %q, which wins over the top-level model_provider", n, active, mp)
			} else {
				add("warn", "profile %q sets model_provider = %q, which wins over the top-level model_provider whenever that profile is used", n, mp)
			}
		}
	}
	if cc := cfg.ccSwitchTables(); len(cc) > 0 {
		add("info", "CC Switch provider tables found (%s); they are left alone and coexist with this tool's", strings.Join(cc, ", "))
	}
	return out
}
