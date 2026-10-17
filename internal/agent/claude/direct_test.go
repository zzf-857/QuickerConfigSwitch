package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func direct(t *testing.T, model, effort string) {
	t.Helper()
	p, err := New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: model, Effort: effort})
	must(t, err)
	must(t, safekeep.Apply(p))
}

func value(t *testing.T, key string) string {
	t.Helper()
	v, _ := edit.GetJSON(settingsPath(), key)
	return v
}

func TestDirectKeepsExactEndpointModelIDs(t *testing.T) {
	for _, model := range []string{
		"claude-opus-4.6", "claude-opus-4-6", "vendor/claude-opus-4.6-region-2.5",
		"claude-opus-4.6.1", "gpt-5.1", "my-claude-relay/model-2.5",
	} {
		t.Run(model, func(t *testing.T) {
			setup(t, `{}`)
			addProv(t, "cpa", "https://fixture.test", model)
			direct(t, model, "")
			for _, key := range []string{"model", "env.ANTHROPIC_DEFAULT_OPUS_MODEL", "env.ANTHROPIC_DEFAULT_SONNET_MODEL", "env.ANTHROPIC_DEFAULT_HAIKU_MODEL", "env.ANTHROPIC_SMALL_FAST_MODEL"} {
				if got := value(t, key); got != model {
					t.Fatalf("%s = %q, want exact endpoint ID %q", key, got, model)
				}
			}
			current, err := New().Current()
			must(t, err)
			if current.Model != model {
				t.Fatalf("current = %+v", current)
			}
		})
	}
}

// These aliases and their labels belonged to the original endpoint. They
// must not continue driving background/subagent requests or identifying the
// newly chosen model, and must return exactly when the switch is undone.
func TestDirectRestoresAliasesCloudsHeadersAndCredentials(t *testing.T) {
	for _, cloud := range cloudSwitches {
		t.Run(cloud, func(t *testing.T) {
			original := "\ufeff{\r\n  // original client configuration\r\n" +
				`  "model":"opus","effortLevel":"low","theme":"dark",` + "\r\n" +
				`  "modelSettings":{"claude-opus-5-5":{"effortLevel":"medium","maxEffortLevel":"xhigh"}},` + "\r\n" +
				`  "env":{"ANTHROPIC_BASE_URL":"https://original.test","ANTHROPIC_AUTH_TOKEN":"old-token",` + "\r\n" +
				`    "ANTHROPIC_API_KEY":"original-api-key","ANTHROPIC_CUSTOM_HEADERS":"Authorization: Bearer original-header\nX-Original: yes",` + "\r\n" +
				`    "ANTHROPIC_MODEL":"old-main","ANTHROPIC_SMALL_FAST_MODEL":"old-small","CLAUDE_CODE_SUBAGENT_MODEL":"old-subagent","CLAUDE_CODE_WORKFLOW_SUBAGENT_MODEL":"old-workflow",` + "\r\n" +
				`    "ANTHROPIC_DEFAULT_FABLE_MODEL":"old-fable","ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":"Old label",` + "\r\n" +
				`    "ANTHROPIC_DEFAULT_SONNET_MODEL_DESCRIPTION":"Old description","ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES":"effort,thinking",` + "\r\n" +
				`    "AWS_REGION":"fixture-region","` + cloud + `":true}}` + "\r\n"
			setup(t, original)
			for _, c := range cloudSwitches {
				t.Setenv(c, "")
			}
			addProv(t, "cpa", "https://fixture.test", "claude-opus-5-5")
			p, _, err := provider.Get("cpa")
			must(t, err)
			p.Headers = map[string]string{"X-Provider": "chosen", "Authorization": "Bearer explicit-header"}
			must(t, provider.Update(p))
			credentials := filepath.Join(appdir.ClaudeHome(), ".credentials.json")
			const secret = `{"claudeAiOauth":{"accessToken":"FAKE-OFFICIAL-ACCESS","refreshToken":"FAKE-OFFICIAL-REFRESH"}}`
			must(t, os.WriteFile(credentials, []byte(secret), 0o600))
			before, err := os.Stat(credentials)
			must(t, err)
			direct(t, "claude-opus-5-5", "high")
			for _, key := range []string{cloud, "ANTHROPIC_MODEL", "ANTHROPIC_API_KEY", "CLAUDE_CODE_SUBAGENT_MODEL", "CLAUDE_CODE_WORKFLOW_SUBAGENT_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL_NAME", "ANTHROPIC_DEFAULT_SONNET_MODEL_DESCRIPTION", "ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES"} {
				if _, exists := edit.GetJSONRaw(settingsPath(), "env."+key); exists {
					t.Fatalf("stale override %s remains", key)
				}
			}
			if value(t, "env.AWS_REGION") != "fixture-region" || value(t, "theme") != "dark" || value(t, "modelSettings.claude-opus-5-5.maxEffortLevel") != "xhigh" {
				t.Fatal("unrelated client settings were changed")
			}
			if value(t, "env.ANTHROPIC_CUSTOM_HEADERS") != "Authorization: Bearer explicit-header\nX-Provider: chosen" || value(t, "modelSettings.claude-opus-5-5.effortLevel") != "high" {
				t.Fatal("provider headers or native per-model effort not applied")
			}
			_, err = safekeep.Restore("claude")
			must(t, err)
			restored, err := os.ReadFile(settingsPath())
			must(t, err)
			if string(restored) != original {
				t.Fatal("original JSONC/BOM/CRLF configuration was not restored byte for byte")
			}
			after, err := os.Stat(credentials)
			must(t, err)
			body, err := os.ReadFile(credentials)
			must(t, err)
			if string(body) != secret || !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("official login credential file changed")
			}
		})
	}
}

