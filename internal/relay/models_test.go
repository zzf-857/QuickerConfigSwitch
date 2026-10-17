package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func decodeCatalog(t *testing.T, data []byte) []map[string]json.RawMessage {
	t.Helper()
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog.Models
}

func TestModelsOfflineCacheFallback(t *testing.T) {
	home := t.TempDir()
	cache := `{"account_id":"private-account","access_token":"private-login","etag":"old-tag","models":[` +
		`{"slug":"gpt-official-b","future":{"n":9007199254740993,"v":1.2300e+20},"service_tiers":[{"id":"priority","name":"Fast"}]},` +
		`{"slug":"old-provider/removed-model"},{"slug":"cpa/gpt-5.5"},{"slug":"gpt-official-a"}]}`
	path := filepath.Join(home, "models_cache.json")
	if err := os.WriteFile(path, []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(home, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"tokens":{"access_token":"auth-file-secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{
		Provider: testProvider("http://127.0.0.1:1"), CodexHome: home,
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("unreachable with private-transport-secret")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+codexPath+"/models", nil))
	if w.Code != 200 || w.Header().Get("X-QCS-Catalog-Source") != "cache" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache response %d, headers %v", w.Code, w.Header())
	}
	models := decodeCatalog(t, w.Body.Bytes())
	if len(models) != 4 || string(models[0]["slug"]) != `"gpt-official-b"` || string(models[1]["slug"]) != `"gpt-official-a"` ||
		string(models[2]["slug"]) != `"cpa/gpt-5.5"` || string(models[3]["slug"]) != `"cpa/vendor/model"` {
		t.Fatalf("cache order, aliases or stale filtering changed: %s", w.Body)
	}
	if string(models[0]["future"]) != `{"n":9007199254740993,"v":1.2300e+20}` || !strings.Contains(string(models[0]["service_tiers"]), "priority") {
		t.Fatalf("cached model metadata changed: %s", w.Body)
	}
	for _, secret := range []string{"private-account", "private-login", "old-tag", "auth-file-secret", "private-transport-secret", "old-provider/removed-model"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("fallback exposed %s", secret)
		}
	}
	if data, _ := os.ReadFile(path); string(data) != cache {
		t.Fatal("fallback wrote cache")
	}
	if data, _ := os.ReadFile(authPath); string(data) != `{"tokens":{"access_token":"auth-file-secret"}}` {
		t.Fatal("fallback changed login file")
	}
}

func TestModelsUnusableCacheUsesOnlyCurrentAliases(t *testing.T) {
	for _, tc := range []struct {
		name, cache string
	}{
		{"missing", ""},
		{"empty", `{}`},
		{"null", `{"models":null}`},
		{"no entries", `{"models":[]}`},
		{"malformed", `{"models":[`},
		{"wrong list", `{"models":{}}`},
		{"wrong entry", `{"models":[{"slug":"gpt-5.5"},7]}`},
		{"empty slug", `{"models":[{"slug":"gpt-5.5"},{"slug":""}]}`},
		{"only old aliases", `{"models":[{"slug":"old-provider/model"}]}`},
		{"oversize", strings.Repeat(" ", maxModelsBytes+1)},
		{"directory", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "models_cache.json")
			if tc.cache != "" {
				if err := os.WriteFile(path, []byte(tc.cache), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if tc.name == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			h, err := New(Options{
				Provider: testProvider("http://127.0.0.1:1"), CodexHome: home,
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("offline")
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+codexPath+"/models", nil))
			if w.Code != 200 || w.Header().Get("X-QCS-Catalog-Source") != "local" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("fallback response %d, headers %v", w.Code, w.Header())
			}
			models := decodeCatalog(t, w.Body.Bytes())
			if len(models) != 2 || string(models[0]["slug"]) != `"cpa/gpt-5.5"` || string(models[1]["slug"]) != `"cpa/vendor/model"` {
				t.Fatalf("fallback included unavailable models: %s", w.Body)
			}
		})
	}
}

