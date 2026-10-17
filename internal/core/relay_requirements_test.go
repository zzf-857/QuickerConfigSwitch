package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

type requirementTransport func(*http.Request) (*http.Response, error)

func (f requirementTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise the requested combination through the real session lifecycle:
// existing official login + no model fetch + all model requests via CPA + Fast.
// Only the official model-list transport is replaced; no real accounts/network.
func TestRelayAllPreservesLoginAndFast(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models []provider.Model
		fast   bool
	}{
		{name: "before model fetch", fast: true},
		{name: "explicit capability off", models: []provider.Model{{ID: "gpt-5.5"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setup(t)
			original := codexCfg + "[features]\nfast_mode = true # original\n[mcp_servers.mine]\ncommand = \"mine\"\n"
			if err := os.WriteFile(cfg, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			auth := filepath.Join(appdir.CodexHome(), "auth.json")
			const login = `{"auth_mode":"chatgpt","tokens":{"access_token":"fixture-login","refresh_token":"fixture-refresh"}}`
			if err := os.WriteFile(auth, []byte(login), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(auth)
			if err != nil {
				t.Fatal(err)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string `json:"model"`
					Tier  string `json:"service_tier"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Model != "gpt-5.5" || body.Tier != "priority" || r.Header.Get("Authorization") != "Bearer k" ||
					r.Header.Get("ChatGPT-Account-ID") != "" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" {
					t.Errorf("CPA received wrong model/tier or official credentials: %s", r.URL.Path)
				}
				if r.URL.Path != "/v1/responses" && r.URL.Path != "/v1/responses/compact" {
					t.Errorf("CPA path %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"fixture-response"}`)
			}))
			defer upstream.Close()
			p, _, _ := provider.Get("cpa")
			p.BaseURL, p.Models = upstream.URL+"/v1", tc.models
			if err := provider.Update(p); err != nil {
				t.Fatal(err)
			}
			transport := http.DefaultTransport
			http.DefaultTransport = requirementTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "chatgpt.com" {
					if r.URL.Path != "/backend-api/codex/models" || r.Header.Get("Authorization") != "Bearer fixture-login" ||
						r.Header.Get("ChatGPT-Account-ID") != "fixture-account" {
						t.Errorf("official model request changed: %s", r.URL.Path)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"models":[{"slug":"gpt-5.5","service_tiers":[{"id":"priority","name":"Fast"}],"official_marker":true}]}`)), Request: r}, nil
				}
				return transport.RoundTrip(r)
			})
			defer func() { http.DefaultTransport = transport }()
			s, err := StartRelay(agent.Target{Mode: agent.Relay, Provider: "cpa", Model: "gpt-5.5", RelayAll: true}, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			req, _ := http.NewRequest(http.MethodGet, s.URL+"/models", nil)
			req.Header.Set("Authorization", "Bearer fixture-login")
			req.Header.Set("ChatGPT-Account-ID", "fixture-account")
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var catalog struct {
				Models []struct {
					Slug   string            `json:"slug"`
					Tiers  []json.RawMessage `json:"service_tiers"`
					Marker bool              `json:"official_marker"`
				} `json:"models"`
			}
			err = json.NewDecoder(res.Body).Decode(&catalog)
			res.Body.Close()
			if err != nil || res.StatusCode != http.StatusOK || len(catalog.Models) != 2 ||
				!catalog.Models[0].Marker || len(catalog.Models[0].Tiers) != 1 ||
				catalog.Models[1].Slug != "cpa/gpt-5.5" || (len(catalog.Models[1].Tiers) > 0) != tc.fast {
				t.Fatalf("official/CPA Fast catalog: %+v, %v", catalog, err)
			}
			for _, path := range []string{"/responses", "/responses/compact"} {
				for _, model := range []string{"gpt-5.5", "cpa/gpt-5.5"} {
					req, _ := http.NewRequest(http.MethodPost, s.URL+path+"?account_id=fixture-account",
						strings.NewReader(`{"model":"`+model+`","service_tier":"priority"}`))
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("Authorization", "Bearer fixture-login")
					req.Header.Set("ChatGPT-Account-ID", "fixture-account")
					req.Header.Set("Cookie", "session=fixture")
					res, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(res.Body)
					res.Body.Close()
					if err != nil || res.StatusCode != http.StatusOK || string(body) != `{"id":"fixture-response"}` {
						t.Fatalf("CPA response %s %s: %s, %v", path, model, body, err)
					}
				}
			}
			if _, err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if readRelayFile(t, cfg) != original || readRelayFile(t, auth) != login {
				t.Fatal("original configuration or official login changed")
			}
			after, err := os.Stat(auth)
			if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
				t.Fatalf("official login identity/mtime/permissions changed: %v", err)
			}
		})
	}
}
