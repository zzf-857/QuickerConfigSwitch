// Package codexcat renders the model catalog (models.json) Codex reads through
// `model_catalog_json`. Codex shows /fast for a model only when the model's
// entry has a service tier named "Fast" (id "priority"), so a third-party
// provider needs a catalog that says so.
//
// Embedded files:
//   - bundled_models.json is codex-rs/models-manager/models.json of
//     openai/codex (Apache-2.0), Codex's own bundled catalog.
//   - codex_prompt.md is Codex's generic system prompt (Apache-2.0,
//     openai/codex, core/gpt-5.2-codex_prompt.md), as taken from
//     yetone/magpie (MIT) internal/codexcat. Third-party models need one
//     because the bundled catalog only carries prompts for OpenAI models.
//
// Fields Codex requires of an entry (codex-rs/protocol ModelInfo, no serde
// default): slug, display_name, supported_reasoning_levels, shell_type,
// visibility, supported_in_api, priority, support_verbosity,
// truncation_policy, experimental_supported_tools, and base_instructions (or
// model_messages.instructions_template). description, availability_nux,
// upgrade, default_verbosity and apply_patch_tool_type are Options and may
// be absent. Older Codex (before 0.145 / 0.147) also required
// supports_reasoning_summaries and supports_parallel_tool_calls, so they are
// always written.
package codexcat

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// Prompt is Codex's generic system prompt, used for models Codex has no entry for.
//
//go:embed codex_prompt.md
var Prompt string

//go:embed bundled_models.json
var bundled []byte

// Model is one model to offer Codex.
type Model struct {
	Slug    string   // id sent to the provider, e.g. "gpt-5.5"
	Name    string   // display name; Slug when empty
	Efforts []string // reasoning levels, may be empty
	Fast    bool     // offer Fast (service tier "priority")
	Context int      // context window, 0 = unknown
	Images  bool     // accepts images
}

type tier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// FastTier is Fast as Codex's own catalog writes it on its GPT models.
var FastTier = tier{ID: "priority", Name: "Fast", Description: "1.5x speed, increased usage"}

type level struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

// entry is a generic models.json entry; every field Codex requires is set.
type entry struct {
	Slug         string   `json:"slug"`
	DisplayName  string   `json:"display_name"`
	Description  string   `json:"description"`
	Instructions string   `json:"base_instructions"`
	DefaultLevel *string  `json:"default_reasoning_level"`
	Levels       []level  `json:"supported_reasoning_levels"`
	Shell        string   `json:"shell_type"`
	Visibility   string   `json:"visibility"`
	InAPI        bool     `json:"supported_in_api"`
	Priority     int      `json:"priority"`
	Verbosity    bool     `json:"support_verbosity"`
	DefVerbosity *string  `json:"default_verbosity"`
	ApplyPatch   string   `json:"apply_patch_tool_type"`
	Truncation   trunc    `json:"truncation_policy"`
	Tools        []string `json:"experimental_supported_tools"`
	Modalities   []string `json:"input_modalities"`
	Context      *int     `json:"context_window,omitempty"`
	Tiers        []tier   `json:"service_tiers"`
	SearchTool   bool     `json:"supports_search_tool"`
	Parallel     bool     `json:"supports_parallel_tool_calls"`
	Summaries    bool     `json:"supports_reasoning_summaries" keep:"true"`
}

type trunc struct {
	Mode  string `json:"mode"`
	Limit int    `json:"limit"`
}

// DefaultEffort picks "medium" or "high" when offered, else the first level;
// "" for none.
func DefaultEffort(e []string) string {
	for _, want := range []string{"medium", "high"} {
		if slices.Contains(e, want) {
			return want
		}
	}
	if len(e) == 0 {
		return ""
	}
	return e[0]
}

// bundledEntries parses the embedded catalog, by slug (fresh maps each call).
func bundledEntries() map[string]map[string]any {
	var f struct {
		Models []map[string]any `json:"models"`
	}
	if json.Unmarshal(bundled, &f) != nil {
		return map[string]map[string]any{}
	}
	return bySlug(f.Models)
}

