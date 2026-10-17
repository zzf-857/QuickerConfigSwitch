package claudedesktop

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/desktopdir"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(t, os.WriteFile(path, []byte(body), 0o600))
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	return string(b)
}

func setup(t *testing.T, goos string) paths {
	t.Helper()
	testenv.Home(t)
	old := desktopdir.OS
	desktopdir.OS = goos
	t.Cleanup(func() { desktopdir.OS = old })
	must(t, provider.Add(provider.Provider{
		ID: "cpa", Name: "CPA", APIKey: "desktop-provider-key", APIs: provider.APIs{Anthropic: "http://127.0.0.1:8317"},
		Models: []provider.Model{{ID: "claude-sonnet-4-6"}, {ID: "gpt-5.5"}},
	}))
	return allPaths()[0]
}

func target() agent.Target {
	return agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "claude-sonnet-4-6"}
}

func switchPlan(t *testing.T) *plan.Plan {
	t.Helper()
	p, err := New().Plan(target())
	must(t, err)
	return p
}

func TestRegisteredAndFreshPlan(t *testing.T) {
	p := setup(t, "linux")
	a, ok := agent.Get("claude-desktop")
	if !ok || a.Name() != "Claude Desktop" || !reflect.DeepEqual(a.Modes(), []agent.Mode{agent.Official, agent.Direct}) {
		t.Fatal("not registered separately from Claude Code")
	}
	if d := a.Detect(); d.Installed || d.SignedIn || d.SignIn != "unknown" || len(d.Files) != 4 {
		t.Fatalf("fresh detection: %+v", d)
	}
	pl := switchPlan(t)
	if len(pl.Edits) != 4 || pl.Agent != "claude-desktop" || !strings.Contains(pl.Notice, "Claude-3p") {
		t.Fatalf("plan: %+v", pl)
	}
	for _, file := range pl.Edits {
		if _, err := os.Stat(file.Path); !os.IsNotExist(err) {
			t.Fatalf("Plan wrote %s: %v", file.Path, err)
		}
	}
	preview, err := safekeep.Preview(pl)
	must(t, err)
	if !strings.Contains(preview, "inferenceModels") {
		t.Fatal("no deployment preview")
	}
	must(t, safekeep.Apply(pl))
	if d := a.Detect(); !d.Installed || d.SignedIn {
		t.Fatalf("detection after deployment: %+v", d)
	}
	profile := read(t, p.profile)
	if st, err := os.Stat(p.profile); err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o600) {
		t.Fatalf("fresh API key profile must be private: %v %v", st, err)
	}
	for _, unwanted := range []string{"coworkEgressAllowedHosts", "disableDeploymentModeChooser", "chatTabEnabled", "isClaudeCodeForDesktopEnabled"} {
		if strings.Contains(profile, unwanted) {
			t.Fatalf("unrequested policy %s added", unwanted)
		}
	}
	if got, _ := edit.GetJSON(p.profile, "inferenceModels.0"); got != target().Model {
		t.Fatalf("model = %q", got)
	}
	if st, err := a.Current(); err != nil || st.Mode != agent.Direct || st.Provider != "cpa" || st.Model != target().Model {
		t.Fatalf("current: %+v %v", st, err)
	}
	report, err := safekeep.Restore("claude-desktop")
	must(t, err)
	if report.Left() {
		t.Fatalf("unexpected drift: %+v", report)
	}
	for _, file := range pl.Edits {
		if _, err := os.Stat(file.Path); !os.IsNotExist(err) {
			t.Fatalf("fresh file left after restore: %s %v", file.Path, err)
		}
	}
}

