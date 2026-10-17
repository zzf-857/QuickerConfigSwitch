package relay

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

type received struct {
	path, query, body string
	header            http.Header
}

func upstream(t *testing.T) (*httptest.Server, <-chan received) {
	t.Helper()
	requests := make(chan received, 10)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read failed", http.StatusBadRequest)
			return
		}
		requests <- received{r.URL.EscapedPath(), r.URL.RawQuery, string(body), r.Header.Clone()}
		w.Header().Set("X-Upstream-Request", "kept")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	t.Cleanup(s.Close)
	return s, requests
}

func testProvider(base string) provider.Provider {
	return provider.Provider{
		ID: "cpa", Name: "CPA", BaseURL: base + "/v1/", APIKey: "provider-secret",
		APIs:    provider.APIs{Responses: true},
		Headers: map[string]string{"X-Team": "configured", "Authorization": "must-be-replaced"},
		Models:  []provider.Model{{ID: "gpt-5.5", Fast: true}, {ID: "vendor/model"}},
	}
}

func start(t *testing.T, o Options) *httptest.Server {
	t.Helper()
	h, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func TestResponsesRouting(t *testing.T) {
	for _, tc := range []struct {
		name, model, path, wantModel string
		all, custom                  bool
	}{
		{"official", "gpt-5.5", "/responses", "gpt-5.5", false, false},
		{"alias", "cpa/gpt-5.5", "/responses", "gpt-5.5", false, true},
		{"compact", "cpa/vendor/model", "/responses/compact", "vendor/model", false, true},
		{"official compact", "gpt-5.5", "/responses/compact", "gpt-5.5", false, false},
		{"all plain", "unlisted-model", "/responses", "unlisted-model", true, true},
		{"all alias", "cpa/gpt-5.5", "/responses", "gpt-5.5", true, true},
		{"other prefix", "vendor/model", "/responses", "vendor/model", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			official, officialRequests := upstream(t)
			custom, customRequests := upstream(t)
			s := start(t, Options{Provider: testProvider(custom.URL), All: tc.all, OfficialURL: official.URL + codexPath})
			body := fmt.Sprintf(`{ "model": %q, "service_tier": "priority", "input": [{"value":9007199254740993}], "unknown":{"number":1.2300e+20}, "stream":true }`, tc.model)
			r, err := http.NewRequest(http.MethodPost, s.URL+codexPath+tc.path+"?client_version=1&tag=a%2Fb&tag=c", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range map[string]string{
				"Authorization": "Bearer chatgpt-secret", "ChatGPT-Account-Id": "account-secret",
				"Cookie": "session=cookie-secret", "X-API-Key": "extra-secret", "OpenAI-Organization": "org-secret",
				"X-Codex-Session-Id": "session-secret", "Session_id": "session-secret", "OpenAI-Beta": "responses=v1",
				"Content-Type": "application/json", "Accept": "text/event-stream", "X-Team": "incoming",
				"Forwarded": "for=account-secret", "X-Forwarded-For": "account-secret",
			} {
				r.Header.Set(k, v)
			}
			resp, err := s.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Upstream-Request") != "kept" {
				t.Fatalf("response: %s, headers %v", resp.Status, resp.Header)
			}
			requests, other := officialRequests, customRequests
			wantPath := codexPath + tc.path
			if tc.custom {
				requests, other = customRequests, officialRequests
				wantPath = "/v1" + tc.path
			}
			got := <-requests
			select {
			case <-other:
				t.Fatal("request reached both upstreams")
			default:
			}
			wantQuery := "client_version=1&tag=a%2Fb&tag=c"
			if tc.custom {
				wantQuery = ""
			}
			if got.path != wantPath || got.query != wantQuery {
				t.Errorf("target = %s?%s", got.path, got.query)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(got.body), &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["model"]) != fmt.Sprintf("%q", tc.wantModel) || string(fields["service_tier"]) != `"priority"` || string(fields["stream"]) != "true" {
				t.Errorf("model/tier/stream changed: %s", got.body)
			}
			if !strings.Contains(got.body, "9007199254740993") || !strings.Contains(got.body, "1.2300e+20") {
				t.Errorf("numeric precision changed: %s", got.body)
			}
			if tc.custom {
				if got.header.Get("Authorization") != "Bearer provider-secret" || got.header.Get("X-Team") != "configured" || got.header.Get("OpenAI-Beta") != "responses=v1" {
					t.Errorf("provider headers: %v", got.header)
				}
				for _, k := range []string{"ChatGPT-Account-Id", "Cookie", "X-API-Key", "OpenAI-Organization", "X-Codex-Session-Id", "Session_id", "Forwarded", "X-Forwarded-For"} {
					if got.header.Get(k) != "" {
						t.Errorf("ChatGPT header %s leaked", k)
					}
				}
			} else if got.body != body || got.header.Get("Authorization") != "Bearer chatgpt-secret" || got.header.Get("ChatGPT-Account-Id") != "account-secret" {
				t.Errorf("official request changed: %+v", got)
			}
		})
	}
}

