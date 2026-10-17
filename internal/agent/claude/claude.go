// Package claude is the Claude Code adapter: it points settings.json at a
// provider's Anthropic endpoint (Direct mode). Switching back is
// safekeep.Restore, so there is no Official plan.
// Cloud, tier and effort rules adapted from yetone/magpie (MIT),
// internal/agent/claude.go at 7660399a121af67db4efd56ecb6bd4fb04dc53f1.
// Copyright (c) 2026 yetone; see third_party/magpie/LICENSE.
package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
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
var cloudSwitches = []string{
	"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
	"CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_ANTHROPIC_GOOGLE_CLOUD", "CLAUDE_CODE_USE_MANTLE",
}

const notice = "重启 Claude Code 后生效（已打开的会话仍用旧设置）。"

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

// Claude's native model matching uses dashed versions, but a direct endpoint
// must receive its exact model ID. Magpie can respell IDs because its gateway
// reverses that spelling before forwarding; this adapter has no such gateway.
// These matchers identify native effort rules without changing the sent ID.
var (
	claudeDotted = regexp.MustCompile(`(?i)claude-(?:opus|sonnet|haiku|fable|mythos)-\d+(?:\.\d+)+`)
	claudeNameRE = regexp.MustCompile(`claude-(opus|sonnet|haiku|fable|mythos)-(\d+)(?:-(\d{1,2}))?(?:[^0-9]|$)`)
)

func dashed(id string) string {
	return claudeDotted.ReplaceAllStringFunc(id, func(s string) string { return strings.ReplaceAll(s, ".", "-") })
}

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
	if t.Fast != nil {
		return nil, errors.New("Claude Code direct mode does not support a Fast override; choose Fast inside Claude Code")
	}
	if len(p.Models) > 0 {
		if _, ok := p.Model(t.Model); !ok {
			return nil, fmt.Errorf("provider %q has no model %q", p.ID, t.Model)
		}
	}
	headers, err := customHeaders(p)
	if err != nil {
		return nil, err
	}
	model := t.Model
	path := settingsPath()
	set := func(k string, v any) plan.Op { return plan.Op{Kind: plan.SetJSON, Key: k, Value: v} }
	ops := []plan.Op{
		set("env.ANTHROPIC_BASE_URL", p.APIs.Anthropic),
		set("env.ANTHROPIC_AUTH_TOKEN", p.APIKey),
		set("env.ANTHROPIC_CUSTOM_HEADERS", headers),
		set("env.ANTHROPIC_DEFAULT_OPUS_MODEL", model),
		set("env.ANTHROPIC_DEFAULT_SONNET_MODEL", model),
		set("env.ANTHROPIC_DEFAULT_HAIKU_MODEL", model),
		set("env.ANTHROPIC_SMALL_FAST_MODEL", model),
		set("model", model),
	}
	del := []string{
		"env.ANTHROPIC_MODEL", "env.ANTHROPIC_API_KEY",
		"env.CLAUDE_CODE_SUBAGENT_MODEL", "env.CLAUDE_CODE_WORKFLOW_SUBAGENT_MODEL",
	}
	if strings.HasPrefix(claudeName(model), "claude-fable-") {
		ops = append(ops, set("env.ANTHROPIC_DEFAULT_FABLE_MODEL", model))
	} else {
		del = append(del, "env.ANTHROPIC_DEFAULT_FABLE_MODEL")
	}
	// A tier's label and capabilities describe the model it used to name.
	// Magpie drops them beside new tier models; safekeep keeps their raw
	// originals and restores them when the user switches back.
	for _, tier := range []string{"OPUS", "SONNET", "HAIKU", "FABLE"} {
		for _, suffix := range []string{"_NAME", "_DESCRIPTION", "_SUPPORTED_CAPABILITIES"} {
			del = append(del, "env.ANTHROPIC_DEFAULT_"+tier+"_MODEL"+suffix)
		}
	}
	for _, c := range cloudSwitches {
		managed, managedHas := edit.GetJSON(managedSettingsPath, "env."+c)
		if managedHas && truthy(managed) {
			return nil, fmt.Errorf("managed settings (%s) enable %s; Claude Code would bypass this provider", managedSettingsPath, c)
		}
		v, has := edit.GetJSON(path, "env."+c)
		lower, _ := cloudSetting(c, globalPath())
		if !managedHas && (!has || truthy(v)) && truthy(lower) {
			// Deleting an enabled user setting would uncover the cloud
			// switch in .claude.json or the shell. An explicit off keeps
			// this direct route authoritative until it is restored.
			ops = append(ops, set("env."+c, "0"))
		} else if has && truthy(v) {
			del = append(del, "env."+c)
		}
	}
	if t.Effort != "" {
		effortOps, err := effortPlan(t.Model, p, t.Effort)
		if err != nil {
			return nil, err
		}
		if err := checkEffortCaps(model, t.Effort, path, globalPath(), managedSettingsPath); err != nil {
			return nil, err
		}
		ops = append(ops, effortOps...)
		if t.Effort != "max" {
			del = append(del, "env.CLAUDE_CODE_EFFORT_LEVEL")
		}
	}
	for _, k := range del {
		isCloud := slices.ContainsFunc(cloudSwitches, func(c string) bool { return k == "env."+c })
		if v, has := edit.GetJSON(managedSettingsPath, k); has && v != "" && !isCloud {
			return nil, fmt.Errorf("managed settings (%s) override %s; Claude Code would ignore this switch", managedSettingsPath, k)
		}
		if v, _ := cloudSetting(strings.TrimPrefix(k, "env."), globalPath()); !isCloud && v != "" {
			// Removing a user override would expose a global or shell
			// model, key or subagent again. Claude's empty strings leave
			// those optional overrides inactive without editing the source.
			ops = append(ops, set(k, ""))
		} else if _, ok := edit.GetJSONRaw(path, k); ok {
			ops = append(ops, plan.Op{Kind: plan.DelJSON, Key: k})
		}
	}
	for _, op := range ops {
		if slices.ContainsFunc(cloudSwitches, func(k string) bool { return op.Key == "env."+k }) {
			continue // enabled managed switches were checked above
		}
		if v, has := edit.GetJSON(managedSettingsPath, op.Key); has &&
			(op.Kind == plan.SetJSON && v != fmt.Sprint(op.Value) || op.Kind == plan.DelJSON && v != "") {
			return nil, fmt.Errorf("managed settings (%s) override %s; Claude Code would ignore this switch", managedSettingsPath, op.Key)
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
		return agent.Status{Mode: agent.Official, Detail: "Claude Code 自己的配置"}, nil
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
		return agent.Status{Mode: agent.Direct, Provider: p.ID, Model: model,
			Detail: fmt.Sprintf("%s（经 %s）", model, p.ID)}, nil
	}
	return agent.Status{Mode: agent.Direct, Model: model,
		Detail: fmt.Sprintf("自定义地址 %s（不是 qcs 设置的）", url)}, nil
}

