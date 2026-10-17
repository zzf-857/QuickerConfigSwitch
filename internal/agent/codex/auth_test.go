package codex

import (
	"os"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func TestDirectRejectsInheritedAuthOverrides(t *testing.T) {
	for _, tc := range []struct{ name, config, key string }{
		{"official auth", "requires_openai_auth = true\n", "requires_openai_auth"},
		{"wrong auth type", "requires_openai_auth = \"fixture-secret\"\n", "requires_openai_auth"},
		{"environment outranks bearer", "env_key = \"QCS_FIXTURE_SECRET\"\n", "env_key"},
		{"empty environment name", "env_key = \"\"\n", "env_key"},
		{"command auth", "auth = {command = \"fixture-never-executed\"}\n", "auth"},
		{"gateway oauth", "gateway_oauth = {client_id = \"fixture-secret\"}\n", "gateway_oauth"},
		{"aws auth", "aws = {profile = \"fixture-secret\"}\n", "aws"},
		{"dynamic authorization", "env_http_headers = {Authorization = \"QCS_FIXTURE_SECRET\"}\n", "env_http_headers"},
		{"dynamic account", "env_http_headers = {\"cHaTgPt-AcCoUnT-Id\" = \"QCS_FIXTURE_SECRET\"}\n", "env_http_headers"},
		{"dynamic cookie", "env_http_headers = {Cookie = \"QCS_FIXTURE_SECRET\"}\n", "env_http_headers"},
		{"static header overridden", "env_http_headers = {\"x-route\" = \"QCS_FIXTURE_SECRET\"}\n", "env_http_headers"},
		{"invalid dynamic shape", "env_http_headers = false\n", "env_http_headers"},
		{"invalid dynamic value", "env_http_headers = {Trace = true}\n", "env_http_headers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := "model = \"gpt-5.5\"\nservice_tier = \"fast\"\n[model_providers.qcs-cpa]\nname = \"original\"\nbase_url = \"https://original.example/v1\"\n" + tc.config
			setup(t, original)
			p, _, err := provider.Get("cpa")
			if err != nil {
				t.Fatal(err)
			}
			p.Headers = map[string]string{"X-Route": "explicit"}
			if err := provider.Update(p); err != nil {
				t.Fatal(err)
			}
			const auth = `{"tokens":{"access_token":"fixture-only-login"}}`
			writeFile(t, AuthPath(), auth)
			_, err = New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("inherited auth accepted or unclear error: %v", err)
			}
			for _, secret := range []string{"fixture-secret", "QCS_FIXTURE_SECRET", "fixture-never-executed", "fixture-only-login"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error exposed %s", secret)
				}
			}
			if b, _ := os.ReadFile(ConfigPath()); string(b) != original {
				t.Fatal("rejected plan modified original config")
			}
			if b, _ := os.ReadFile(AuthPath()); string(b) != auth {
				t.Fatal("rejected plan modified login")
			}
		})
	}
}

func TestDirectKeepsUnrelatedProviderOverrides(t *testing.T) {
	original := "model = \"gpt-5.5\"\nservice_tier = \"fast\"\n[model_providers.qcs-cpa]\n" +
		"name = \"original\"\nbase_url = \"https://original.example/v1\"\nrequires_openai_auth = false\nrequest_max_retries = 7 # mine\n" +
		"env_http_headers = {\"X-Trace\" = \"QCS_FIXTURE_TRACE\"} # mine too\n"
	setup(t, original)
	p := mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})
	if err := safekeep.Apply(p); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"requires_openai_auth = false", "request_max_retries = 7 # mine", "env_http_headers = {\"X-Trace\" = \"QCS_FIXTURE_TRACE\"} # mine too"} {
		if b, _ := os.ReadFile(ConfigPath()); !strings.Contains(string(b), text) {
			t.Fatalf("unrelated override changed: %s", text)
		}
	}
	if _, err := safekeep.Restore(ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(ConfigPath()); strings.Contains(string(b), "sk-secret-cpa") {
		t.Fatal("provider key retained after restore")
	}
}

func TestDirectRejectsRetainedStaticCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		headers      map[string]string
	}{
		{"inline authorization", `http_headers = {Authorization = "fixture-original-secret"}`, nil},
		{"inline cookie", `http_headers = {Cookie = "fixture-original-secret"}`, nil},
		{"child authorization", "[model_providers.qcs-cpa.http_headers]\nAuthorization = \"fixture-original-secret\"", nil},
		{"child api key", "[model_providers.qcs-cpa.http_headers]\n\"X-Api-Key\" = \"fixture-original-secret\"", map[string]string{"X-Route": "chosen"}},
		{"child cookie", "[model_providers.qcs-cpa.http_headers]\nCookie = \"fixture-original-secret\"", map[string]string{"Authorization": "Bearer chosen"}},
		{"child account", "[model_providers.qcs-cpa.http_headers]\n\"ChatGPT-Account-Id\" = \"fixture-original-secret\"", nil},
		{"child different casing", "[model_providers.qcs-cpa.http_headers]\nauthorization = \"fixture-original-secret\"", map[string]string{"Authorization": "Bearer chosen"}},
		{"child duplicate casing", "[model_providers.qcs-cpa.http_headers]\nAuthorization = \"fixture-original-secret\"\nauthorization = \"fixture-original-secret\"", map[string]string{"Authorization": "Bearer chosen"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := "model = \"gpt-5.5\"\n[model_providers.qcs-cpa]\nbase_url = \"https://original.example/v1\"\n" + tc.config + "\n"
			setup(t, original)
			p, _, err := provider.Get("cpa")
			if err != nil {
				t.Fatal(err)
			}
			p.Headers = tc.headers
			if err := provider.Update(p); err != nil {
				t.Fatal(err)
			}
			const auth = `{"tokens":{"access_token":"fixture-only-login"}}`
			writeFile(t, AuthPath(), auth)
			_, err = New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})
			if err == nil || !strings.Contains(err.Error(), "http_headers") {
				t.Fatalf("retained credential accepted or unclear error: %v", err)
			}
			if strings.Contains(err.Error(), "fixture-original-secret") || strings.Contains(err.Error(), "fixture-only-login") {
				t.Fatal("refusal exposed a saved credential")
			}
			if b, _ := os.ReadFile(ConfigPath()); string(b) != original {
				t.Fatal("refused switch modified original config")
			}
			if b, _ := os.ReadFile(AuthPath()); string(b) != auth {
				t.Fatal("refused switch modified official login")
			}
			if safekeep.Touched(ID) {
				t.Fatal("refused switch created management records")
			}
		})
	}
}

