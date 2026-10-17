package codexcat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

type catFile struct {
	Models []map[string]any `json:"models"`
}

func render(t *testing.T, ms []Model, home string) []map[string]any {
	t.Helper()
	b, err := Catalog(ms, home)
	if err != nil {
		t.Fatal(err)
	}
	var f catFile
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f.Models
}

func tiersOf(e map[string]any) []string {
	var ids []string
	l, _ := e["service_tiers"].([]any)
	for _, x := range l {
		ids = append(ids, x.(map[string]any)["id"].(string))
	}
	return ids
}

func TestCopiedOwnEntry(t *testing.T) {
	home := testenv.Home(t)
	got := render(t, []Model{{Slug: "gpt-5.5", Fast: true}}, home)[0]
	want := bundledEntries()["gpt-5.5"]
	delete(want, "upgrade")
	delete(want, "availability_nux")
	want["priority"] = 1
	// numbers decode as float64 in got
	wb, _ := json.Marshal(want)
	var w map[string]any
	json.Unmarshal(wb, &w)
	if !reflect.DeepEqual(got, w) {
		t.Fatalf("copy differs from bundled entry")
	}
	if !reflect.DeepEqual(tiersOf(got), []string{"priority"}) {
		t.Fatalf("tiers %v", tiersOf(got))
	}
}

func TestNativeDisplayNameAfterProviderFetch(t *testing.T) {
	home := testenv.Home(t)
	for _, tc := range []struct{ name, want string }{
		{"", "GPT-5.5"},
		{"gpt-5.5", "GPT-5.5"},
		{"My CPA GPT", "My CPA GPT"},
	} {
		e := render(t, []Model{{Slug: "gpt-5.5", Name: tc.name, Fast: true}}, home)[0]
		if e["display_name"] != tc.want || !reflect.DeepEqual(tiersOf(e), []string{"priority"}) {
			t.Fatalf("fetched native model name=%q display=%v tiers=%v", tc.name, e["display_name"], tiersOf(e))
		}
	}
}

// requiredInfo mirrors the fields Codex's ModelInfo has no serde default for.
type requiredInfo struct {
	Slug        *string           `json:"slug"`
	DisplayName *string           `json:"display_name"`
	Levels      *[]map[string]any `json:"supported_reasoning_levels"`
	Shell       *string           `json:"shell_type"`
	Visibility  *string           `json:"visibility"`
	InAPI       *bool             `json:"supported_in_api"`
	Priority    *int              `json:"priority"`
	Verbosity   *bool             `json:"support_verbosity"`
	Truncation  *struct {
		Mode  string
		Limit int
	} `json:"truncation_policy"`
	Tools        *[]string `json:"experimental_supported_tools"`
	Instructions *string   `json:"base_instructions"`
}

