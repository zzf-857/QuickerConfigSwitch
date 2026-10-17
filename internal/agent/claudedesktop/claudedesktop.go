// Package claudedesktop adapts Magpie's Claude Desktop third-party deployment
// wiring into reversible plans. It does not change Claude Code's settings or
// read either client's login credentials.
//
// Adapted from yetone/magpie (MIT), internal/agent/claudedesktop.go and
// internal/gateway/desktop.go. Copyright (c) 2026 yetone.
// See third_party/magpie/LICENSE.
package claudedesktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tidwall/jsonc"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/desktopdir"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

// A different profile from Magpie's, so the tools never share ownership.
const profileID = "00000000-0000-4000-8000-716373000001"

const notice = "完全退出并重启 Claude Desktop 后生效。第三方模式使用独立的 Claude-3p 数据目录，不使用官方 Anthropic 登录；原登录数据、MCP 和会话保留在原目录，切回官方后恢复使用。此适配器只配置 Claude 兼容模型，不提供任意模型转换或子代理路由。"

type desktop struct{}

func init() { agent.Register(New()) }

// New returns the Claude Desktop adapter, separate from Claude Code.
func New() agent.Agent { return desktop{} }

func (desktop) ID() string   { return "claude-desktop" }
func (desktop) Name() string { return "Claude Desktop" }
func (desktop) Modes() []agent.Mode {
	return []agent.Mode{agent.Official, agent.Direct}
}

type paths struct {
	desktopdir.Dirs
	config, config3p, meta, profile string
}

func pathsOf(d desktopdir.Dirs) paths {
	lib := filepath.Join(d.ThreeP, "configLibrary")
	return paths{Dirs: d,
		config:   filepath.Join(d.Mode, "claude_desktop_config.json"),
		config3p: filepath.Join(d.ThreeP, "claude_desktop_config.json"),
		meta:     filepath.Join(lib, "_meta.json"),
		profile:  filepath.Join(lib, profileID+".json"),
	}
}

func allPaths() []paths {
	var out []paths
	for _, d := range desktopdir.Sets(desktopdir.OS, appdir.Home(), os.Getenv) {
		out = append(out, pathsOf(d))
	}
	return out
}

func (desktop) Detect() agent.Detection {
	ps := allPaths()
	d := agent.Detection{Dir: ps[0].Mode, SignIn: "unknown"}
	for _, p := range ps {
		d.Files = append(d.Files, p.config, p.config3p, p.meta, p.profile)
		for _, dir := range p.All() {
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				d.Installed = true
			}
		}
	}
	d.Installed = d.Installed || desktopdir.Installed(desktopdir.OS, appdir.Home(), os.Getenv)
	return d
}

// desktopAccepts follows Desktop's catalog filter copied from Magpie. The
// stricter direct spelling below also avoids relying on catalog aliases that
// only a running gateway can translate back to the provider's model.
var (
	desktopDenied = regexp.MustCompile(`ark-code|astron|command-r|deepseek|doubao|gemini|gemma|glm|gpt|grok|hermes|hy3|kimi|lfm|\bling\b|llama|longcat|mimo|minimax|mistral|mixtral|moonshot|nemotron|openai|phi-|qianfan|qwen|tc-code|\bunic\b|yi-|stepfun|step-3|seed-|bytedance|hunyuan|granite|amazon\.nova|nova-|devstral|ministral|ernie|codex|arcee|trinity|abab|phi\d|\bk2\.|\bm2\.|jamba|arctic|solar|mercury|zamba|kat-coder|\bds-|dpsk`)
	desktopTier   = regexp.MustCompile(`^(sonnet|opus|haiku|fable|mythos)(-[\d.]+)?$`)
	desktopWords  = []string{"claude", "sonnet", "opus", "haiku", "fable", "mythos", "anthropic"}
	directModel   = regexp.MustCompile(`^(anthropic/)?claude-(sonnet|opus|haiku|fable|mythos)-[a-z0-9][a-z0-9._:-]*$`)
)

