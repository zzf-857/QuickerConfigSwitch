package codex

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func TestDirectRejectsActiveProfileConflictsWithoutWriting(t *testing.T) {
	for _, tt := range []struct {
		name    string
		profile string
		model   string
		fast    *bool
		effort  string
		key     string
	}{
		{name: "provider", profile: `model_provider = "openai"`, key: "model_provider"},
		{name: "model", profile: `model = "another-model"`, key: "model"},
		{name: "built-in catalog", profile: `model_catalog_json = "/different/catalog.json"`, key: "model_catalog_json"},
		{name: "generated catalog", profile: `model_catalog_json = "/different/catalog.json"`, model: "alias", key: "model_catalog_json"},
		{name: "enable Fast", profile: `service_tier = "default"`, fast: ptr(true), key: "service_tier"},
		{name: "disable Fast", profile: `service_tier = "fast"`, fast: ptr(false), key: "service_tier"},
		{name: "reasoning effort", profile: `model_reasoning_effort = "low"`, effort: "high", key: "model_reasoning_effort"},
		{name: "invalid model type", profile: `model = ["gpt-5.5"]`, key: "model"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := tt.model
			if model == "" {
				model = "gpt-5.5"
			}
			original := "# preserved\nprofile = \"work\"\nmodel = \"original-model\"\n[profiles.work]\n" + tt.profile + " # mine\n"
			setup(t, original, provider.Model{ID: model, Fast: true})
			const auth = `{"tokens":{"access_token":"fixture"}}`
			writeFile(t, AuthPath(), auth)
			p, err := New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: model, Fast: tt.fast, Effort: tt.effort})
			if err == nil || p != nil || !strings.Contains(err.Error(), `active legacy profile "work" overrides `+tt.key) {
				t.Fatalf("plan=%+v error=%v; want conflict for %s", p, err, tt.key)
			}
			if got, err := os.ReadFile(ConfigPath()); err != nil || string(got) != original {
				t.Fatalf("Plan changed config: %s, %v", got, err)
			}
			if got, err := os.ReadFile(AuthPath()); err != nil || string(got) != auth {
				t.Fatalf("Plan changed auth: %s, %v", got, err)
			}
		})
	}
}

func TestDirectPreservesCompatibleAndInactiveProfiles(t *testing.T) {
	for _, tt := range []struct {
		name    string
		active  string
		profile func() string
		model   string
		fast    *bool
		effort  string
	}{
		{name: "unselected", profile: func() string { return "model_provider = \"openai\"\nmodel = \"another-model\"" }},
		{name: "other profile", active: "other", profile: func() string { return `model = "another-model"` }},
		{name: "matching provider and model", active: "work", profile: func() string { return "model_provider = \"qcs-cpa\"\nmodel = \"gpt-5.5\"" }},
		{name: "unmodified choices", active: "work", profile: func() string {
			return "service_tier = \"fast\"\nmodel_reasoning_effort = \"high\"\nopenai_base_url = \"https://official.example/codex\""
		}},
		{name: "matching generated catalog", active: "work", model: "alias", profile: func() string { return fmt.Sprintf("model_catalog_json = %q", CatalogPath("cpa")) }},
		{name: "Fast request spelling", active: "work", fast: ptr(true), profile: func() string { return `service_tier = "priority"` }},
		{name: "ordinary service", active: "work", fast: ptr(false), profile: func() string { return `service_tier = "default"` }},
		{name: "matching effort", active: "work", effort: "high", profile: func() string { return `model_reasoning_effort = "high"` }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			model := tt.model
			if model == "" {
				model = "gpt-5.5"
			}
			setup(t, "", provider.Model{ID: model, Fast: true})
			original := "# preserved\nmodel = \"original-model\"\n"
			if tt.active != "" {
				original += "profile = \"" + tt.active + "\"\n"
			}
			profile := "[profiles.work]\n" + tt.profile() + " # mine\n"
			original += profile
			writeFile(t, ConfigPath(), original)
			p := mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: model, Fast: tt.fast, Effort: tt.effort})
			if err := safekeep.Apply(p); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(ConfigPath()); err != nil || !strings.Contains(string(got), profile) {
				t.Fatalf("switch changed profile: %s, %v", got, err)
			}
			if _, err := safekeep.Restore(ID); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(ConfigPath()); err != nil || !strings.Contains(string(got), original) {
				t.Fatalf("restore changed original config/profile: %s, %v", got, err)
			}
		})
	}
}
