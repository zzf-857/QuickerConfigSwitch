package provider

import (
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func sample() Provider {
	return Provider{
		ID:      "cpa",
		Name:    "CPA local",
		BaseURL: "http://127.0.0.1:8317/v1",
		APIKey:  "sk-abcdefghijklmnopqrstuvwxyz1234wxyz",
		APIs:    APIs{Responses: true, Chat: true, Anthropic: "http://127.0.0.1:8317"},
		Headers: map[string]string{"X-Team": "ops"},
		Models: []Model{{
			ID:      "gpt-5.5",
			Name:    "GPT-5.5",
			Efforts: []string{"low", "high"},
			Fast:    true,
			Context: 272000,
			Images:  true,
		}},
	}
}

func TestCRUDRoundtrip(t *testing.T) {
	dir := testenv.Home(t)

	ps, err := List()
	if err != nil || len(ps) != 0 {
		t.Fatalf("List empty: %v, %v", ps, err)
	}

	p := sample()
	if err := Add(p); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Add(p); err == nil {
		t.Error("Add of a duplicate id succeeded")
	}

	path := appdir.Path("providers.json")
	if !strings.HasPrefix(path, dir) {
		t.Fatalf("providers file %s is outside the test home %s", path, dir)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("providers.json mode = %o, want 600", fi.Mode().Perm())
		}
	}

	got, ok, err := Get("cpa")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, p) {
		t.Errorf("Get roundtrip:\n got %+v\nwant %+v", got, p)
	}

	p.Name = "CPA renamed"
	p.Models = append(p.Models, Model{ID: "openai/gpt-5"})
	if err := Update(p); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _, _ = Get("cpa")
	if got.Name != "CPA renamed" || len(got.Models) != 2 {
		t.Errorf("after Update: %+v", got)
	}

	if err := Update(Provider{ID: "nope", Name: "x", APIs: APIs{Chat: true}, BaseURL: "https://x.example"}); err == nil {
		t.Error("Update of a missing id succeeded")
	}

	second := sample()
	second.ID, second.Name, second.APIKey = "openai-compat", "Relay", ""
	if err := Add(second); err != nil {
		t.Fatalf("Add second: %v", err)
	}
	ps, _ = List()
	if len(ps) != 2 || ps[0].ID != "cpa" || ps[1].ID != "openai-compat" {
		t.Errorf("List order: %+v", ps)
	}

	if err := Remove("cpa"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok, _ := Get("cpa"); ok {
		t.Error("Get after Remove still finds cpa")
	}
	if err := Remove("cpa"); err == nil {
		t.Error("Remove of a missing id succeeded")
	}
	ps, _ = List()
	if len(ps) != 1 || ps[0].ID != "openai-compat" {
		t.Errorf("after Remove: %+v", ps)
	}
}

func TestValidID(t *testing.T) {
	long32 := "a" + strings.Repeat("b", 31)
	cases := map[string]bool{
		"cpa":               true,
		"a":                 true,
		"openai-compatible": true,
		"a1-b2":             true,
		long32:              true,
		"":                  false,
		"1cpa":              false,
		"CPA":               false,
		"cpa_x":             false,
		"-cpa":              false,
		"cpa.x":             false,
		"cpa x":             false,
		long32 + "c":        false,
	}
	for id, want := range cases {
		if err := ValidID(id); (err == nil) != want {
			t.Errorf("ValidID(%q) = %v, want ok=%v", id, err, want)
		}
	}
}