func TestDirectRestoreKeepsLaterUserChanges(t *testing.T) {
	setup(t, `{"model":"sonnet","env":{"CLAUDE_CODE_SUBAGENT_MODEL":"original-subagent","CLAUDE_CODE_WORKFLOW_SUBAGENT_MODEL":"original-workflow","ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":"Original label","ANTHROPIC_CUSTOM_HEADERS":"X-Original: yes"}}`)
	addProv(t, "cpa", "https://fixture.test", "claude-opus-5-5")
	direct(t, "claude-opus-5-5", "max")
	must(t, edit.SetJSON(settingsPath(),
		edit.KV{Path: "model", Value: "user-model"},
		edit.KV{Path: "env.CLAUDE_CODE_SUBAGENT_MODEL", Value: "user-subagent"},
		edit.KV{Path: "env.CLAUDE_CODE_WORKFLOW_SUBAGENT_MODEL", Value: "user-workflow"},
		edit.KV{Path: "env.ANTHROPIC_DEFAULT_OPUS_MODEL_NAME", Value: "User label"},
		edit.KV{Path: "env.ANTHROPIC_CUSTOM_HEADERS", Value: "X-User: changed"},
		edit.KV{Path: "env.CLAUDE_CODE_EFFORT_LEVEL", Value: "low"},
		edit.KV{Path: "permissions.allow", Value: []string{"Read"}},
	))
	if len(safekeep.Check("claude")) == 0 {
		t.Fatal("later changes were not reported as drift")
	}
	report, err := safekeep.Restore("claude")
	must(t, err)
	if !report.Left() {
		t.Fatal("preserved user edits were not reported")
	}
	for key, want := range map[string]string{
		"model": "user-model", "env.CLAUDE_CODE_SUBAGENT_MODEL": "user-subagent",
		"env.CLAUDE_CODE_WORKFLOW_SUBAGENT_MODEL": "user-workflow",
		"env.ANTHROPIC_DEFAULT_OPUS_MODEL_NAME":   "User label", "env.ANTHROPIC_CUSTOM_HEADERS": "X-User: changed",
		"env.CLAUDE_CODE_EFFORT_LEVEL": "low",
	} {
		if value(t, key) != want {
			t.Fatalf("%s was not kept", key)
		}
	}
	if _, exists := edit.GetJSONRaw(settingsPath(), "permissions.allow"); !exists {
		t.Fatal("unrelated later client change was lost")
	}
}

func TestDirectCredentialsAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		headers   map[string]string
		wantError bool
	}{
		{name: "key", key: "provider-key"},
		{name: "explicit authorization", headers: map[string]string{"authorization": "Bearer provider-header"}},
		{name: "empty token would expose official login", wantError: true},
		{name: "x-api-key does not suppress official bearer", headers: map[string]string{"X-Api-Key": "provider-key"}, wantError: true},
		{name: "blank authorization", headers: map[string]string{"Authorization": "  "}, wantError: true},
		{name: "duplicate casing", key: "key", headers: map[string]string{"X-Token": "one", "x-token": "two"}, wantError: true},
		{name: "invalid name", key: "key", headers: map[string]string{"(bad)": "value"}, wantError: true},
		{name: "invalid value", key: "key", headers: map[string]string{"X-Test": "bad\r\nvalue"}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := customHeaders(provider.Provider{ID: "fixture", APIKey: tc.key, Headers: tc.headers})
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, want error %v", err, tc.wantError)
			}
		})
	}
	setup(t, `{"model":"sonnet"}`)
	addProv(t, "cpa", "https://fixture.test", "m1")
	p, _, err := provider.Get("cpa")
	must(t, err)
	p.APIKey = ""
	must(t, provider.Update(p))
	if _, err := New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "m1"}); err == nil || !strings.Contains(err.Error(), "official sign-in") {
		t.Fatalf("empty direct credentials = %v", err)
	}
	if value(t, "model") != "sonnet" || safekeep.Touched("claude") {
		t.Fatal("refused plan wrote configuration")
	}
}