func desktopAccepts(id string) bool {
	l := strings.ToLower(id)
	if desktopTier.MatchString(l) {
		return true
	}
	if desktopDenied.MatchString(l) {
		return false
	}
	return slices.ContainsFunc(desktopWords, func(w string) bool { return strings.Contains(l, w) })
}

// CompatibleModel says whether a provider model can be used directly without
// an alias translation gateway. The provider's spelling is kept unchanged.
func CompatibleModel(id string) bool {
	return directModel.MatchString(strings.ToLower(id)) && desktopAccepts(id)
}

// SupportsModel lets the UI offer only direct configurations this adapter can
// represent. Plan remains authoritative and validates the request again.
func (desktop) SupportsModel(p provider.Provider, m provider.Model) bool {
	_, err := gatewayURL(p.APIs.Anthropic)
	return err == nil && len(p.Headers) == 0 && validKey(p.APIKey) && CompatibleModel(m.ID)
}

func validKey(key string) bool {
	return strings.TrimSpace(key) != "" && !strings.ContainsAny(key, "\r\n\x00")
}

// gatewayURL accepts the root URL proven by Magpie's deployment and its
// /v1/messages requests. A suffixed /v1 or another path is not guessed into a
// different endpoint: the caller must provide an explicit gateway root.
func gatewayURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.Trim(u.Path, "/") != "" {
		return "", errors.New("Claude Desktop needs an http(s) Anthropic gateway root URL without /v1, another path, query, credentials or fragment")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func object(path string) (map[string]json.RawMessage, error) {
	b, err := edit.Read(path)
	if err != nil {
		return nil, err
	}
	b = edit.TrimBOM(b)
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(jsonc.ToJSON(b), &doc) != nil || doc == nil {
		return nil, fmt.Errorf("%s: not a JSON object", path)
	}
	return doc, nil
}

func stringSlot(doc map[string]json.RawMessage, key, path string) (string, error) {
	raw, ok := doc[key]
	if !ok {
		return "", nil
	}
	var s string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &s) != nil {
		return "", fmt.Errorf("%s: %s must be a string", path, key)
	}
	return s, nil
}

func entriesOf(doc map[string]json.RawMessage, path string) ([]json.RawMessage, error) {
	raw, ok := doc["entries"]
	if !ok {
		return []json.RawMessage{}, nil
	}
	var entries []json.RawMessage
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &entries) != nil {
		return nil, fmt.Errorf("%s: entries must be a list", path)
	}
	seen := map[string]bool{}
	for _, raw := range entries {
		var e map[string]json.RawMessage
		if json.Unmarshal(raw, &e) != nil || e == nil {
			return nil, fmt.Errorf("%s: entry must be an object", path)
		}
		id, err := stringSlot(e, "id", path)
		if err != nil || id == "" || seen[id] {
			return nil, fmt.Errorf("%s: entries need unique nonempty string ids", path)
		}
		seen[id] = true
	}
	return entries, nil
}

func entryID(raw json.RawMessage) string {
	var e struct {
		ID string `json:"id"`
	}
	json.Unmarshal(raw, &e)
	return e.ID
}

