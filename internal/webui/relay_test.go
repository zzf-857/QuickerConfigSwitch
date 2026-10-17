package webui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/agent/codex"
	"github.com/zzf-857/quickerconfigswitch/internal/core"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func relayClient(t *testing.T) (*client, *core.RelayController, string) {
	t.Helper()
	cfg := setup(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("incorrect provider request")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["model"] != "gpt-5.5" || body["service_tier"] != "priority" {
			t.Error("model or Fast changed")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"fixture-response"}`))
	}))
	t.Cleanup(upstream.Close)
	addCPA(t, upstream.URL+"/v1")
	control := core.NewRelayController()
	t.Cleanup(func() { control.Close() })
	c := newClient(t, Options{Token: "dev", Relay: control})
	c.hdr[TokenHeader] = "dev"
	return c, control, cfg
}

func previewRelayBody() map[string]any {
	return map[string]any{"provider": "cpa", "model": "gpt-5.5", "port": 0}
}

func TestRelayAPIExactPreviewAndSharedOwner(t *testing.T) {
	c, control, cfg := relayClient(t)
	second := newClient(t, Options{Relay: control})
	pv := c.do("POST", "/api/relay/preview", previewRelayBody(), http.StatusOK)
	id, _ := pv["id"].(string)
	url, _ := pv["url"].(string)
	if id == "" || !strings.Contains(url, "127.0.0.1:") || !strings.Contains(pv["diff"].(string), "openai_base_url") {
		t.Fatalf("preview: %+v", pv)
	}
	if data, _ := os.ReadFile(cfg); string(data) != codexCfg {
		t.Fatal("preview wrote config")
	}
	second.do("POST", "/api/relay/preview", previewRelayBody(), http.StatusConflict)
	second.do("POST", "/api/use", core.Request{Agent: "codex", Ref: "official"}, http.StatusConflict)
	second.do("POST", "/api/profiles/official/apply", map[string]any{}, http.StatusConflict)
	second.do("POST", "/api/use", core.Request{Agent: "claude", Ref: "official"}, http.StatusOK)
	c.do("POST", "/api/relay/start", map[string]string{"id": "wrong"}, http.StatusBadRequest)
	res := c.do("POST", "/api/relay/start", map[string]string{"id": id}, http.StatusOK)
	if get(res, "state", "relay", "state") != "running" || get(res, "state", "relay", "url") != url {
		t.Fatalf("running state: %+v", res)
	}
	if data, _ := os.ReadFile(cfg); !strings.Contains(string(data), url) {
		t.Fatal("did not apply the previewed port")
	}
	resp, err := http.Post(url+"/responses", "application/json", strings.NewReader(`{"model":"cpa/gpt-5.5","service_tier":"priority"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("relay response %d", resp.StatusCode)
	}
	c.do("POST", "/api/relay/start", map[string]string{"id": id}, http.StatusBadRequest)
	second.do("POST", "/api/relay/stop", map[string]any{}, http.StatusOK)
	if data, _ := os.ReadFile(cfg); string(data) != codexCfg || safekeep.Touched("codex") {
		t.Fatal("stop did not restore original")
	}
}

func TestRelayAPICancelAndChangedPreview(t *testing.T) {
	c, _, cfg := relayClient(t)
	pv := c.do("POST", "/api/relay/preview", previewRelayBody(), http.StatusOK)
	id := map[string]any{"id": pv["id"]}
	c.do("POST", "/api/relay/cancel", id, http.StatusOK)
	c.do("POST", "/api/relay/start", id, http.StatusBadRequest)
	pv = c.do("POST", "/api/relay/preview", previewRelayBody(), http.StatusOK)
	changed := codexCfg + "# edited while reading preview\n"
	if err := os.WriteFile(cfg, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	c.do("POST", "/api/relay/start", map[string]any{"id": pv["id"]}, http.StatusBadRequest)
	if data, _ := os.ReadFile(cfg); string(data) != changed {
		t.Fatal("start overwrote a change made after preview")
	}
	// A failed preview/start releases ownership so a fresh attempt works.
	c.do("POST", "/api/relay/preview", previewRelayBody(), http.StatusOK)
}

func TestRelayAPIExternalStaleAndShutdown(t *testing.T) {
	c, control, cfg := relayClient(t)
	external := core.NewRelayController()
	t.Cleanup(func() { external.Close() })
	pv, err := external.Preview(agent.Target{Provider: "cpa", Model: "gpt-5.5"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := external.Start(pv.ID); err != nil {
		t.Fatal(err)
	}
	res := c.do("GET", "/api/state", nil, http.StatusOK)
	if get(res, "relay", "state") != "external" {
		t.Fatalf("external state: %+v", res)
	}
	c.do("POST", "/api/relay/stop", map[string]any{}, http.StatusConflict)
	c.do("POST", "/api/relay/recover", map[string]any{}, http.StatusConflict)
	if err := external.Close(); err != nil {
		t.Fatal(err)
	}
	p, err := codex.New().Plan(agent.Target{Mode: agent.Relay, Provider: "cpa", Model: "gpt-5.5", RelayURL: "http://127.0.0.1:17321/backend-api/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err := safekeep.Apply(p); err != nil {
		t.Fatal(err)
	}
	res = c.do("GET", "/api/state", nil, http.StatusOK)
	if get(res, "relay", "state") != "stale" {
		t.Fatalf("stale state: %+v", res)
	}
	c.do("POST", "/api/relay/recover", map[string]any{}, http.StatusOK)
	pending := c.do("POST", "/api/relay/preview", previewRelayBody(), http.StatusOK)
	c.do("POST", "/api/relay/start", map[string]any{"id": pending["id"]}, http.StatusOK)
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(cfg); string(data) != codexCfg {
		t.Fatal("owner shutdown did not restore")
	}
	c.do("POST", "/api/relay/preview", previewRelayBody(), http.StatusConflict)
}

func TestRelayAPISameAuthenticationAndUnavailableHost(t *testing.T) {
	c, _, _ := relayClient(t)
	for _, path := range []string{"preview", "start", "cancel", "stop", "recover"} {
		endpoint := "/api/relay/" + path
		if w := c.raw("POST", endpoint, map[string]any{}, map[string]string{TokenHeader: ""}); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s allowed no token: %d", endpoint, w.Code)
		}
		if w := c.raw("POST", endpoint, map[string]any{}, map[string]string{"Origin": "https://foreign.example"}); w.Code != http.StatusForbidden {
			t.Fatalf("%s allowed foreign origin: %d", endpoint, w.Code)
		}
	}
	without := newClient(t, Options{})
	without.do("POST", "/api/relay/preview", previewRelayBody(), http.StatusServiceUnavailable)
	st := without.do("GET", "/api/state", nil, http.StatusOK)
	if get(st, "relay", "state") != "unavailable" {
		t.Fatalf("unavailable state: %+v", st)
	}
	if !bytes.Contains(c.raw("GET", "/api/state", nil, nil).Body.Bytes(), []byte(`"relay"`)) {
		t.Fatal("relay state missing")
	}
}