func TestDirectEffortIsAppliedOrRefused(t *testing.T) {
	for _, tc := range []struct {
		model, effort string
		listed        []string
		wantError     bool
	}{
		{model: "claude-opus-5-5", effort: "xhigh"},
		{model: "claude-opus-4-6", effort: "max"},
		{model: "claude-opus-4.6", effort: "high", wantError: true},
		{model: "claude-opus-4-6", effort: "xhigh", wantError: true},
		{model: "claude-opus-4-5", effort: "max", wantError: true},
		{model: "claude-haiku-4-5", effort: "low", wantError: true},
		{model: "claude-3-7-sonnet", effort: "high", wantError: true},
		{model: "vendor/custom", effort: "medium", listed: []string{"low", "medium"}},
		{model: "vendor/custom", effort: "high", listed: []string{"low", "medium"}, wantError: true},
		{model: "vendor/custom", effort: "invalid", wantError: true},
	} {
		t.Run(tc.model+"/"+tc.effort, func(t *testing.T) {
			setup(t, `{"effortLevel":"low","env":{"CLAUDE_CODE_EFFORT_LEVEL":"high"},"modelSettings":{"claude-opus-5-5":{"maxEffortLevel":"xhigh"}}}`)
			addProv(t, "cpa", "https://fixture.test", tc.model)
			p, _, err := provider.Get("cpa")
			must(t, err)
			p.Models[0].Efforts = tc.listed
			must(t, provider.Update(p))
			plan, err := New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: tc.model, Effort: tc.effort})
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, want error %v", err, tc.wantError)
			}
			if err != nil {
				return
			}
			must(t, safekeep.Apply(plan))
			if tc.effort == "max" {
				if value(t, "env.CLAUDE_CODE_EFFORT_LEVEL") != "max" {
					t.Fatal("max effort is not persisted in its supported env")
				}
			} else {
				if value(t, "effortLevel") != tc.effort || value(t, "env.CLAUDE_CODE_EFFORT_LEVEL") != "" {
					t.Fatal("effort ignored or overridden by original env")
				}
				if name := claudeName(tc.model); name != "" && value(t, "modelSettings."+name+".effortLevel") != tc.effort {
					t.Fatal("native model effort was not saved")
				}
			}
		})
	}
	setup(t, `{"effortLevel":"low","env":{"CLAUDE_CODE_EFFORT_LEVEL":"high"}}`)
	addProv(t, "cpa", "https://fixture.test", "m1")
	direct(t, "m1", "")
	if value(t, "effortLevel") != "low" || value(t, "env.CLAUDE_CODE_EFFORT_LEVEL") != "high" {
		t.Fatal("an unspecified effort changed the client's own choice")
	}
	fast := true
	if _, err := New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "m1", Fast: &fast}); err == nil {
		t.Fatal("unsupported Fast override was silently accepted")
	}
}