func TestModelsFallbackSizeLimit(t *testing.T) {
	home := t.TempDir()
	entry := json.RawMessage(`{"slug":"official","description":"` + strings.Repeat("x", maxModelsBytes-70) + `"}`)
	cache, err := modelList([]json.RawMessage{entry})
	if err != nil || len(cache) >= maxModelsBytes {
		t.Fatal("invalid fixture size")
	}
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), cache, 0o600); err != nil {
		t.Fatal(err)
	}
	aliases := []json.RawMessage{json.RawMessage(`{"slug":"cpa/model","description":"` + strings.Repeat("y", 100) + `"}`)}
	p := proxy(roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") }), func(*httputil.ProxyRequest) {}, nil)
	h, err := catalogHandler(p, aliases, home, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/models", nil))
	if w.Header().Get("X-QCS-Catalog-Source") != "local" || len(w.Body.Bytes()) > maxModelsBytes || len(decodeCatalog(t, w.Body.Bytes())) != 1 {
		t.Fatal("combined fallback did not respect output limit")
	}
	tooLarge := []json.RawMessage{json.RawMessage(`{"slug":"cpa/model","description":"` + strings.Repeat("x", maxModelsBytes) + `"}`)}
	if _, err := catalogHandler(p, tooLarge, "", time.Second); err == nil {
		t.Fatal("accepted oversized local catalog")
	}
}

func TestModelsSlowOfficialFallsBackWithinRefreshBudget(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer upstream.Close()
	h, err := New(Options{Provider: testProvider("http://127.0.0.1:1"), OfficialURL: upstream.URL + codexPath})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+codexPath+"/models", nil))
	elapsed := time.Since(start)
	if elapsed < modelsWait || elapsed >= 5*time.Second {
		t.Fatalf("official list used %s of the 5s refresh budget", elapsed)
	}
	if w.Code != 200 || w.Header().Get("X-QCS-Catalog-Source") != "local" || len(decodeCatalog(t, w.Body.Bytes())) != 2 {
		t.Fatalf("missing timeout fallback %d, headers %v", w.Code, w.Header())
	}
}

func TestModelsBodyReadTimeoutAndClientCancellation(t *testing.T) {
	for _, cancelClient := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancelClient), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if cancelClient {
					cancel()
					return nil, ctx.Err()
				}
				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{},
					Body: &waitingBody{ctx: r.Context()}, Request: r,
				}, nil
			})
			p := proxy(transport, func(*httputil.ProxyRequest) {}, func(r *http.Response) error { return mergeModels(r, nil) })
			h, err := catalogHandler(p, []json.RawMessage{json.RawMessage(`{"slug":"cpa/model"}`)}, "", 20*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/models", nil).WithContext(ctx))
			if cancelClient {
				if w.Body.Len() != 0 || w.Header().Get("X-QCS-Catalog-Source") != "" {
					t.Fatal("canceled client received a fallback")
				}
			} else if w.Code != 200 || w.Header().Get("X-QCS-Catalog-Source") != "local" {
				t.Fatalf("body stall failed to fall back: %d, headers %v", w.Code, w.Header())
			}
		})
	}
}

type waitingBody struct{ ctx context.Context }

func (b *waitingBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (b *waitingBody) Close() error             { return nil }

func TestModelsAlreadyCanceledDoesNotReachUpstream(t *testing.T) {
	called := false
	h, err := New(Options{Provider: testProvider("http://127.0.0.1:1"), Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+codexPath+"/models", nil).WithContext(ctx))
	if called || w.Body.Len() != 0 || len(w.Header()) != 0 {
		t.Fatal("canceled catalog request reached upstream or wrote a response")
	}
}

func TestModelsConcurrentFallbacks(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(`{"models":[{"slug":"gpt-official"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := New(Options{Provider: testProvider("http://127.0.0.1:1"), CodexHome: home, Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+codexPath+"/models", nil))
			if w.Code != 200 || w.Header().Get("X-QCS-Catalog-Source") != "cache" || len(decodeCatalog(t, w.Body.Bytes())) != 3 {
				t.Errorf("concurrent fallback failed: %d, headers %v", w.Code, w.Header())
			}
		})
	}
	wg.Wait()
}

func TestUnknownNamespaceNeverReachesUpstream(t *testing.T) {
	for _, path := range []string{"/responses", "/responses/compact"} {
		up, requests := upstream(t)
		h, err := New(Options{Provider: testProvider(up.URL), OfficialURL: up.URL + codexPath})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+codexPath+path, strings.NewReader(`{"model":"other/model"}`)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("unknown namespace returned %d", w.Code)
		}
		select {
		case <-requests:
			t.Fatal("unknown provider namespace reached an upstream")
		default:
		}
	}
}

var _ io.ReadCloser = (*waitingBody)(nil)