func TestRoundTripKeepsOriginalSettingsAndLogin(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			p := setup(t, goos)
			originals := map[string]string{
				p.config:   "\xef\xbb\xbf{\r\n  // original client settings\r\n  \"deploymentMode\": \"1p\",\r\n  \"mcpServers\": {\"mine\":{\"command\":\"keep\"}},\r\n  \"globalShortcut\": \"Alt+Space\"\r\n}\r\n",
				p.config3p: "{\n  \"preferences\": {\"theme\":\"dark\"}\n}\n",
				p.meta:     "{\n \"appliedId\":\"mine\",\n \"entries\":[{\"id\":\"mine\",\"name\":\"Corp\",\"extra\":42}],\n \"other\":true\n}\n",
				p.profile:  "{\n \"inferenceProvider\":\"gateway\",\n \"inferenceGatewayBaseUrl\":\"https://previous.example\",\n \"inferenceGatewayApiKey\":\"previous-key\",\n \"inferenceGatewayAuthScheme\":\"bearer\",\n \"inferenceModels\":[{\"name\":\"claude-opus-4-6\",\"supports1m\":true}],\n \"coworkEgressAllowedHosts\":[\"corp.example\"],\n \"disableDeploymentModeChooser\":false,\n \"chatTabEnabled\":true\n}\n",
				filepath.Join(p.Data, "Local Storage", "credential-fixture"): "unchanged official desktop credentials",
				filepath.Join(appdir.ClaudeHome(), ".credentials.json"):      `{"claudeAiOauth":{"accessToken":"unchanged"}}`,
				filepath.Join(appdir.ClaudeHome(), "settings.json"):          `{"env":{"ANTHROPIC_BASE_URL":"https://cli.example"}}`,
				filepath.Join(filepath.Dir(p.meta), "mine.json"):             `{"inferenceProvider":"bedrock","original":true}`,
			}
			for path, body := range originals {
				write(t, path, body)
			}
			credentialPath := filepath.Join(p.Data, "Local Storage", "credential-fixture")
			before, err := os.Stat(credentialPath)
			must(t, err)
			pl := switchPlan(t)
			must(t, safekeep.Apply(pl))
			must(t, safekeep.Apply(switchPlan(t)))
			if s, _ := edit.GetJSON(p.profile, "coworkEgressAllowedHosts.0"); s != "corp.example" {
				t.Fatal("existing user policy lost")
			}
			report, err := safekeep.Restore("claude-desktop")
			must(t, err)
			if report.Left() {
				t.Fatalf("clean roundtrip drift: %+v", report)
			}
			for path, body := range originals {
				if read(t, path) != body {
					t.Errorf("original changed: %s", path)
				}
			}
			after, err := os.Stat(credentialPath)
			must(t, err)
			if !os.SameFile(before, after) || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() {
				t.Fatal("login file metadata changed")
			}
		})
	}
}

func TestRestoreKeepsConcurrentUserChangesAndRevokesOurKey(t *testing.T) {
	p := setup(t, "linux")
	write(t, p.meta, `{"appliedId":"mine","entries":[{"id":"mine","name":"Mine"}]}`)
	must(t, safekeep.Apply(switchPlan(t)))
	must(t, edit.SetJSON(p.profile, edit.KV{Path: "preferences.theme", Value: "new"}))
	must(t, edit.SetJSON(p.meta, edit.KV{Path: "entries", Value: []map[string]string{
		{"id": "mine", "name": "Mine"}, {"id": profileID, "name": "QuickerConfigSwitch"}, {"id": "later", "name": "Added while running"},
	}}))
	report, err := safekeep.Restore("claude-desktop")
	must(t, err)
	if !report.Left() {
		t.Fatal("edited entries should be reported")
	}
	if got, _ := edit.GetJSON(p.meta, "appliedId"); got != "mine" {
		t.Fatalf("original active profile not restored: %q", got)
	}
	if got, _ := edit.GetJSON(p.meta, "entries.2.id"); got != "later" {
		t.Fatal("concurrent added profile lost")
	}
	if _, exists := edit.GetJSON(p.profile, "inferenceGatewayApiKey"); exists {
		t.Fatal("unchanged qcs key left in user-edited profile")
	}
	if got, _ := edit.GetJSON(p.profile, "preferences.theme"); got != "new" {
		t.Fatal("user's added preferences lost")
	}
}

func TestRestoreKeepsUserSelectedProfileAndEditedKey(t *testing.T) {
	p := setup(t, "linux")
	write(t, p.meta, `{"appliedId":"mine","entries":[{"id":"mine","name":"Mine"}]}`)
	must(t, safekeep.Apply(switchPlan(t)))
	must(t, edit.SetJSON(p.meta, edit.KV{Path: "appliedId", Value: "mine"}))
	must(t, edit.SetJSON(p.profile, edit.KV{Path: "inferenceGatewayApiKey", Value: "user-new-key"}))
	report, err := safekeep.Restore("claude-desktop")
	must(t, err)
	if !report.Left() || read(t, p.profile) == "" {
		t.Fatal("edited profile should remain and be reported")
	}
	if got, _ := edit.GetJSON(p.meta, "appliedId"); got != "mine" {
		t.Fatal("user-selected original profile changed")
	}
	if got, _ := edit.GetJSON(p.profile, "inferenceGatewayApiKey"); got != "user-new-key" {
		t.Fatal("user-edited API key changed")
	}
}