func TestDirectCloudPrecedenceAndManagedConflicts(t *testing.T) {
	for _, source := range []string{"global", "shell"} {
		t.Run(source, func(t *testing.T) {
			setup(t, `{"env":{"CLAUDE_CODE_USE_ANTHROPIC_AWS":true}}`)
			addProv(t, "cpa", "https://fixture.test", "m1")
			if source == "global" {
				must(t, os.WriteFile(globalPath(), []byte(`{"env":{"CLAUDE_CODE_USE_ANTHROPIC_AWS":"1","ANTHROPIC_API_KEY":"old-global-key","CLAUDE_CODE_SUBAGENT_MODEL":"old-global-model","CLAUDE_CODE_WORKFLOW_SUBAGENT_MODEL":"old-workflow"}}`), 0o600))
			} else {
				t.Setenv("CLAUDE_CODE_USE_ANTHROPIC_AWS", "1")
				t.Setenv("ANTHROPIC_API_KEY", "old-shell-key")
				t.Setenv("CLAUDE_CODE_SUBAGENT_MODEL", "old-shell-model")
				t.Setenv("CLAUDE_CODE_WORKFLOW_SUBAGENT_MODEL", "old-shell-workflow")
			}
			direct(t, "m1", "")
			if value(t, "env.CLAUDE_CODE_USE_ANTHROPIC_AWS") != "0" || value(t, "env.ANTHROPIC_API_KEY") != "" || value(t, "env.CLAUDE_CODE_SUBAGENT_MODEL") != "" || value(t, "env.CLAUDE_CODE_WORKFLOW_SUBAGENT_MODEL") != "" {
				t.Fatal("removing an override exposed a lower-priority cloud, key or model")
			}
			for _, problem := range New().Doctor() {
				if strings.Contains(problem.Message, "CLAUDE_CODE_USE_ANTHROPIC_AWS is on") {
					t.Fatal("doctor ignored the explicit off in user settings")
				}
			}
			_, err := safekeep.Restore("claude")
			must(t, err)
			if raw, _ := edit.GetJSONRaw(settingsPath(), "env.CLAUDE_CODE_USE_ANTHROPIC_AWS"); raw != "true" {
				t.Fatal("raw original switch not restored")
			}
		})
	}
	for _, managed := range []string{
		`{"env":{"CLAUDE_CODE_USE_MANTLE":"1"}}`,
		`{"env":{"ANTHROPIC_BASE_URL":"https://managed.test"}}`,
		`{"env":{"ANTHROPIC_API_KEY":"managed-secret"}}`,
		`{"model":"managed-model"}`,
	} {
		t.Run(managed, func(t *testing.T) {
			setup(t, `{"model":"sonnet"}`)
			addProv(t, "cpa", "https://fixture.test", "m1")
			must(t, os.WriteFile(managedSettingsPath, []byte(managed), 0o600))
			if _, err := New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "m1"}); err == nil || !strings.Contains(err.Error(), "managed settings") {
				t.Fatalf("managed override = %v", err)
			}
			if value(t, "model") != "sonnet" || safekeep.Touched("claude") {
				t.Fatal("managed conflict wrote configuration")
			}
		})
	}
}

func TestDirectEffortKeepsAndRespectsClientCaps(t *testing.T) {
	for _, tc := range []struct {
		name, user, global, managed, model, effort string
		wantError                                  bool
	}{
		{name: "user top", user: `{"maxEffortLevel":"low"}`, wantError: true},
		{name: "user per-model", user: `{"modelSettings":{"claude-opus-5-5":{"maxEffortLevel":"low"}}}`, wantError: true},
		{name: "dated cap matches canonical", user: `{"modelSettings":{"claude-opus-5-5-20260922":{"maxEffortLevel":"low"}}}`, wantError: true},
		{name: "global cap", global: `{"maxEffortLevel":"low"}`, wantError: true},
		{name: "managed cap", managed: `{"maxEffortLevel":"low"}`, wantError: true},
		{name: "per-model exemption", user: `{"maxEffortLevel":"low","modelSettings":{"claude-opus-5-5":{"maxEffortLevel":"max"}}}`},
		{name: "lowest across files", user: `{"modelSettings":{"claude-opus-5-5":{"maxEffortLevel":"max"}}}`, managed: `{"maxEffortLevel":"low"}`, wantError: true},
		{name: "max still capped", user: `{"maxEffortLevel":"high"}`, effort: "max", wantError: true},
		{name: "custom exact model cap", user: `{"modelSettings":{"vendor/model-2.5":{"maxEffortLevel":"low"}}}`, model: "vendor/model-2.5", wantError: true},
		{name: "unrelated model cap", user: `{"modelSettings":{"claude-sonnet-5-5":{"maxEffortLevel":"low"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.user == "" {
				tc.user = `{}`
			}
			if tc.model == "" {
				tc.model = "claude-opus-5-5"
			}
			if tc.effort == "" {
				tc.effort = "high"
			}
			setup(t, tc.user)
			addProv(t, "cpa", "https://fixture.test", tc.model)
			if tc.global != "" {
				must(t, os.WriteFile(globalPath(), []byte(tc.global), 0o600))
			}
			if tc.managed != "" {
				must(t, os.WriteFile(managedSettingsPath, []byte(tc.managed), 0o600))
			}
			p, err := New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: tc.model, Effort: tc.effort})
			if (err != nil) != tc.wantError {
				t.Fatalf("cap error = %v, want error %v", err, tc.wantError)
			}
			if err != nil && !strings.Contains(err.Error(), "maxEffortLevel") {
				t.Fatalf("cap refusal did not explain its source: %v", err)
			}
			if p != nil {
				for _, op := range p.Edits[0].Ops {
					if strings.Contains(op.Key, "maxEffortLevel") {
						t.Fatal("plan changed a client effort cap")
					}
				}
			}
			body, err := os.ReadFile(settingsPath())
			must(t, err)
			if string(body) != tc.user || safekeep.Touched("claude") {
				t.Fatal("planning changed the user's original caps")
			}
		})
	}
}