func (desktop) Plan(t agent.Target) (*plan.Plan, error) {
	if t.Mode != agent.Direct {
		return nil, errors.New("Claude Desktop supports direct third-party configuration only; official uses safekeep.Restore")
	}
	if t.Fast != nil || t.Effort != "" {
		return nil, errors.New("Claude Desktop leaves Fast and reasoning effort to the client; do not use --fast, --no-fast or --effort")
	}
	p, ok, err := provider.Get(t.Provider)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("provider %q not found", t.Provider)
	}
	if len(p.Headers) > 0 {
		return nil, errors.New("Claude Desktop direct configuration cannot represent provider custom headers")
	}
	u, err := gatewayURL(p.APIs.Anthropic)
	if err != nil {
		return nil, err
	}
	if !validKey(p.APIKey) {
		return nil, errors.New("Claude Desktop's bearer gateway configuration needs a valid nonempty provider API key")
	}
	if !CompatibleModel(t.Model) {
		return nil, fmt.Errorf("Claude Desktop direct mode cannot use model %q: choose a Claude-compatible claude-<tier>-<version> id; other vendors require a model translation gateway", t.Model)
	}
	if len(p.Models) > 0 {
		if _, ok := p.Model(t.Model); !ok {
			return nil, fmt.Errorf("provider %q has no model %q", p.ID, t.Model)
		}
	}
	result := &plan.Plan{Agent: "claude-desktop", Notice: notice,
		State: map[string]string{"mode": "direct", "provider": p.ID, "model": t.Model}}
	set := func(k string, v any) plan.Op { return plan.Op{Kind: plan.SetJSON, Key: k, Value: v} }
	for _, paths := range allPaths() {
		docs := map[string]map[string]json.RawMessage{}
		for _, f := range []string{paths.config, paths.config3p, paths.profile, paths.meta} {
			docs[f], err = object(f)
			if err != nil {
				return nil, err
			}
		}
		for _, f := range []string{paths.config, paths.config3p} {
			mode, e := stringSlot(docs[f], "deploymentMode", f)
			if e != nil {
				return nil, e
			}
			if mode != "" && mode != "1p" && mode != "3p" {
				return nil, fmt.Errorf("%s: unsupported deploymentMode %q", f, mode)
			}
		}
		if _, err := stringSlot(docs[paths.meta], "appliedId", paths.meta); err != nil {
			return nil, err
		}
		entries, err := entriesOf(docs[paths.meta], paths.meta)
		if err != nil {
			return nil, err
		}
		for _, k := range []string{"inferenceProvider", "inferenceGatewayBaseUrl", "inferenceGatewayApiKey", "inferenceGatewayAuthScheme"} {
			if _, err := stringSlot(docs[paths.profile], k, paths.profile); err != nil {
				return nil, err
			}
		}
		if raw, ok := docs[paths.profile]["inferenceModels"]; ok {
			if _, err := modelList(raw); err != nil {
				return nil, fmt.Errorf("%s: %w", paths.profile, err)
			}
		}
		if !slices.ContainsFunc(entries, func(e json.RawMessage) bool { return entryID(e) == profileID }) {
			entry, _ := json.Marshal(map[string]string{"id": profileID, "name": "QuickerConfigSwitch"})
			entries = append(entries, entry)
		}
		result.Edits = append(result.Edits,
			plan.FileEdit{Path: paths.profile, Format: plan.JSON, Ops: []plan.Op{
				set("inferenceProvider", "gateway"), set("inferenceGatewayBaseUrl", u),
				set("inferenceGatewayApiKey", p.APIKey), set("inferenceGatewayAuthScheme", "bearer"),
				set("inferenceModels", []string{t.Model}),
			}},
			plan.FileEdit{Path: paths.meta, Format: plan.JSON, Ops: []plan.Op{set("entries", entries), set("appliedId", profileID)}},
			plan.FileEdit{Path: paths.config3p, Format: plan.JSON, Ops: []plan.Op{set("deploymentMode", "3p")}},
			plan.FileEdit{Path: paths.config, Format: plan.JSON, Ops: []plan.Op{set("deploymentMode", "3p")}},
		)
	}
	return result, nil
}

func modelList(raw json.RawMessage) ([]string, error) {
	var values []json.RawMessage
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &values) != nil {
		return nil, errors.New("inferenceModels must be a list of strings or named objects")
	}
	var names []string
	for _, value := range values {
		var name string
		if json.Unmarshal(value, &name) != nil {
			var object struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(value, &object) != nil {
				return nil, errors.New("inferenceModels entry must be a string or named object")
			}
			name = object.Name
		}
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("inferenceModels entry needs a nonempty name")
		}
		names = append(names, name)
	}
	return names, nil
}

// readPaths follows Magpie's Local State recency rule: an obsolete MSIX copy
// cleared by an update must not hide the configuration the app actually reads.
func readPaths(ps []paths) []paths {
	var newest time.Time
	var at []paths
	for _, p := range ps {
		if t := desktopdir.LastRun(p.ThreeP); t.After(newest) {
			newest, at = t, []paths{p}
		}
	}
	if at == nil {
		return ps
	}
	return at
}