func TestModelsMergePreservesMetadataAndOwnEntry(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprintf("gzip=%v", compressed), func(t *testing.T) {
			codexHome := t.TempDir()
			if err := os.WriteFile(filepath.Join(codexHome, "models_cache.json"), []byte(`{"models":[{"slug":"gpt-5.5","base_instructions":"own instructions","future_capability":true,"service_tiers":[{"id":"priority","name":"Fast"}]}]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			var request received
			official := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request = received{path: r.URL.Path, query: r.URL.RawQuery, header: r.Header.Clone()}
				w.Header().Set("X-Request-Id", "official-id")
				w.Header().Set("ETag", `"original"`)
				w.Header().Set("Last-Modified", "yesterday")
				var dest io.Writer = w
				if compressed {
					w.Header().Set("Content-Encoding", "gzip")
					gz := gzip.NewWriter(w)
					defer gz.Close()
					dest = gz
				}
				fmt.Fprint(dest, `{"models":[{"slug":"gpt-5.5","future":{"n":9007199254740993},"service_tiers":[{"id":"priority","name":"Fast"}]}],"cursor":9007199254740993,"future_meta":{"keep":true}}`)
			}))
			defer official.Close()
			s := start(t, Options{Provider: testProvider("http://127.0.0.1:1"), OfficialURL: official.URL + codexPath, CodexHome: codexHome})
			r, _ := http.NewRequest(http.MethodGet, s.URL+codexPath+"/models?client_version=123", nil)
			r.Header.Set("Authorization", "Bearer official-key")
			r.Header.Set("ChatGPT-Account-Id", "account")
			r.Header.Set("If-None-Match", `"old"`)
			resp, err := s.Client().Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %s: %s", resp.Status, body)
			}
			var result struct {
				Models []map[string]json.RawMessage `json:"models"`
				Cursor json.RawMessage              `json:"cursor"`
				Meta   json.RawMessage              `json:"future_meta"`
			}
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Models) != 3 || string(result.Cursor) != "9007199254740993" || string(result.Meta) != `{"keep":true}` {
				t.Fatalf("catalog metadata lost: %s", body)
			}
			if string(result.Models[0]["slug"]) != `"gpt-5.5"` || string(result.Models[0]["future"]) != `{"n":9007199254740993}` {
				t.Errorf("official entry changed: %s", body)
			}
			alias := result.Models[1]
			if string(alias["slug"]) != `"cpa/gpt-5.5"` || string(alias["display_name"]) != `"cpa/gpt-5.5"` || string(alias["base_instructions"]) != `"own instructions"` || string(alias["future_capability"]) != "true" || !strings.Contains(string(alias["service_tiers"]), "priority") {
				t.Errorf("alias lost own model fields: %s", body)
			}
			if string(result.Models[2]["slug"]) != `"cpa/vendor/model"` || string(result.Models[2]["service_tiers"]) != "[]" {
				t.Errorf("non-Fast alias changed: %s", body)
			}
			if resp.Header.Get("ETag") != "" || resp.Header.Get("Content-Encoding") != "" || resp.Header.Get("Last-Modified") != "" || resp.Header.Get("X-Request-Id") != "official-id" {
				t.Errorf("catalog headers: %v", resp.Header)
			}
			if request.header.Get("Authorization") != "Bearer official-key" || request.header.Get("ChatGPT-Account-Id") != "account" || request.header.Get("If-None-Match") != "" || request.query != "client_version=123" {
				t.Errorf("official catalog request: %+v", request)
			}
		})
	}
}

func TestResponsesStreamBeforeUpstreamCompletes(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprintf("custom=%v", custom), func(t *testing.T) {
			done := make(chan struct{})
			defer close(done)
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: first\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-done:
				case <-r.Context().Done():
				}
			}))
			t.Cleanup(up.Close)
			s := start(t, Options{Provider: testProvider(up.URL), OfficialURL: up.URL + codexPath})
			model := "gpt-5.5"
			if custom {
				model = "cpa/" + model
			}
			client := s.Client()
			client.Timeout = 3 * time.Second
			resp, err := client.Post(s.URL+codexPath+"/responses", "application/json", strings.NewReader(fmt.Sprintf(`{"model":%q}`, model)))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			line, err := bufio.NewReader(resp.Body).ReadString('\n')
			if err != nil || line != "data: first\n" || resp.Header.Get("Content-Type") != "text/event-stream" {
				t.Fatalf("first SSE event: %q, %v", line, err)
			}
		})
	}
}

func TestGuardsAndInvalidBodies(t *testing.T) {
	h, err := New(Options{Provider: testProvider("http://127.0.0.1:1")})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, body, host, origin, upgrade string
		want                                            int
	}{
		{name: "host", method: "GET", path: "/models", host: "evil.example", want: 403},
		{name: "origin", method: "GET", path: "/models", origin: "https://evil.example", want: 403},
		{name: "null origin", method: "GET", path: "/models", origin: "null", want: 403},
		{name: "websocket", method: "GET", path: "/responses", upgrade: "websocket", want: 426},
		{name: "models method", method: "POST", path: "/models", want: 405},
		{name: "responses method", method: "GET", path: "/responses", want: 405},
		{name: "compact method", method: "DELETE", path: "/responses/compact", want: 405},
		{name: "empty", method: "POST", path: "/responses", want: 400},
		{name: "no model", method: "POST", path: "/responses", body: `{}`, want: 400},
		{name: "wrong model type", method: "POST", path: "/responses", body: `{"model":17}`, want: 400},
		{name: "array", method: "POST", path: "/responses", body: `[]`, want: 400},
		{name: "trailing body", method: "POST", path: "/responses", body: `{"model":"gpt-5.5"}{}`, want: 400},
		{name: "unknown alias", method: "POST", path: "/responses", body: `{"model":"cpa/unknown"}`, want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://127.0.0.1"+codexPath+tc.path, strings.NewReader(tc.body))
			if tc.host != "" {
				r.Host = tc.host
			}
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Upgrade", tc.upgrade)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+codexPath+"/responses", strings.NewReader(strings.Repeat(" ", maxRequestBytes+1)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request: %d", w.Code)
	}
}

func TestOfficialOtherPath(t *testing.T) {
	official, requests := upstream(t)
	s := start(t, Options{Provider: testProvider("http://127.0.0.1:1"), OfficialURL: official.URL + "/override/codex"})
	r, _ := http.NewRequest(http.MethodGet, s.URL+codexPath+"/wham/usage?x=a%2Fb", nil)
	r.Header.Set("Authorization", "Bearer official-key")
	r.Header.Set("Origin", s.URL)
	resp, err := s.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := <-requests
	if got.path != "/override/codex/wham/usage" || got.query != "x=a%2Fb" || got.header.Get("Authorization") != "Bearer official-key" {
		t.Fatalf("official passthrough: %+v", got)
	}
}

func TestUpstreamStatusAndRedirects(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var followed bool
			dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true }))
			defer dest.Close()
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", dest.URL)
				w.Header().Set("Retry-After", "10")
				w.WriteHeader(code)
				fmt.Fprint(w, "upstream body")
			}))
			defer up.Close()
			h, err := New(Options{Provider: testProvider(up.URL), OfficialURL: up.URL + codexPath})
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/models", "/responses"} {
				method, body := http.MethodGet, ""
				if path == "/responses" {
					method, body = http.MethodPost, `{"model":"cpa/gpt-5.5"}`
				}
				r := httptest.NewRequest(method, "http://127.0.0.1"+codexPath+path, strings.NewReader(body))
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if code >= 300 && code <= 399 && path == "/responses" {
					if w.Code != 502 || w.Header().Get("Location") != "" || w.Header().Get("Retry-After") != "" || w.Body.String() != "relay upstream unavailable\n" || followed {
						t.Fatalf("provider redirect exposed: %d %s, headers %v", w.Code, w.Body.String(), w.Header())
					}
				} else if w.Code != code || w.Body.String() != "upstream body" || w.Header().Get("Retry-After") != "10" || followed {
					t.Fatalf("upstream result: %d %s, followed=%v", w.Code, w.Body.String(), followed)
				}
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTransportErrorDoesNotExposeSecrets(t *testing.T) {
	h, err := New(Options{Provider: testProvider("http://127.0.0.1:1"), Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("failed with provider-secret and chatgpt-secret")
	})})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+codexPath+"/responses", strings.NewReader(`{"model":"cpa/gpt-5.5"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 502 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("transport failure: %d %s", w.Code, w.Body.String())
	}
}

func TestInvalidOfficialCatalog(t *testing.T) {
	for _, body := range []string{`{}`, `{"models":null}`, `{"models":{}}`, `{"models":[]} trailing`, strings.Repeat(" ", maxModelsBytes+1)} {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		h, err := New(Options{Provider: testProvider(up.URL), OfficialURL: up.URL + codexPath})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+codexPath+"/models", nil))
		up.Close()
		if w.Code != 502 {
			t.Fatalf("invalid official catalog returned %d", w.Code)
		}
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	for _, change := range []func(*Options){
		func(o *Options) { o.Provider.APIs.Responses = false },
		func(o *Options) { o.Provider.ID = "../bad" },
		func(o *Options) { o.Provider.BaseURL = "file:///secret" },
		func(o *Options) { o.Provider.BaseURL = "https://user:secret@example.com/v1" },
		func(o *Options) { o.Provider.BaseURL = "https://example.com/v1?key=secret" },
		func(o *Options) { o.OfficialURL = "https://example.com/#fragment" },
		func(o *Options) { o.Provider.Headers["X-Team"] = "value\r\nInjected: secret" },
		func(o *Options) { o.Provider.Headers["Host"] = "example.com" },
		func(o *Options) { o.Provider.Headers["Connection"] = "Authorization" },
		func(o *Options) { o.Provider.Headers["Content-Encoding"] = "zstd" },
		func(o *Options) { o.Provider.APIKey = "secret\n" },
		func(o *Options) { o.Provider.Models = append(o.Provider.Models, o.Provider.Models[0]) },
	} {
		o := Options{Provider: testProvider("http://127.0.0.1:1")}
		change(&o)
		if _, err := New(o); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid configuration error = %v", err)
		}
	}
}

func compressedBody(t *testing.T, encoding string, body []byte) []byte {
	t.Helper()
	if encoding == "zstd" {
		encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
		if err != nil {
			t.Fatal(err)
		}
		defer encoder.Close()
		return encoder.EncodeAll(body, nil)
	}
	var buf bytes.Buffer
	encoder := gzip.NewWriter(&buf)
	if _, err := encoder.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCompressedResponses(t *testing.T) {
	for _, encoding := range []string{"zstd", "gzip"} {
		for _, mode := range []string{"official", "alias", "all"} {
			t.Run(encoding+"/"+mode, func(t *testing.T) {
				up, requests := upstream(t)
				s := start(t, Options{Provider: testProvider(up.URL), OfficialURL: up.URL + codexPath, All: mode == "all"})
				model := "gpt-5.5"
				if mode == "alias" {
					model = "cpa/" + model
				}
				decoded := fmt.Sprintf(`{ "model":%q,"stream":true,"service_tier":"priority","future":{"precise":9007199254740993,"exponent":1.2300e+20}}`, model)
				wire := compressedBody(t, encoding, []byte(decoded))
				r, err := http.NewRequest(http.MethodPost, s.URL+codexPath+"/responses?access_token=account-secret&chatgpt_account_id=account-secret", bytes.NewReader(wire))
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Content-Encoding", encoding)
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer chatgpt-secret")
				r.Header.Set("ChatGPT-Account-Id", "account-secret")
				resp, err := s.Client().Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != 200 {
					t.Fatalf("compressed response: %s", resp.Status)
				}
				got := <-requests
				if mode == "official" {
					if got.body != string(wire) || got.header.Get("Content-Encoding") != encoding || got.header.Get("Authorization") != "Bearer chatgpt-secret" || !strings.Contains(got.query, "access_token=account-secret") {
						t.Fatal("official compressed request was changed")
					}
				} else {
					if got.query != "" || got.header.Get("Content-Encoding") != "" || got.header.Get("Authorization") != "Bearer provider-secret" || got.header.Get("ChatGPT-Account-Id") != "" {
						t.Fatalf("custom compression/header isolation failed: %+v", got)
					}
					var fields map[string]json.RawMessage
					if json.Unmarshal([]byte(got.body), &fields) != nil || string(fields["model"]) != `"gpt-5.5"` || string(fields["service_tier"]) != `"priority"` || !strings.Contains(got.body, "9007199254740993") || !strings.Contains(got.body, "1.2300e+20") {
						t.Fatalf("custom decoded body changed: %s", got.body)
					}
				}
			})
		}
	}
}

func TestCompressedRequestLimitsAndErrors(t *testing.T) {
	h, err := New(Options{Provider: testProvider("http://127.0.0.1:1")})
	if err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, encoding string, body []byte, want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+codexPath+"/responses", bytes.NewReader(body))
		r.Header.Set("Content-Encoding", encoding)
		r.Header.Set("Authorization", "Bearer chatgpt-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("encoding %q: %d %s, want %d", encoding, w.Code, w.Body.String(), want)
		}
	}
	for _, encoding := range []string{"br", "zstd,gzip"} {
		check(t, encoding, []byte("secret"), 415)
	}
	for _, encoding := range []string{"zstd", "gzip"} {
		t.Run(encoding, func(t *testing.T) {
			check(t, encoding, []byte("secret"), 400)
			valid := compressedBody(t, encoding, []byte(`{"model":"gpt-5.5"}`))
			check(t, encoding, valid[:len(valid)-1], 400)
			// Both the wire size and the expanded size are bounded. A small
			// compressed frame must not make the routing read unbounded data.
			large := bytes.Repeat([]byte(" "), maxRequestBytes+1)
			check(t, encoding, large, 413)
			check(t, encoding, compressedBody(t, encoding, large), 413)
		})
	}
}

func TestConfiguredProviderAuthorizationWithoutAPIKey(t *testing.T) {
	up, requests := upstream(t)
	p := testProvider(up.URL)
	p.APIKey = ""
	p.Headers["Authorization"] = "Basic provider-credential"
	s := start(t, Options{Provider: p})
	r, _ := http.NewRequest(http.MethodPost, s.URL+codexPath+"/responses", strings.NewReader(`{"model":"cpa/gpt-5.5"}`))
	r.Header.Set("Authorization", "Bearer chatgpt-secret")
	resp, err := s.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := <-requests; got.header.Get("Authorization") != "Basic provider-credential" {
		t.Errorf("explicit provider authorization was replaced: %v", got.header)
	}
}