// customHeaders uses Claude Code's supported newline-separated header env.
// An empty auth token is not anonymous: Claude Code falls back to its saved
// OAuth bearer. Only an explicit provider bearer or Authorization header can
// keep that credential from reaching a direct third-party endpoint. An
// X-Api-Key header alone does not suppress the saved OAuth bearer (2.1.302).
func customHeaders(p provider.Provider) (string, error) {
	keys := make([]string, 0, len(p.Headers))
	seen := map[string]bool{}
	hasAuth := false
	for k, v := range p.Headers {
		lower := strings.ToLower(k)
		if seen[lower] {
			return "", fmt.Errorf("provider %q has duplicate case-insensitive header %q", p.ID, k)
		}
		seen[lower] = true
		if k == "" || strings.IndexFunc(k, func(r rune) bool {
			return r > 126 || r < 33 || strings.ContainsRune("()<>@,;:\\\"/[]?={}", r)
		}) >= 0 || strings.ContainsAny(v, "\r\n\x00") {
			return "", fmt.Errorf("provider %q has an invalid Claude Code header %q", p.ID, k)
		}
		if lower == "authorization" && strings.TrimSpace(v) != "" {
			hasAuth = true
		}
		keys = append(keys, k)
	}
	if strings.ContainsAny(p.APIKey, "\r\n\x00") {
		return "", fmt.Errorf("provider %q has an invalid API key", p.ID)
	}
	if strings.TrimSpace(p.APIKey) == "" && !hasAuth {
		return "", fmt.Errorf("provider %q needs an API key or an explicit Authorization header for Claude Code direct mode; an empty token could forward the saved official sign-in", p.ID)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+": "+p.Headers[k])
	}
	return strings.Join(lines, "\n"), nil
}

// claudeName and effortPlan adapt Magpie's native effort rules. Native
// Claude model entries use the canonical name, other vendors use the top
// level. Max is session-only there, so Claude's dedicated env is required.
func claudeName(model string) string {
	m := claudeNameRE.FindStringSubmatch(strings.ToLower(dashed(model)))
	if m == nil {
		return ""
	}
	name := "claude-" + m[1] + "-" + m[2]
	if m[3] != "" {
		name += "-" + m[3]
	}
	return name
}

