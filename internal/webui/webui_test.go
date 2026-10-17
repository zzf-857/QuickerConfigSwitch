package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

const (
	codexCfg = "# mine\nmodel = \"gpt-5.5\"\n"
	secret   = "sk-very-secret-key-0123456789"
)

// setup gives the test a home with a Codex config and returns the config path.
func setup(t *testing.T) string {
	t.Helper()
	home := testenv.Home(t)
	cx := filepath.Join(home, ".codex")
	os.MkdirAll(cx, 0o700)
	t.Setenv("CODEX_HOME", cx)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	cfg := filepath.Join(cx, "config.toml")
	if err := os.WriteFile(cfg, []byte(codexCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func addCPA(t *testing.T, base string) {
	t.Helper()
	if err := provider.Add(provider.Provider{ID: "cpa", Name: "CPA", BaseURL: base, APIKey: secret,
		APIs:    provider.APIs{Responses: true, Anthropic: "http://127.0.0.1:8317"},
		Headers: map[string]string{"X-Api-Key": "header-secret-value-123456", "X-Team": "ops"},
		Models:  []provider.Model{{ID: "gpt-5.5", Fast: true, Efforts: []string{"low", "high"}}}}); err != nil {
		t.Fatal(err)
	}
}

type client struct {
	t   *testing.T
	h   http.Handler
	hdr map[string]string
}

func newClient(t *testing.T, o Options) *client {
	return &client{t: t, h: Handler(o), hdr: map[string]string{}}
}

func (c *client) raw(method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:8000"+path, rd)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range c.hdr {
		r.Header.Set(k, v)
	}
	for k, v := range hdr {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "header-secret-value-123456") {
		c.t.Fatalf("%s %s leaked a secret: %s", method, path, w.Body)
	}
	return w
}

// do sends a request and decodes the JSON answer, failing on another status.
func (c *client) do(method, path string, body any, want int) map[string]any {
	c.t.Helper()
	w := c.raw(method, path, body, nil)
	if w.Code != want {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, w.Code, want, w.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		var arr []any
		if json.Unmarshal(w.Body.Bytes(), &arr) == nil {
			return map[string]any{"list": arr}
		}
		c.t.Fatalf("%s %s: not JSON: %s", method, path, w.Body)
	}
	return out
}

func get(m any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			mm, _ := m.(map[string]any)
			m = mm[k]
		case int:
			a, _ := m.([]any)
			if k >= len(a) {
				return nil
			}
			m = a[k]
		}
	}
	return m
}

func TestHandlerBuildsAndServes(t *testing.T) {
	setup(t)
	for _, o := range []Options{{}, {Token: "x"}} {
		h := Handler(o)
		for _, path := range []string{"/", "/app.js", "/style.css", "/api/state"} {
			r := httptest.NewRequest("GET", "http://localhost"+path, nil)
			r.Header.Set(TokenHeader, o.Token)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Errorf("token %q GET %s: %d %s", o.Token, path, w.Code, w.Body)
			}
		}
		r := httptest.NewRequest("GET", "http://localhost/logic.test.mjs", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Errorf("test file served: %d", w.Code)
		}
	}
}

func TestState(t *testing.T) {
	setup(t)
	addCPA(t, "http://127.0.0.1:8317/v1")
	c := newClient(t, Options{Version: "1.2.3"})
	st := c.do("GET", "/api/state", nil, 200)
	if st["version"] != "1.2.3" {
		t.Errorf("version %v", st["version"])
	}
	agents, _ := st["agents"].([]any)
	if len(agents) < 2 {
		t.Fatalf("agents %v", st["agents"])
	}
	p := get(st, "providers", 0)
	if get(p, "apiKey") != provider.MaskKey(secret) || get(p, "hasKey") != true {
		t.Errorf("provider %v", p)
	}
	if get(p, "headers", "X-Team") != "ops" {
		t.Errorf("plain header masked: %v", get(p, "headers"))
	}
	if get(st, "profiles", 0, "name") != "official" {
		t.Errorf("profiles %v", st["profiles"])
	}
}