func TestAddAndUpdateValidation(t *testing.T) {
	base := func() Provider { return sample() }
	cases := []struct {
		name   string
		mutate func(p *Provider)
	}{
		{"bad id", func(p *Provider) { p.ID = "Bad_ID" }},
		{"empty name", func(p *Provider) { p.Name = "  " }},
		{"no api", func(p *Provider) { p.APIs = APIs{} }},
		{"responses without base url", func(p *Provider) { p.BaseURL = "" }},
		{"base url not http", func(p *Provider) { p.BaseURL = "ftp://x.example/v1" }},
		{"base url without scheme", func(p *Provider) { p.BaseURL = "127.0.0.1:8317/v1" }},
		{"anthropic url not http", func(p *Provider) { p.APIs.Anthropic = "localhost:8080" }},
		{"header name with colon", func(p *Provider) { p.Headers = map[string]string{"A:B": "c"} }},
		{"header value with newline", func(p *Provider) { p.Headers = map[string]string{"A": "b\r\nX: y"} }},
		{"empty model id", func(p *Provider) { p.Models = []Model{{ID: ""}} }},
		{"model id with space", func(p *Provider) { p.Models = []Model{{ID: "gpt 5"}} }},
		{"duplicate model", func(p *Provider) { p.Models = []Model{{ID: "a"}, {ID: "a"}} }},
		{"negative context", func(p *Provider) { p.Models = []Model{{ID: "a", Context: -1}} }},
		{"empty effort", func(p *Provider) { p.Models = []Model{{ID: "a", Efforts: []string{" "}}} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testenv.Home(t)
			p := base()
			tc.mutate(&p)
			if err := Add(p); err == nil {
				t.Error("Add accepted an invalid provider")
			}
			if ps, _ := List(); len(ps) != 0 {
				t.Errorf("invalid provider was saved: %+v", ps)
			}
		})
	}

	t.Run("update validates too", func(t *testing.T) {
		testenv.Home(t)
		if err := Add(sample()); err != nil {
			t.Fatal(err)
		}
		p := sample()
		p.BaseURL = "not a url"
		if err := Update(p); err == nil {
			t.Error("Update accepted an invalid provider")
		}
		got, _, _ := Get("cpa")
		if got.BaseURL != "http://127.0.0.1:8317/v1" {
			t.Errorf("invalid Update changed the saved provider: %q", got.BaseURL)
		}
	})

	t.Run("valid shapes", func(t *testing.T) {
		testenv.Home(t)
		anthropicOnly := Provider{ID: "claude-relay", Name: "Relay", APIs: APIs{Anthropic: "https://relay.example"}}
		if err := Add(anthropicOnly); err != nil {
			t.Errorf("anthropic-only provider rejected: %v", err)
		}
		for _, tpl := range Templates() {
			// templates leave the base URL to the user; with one filled in they must validate
			if tpl.BaseURL == "" && (tpl.APIs.Responses || tpl.APIs.Chat) {
				tpl.BaseURL = "https://relay.example/v1"
			}
			if err := validate(tpl); err != nil {
				t.Errorf("template %s invalid: %v", tpl.ID, err)
			}
		}
	})
}

func TestParseRef(t *testing.T) {
	cases := []struct {
		ref, p, m string
		ok        bool
	}{
		{"cpa/gpt-5.5", "cpa", "gpt-5.5", true},
		{"cpa/openai/gpt-5", "cpa", "openai/gpt-5", true},
		{"a/b/c/d", "a", "b/c/d", true},
		{"cpa/", "", "", false},
		{"/gpt-5", "", "", false},
		{"gpt-5", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		p, m, ok := ParseRef(c.ref)
		if p != c.p || m != c.m || ok != c.ok {
			t.Errorf("ParseRef(%q) = (%q, %q, %v), want (%q, %q, %v)", c.ref, p, m, ok, c.p, c.m, c.ok)
		}
	}
}

func TestDefaultFast(t *testing.T) {
	cases := map[string]bool{
		"gpt-5.5":       true,
		"GPT-5":         true,
		"openai/gpt-5":  true,
		"gpt-x/o3":      true,
		"o3":            true,
		"O4-mini":       true,
		"o1":            true,
		"oss-20b":       false,
		"o":             false,
		"gpt5":          false,
		"xgpt-4":        false,
		"claude-opus":   false,
		"foo/gpt-x/bar": false,
		"":              false,
	}
	for id, want := range cases {
		if got := DefaultFast(id); got != want {
			t.Errorf("DefaultFast(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestMaskKey(t *testing.T) {
	cases := map[string]string{
		"":                                      "",
		"sk-short":                              "****",
		"sk-123456789012":                       "****",         // 15 chars
		"sk-1234567890123":                      "sk-1234…0123", // 16 chars is the shortest masked key
		"sk-abcdefghijklmnopqrstuvwxyz1234wxyz": "sk-abcd…wxyz",
	}
	for in, want := range cases {
		if got := MaskKey(in); got != want {
			t.Errorf("MaskKey(%q) = %q, want %q", in, got, want)
		}
	}
	if got := MaskKey("sk-abcdefghijklmnopqrstuvwxyz1234wxyz"); strings.Contains(got, "ijklm") {
		t.Errorf("MaskKey leaked the middle: %q", got)
	}
}

func TestModelLookup(t *testing.T) {
	p := sample()
	if m, ok := p.Model("gpt-5.5"); !ok || m.Context != 272000 {
		t.Errorf("Model(gpt-5.5) = %+v, %v", m, ok)
	}
	if _, ok := p.Model("missing"); ok {
		t.Error("Model found a missing id")
	}
}