func effortPlan(model string, p provider.Provider, effort string) ([]plan.Op, error) {
	if dashed(model) != model {
		return nil, fmt.Errorf("Claude Code cannot reliably apply effort to dotted Claude model %q in direct mode; use an exact dashed model ID served by the provider", model)
	}
	levels := []string{"low", "medium", "high", "xhigh", "max"}
	switch claudeName(model) {
	case "claude-opus-4-5":
		levels = []string{"low", "medium", "high"}
	case "claude-opus-4-6", "claude-sonnet-4-6":
		levels = []string{"low", "medium", "high", "max"}
	case "claude-haiku-4-5", "claude-sonnet-4-5", "claude-opus-4", "claude-sonnet-4":
		levels = nil
	case "":
		if strings.Contains(strings.ToLower(model), "claude-3") {
			levels = nil
		}
	}
	if !slices.Contains(levels, effort) {
		return nil, fmt.Errorf("Claude Code does not send effort %q to %s (supported: %s)", effort, model, strings.Join(levels, ", "))
	}
	if m, ok := p.Model(model); ok && len(m.Efforts) > 0 && !slices.Contains(m.Efforts, effort) {
		return nil, fmt.Errorf("provider %q model %q does not list effort %q", p.ID, model, effort)
	}
	set := func(k string) plan.Op { return plan.Op{Kind: plan.SetJSON, Key: k, Value: effort} }
	if effort == "max" {
		return []plan.Op{set("env.CLAUDE_CODE_EFFORT_LEVEL")}, nil
	}
	result := []plan.Op{set("effortLevel")}
	if name := claudeName(model); name != "" {
		result = append(result, set("modelSettings."+name+".effortLevel"))
	}
	return result, nil
}

func globalPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	return filepath.Join(appdir.Home(), ".claude.json")
}

// Claude Code takes the lowest applicable cap across settings files. Within
// one file, a matching model cap replaces the top-level cap, including "max"
// which exempts that model in that file. Preserve every cap and reject a
// request the native client would silently clamp instead of reporting success.
func checkEffortCaps(model, effort string, paths ...string) error {
	levels := []string{"low", "medium", "high", "xhigh", "max"}
	identity := func(id string) string {
		if name := claudeName(id); name != "" {
			return name
		}
		return strings.ToLower(strings.TrimSpace(strings.TrimSuffix(id, "[1m]")))
	}
	for _, path := range paths {
		cap, _ := edit.GetJSON(path, "maxEffortLevel")
		key := "maxEffortLevel"
		var models map[string]struct {
			Max string `json:"maxEffortLevel"`
		}
		if raw, has := edit.GetJSONRaw(path, "modelSettings"); has && json.Unmarshal([]byte(raw), &models) == nil {
			modelCap := ""
			for id, settings := range models {
				if identity(id) == identity(model) && slices.Contains(levels, settings.Max) &&
					(modelCap == "" || slices.Index(levels, settings.Max) < slices.Index(levels, modelCap)) {
					modelCap, key = settings.Max, "modelSettings."+id+".maxEffortLevel"
				}
			}
			if modelCap != "" {
				cap = modelCap
			}
		}
		if slices.Contains(levels, cap) && slices.Index(levels, effort) > slices.Index(levels, cap) {
			return fmt.Errorf("%s sets %s below the requested effort %q; keep the cap and choose a supported effort", path, key, effort)
		}
	}
	return nil
}

// cloudSetting follows the same precedence as Magpie's claudeCloud: managed,
// user, global, shell. An explicit off at a higher level hides lower values.
func cloudSetting(key string, files ...string) (value, source string) {
	for _, path := range files {
		if v, has := edit.GetJSON(path, "env."+key); has {
			return v, path
		}
	}
	return os.Getenv(key), "the environment"
}

func (claude) Doctor() []agent.Problem {
	var out []agent.Problem
	warn := func(f string, a ...any) {
		out = append(out, agent.Problem{Level: "warn", Message: fmt.Sprintf(f, a...)})
	}

	if u, ok := edit.GetJSON(managedSettingsPath, "env.ANTHROPIC_BASE_URL"); ok && u != "" {
		warn("managed settings (%s) set ANTHROPIC_BASE_URL=%s; it overrides this tool and cannot be changed here", managedSettingsPath, u)
	}
	for _, k := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_CUSTOM_HEADERS", "ANTHROPIC_MODEL", "CLAUDE_CODE_EFFORT_LEVEL"} {
		if v := os.Getenv(k); v != "" {
			warn("%s is set in the environment and can conflict with the settings.json this tool writes", k)
		}
	}
	path := settingsPath()
	for _, c := range cloudSwitches {
		if v, from := cloudSetting(c, managedSettingsPath, path, globalPath()); truthy(v) {
			warn("%s is on in %s; Claude Code goes to the cloud and ignores ANTHROPIC_BASE_URL", c, from)
		}
	}
	if u, ok := edit.GetJSON(globalPath(), "env.ANTHROPIC_BASE_URL"); ok && u != "" {
		warn("%s sets env.ANTHROPIC_BASE_URL=%s", globalPath(), u)
	}
	return out
}