func TestMalformedSlotsProduceNoPlanOrWrites(t *testing.T) {
	for _, tc := range []struct{ file, body string }{
		{"config", `[]`}, {"config", `{"deploymentMode":true}`}, {"config", `{"deploymentMode":"unexpected"}`},
		{"config3p", `null`}, {"profile", `{"inferenceGatewayApiKey":42}`}, {"profile", `{"inferenceModels":{}}`},
		{"profile", `{"inferenceModels":[null]}`}, {"meta", `{"entries":null}`}, {"meta", `{"entries":{}}`},
		{"meta", `{"entries":["bad"]}`}, {"meta", `{"entries":[{"id":"a"},{"id":"a"}]}`},
		{"meta", `{"appliedId":false}`},
	} {
		t.Run(tc.file+tc.body, func(t *testing.T) {
			p := setup(t, "linux")
			files := map[string]string{"config": p.config, "config3p": p.config3p, "profile": p.profile, "meta": p.meta}
			write(t, files[tc.file], tc.body)
			if plan, err := New().Plan(target()); err == nil || plan != nil {
				t.Fatalf("malformed slot accepted: %+v %v", plan, err)
			}
			if read(t, files[tc.file]) != tc.body || safekeep.Touched("claude-desktop") {
				t.Fatal("invalid file changed")
			}
		})
	}
}

func TestUnsupportedRequestsAndModelChoices(t *testing.T) {
	setup(t, "linux")
	for _, id := range []string{"gpt-5.5", "anthropic/deepseek-flash", "claude-gpt-5", "opus", "claude-sonnet-", "claude-sonnet-4-6[1m]"} {
		if CompatibleModel(id) {
			t.Errorf("unsupported model accepted: %s", id)
		}
	}
	for _, id := range []string{"claude-sonnet-4-6", "anthropic/claude-opus-4-6", "claude-haiku-4-5-20251001"} {
		if !CompatibleModel(id) {
			t.Errorf("compatible model refused: %s", id)
		}
	}
	for _, mutate := range []func(*agent.Target){
		func(t *agent.Target) { t.Model = "gpt-5.5" },
		func(t *agent.Target) { t.Model = "claude-opus-4-6" },
		func(t *agent.Target) { t.Mode = agent.Relay },
		func(t *agent.Target) { t.Mode = agent.Official },
		func(t *agent.Target) { f := false; t.Fast = &f },
		func(t *agent.Target) { t.Effort = "high" },
	} {
		target := target()
		mutate(&target)
		if _, err := New().Plan(target); err == nil {
			t.Errorf("unsupported target accepted: %+v", target)
		}
	}
	for _, mutate := range []func(*provider.Provider){
		func(p *provider.Provider) { p.Headers = map[string]string{"X-Provider": "required"} },
		func(p *provider.Provider) { p.APIKey = "" },
		func(p *provider.Provider) { p.APIKey = "invalid\nkey" },
		func(p *provider.Provider) { p.APIs.Anthropic += "/v1" },
		func(p *provider.Provider) { p.APIs.Anthropic += "/gateway" },
		func(p *provider.Provider) { p.APIs.Anthropic += "?secret=never-print" },
		func(p *provider.Provider) { p.APIs.Anthropic += "?" },
		func(p *provider.Provider) { p.APIs.Anthropic += "/%2f" },
	} {
		p, _, err := provider.Get("cpa")
		must(t, err)
		p.APIs.Anthropic, p.APIKey, p.Headers = "http://127.0.0.1:8317", "desktop-key", nil
		mutate(&p)
		must(t, provider.Update(p))
		if (desktop{}).SupportsModel(p, p.Models[0]) {
			t.Error("UI advertised unsupported provider")
		}
		if _, err := New().Plan(target()); err == nil {
			t.Error("unsupported provider accepted")
		}
	}
}