func TestProviderCRUD(t *testing.T) {
	setup(t)
	c := newClient(t, Options{})
	np := provider.Provider{ID: "relay", Name: "Relay", BaseURL: "https://relay.example/v1", APIKey: secret,
		APIs: provider.APIs{Responses: true}, Headers: map[string]string{"Authorization": "Bearer header-secret-value-123456"}}
	res := c.do("POST", "/api/providers", np, 200)
	if get(res, "provider", "apiKey") != provider.MaskKey(secret) {
		t.Errorf("add answer %v", res["provider"])
	}
	c.do("POST", "/api/providers", np, 409)
	c.do("POST", "/api/providers", provider.Provider{ID: "Bad ID"}, 400)

	// Update with the masked key and masked header: both kept.
	up := map[string]any{"name": "Relay 2", "baseURL": "https://relay.example/v1", "apiKey": provider.MaskKey(secret),
		"apis": map[string]any{"responses": true}, "headers": map[string]string{"Authorization": provider.MaskKey("Bearer header-secret-value-123456")}}
	c.do("PUT", "/api/providers/relay", up, 200)
	p, _, _ := provider.Get("relay")
	if p.Name != "Relay 2" || p.APIKey != secret || p.Headers["Authorization"] != "Bearer header-secret-value-123456" {
		t.Fatalf("after masked update: %+v", p)
	}
	// Empty key keeps it too.
	up["apiKey"] = ""
	c.do("PUT", "/api/providers/relay", up, 200)
	if p, _, _ = provider.Get("relay"); p.APIKey != secret {
		t.Fatal("empty apiKey dropped the key")
	}
	// A new key replaces it.
	up["apiKey"] = "sk-new-key-abcdefghijklmnop"
	c.do("PUT", "/api/providers/relay", up, 200)
	if p, _, _ = provider.Get("relay"); p.APIKey != "sk-new-key-abcdefghijklmnop" {
		t.Fatal("new key not saved")
	}
	// clearKey removes it.
	up["apiKey"] = ""
	up["clearKey"] = true
	c.do("PUT", "/api/providers/relay", up, 200)
	if p, _, _ = provider.Get("relay"); p.APIKey != "" {
		t.Fatal("clearKey kept the key")
	}
	up["id"] = "other"
	c.do("PUT", "/api/providers/relay", up, 400)
	delete(up, "id")
	c.do("PUT", "/api/providers/nope", up, 404)

	tpl := c.do("GET", "/api/templates", nil, 200)
	if get(tpl, "list", 0, "id") != "cpa" {
		t.Errorf("templates %v", tpl)
	}

	c.do("DELETE", "/api/providers/relay", map[string]any{}, 200)
	c.do("DELETE", "/api/providers/relay", map[string]any{}, 404)
	if ps, _ := provider.List(); len(ps) != 0 {
		t.Fatalf("left %v", ps)
	}
}

