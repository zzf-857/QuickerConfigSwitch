package codex

import (
	"os"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func relayTarget() agent.Target {
	return agent.Target{Mode: agent.Relay, Provider: "cpa", Model: "gpt-5.5", RelayURL: "http://127.0.0.1:17321/backend-api/codex"}
}

func TestRelayPlanAndRoundTrip(t *testing.T) {
	for _, original := range []string{fixture, "openai_base_url = \"https://existing.example/codex\" # mine\n" + fixture[3:]} {
		t.Run(original[:8], func(t *testing.T) {
			setup(t, original, provider.Model{ID: "gpt-5.5", Fast: true})
			writeFile(t, AuthPath(), "{\"tokens\":{\"access_token\":\"fixture\"}}")
			before, _ := os.ReadFile(AuthPath())
			p := mustPlan(t, relayTarget())
			if len(p.Edits) != 1 || len(p.Edits[0].Ops) != 1 || p.Edits[0].Ops[0].Key != "openai_base_url" {
				t.Fatalf("relay must only change address: %+v", p)
			}
			if data, _ := os.ReadFile(ConfigPath()); string(data) != original {
				t.Fatal("Plan wrote config")
			}
			if err := safekeep.Apply(p); err != nil {
				t.Fatal(err)
			}
			st, err := New().Current()
			if err != nil || st.Mode != agent.Relay || st.Provider != "cpa" {
				t.Fatalf("current: %+v %v", st, err)
			}
			if _, err := safekeep.Restore(ID); err != nil {
				t.Fatal(err)
			}
			if data, _ := os.ReadFile(ConfigPath()); string(data) != original {
				t.Fatalf("not byte-exact after relay: %s", data)
			}
			if after, _ := os.ReadFile(AuthPath()); string(after) != string(before) {
				t.Fatal("auth changed")
			}
		})
	}
}

func TestRelayRejectsOverridesAndInvalidTargets(t *testing.T) {
	for _, cfg := range []string{
		"model_provider = \"custom\"\n",
		"model_provider = 1\n",
		"model_catalog_json = false\n",
		"openai_base_url = []\n",
		"model_catalog_json = \"/tmp/catalog.json\"\n",
		"profile = \"work\"\n[profiles.work]\nmodel_provider = \"custom\"\n",
		"profile = \"work\"\n[profiles.work]\nopenai_base_url = \"http://elsewhere\"\n",
		"[invalid\n",
	} {
		t.Run(cfg, func(t *testing.T) {
			setup(t, cfg)
			if _, err := New().Plan(relayTarget()); err == nil {
				t.Fatal("accepted overriding or invalid config")
			}
		})
	}
	setup(t, fixture, provider.Model{ID: "gpt-5.5"})
	for _, u := range []string{"", "http://0.0.0.0:17321/backend-api/codex", "https://127.0.0.1:17321/backend-api/codex", "http://127.0.0.1:0/backend-api/codex", "http://127.0.0.1:17321/backend-api/codex?token=x", "http://127.0.0.1:17321/other"} {
		tg := relayTarget()
		tg.RelayURL = u
		if _, err := New().Plan(tg); err == nil {
			t.Fatalf("accepted URL %q", u)
		}
	}
	for _, mutate := range []func(*agent.Target){
		func(tg *agent.Target) { tg.Model = "missing" },
		func(tg *agent.Target) { tg.Provider = "missing" },
		func(tg *agent.Target) { tg.Fast = ptr(true) },
		func(tg *agent.Target) { tg.Effort = "high" },
	} {
		tg := relayTarget()
		mutate(&tg)
		if _, err := New().Plan(tg); err == nil {
			t.Fatalf("accepted target %+v", tg)
		}
	}
}

func TestRelayRestorePreservesUserAddressChange(t *testing.T) {
	setup(t, fixture)
	if err := safekeep.Apply(mustPlan(t, relayTarget())); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	next := strings.ReplaceAll(string(b), relayTarget().RelayURL, "https://user.example/codex")
	writeFile(t, ConfigPath(), next)
	rep, err := safekeep.Restore(ID)
	if err != nil || !rep.Left() {
		t.Fatalf("restore: %+v %v", rep, err)
	}
	if got, _ := os.ReadFile(ConfigPath()); string(got) != next {
		t.Fatalf("overwrote user change: %s", got)
	}
}