func TestWindowsPackageDualWritesAndRollback(t *testing.T) {
	setup(t, "windows")
	packageCache := filepath.Join(os.Getenv("LOCALAPPDATA"), "Packages", "AnthropicPBC.Claude_fixture", "LocalCache")
	write(t, filepath.Join(packageCache, "Roaming", "Claude", "Local State"), `{}`)
	ps := allPaths()
	if len(ps) != 2 || !New().Detect().Installed {
		t.Fatalf("package sets: %+v", ps)
	}
	for _, p := range ps {
		write(t, p.config, `{"deploymentMode":"1p","keep":true}`)
	}
	if len(switchPlan(t).Edits) != 8 {
		t.Fatal("both real and MSIX sets must be planned")
	}
	// A late unwritable directory must roll the first set back too.
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		dir := filepath.Dir(ps[1].config)
		must(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if err := safekeep.Apply(switchPlan(t)); err == nil {
			t.Fatal("unwritable second set accepted")
		}
		for _, p := range ps {
			if read(t, p.config) != `{"deploymentMode":"1p","keep":true}` {
				t.Fatal("partial deployment left after rollback")
			}
			if _, err := os.Stat(p.profile); !os.IsNotExist(err) {
				t.Fatalf("profile written on failed deployment: %v", err)
			}
		}
		must(t, os.Chmod(dir, 0o700))
	}
	must(t, safekeep.Apply(switchPlan(t)))
	for _, p := range ps {
		if mode, _ := edit.GetJSON(p.config3p, "deploymentMode"); mode != "3p" {
			t.Fatalf("set %s was not configured", p.ThreeP)
		}
	}
	// New Windows builds run in the real folder; an emptied old package
	// copy must not masquerade as the app's active configuration.
	write(t, filepath.Join(ps[1].ThreeP, "Local State"), `{}`)
	must(t, os.Chtimes(filepath.Join(ps[1].ThreeP, "Local State"), time.Now(), time.Now()))
	must(t, os.Remove(ps[0].config3p))
	st, err := New().Current()
	must(t, err)
	if st.Provider != "cpa" {
		t.Fatalf("obsolete package copy hid active configuration: %+v", st)
	}
}

func TestCurrentRequiresActiveWiringAndMatchingProvider(t *testing.T) {
	for _, tc := range []struct {
		file, key string
		value     any
		mode      agent.Mode
		ours      bool
	}{
		{"config3p", "deploymentMode", "1p", agent.Direct, false},
		{"meta", "appliedId", "other", agent.Direct, false},
		{"meta", "entries", []map[string]string{}, agent.Direct, false},
		{"profile", "inferenceProvider", "bedrock", agent.Direct, false},
		{"profile", "inferenceGatewayBaseUrl", "https://other.example", agent.Direct, false},
		{"profile", "inferenceGatewayApiKey", "different-key", agent.Direct, false},
	} {
		t.Run(tc.key, func(t *testing.T) {
			p := setup(t, "linux")
			must(t, safekeep.Apply(switchPlan(t)))
			file := map[string]string{"config3p": p.config3p, "meta": p.meta, "profile": p.profile}[tc.file]
			must(t, edit.SetJSON(file, edit.KV{Path: tc.key, Value: tc.value}))
			st, err := New().Current()
			must(t, err)
			if st.Mode != tc.mode || (st.Provider == "cpa") != tc.ours {
				t.Fatalf("current wiring: %+v", st)
			}
		})
	}
}

func TestDetectWindowsInstalledBeforeFirstRun(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		file, want bool
	}{
		{"absent", "", false, false},
		{"Squirrel installed", "AnthropicClaude/claude.exe", true, true},
		{"Squirrel leftover", "AnthropicClaude/packages", false, false},
		{"MSIX", "Packages/Claude_custompublisher", false, true},
		{"Store MSIX", "Packages/AnthropicPBC.Claude_custompublisher", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup(t, "windows")
			if tc.path != "" {
				path := filepath.Join(os.Getenv("LOCALAPPDATA"), filepath.FromSlash(tc.path))
				if tc.file {
					write(t, path, "fake executable")
				} else {
					must(t, os.MkdirAll(path, 0o755))
				}
			}
			if d := New().Detect(); d.Installed != tc.want || d.SignedIn {
				t.Fatalf("detection: %+v", d)
			}
		})
	}
}

func TestWindowsMalformedSecondSetNeverChangesFirst(t *testing.T) {
	setup(t, "windows")
	write(t, filepath.Join(os.Getenv("LOCALAPPDATA"), "Packages", "Claude_fixture", "LocalCache", "Roaming", "Claude", "Local State"), `{}`)
	ps := allPaths()
	write(t, ps[0].config, `{"deploymentMode":"1p","original":true}`)
	write(t, ps[1].meta, `{"entries":false}`)
	if p, err := New().Plan(target()); err == nil || p != nil {
		t.Fatal("malformed second copy accepted")
	}
	if read(t, ps[0].config) != `{"deploymentMode":"1p","original":true}` {
		t.Fatal("first copy changed before all validation")
	}
	if _, err := os.Stat(ps[0].profile); !os.IsNotExist(err) {
		t.Fatal("profile created while planning an invalid second copy")
	}
}
