package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

const testKey = "sk-test-secret-key-1234567890"

// serve starts a test server whose /v1/models answers with handler.
func serve(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func ids(ms []Model) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func TestFetchModelsOpenAIFormat(t *testing.T) {
	testenv.Home(t)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("X-Team"); got != "ops" {
			http.Error(w, "missing header", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `{"object":"list","data":[{"id":"o3","object":"model"},{"id":"gpt-5.5"},{"id":"claude-x"},{"id":"openai/gpt-5"},{"id":"gpt-5.5"}]}`)
	})
	p := Provider{
		ID:      "cpa",
		Name:    "CPA",
		BaseURL: srv.URL + "/v1/", // trailing slash must not double up
		APIKey:  testKey,
		Headers: map[string]string{"X-Team": "ops"},
		APIs:    APIs{Responses: true, Chat: true},
	}
	models, err := FetchModels(context.Background(), p)
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	wantIDs := []string{"claude-x", "gpt-5.5", "o3", "openai/gpt-5"}
	if got := ids(models); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("ids = %v, want %v", got, wantIDs)
	}
	wantFast := map[string]bool{"claude-x": false, "gpt-5.5": true, "o3": true, "openai/gpt-5": true}
	for _, m := range models {
		if m.Name != m.ID {
			t.Errorf("%s: Name = %q, want the id", m.ID, m.Name)
		}
		if m.Fast != wantFast[m.ID] {
			t.Errorf("%s: Fast = %v, want %v", m.ID, m.Fast, wantFast[m.ID])
		}
	}
}

func TestFetchModelsFastOnlyForResponses(t *testing.T) {
	testenv.Home(t)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"gpt-5.5"}]}`)
	})
	p := Provider{ID: "chat", Name: "Chat only", BaseURL: srv.URL + "/v1", APIs: APIs{Chat: true}}
	models, err := FetchModels(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Fast {
		t.Errorf("Fast set for a provider without Responses: %+v", models)
	}
}

func TestFetchModelsAlternateFormats(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"top-level array of objects", `[{"id":"b"},{"name":"a"},{"slug":"c"},{"id":""}]`, []string{"a", "b", "c"}},
		{"top-level array of strings", `["z-model","a-model"]`, []string{"a-model", "z-model"}},
		{"models object with slug", `{"models":[{"slug":"m2"},{"name":"m1"}]}`, []string{"m1", "m2"}},
		{"empty data array", `{"data":[]}`, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testenv.Home(t)
			srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tc.body)
			})
			p := Provider{ID: "x", Name: "x", BaseURL: srv.URL, APIs: APIs{Chat: true}}
			models, err := FetchModels(context.Background(), p)
			if err != nil {
				t.Fatalf("FetchModels: %v", err)
			}
			if got := ids(models); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ids = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFetchModelsUnrecognizedBody(t *testing.T) {
	testenv.Home(t)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":"nope"}`)
	})
	p := Provider{ID: "x", Name: "x", BaseURL: srv.URL, APIs: APIs{Chat: true}}
	if _, err := FetchModels(context.Background(), p); err == nil {
		t.Error("expected an error for an unrecognized body")
	}
}

func TestFetchModelsErrorHidesKey(t *testing.T) {
	testenv.Home(t)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		// an upstream that echoes the key back in its error body
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"Incorrect API key provided: %s"}}`, r.Header.Get("Authorization")[len("Bearer "):])
	})
	p := Provider{ID: "x", Name: "x", BaseURL: srv.URL, APIKey: testKey, APIs: APIs{Chat: true}}
	_, err := FetchModels(context.Background(), p)
	if err == nil {
		t.Fatal("expected an error on 401")
	}
	msg := err.Error()
	if !strings.Contains(msg, "401") || !strings.Contains(msg, "Incorrect API key") {
		t.Errorf("error lacks status or body: %q", msg)
	}
	if strings.Contains(msg, testKey) {
		t.Errorf("error leaks the API key: %q", msg)
	}
}

func TestFetchModelsErrorBodyIsCapped(t *testing.T) {
	testenv.Home(t)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, strings.Repeat("x", 5000))
	})
	p := Provider{ID: "x", Name: "x", BaseURL: srv.URL, APIs: APIs{Chat: true}}
	_, err := FetchModels(context.Background(), p)
	if err == nil {
		t.Fatal("expected an error on 502")
	}
	if n := strings.Count(err.Error(), "x"); n > maxErrBody {
		t.Errorf("error quotes %d bytes of body, want at most %d", n, maxErrBody)
	}
}

func TestFetchModelsNoKeyNoAuthorization(t *testing.T) {
	testenv.Home(t)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if a := r.Header.Get("Authorization"); a != "" {
			http.Error(w, "unexpected Authorization: "+a, http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"local"}]}`)
	})
	p := Provider{ID: "x", Name: "x", BaseURL: srv.URL, APIs: APIs{Chat: true}}
	models, err := FetchModels(context.Background(), p)
	if err != nil || len(models) != 1 {
		t.Fatalf("FetchModels: %v, %+v", err, models)
	}
}

func TestFetchModelsNoBaseURL(t *testing.T) {
	testenv.Home(t)
	if _, err := FetchModels(context.Background(), Provider{ID: "x", Name: "x", APIs: APIs{Anthropic: "https://a.example"}}); err == nil {
		t.Error("expected an error without a base URL")
	}
}

func TestFetchModelsHonoursContextDeadline(t *testing.T) {
	testenv.Home(t)
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	p := Provider{ID: "x", Name: "x", BaseURL: srv.URL, APIs: APIs{Chat: true}}
	if _, err := FetchModels(ctx, p); err == nil {
		t.Fatal("expected a timeout error")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v, the context deadline was ignored", d)
	}
}