func bySlug(ms []map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, m := range ms {
		if s, _ := m["slug"].(string); s != "" {
			out[s] = m
		}
	}
	return out
}

// OwnEntries are Codex's own entries by slug: the bundled catalog, with each
// entry of <codexHome>/models_cache.json (what the ChatGPT backend gave this
// account, service tiers included) taking the place of the bundled one. The
// maps are the caller's to change.
func OwnEntries(codexHome string) map[string]map[string]any {
	// the bundled catalog fills in slugs the account's cache doesn't list
	// (a provider may serve a model this account isn't offered)
	out := bundledEntries()
	if codexHome != "" {
		if b, err := os.ReadFile(filepath.Join(codexHome, "models_cache.json")); err == nil {
			var c struct {
				Models []map[string]any `json:"models"`
			}
			if json.Unmarshal(b, &c) == nil {
				for k, v := range bySlug(c.Models) {
					out[k] = v
				}
			}
		}
	}
	return out
}

var datedSuffix = regexp.MustCompile(`-(\d{4}-\d{2}-\d{2}|\d{8})$`)

// baseSlug strips a "vendor/" prefix and a date suffix.
func baseSlug(s string) string {
	s = strings.ToLower(s)
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return datedSuffix.ReplaceAllString(s, "")
}

func hasFast(e map[string]any) bool {
	ts, _ := e["service_tiers"].([]any)
	for _, t := range ts {
		if m, _ := t.(map[string]any); m != nil && m["id"] == FastTier.ID {
			return true
		}
	}
	return false
}

func asAnyTiers(ts ...tier) []any {
	out := make([]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "description": t.Description})
	}
	return out
}

// Catalog renders a whole models.json ({"models":[...]}) for models, ranked
// in the given order.
//   - Same slug as an own entry (see OwnEntries): that entry copied, with
//     display_name (when Name is set), priority and visibility "list" set,
//     start-up notice and upgrade prompt removed, and context_window set when
//     Context is known. Fast=false strips its service tiers; Fast=true on an
//     entry with none adds Fast.
//   - Slug equal to an own one after stripping a "vendor/" prefix and a date
//     suffix: a generic entry whose service tiers are the own entry's (none
//     when Fast=false; Fast when Fast=true and it has none).
//   - Otherwise a generic entry; Fast=true gives it the Fast tier.
func Catalog(models []Model, codexHome string) ([]byte, error) {
	own := OwnEntries(codexHome)
	out := make([]any, 0, len(models))
	for i, m := range models {
		name := m.Name
		if name == "" {
			name = m.Slug
		}
		if raw, ok := own[m.Slug]; ok {
			out = append(out, copyOwn(raw, m, name, i+1))
			continue
		}
		e := &entry{
			Slug: m.Slug, DisplayName: name, Description: name,
			Instructions: Prompt, Levels: []level{},
			Shell: "unified_exec", Visibility: "list", InAPI: true, Priority: i + 1,
			ApplyPatch: "freeform", Truncation: trunc{"tokens", 10000},
			Tools: []string{}, Modalities: []string{"text"},
			Tiers: []tier{}, SearchTool: true, Parallel: true,
		}
		if m.Images {
			e.Modalities = append(e.Modalities, "image")
		}
		if m.Context > 0 {
			c := m.Context
			e.Context = &c
		}
		for _, ef := range m.Efforts {
			e.Levels = append(e.Levels, level{Effort: ef})
		}
		if len(m.Efforts) > 0 {
			d := DefaultEffort(m.Efforts)
			e.DefaultLevel = &d
			e.Summaries = true
		}
		if m.Fast {
			e.Tiers = []tier{FastTier}
			if raw, ok := own[baseSlug(m.Slug)]; ok {
				if ts := ownTiers(raw); len(ts) > 0 {
					e.Tiers = ts
				}
			}
		}
		out = append(out, e)
	}
	return marshal(map[string]any{"models": out})
}