func TestGenericEntryRequiredFields(t *testing.T) {
	home := testenv.Home(t)
	b, _ := Catalog([]Model{{Slug: "kimi-k3", Name: "Kimi K3", Efforts: []string{"low", "high"}, Fast: true, Context: 200000, Images: true}}, home)
	var f struct {
		Models []requiredInfo `json:"models"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	r := f.Models[0]
	if r.Slug == nil || r.DisplayName == nil || r.Levels == nil || r.Shell == nil || r.Visibility == nil ||
		r.InAPI == nil || r.Priority == nil || r.Verbosity == nil || r.Truncation == nil || r.Tools == nil ||
		r.Instructions == nil || *r.Instructions == "" {
		t.Fatalf("missing required field: %s", b)
	}
	if *r.Shell != "unified_exec" || r.Truncation.Mode != "tokens" || r.Truncation.Limit != 10000 {
		t.Fatalf("bad values: %s", b)
	}
	e := render(t, []Model{{Slug: "kimi-k3", Efforts: []string{"low", "high"}, Fast: true, Context: 200000, Images: true}}, home)[0]
	if e["default_reasoning_level"] != "high" || e["supports_reasoning_summaries"] != true ||
		e["supports_parallel_tool_calls"] != true || e["supports_search_tool"] != true ||
		e["apply_patch_tool_type"] != "freeform" || e["context_window"] != float64(200000) ||
		!reflect.DeepEqual(e["input_modalities"], []any{"text", "image"}) {
		t.Fatalf("bad entry: %v", e)
	}
	n := render(t, []Model{{Slug: "plain"}}, home)[0]
	if n["default_reasoning_level"] != nil || n["supports_reasoning_summaries"] != false || len(tiersOf(n)) != 0 {
		t.Fatalf("bad plain entry: %v", n)
	}
}

func TestFastToggle(t *testing.T) {
	home := testenv.Home(t)
	if got := tiersOf(render(t, []Model{{Slug: "foo", Fast: true}}, home)[0]); !reflect.DeepEqual(got, []string{"priority"}) {
		t.Fatalf("generic fast: %v", got)
	}
	if got := tiersOf(render(t, []Model{{Slug: "foo"}}, home)[0]); len(got) != 0 {
		t.Fatalf("generic slow: %v", got)
	}
	e := render(t, []Model{{Slug: "gpt-5.5"}}, home)[0]
	if len(tiersOf(e)) != 0 || e["additional_speed_tiers"] != nil {
		t.Fatalf("own entry with Fast=false keeps tiers: %v", e["service_tiers"])
	}
}

func TestDatedSuffixAndPrefix(t *testing.T) {
	home := testenv.Home(t)
	for _, slug := range []string{"gpt-5.5-2026-09-14", "gpt-5.5-20260914", "openai/gpt-5.5", "openai/gpt-5.5-20260914"} {
		e := render(t, []Model{{Slug: slug, Fast: true}}, home)[0]
		if e["slug"] != slug || !reflect.DeepEqual(tiersOf(e), []string{"priority"}) {
			t.Fatalf("%s: %v", slug, e)
		}
		if e["base_instructions"] != Prompt {
			t.Fatalf("%s should be a generic entry", slug)
		}
		if len(tiersOf(render(t, []Model{{Slug: slug}}, home)[0])) != 0 {
			t.Fatalf("%s: Fast=false keeps tiers", slug)
		}
	}
	// an own entry that has no Fast tier hands none on
	if got := tiersOf(render(t, []Model{{Slug: "gpt-daybreak-blue-latest-20260101", Fast: true}}, home)[0]); !reflect.DeepEqual(got, []string{"priority"}) {
		t.Fatalf("got %v", got) // Fast=true still offers Fast
	}
}

func TestCachePreferred(t *testing.T) {
	home := testenv.Home(t)
	cache := `{"fetched_at":"2026-10-17T00:00:00Z","etag":"W/\"x\"","client_version":"0.150.0","models":[
	 {"slug":"gpt-5.5","display_name":"GPT-5.5 cache","base_instructions":"CACHED","supported_reasoning_levels":[],"shell_type":"unified_exec","visibility":"list","supported_in_api":true,"priority":1,"support_verbosity":false,"truncation_policy":{"mode":"tokens","limit":1},"experimental_supported_tools":[],
	  "service_tiers":[{"id":"priority","name":"Fast","description":"cache"},{"id":"ultrafast","name":"Ultra","description":"u"}]}]}`
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}
	own := OwnEntries(home)
	if own["gpt-5.5"]["base_instructions"] != "CACHED" {
		t.Fatalf("cache not used for gpt-5.5")
	}
	if _, ok := own["gpt-6-sol"]; !ok {
		t.Fatalf("bundled entries should fill slugs the cache lacks")
	}
	e := render(t, []Model{{Slug: "gpt-5.5", Fast: true}, {Slug: "gpt-5.5-20260914", Fast: true}}, home)
	if e[0]["base_instructions"] != "CACHED" || !reflect.DeepEqual(tiersOf(e[0]), []string{"priority", "ultrafast"}) {
		t.Fatalf("copy from cache: %v", e[0])
	}
	if !reflect.DeepEqual(tiersOf(e[1]), []string{"priority", "ultrafast"}) {
		t.Fatalf("dated tiers from cache: %v", tiersOf(e[1]))
	}
	// broken cache falls back to the bundled catalog
	os.WriteFile(filepath.Join(home, "models_cache.json"), []byte("{nope"), 0o600)
	if len(OwnEntries(home)) < 5 {
		t.Fatal("no fallback to bundled")
	}
}

func TestKeep(t *testing.T) {
	home := testenv.Home(t)
	first, _ := Catalog([]Model{{Slug: "a", Efforts: []string{"low"}}, {Slug: "b"}, {Slug: "gpt-5.5", Fast: true}}, home)
	var f catFile
	json.Unmarshal(first, &f)
	f.Models[0]["my_key"] = "mine"
	f.Models[1]["supports_reasoning_summaries"] = true
	f.Models[0]["description"] = "edited" // owned: ours wins
	f.Models[2]["my_own"] = 7
	cur, _ := json.Marshal(f)
	next, _ := Catalog([]Model{{Slug: "a", Efforts: []string{"low"}}, {Slug: "b"}, {Slug: "gpt-5.5", Fast: true}}, home)
	var out catFile
	if err := json.Unmarshal(Keep(cur, next), &out); err != nil {
		t.Fatal(err)
	}
	if out.Models[0]["my_key"] != "mine" || out.Models[0]["description"] != "a" ||
		out.Models[1]["supports_reasoning_summaries"] != true || out.Models[2]["my_own"] != float64(7) {
		t.Fatalf("not kept: %v", out.Models)
	}
	// a model dropped from next loses its entry; nothing to keep returns next as is
	only, _ := Catalog([]Model{{Slug: "b"}}, home)
	var o2 catFile
	json.Unmarshal(Keep(cur, only), &o2)
	if len(o2.Models) != 1 || o2.Models[0]["slug"] != "b" || o2.Models[0]["my_key"] != nil {
		t.Fatalf("removed model leaked: %v", o2.Models)
	}
	if string(Keep([]byte("junk"), next)) != string(next) || string(Keep(nil, next)) != string(next) {
		t.Fatal("bad cur should return next")
	}
}

func TestNeedsCatalog(t *testing.T) {
	if NeedsCatalog([]Model{{Slug: "gpt-5.5", Fast: true}, {Slug: "gpt-6-sol", Fast: true}}) {
		t.Fatal("bundled models with Fast need no catalog")
	}
	if !NeedsCatalog([]Model{{Slug: "gpt-5.5"}}) {
		t.Fatal("Fast off for a Fast model needs one")
	}
	if !NeedsCatalog([]Model{{Slug: "gpt-5.5", Fast: true}, {Slug: "kimi", Fast: true}}) {
		t.Fatal("unknown model needs one")
	}
	if !NeedsCatalog([]Model{{Slug: "gpt-daybreak-blue-latest", Fast: true}}) {
		t.Fatal("bundled entry without Fast needs one")
	}
	if NeedsCatalog([]Model{{Slug: "gpt-daybreak-blue-latest"}}) {
		t.Fatal("matching no-Fast entry needs none")
	}
}