func currentAt(p paths, ps []provider.Provider) (agent.Status, error) {
	status := agent.Status{Mode: agent.Official, Detail: "Claude Desktop 自己的配置（登录状态未知）"}
	var modes []string
	for _, f := range []string{p.config, p.config3p} {
		doc, err := object(f)
		if err != nil {
			return agent.Status{}, err
		}
		mode, err := stringSlot(doc, "deploymentMode", f)
		if err != nil {
			return agent.Status{}, err
		}
		if mode != "" && mode != "1p" && mode != "3p" {
			return agent.Status{}, fmt.Errorf("%s: unsupported deploymentMode %q", f, mode)
		}
		modes = append(modes, mode)
	}
	if modes[0] != "3p" && modes[1] != "3p" {
		return status, nil
	}
	if modes[0] != modes[1] {
		return agent.Status{Mode: agent.Direct, Detail: "Claude Desktop 的 deploymentMode 配置不一致；重启前请检查配置"}, nil
	}
	status.Mode, status.Detail = agent.Direct, "Claude Desktop 使用第三方配置（不是 qcs 当前配置）"
	meta, err := object(p.meta)
	if err != nil {
		return agent.Status{}, err
	}
	id, err := stringSlot(meta, "appliedId", p.meta)
	if err != nil {
		return agent.Status{}, err
	}
	if id != profileID {
		return status, nil
	}
	entries, err := entriesOf(meta, p.meta)
	if err != nil {
		return agent.Status{}, err
	}
	if !slices.ContainsFunc(entries, func(e json.RawMessage) bool { return entryID(e) == profileID }) {
		status.Detail = "Claude Desktop 第三方配置的 appliedId 未在 entries 中注册"
		return status, nil
	}
	prof, err := object(p.profile)
	if err != nil {
		return agent.Status{}, err
	}
	values := map[string]string{}
	for _, k := range []string{"inferenceProvider", "inferenceGatewayBaseUrl", "inferenceGatewayApiKey", "inferenceGatewayAuthScheme"} {
		values[k], err = stringSlot(prof, k, p.profile)
		if err != nil {
			return agent.Status{}, err
		}
	}
	if values["inferenceProvider"] != "gateway" || values["inferenceGatewayAuthScheme"] != "bearer" {
		status.Detail = "Claude Desktop 的 qcs profile 已使用其它 provider 或认证方式"
		return status, nil
	}
	models, err := modelList(prof["inferenceModels"])
	if err != nil {
		return agent.Status{}, fmt.Errorf("%s: %w", p.profile, err)
	}
	if len(models) == 1 {
		status.Model = models[0]
	}
	for _, provider := range ps {
		u, err := gatewayURL(provider.APIs.Anthropic)
		if err == nil && values["inferenceGatewayBaseUrl"] == u && values["inferenceGatewayApiKey"] == provider.APIKey {
			status.Provider = provider.ID
			status.Detail = fmt.Sprintf("%s（经 %s；第三方部署，不使用官方登录）", status.Model, provider.ID)
			return status, nil
		}
	}
	return status, nil
}

func (desktop) Current() (agent.Status, error) {
	providers, err := provider.List()
	if err != nil {
		return agent.Status{}, err
	}
	var current agent.Status
	for i, p := range readPaths(allPaths()) {
		status, err := currentAt(p, providers)
		if err != nil {
			return agent.Status{}, err
		}
		if i > 0 && (current.Mode != status.Mode || current.Provider != status.Provider || current.Model != status.Model) {
			return agent.Status{Mode: agent.Direct, Detail: "Claude Desktop 的 Windows 配置副本不一致；重启前请检查配置"}, nil
		}
		current = status
	}
	return current, nil
}

func (a desktop) Doctor() []agent.Problem {
	status, err := a.Current()
	if err != nil {
		return []agent.Problem{{Level: "error", Message: err.Error()}}
	}
	if status.Mode == agent.Direct {
		return []agent.Problem{{Level: "info", Message: notice}, {Level: "info", Message: status.Detail}}
	}
	return nil
}