func ownTiers(raw map[string]any) []tier {
	var ts []tier
	l, _ := raw["service_tiers"].([]any)
	for _, r := range l {
		o, _ := r.(map[string]any)
		id, _ := o["id"].(string)
		if id == "" {
			continue
		}
		n, _ := o["name"].(string)
		d, _ := o["description"].(string)
		ts = append(ts, tier{id, n, d})
	}
	return ts
}

func copyOwn(raw map[string]any, m Model, name string, prio int) map[string]any {
	e := maps.Clone(raw)
	delete(e, "availability_nux")
	delete(e, "upgrade")
	e["slug"], e["priority"], e["visibility"] = m.Slug, prio, "list"
	if m.Name != "" {
		e["display_name"] = name
	}
	if s, _ := e["base_instructions"].(string); s == "" {
		if mm, _ := e["model_messages"].(map[string]any); mm == nil || mm["instructions_template"] == nil {
			e["base_instructions"] = Prompt
		}
	}
	switch {
	case !m.Fast:
		e["service_tiers"] = []any{}
		delete(e, "additional_speed_tiers")
		delete(e, "default_service_tier")
	case !hasFast(e):
		e["service_tiers"] = append(sliceOr(e["service_tiers"]), asAnyTiers(FastTier)...)
	}
	if m.Context > 0 {
		e["context_window"] = m.Context
		if mx, ok := e["max_context_window"].(float64); ok && int(mx) < m.Context {
			e["max_context_window"] = m.Context
		}
	}
	return e
}

func sliceOr(v any) []any {
	s, _ := v.([]any)
	return slices.Clone(s)
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// owned are the keys Catalog writes on a generic entry; keepTrue are those
// where a true the user set by hand stays over our false.
var owned, keepTrue = func() (own, yes map[string]bool) {
	own = map[string]bool{"availability_nux": true, "upgrade": true}
	yes = map[string]bool{}
	t := reflect.TypeFor[entry]()
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		own[name] = true
		if f.Tag.Get("keep") == "true" {
			yes[name] = true
		}
	}
	return
}()

// Keep returns next (a catalog as Catalog renders it) with what the user
// added by hand to the entries of cur (the catalog on disk about to be
// overwritten): keys of an entry of the same slug that we don't write, and a
// true where we write false for supports_reasoning_summaries. Entries of
// models no longer in next are dropped. next is returned unchanged when there
// is nothing to keep or either side isn't a catalog.
func Keep(cur, next []byte) []byte {
	type list struct {
		Models []map[string]any `json:"models"`
	}
	read := func(raw []byte, l *list) error {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		return d.Decode(l)
	}
	var was, now list
	if len(cur) == 0 || read(cur, &was) != nil {
		return next
	}
	add := map[string]map[string]any{}
	for _, e := range was.Models {
		slug, _ := e["slug"].(string)
		if slug == "" {
			continue
		}
		for k, v := range e {
			if !owned[k] || keepTrue[k] && v == true {
				if add[slug] == nil {
					add[slug] = map[string]any{}
				}
				add[slug][k] = v
			}
		}
	}
	if len(add) == 0 || read(next, &now) != nil {
		return next
	}
	kept := false
	for _, e := range now.Models {
		slug, _ := e["slug"].(string)
		for k, v := range add[slug] {
			if cur, ok := e[k]; !ok || keepTrue[k] && cur != true {
				e[k], kept = v, true
			}
		}
	}
	if !kept {
		return next
	}
	out, err := marshal(now)
	if err != nil {
		return next
	}
	return out
}

// NeedsCatalog reports whether a model_catalog_json is needed: false only when
// every model's slug is in Codex's bundled catalog and Fast matches whether
// that entry offers it, i.e. Codex would list the models as wanted on its
// own. Names, efforts and context are not compared.
func NeedsCatalog(models []Model) bool {
	b := bundledEntries()
	for _, m := range models {
		e, ok := b[m.Slug]
		if !ok || hasFast(e) != m.Fast {
			return true
		}
	}
	return false
}