func TestDirectStaticHeadersSafeReplacement(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		headers      map[string]string
	}{
		{"inline harmless", `http_headers = {"X-Trace" = "keep"}`, nil},
		{"child harmless", "[model_providers.qcs-cpa.http_headers]\n\"X-Trace\" = \"keep\"", map[string]string{"Authorization": "Bearer chosen"}},
		{"child exact replacement", "[model_providers.qcs-cpa.http_headers]\nAuthorization = \"fixture-original-secret\"\nCookie = \"fixture-cookie\"", map[string]string{"Authorization": "Bearer chosen", "Cookie": "chosen-cookie"}},
		{"inline whole replacement", `http_headers = {Authorization = "fixture-original-secret", Cookie = "fixture-cookie"}`, map[string]string{"X-Route": "chosen"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := "model = \"gpt-5.5\"\n[model_providers.qcs-cpa]\nbase_url = \"https://original.example/v1\"\n" + tc.config + "\n"
			setup(t, original)
			p, _, err := provider.Get("cpa")
			if err != nil {
				t.Fatal(err)
			}
			p.Headers = tc.headers
			if err := provider.Update(p); err != nil {
				t.Fatal(err)
			}
			if err := safekeep.Apply(mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})); err != nil {
				t.Fatal(err)
			}
			cfg, err := readConfig(ConfigPath())
			if err != nil || cfg.err != nil {
				t.Fatalf("written config is invalid: %v %v", err, cfg.err)
			}
			headers := cfg.providers()["qcs-cpa"]["http_headers"].(map[string]any)
			for key, value := range p.Headers {
				if headers[key] != value {
					t.Fatalf("explicit header %s was not selected", key)
				}
			}
			for _, value := range headers {
				if value == "fixture-original-secret" || value == "fixture-cookie" {
					t.Fatal("original credential retained in selected provider")
				}
			}
			if _, err := safekeep.Restore(ID); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(ConfigPath()); string(b) != original {
				t.Fatal("safe replacement did not restore the original config")
			}
		})
	}
}

func TestDirectDoesNotInheritOldBearerWithoutProviderKey(t *testing.T) {
	for _, tc := range []struct {
		name, token string
		headers     map[string]string
	}{
		{"anonymous endpoint", `"fixture-original-secret"`, nil},
		{"header authentication", `"fixture-original-secret"`, map[string]string{"Authorization": "Bearer chosen"}},
		{"wrong bearer type", `true`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := "model = \"gpt-5.5\"\n[model_providers.qcs-cpa]\nbase_url = \"https://original.example/v1\"\nexperimental_bearer_token = " + tc.token + "\n"
			setup(t, original)
			p, _, err := provider.Get("cpa")
			if err != nil {
				t.Fatal(err)
			}
			p.APIKey, p.Headers = "", tc.headers
			if err := provider.Update(p); err != nil {
				t.Fatal(err)
			}
			const auth = `{"tokens":{"access_token":"fixture-only-login"}}`
			writeFile(t, AuthPath(), auth)
			_, err = New().Plan(agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})
			if err == nil || !strings.Contains(err.Error(), "experimental_bearer_token") {
				t.Fatalf("inherited bearer accepted or unclear error: %v", err)
			}
			if strings.Contains(err.Error(), "fixture-original-secret") {
				t.Fatal("refusal exposed the original bearer")
			}
			if b, _ := os.ReadFile(ConfigPath()); string(b) != original {
				t.Fatal("refused switch modified the original config")
			}
			if b, _ := os.ReadFile(AuthPath()); string(b) != auth {
				t.Fatal("refused switch modified official login")
			}
			if safekeep.Touched(ID) {
				t.Fatal("refused switch created management records")
			}
		})
	}
	const original = "model = \"gpt-5.5\"\n[model_providers.qcs-cpa]\nexperimental_bearer_token = \"\" # no credential\n"
	setup(t, original)
	p, _, err := provider.Get("cpa")
	if err != nil {
		t.Fatal(err)
	}
	p.APIKey = ""
	if err := provider.Update(p); err != nil {
		t.Fatal(err)
	}
	if err := safekeep.Apply(mustPlan(t, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})); err != nil {
		t.Fatal(err)
	}
	if _, err := safekeep.Restore(ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(ConfigPath()); string(b) != original {
		t.Fatal("empty bearer was not preserved through the switch")
	}
}
