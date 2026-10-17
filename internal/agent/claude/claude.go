// Package claude is the Claude Code adapter: it points settings.json at a
// provider's Anthropic endpoint (Direct mode). Switching back is
// safekeep.Restore, so there is no Official plan.
package claude

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

func init() { agent.Register(New()) }

// managedSettingsPath is Claude Code's admin-managed settings file; a
// variable so tests can point it elsewhere.
var managedSettingsPath = defaultManagedPath()

func defaultManagedPath() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	case "windows":
		return `C:\ProgramData\ClaudeCode\managed-settings.json`
	}
	return "/etc/claude-code/managed-settings.json"
}

// cloudSwitches route Claude Code to a cloud and bypass ANTHROPIC_BASE_URL.
var cloudSwitches = []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"}

const notice = "Restart Claude Code (open sessions keep the old settings)."

type claude struct{}

// New returns the Claude Code adapter.
func New() agent.Agent { return claude{} }

func (claude) ID() string   { return "claude" }
func (claude) Name() string { return "Claude Code" }

func settingsPath() string { return filepath.Join(appdir.ClaudeHome(), "settings.json") }

func (claude) Modes() []agent.Mode { return []agent.Mode{agent.Official, agent.Direct} }

func (claude) Detect() agent.Detection {
	dir := appdir.ClaudeHome()
	d := agent.Detection{Dir: dir, Files: []string{settingsPath()}}
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		d.Installed = true
	} else if _, err := exec.LookPath("claude"); err == nil {
		d.Installed = true
	}
	// Only the file's presence is checked; tokens are never read. On macOS
	// the sign-in lives in the keychain, so it is unknown there.
	if _, err := os.Stat(filepath.Join(dir, ".credentials.json")); err == nil {
		d.SignedIn, d.SignIn = true, "claudeai"
	}
	return d
}

// Spelled writes a Claude model's dotted version with dashes
// ("claude-opus-4.6" -> "claude-opus-4-6", as Claude Code matches it). Ids
// without "claude" are left as they are.
func Spelled(id string) string {
	if !strings.Contains(strings.ToLower(id), "claude") {
		return id
	}
	return dottedVersion.ReplaceAllString(id, "$1-$2")
}

var dottedVersion = regexp.MustCompile(`(\d)\.(\d)`)

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (claude) Plan(t agent.Target) (*plan.Plan, error) {
	if t.Mode != agent.Direct {
		if t.Mode == agent.Official {
			return nil, errors.New("use safekeep.Restore for official")
		}
		return nil, fmt.Errorf("Claude Code does not support mode %q", t.Mode)
	}
	p, ok, err := provider.Get(t.Provider)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("provider %q not found", t.Provider)
	}
	if p.APIs.Anthropic == "" {
		return nil, fmt.Errorf("provider %q has no Anthropic endpoint (Claude Code needs one)", p.ID)
	}
	if t.Model == "" {
		return nil, errors.New("no model chosen")
	}
	if len(p.Models) > 0 {
		if _, ok := p.Model(t.Model); !ok {
			return nil, fmt.Errorf("provider %q has no model %q", p.ID, t.Model)
		}
	}
	model := Spelled(t.Model)
	path := settingsPath()
	set := func(k string, v any) plan.Op { return plan.Op{Kind: plan.SetJSON, Key: k, Value: v} }
	ops := []plan.Op{
		set("env.ANTHROPIC_BASE_URL", p.APIs.Anthropic),
		set("env.ANTHROPIC_AUTH_TOKEN", p.APIKey),
		set("env.ANTHROPIC_DEFAULT_OPUS_MODEL", model),
		set("env.ANTHROPIC_DEFAULT_SONNET_MODEL", model),
		set("env.ANTHROPIC_DEFAULT_HAIKU_MODEL", model),
		set("model", model),
	}
	del := []string{"env.ANTHROPIC_MODEL", "env.ANTHROPIC_API_KEY"}
	for _, c := range cloudSwitches {
		if v, ok := edit.GetJSON(path, "env."+c); ok && truthy(v) {
			del = append(del, "env."+c)
		}
	}
	for _, k := range del {
		if _, ok := edit.GetJSONRaw(path, k); ok {
			ops = append(ops, plan.Op{Kind: plan.DelJSON, Key: k})
		}
	}
	return &plan.Plan{
		Agent:  "claude",
		Edits:  []plan.FileEdit{{Path: path, Format: plan.JSON, Ops: ops}},
		Notice: notice,
		State:  map[string]string{"mode": "direct", "provider": p.ID, "model": t.Model},
	}, nil
}

func trimURL(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }

func (claude) Current() (agent.Status, error) {
	path := settingsPath()
	url, ok := edit.GetJSON(path, "env.ANTHROPIC_BASE_URL")
	if !ok || trimURL(url) == "" {
		return agent.Status{Mode: agent.Official, Detail: "Claude Code's own setup"}, nil
	}
	model, _ := edit.GetJSON(path, "model")
	ps, err := provider.List()
	if err != nil {
		return agent.Status{}, err
	}
	for _, p := range ps {
		if p.APIs.Anthropic == "" || trimURL(p.APIs.Anthropic) != trimURL(url) {
			continue
		}
		// Models are written respelled; give back the provider's own id.
		for _, m := range p.Models {
			if m.ID == model || Spelled(m.ID) == model {
				model = m.ID
				break
			}
		}
		return agent.Status{Mode: agent.Direct, Provider: p.ID, Model: model,
			Detail: fmt.Sprintf("%s via %s", model, p.ID)}, nil
	}
	return agent.Status{Mode: agent.Direct, Model: model,
		Detail: fmt.Sprintf("custom endpoint %s (not managed by this tool)", url)}, nil
}

func (claude) Doctor() []agent.Problem {
	var out []agent.Problem
	warn := func(f string, a ...any) {
		out = append(out, agent.Problem{Level: "warn", Message: fmt.Sprintf(f, a...)})
	}

	if u, ok := edit.GetJSON(managedSettingsPath, "env.ANTHROPIC_BASE_URL"); ok && u != "" {
		warn("managed settings (%s) set ANTHROPIC_BASE_URL=%s; it overrides this tool and cannot be changed here", managedSettingsPath, u)
	}
	for _, k := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"} {
		if v := os.Getenv(k); v != "" {
			warn("%s is set in the environment and can conflict with the settings.json this tool writes", k)
		}
	}
	path := settingsPath()
	for _, c := range cloudSwitches {
		if v, ok := edit.GetJSON(path, "env."+c); ok && truthy(v) {
			warn("%s is on in %s; Claude Code goes to the cloud and ignores ANTHROPIC_BASE_URL", c, path)
		}
		if truthy(os.Getenv(c)) {
			warn("%s is on in the environment; Claude Code goes to the cloud and ignores ANTHROPIC_BASE_URL", c)
		}
	}
	if u, ok := edit.GetJSON(filepath.Join(appdir.Home(), ".claude.json"), "env.ANTHROPIC_BASE_URL"); ok && u != "" {
		warn("~/.claude.json sets env.ANTHROPIC_BASE_URL=%s", u)
	}
	return out
}