func TestFetchMergesModels(t *testing.T) {
	setup(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(401)
			fmt.Fprintf(w, `{"error":"bad key %s"}`, r.Header.Get("Authorization"))
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"gpt-5.5"},{"id":"o3"},{"id":"claude-x"}]}`)
	}))
	defer up.Close()
	addCPA(t, up.URL+"/v1")
	c := newClient(t, Options{})
	res := c.do("POST", "/api/providers/cpa/fetch", map[string]any{}, 200)
	if fmt.Sprint(res["added"]) != "[claude-x o3]" || res["offered"] != float64(3) {
		t.Errorf("added %v offered %v", res["added"], res["offered"])
	}
	p, _, _ := provider.Get("cpa")
	if len(p.Models) != 3 || p.Models[0].ID != "gpt-5.5" || len(p.Models[0].Efforts) != 2 {
		t.Fatalf("models %+v", p.Models)
	}
	if m, _ := p.Model("o3"); !m.Fast {
		t.Error("o3 not fast by default")
	}
	// Again: nothing new.
	res = c.do("POST", "/api/providers/cpa/fetch", map[string]any{}, 200)
	if fmt.Sprint(res["added"]) != "[]" {
		t.Errorf("added %v", res["added"])
	}
	// An upstream error that echoes the key: 502, key scrubbed.
	p.APIKey = secret + "x"
	provider.Update(p)
	w := c.raw("POST", "/api/providers/cpa/fetch", map[string]any{}, nil)
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), secret) {
		t.Errorf("fetch error: %d %s", w.Code, w.Body)
	}
	c.do("POST", "/api/providers/nope/fetch", map[string]any{}, 404)
}

func TestPreviewUseAndBack(t *testing.T) {
	cfg := setup(t)
	addCPA(t, "http://127.0.0.1:8317/v1")
	c := newClient(t, Options{})
	req := map[string]any{"agent": "codex", "ref": "cpa/gpt-5.5", "fast": true, "effort": "high"}
	pv := c.do("POST", "/api/preview", req, 200)
	if !strings.Contains(fmt.Sprint(pv["diff"]), `model_provider = "qcs-cpa"`) || pv["nothing"] != false {
		t.Fatalf("preview %v", pv)
	}
	if !strings.Contains(fmt.Sprint(pv["diff"]), provider.MaskKey(secret)) {
		t.Errorf("masked key missing from the diff: %v", pv["diff"])
	}
	if b, _ := os.ReadFile(cfg); string(b) != codexCfg {
		t.Fatal("preview wrote")
	}
	res := c.do("POST", "/api/use", req, 200)
	var a any
	for _, view := range get(res, "state", "agents").([]any) {
		if get(view, "id") == "codex" {
			a = view
			break
		}
	}
	if get(a, "provider") != "cpa" || get(a, "model") != "gpt-5.5" || get(a, "fast") != "true" || get(a, "managed") != true {
		t.Fatalf("after use: %v", a)
	}
	doc := c.do("GET", "/api/doctor?agent=codex", nil, 200)
	if _, ok := doc["problems"].([]any); !ok {
		t.Errorf("doctor %v", doc)
	}
	c.do("GET", "/api/doctor?agent=nope", nil, 404)
	c.do("GET", "/api/doctor", nil, 400)

	back := map[string]any{"agent": "codex", "ref": "official"}
	pv = c.do("POST", "/api/preview", back, 200)
	if pv["nothing"] != false {
		t.Fatalf("back preview %v", pv)
	}
	res = c.do("POST", "/api/use", back, 200)
	if get(res, "result", "report", "agent") != "codex" {
		t.Errorf("report %v", res["result"])
	}
	if b, _ := os.ReadFile(cfg); !strings.HasPrefix(string(b), codexCfg) {
		t.Fatalf("not restored:\n%s", b)
	}
	c.do("POST", "/api/use", map[string]any{"agent": "nope", "ref": "official"}, 404)
	c.do("POST", "/api/use", map[string]any{"agent": "codex", "ref": "garbage"}, 400)
	c.do("POST", "/api/use", nil, 415) // no content type, no body
}

func TestProfiles(t *testing.T) {
	cfg := setup(t)
	addCPA(t, "http://127.0.0.1:8317/v1")
	c := newClient(t, Options{})
	c.do("POST", "/api/use", map[string]any{"agent": "codex", "ref": "cpa/gpt-5.5"}, 200)
	res := c.do("POST", "/api/profiles", map[string]any{"name": "work"}, 200)
	if get(res, "profile", "name") != "work" || len(get(res, "state", "profiles").([]any)) != 2 {
		t.Fatalf("save %v", res)
	}
	c.do("POST", "/api/profiles", map[string]any{"name": "official"}, 400)

	steps := c.do("POST", "/api/profiles/official/preview", map[string]any{}, 200)
	if get(steps, "steps", 0, "agent") == nil {
		t.Fatalf("preview %v", steps)
	}
	c.do("POST", "/api/profiles/official/apply", map[string]any{}, 200)
	if b, _ := os.ReadFile(cfg); !strings.HasPrefix(string(b), codexCfg) {
		t.Fatalf("official not applied:\n%s", b)
	}
	res = c.do("POST", "/api/profiles/work/apply", map[string]any{}, 200)
	if b, _ := os.ReadFile(cfg); !strings.Contains(string(b), "qcs-cpa") {
		t.Fatalf("work not applied:\n%s %v", b, res)
	}

	c.do("PUT", "/api/profiles/mine", map[string]any{"agents": []any{map[string]any{"agent": "codex", "ref": "official"}}}, 200)
	c.do("PUT", "/api/profiles/mine", map[string]any{"name": "x"}, 400)
	c.do("PUT", "/api/profiles/bad", map[string]any{"agents": []any{map[string]any{"agent": "nope", "ref": "official"}}}, 404)
	c.do("DELETE", "/api/profiles/mine", map[string]any{}, 200)
	c.do("DELETE", "/api/profiles/mine", map[string]any{}, 404)
	c.do("POST", "/api/profiles/nope/preview", map[string]any{}, 404)
}

func TestImportEmpty(t *testing.T) {
	setup(t)
	c := newClient(t, Options{})
	res := c.do("GET", "/api/import", nil, 200)
	if fmt.Sprint(res["candidates"]) != "[]" || fmt.Sprint(res["errors"]) != "[]" {
		t.Errorf("scan %v", res)
	}
	c.do("POST", "/api/import", map[string]any{"index": 0, "id": "x"}, 404)
	c.do("POST", "/api/import", map[string]any{"id": "x"}, 400)
}

func TestSecurity(t *testing.T) {
	setup(t)
	c := newClient(t, Options{Token: "tok"})

	// No token, wrong token.
	c.do("GET", "/api/state", nil, 401)
	if w := c.raw("GET", "/api/state", nil, map[string]string{TokenHeader: "nope"}); w.Code != 401 {
		t.Errorf("wrong token: %d", w.Code)
	}
	if w := c.raw("GET", "/api/state", nil, map[string]string{TokenHeader: "tok"}); w.Code != 200 {
		t.Errorf("header token: %d", w.Code)
	}
	// The page itself loads without a token (it holds no data).
	if w := c.raw("GET", "/", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "app.js") {
		t.Errorf("page: %d", w.Code)
	}

	// ?token= sets the cookie and redirects without it, keeping other params.
	w := c.raw("GET", "/?mode=panel&token=tok", nil, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/?mode=panel" {
		t.Fatalf("token load: %d %q", w.Code, w.Header().Get("Location"))
	}
	var ck *http.Cookie
	for _, k := range w.Result().Cookies() {
		if k.Name == CookieName {
			ck = k
		}
	}
	if ck == nil || ck.Value != "tok" || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie %+v", ck)
	}
	// A wrong ?token= sets nothing.
	if w := c.raw("GET", "/?token=bad", nil, nil); len(w.Result().Cookies()) != 0 || w.Code != http.StatusSeeOther {
		t.Errorf("bad token load set a cookie")
	}
	if w := c.raw("GET", "/api/state", nil, map[string]string{"Cookie": CookieName + "=tok"}); w.Code != 200 {
		t.Errorf("cookie token: %d", w.Code)
	}
	if w := c.raw("GET", "/api/state", nil, map[string]string{"Cookie": CookieName + "=bad"}); w.Code != 401 {
		t.Errorf("bad cookie: %d", w.Code)
	}

	c.hdr[TokenHeader] = "tok"
	// Origin: same host fine, other refused, on the page too.
	if w := c.raw("GET", "/api/state", nil, map[string]string{"Origin": "http://127.0.0.1:8000"}); w.Code != 200 {
		t.Errorf("same origin: %d", w.Code)
	}
	for _, o := range []string{"http://evil.example", "http://127.0.0.1:9999", "null"} {
		if w := c.raw("POST", "/api/use", map[string]any{"agent": "codex", "ref": "official"}, map[string]string{"Origin": o}); w.Code != 403 {
			t.Errorf("origin %s: %d", o, w.Code)
		}
	}
	// Host: a rebound DNS name is refused when a token is set.
	for host, want := range map[string]int{"evil.example:8000": 403, "localhost:8000": 200, "[::1]:8000": 200, "127.0.0.1": 200} {
		r := httptest.NewRequest("GET", "http://x/api/state", nil)
		r.Host = host
		r.Header.Set(TokenHeader, "tok")
		w := httptest.NewRecorder()
		c.h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("host %s: %d, want %d", host, w.Code, want)
		}
	}
	// Mutations need JSON.
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data"} {
		w := c.raw("POST", "/api/use", map[string]any{"agent": "codex", "ref": "official"}, map[string]string{"Content-Type": ct})
		if w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("content type %q: %d", ct, w.Code)
		}
	}
	if w := c.raw("POST", "/api/use", map[string]any{"agent": "codex", "ref": "official"}, map[string]string{"Content-Type": "application/json; charset=utf-8"}); w.Code != 200 {
		t.Errorf("json with charset: %d %s", w.Code, w.Body)
	}
	if w := c.raw("DELETE", "/api/providers/x", nil, nil); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("delete without json: %d", w.Code)
	}
	// Wrong method on a route: 405 from the mux; unknown: 404 JSON.
	if w := c.raw("GET", "/api/use", nil, nil); w.Code != 405 && w.Code != 404 {
		t.Errorf("GET /api/use: %d", w.Code)
	}
	if w := c.raw("PATCH", "/api/state", map[string]any{}, nil); w.Code != 405 {
		t.Errorf("PATCH: %d", w.Code)
	}
	if w := c.raw("GET", "/api/nope", nil, nil); w.Code != 404 || !strings.Contains(w.Body.String(), `"error"`) {
		t.Errorf("unknown: %d %s", w.Code, w.Body)
	}
	if w := c.raw("POST", "/", map[string]any{}, nil); w.Code != 405 {
		t.Errorf("POST /: %d", w.Code)
	}

	// Without a token, the Host isn't checked (the desktop webview) but
	// Origin still is, with AllowOrigins honoured.
	d := newClient(t, Options{AllowOrigins: []string{"wails://wails"}})
	r := httptest.NewRequest("GET", "http://wails.localhost/api/state", nil)
	r.Header.Set("Origin", "wails://wails")
	rw := httptest.NewRecorder()
	d.h.ServeHTTP(rw, r)
	if rw.Code != 200 {
		t.Errorf("desktop origin: %d", rw.Code)
	}
	if w := d.raw("GET", "/api/state", nil, map[string]string{"Origin": "http://evil.example"}); w.Code != 403 {
		t.Errorf("desktop evil origin: %d", w.Code)
	}
}

func TestBodyLimitAndBadJSON(t *testing.T) {
	setup(t)
	c := newClient(t, Options{})
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/preview", strings.NewReader("{"))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Errorf("bad json: %d", w.Code)
	}
	big := `{"agent":"` + strings.Repeat("a", 2<<20) + `"}`
	r = httptest.NewRequest("POST", "http://127.0.0.1/api/preview", strings.NewReader(big))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("big body: %d", w.Code)
	}
}

func TestMaskText(t *testing.T) {
	setup(t)
	in := "+experimental_bearer_token = \"abc-unknown-token-value\"\n" +
		"+    \"ANTHROPIC_AUTH_TOKEN\": \"some-long-token-1234567\",\n" +
		"-key: sk-ant-abcdefghijklmnop\n" +
		"+model = \"gpt-5.5\"\n"
	out := maskText(in)
	for _, s := range []string{"abc-unknown-token-value", "some-long-token-1234567", "sk-ant-abcdefghijklmnop"} {
		if strings.Contains(out, s) {
			t.Errorf("not masked %q:\n%s", s, out)
		}
	}
	if !strings.Contains(out, `model = "gpt-5.5"`) || maskText(out) != out {
		t.Errorf("over-masked or not idempotent:\n%s", out)
	}
}
