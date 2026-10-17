// Package provider keeps the user's custom providers (OpenAI- or
// Anthropic-compatible upstreams such as CPA) in providers.json under the
// app folder, and fetches their model lists.
package provider

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/store"
)

// Model is one model a provider offers.
type Model struct {
	ID      string   `json:"id"`
	Name    string   `json:"name,omitempty"`
	Efforts []string `json:"efforts,omitempty"`
	Fast    bool     `json:"fast,omitempty"`
	Context int      `json:"context,omitempty"`
	Images  bool     `json:"images,omitempty"`
}

// APIs says which protocols an upstream speaks.
type APIs struct {
	Responses bool   `json:"responses,omitempty"` // speaks OpenAI Responses at BaseURL
	Chat      bool   `json:"chat,omitempty"`
	Anthropic string `json:"anthropic,omitempty"` // Anthropic Messages base URL, "" = none
}

// Provider is one custom upstream.
type Provider struct {
	ID      string            `json:"id"` // lowercase [a-z0-9-], 1..32, starts with a letter
	Name    string            `json:"name"`
	BaseURL string            `json:"baseURL"` // OpenAI-compatible base, e.g. http://127.0.0.1:8317/v1
	APIKey  string            `json:"apiKey,omitempty"`
	APIs    APIs              `json:"apis"`
	Headers map[string]string `json:"headers,omitempty"`
	Models  []Model           `json:"models,omitempty"`
}

// fileName is the providers file under the app folder.
const fileName = "providers.json"

// mu serializes the read-modify-write of the providers file within this process.
var mu sync.Mutex

// fileData is the on-disk shape of providers.json.
type fileData struct {
	Providers []Provider `json:"providers"`
}

func file() string { return appdir.Path(fileName) }

func load() ([]Provider, error) {
	var f fileData
	if err := store.Load(file(), &f); err != nil {
		return nil, err
	}
	return f.Providers, nil
}

func save(ps []Provider) error {
	if ps == nil {
		ps = []Provider{}
	}
	return store.Save(file(), fileData{Providers: ps}, 0o600)
}

// List returns every saved provider, in the order they were added.
func List() ([]Provider, error) {
	mu.Lock()
	defer mu.Unlock()
	ps, err := load()
	if err != nil {
		return nil, err
	}
	if ps == nil {
		ps = []Provider{}
	}
	return ps, nil
}

// Get returns the provider with the given id; ok is false when there is none.
func Get(id string) (p Provider, ok bool, err error) {
	ps, err := List()
	if err != nil {
		return Provider{}, false, err
	}
	for _, q := range ps {
		if q.ID == id {
			return q, true, nil
		}
	}
	return Provider{}, false, nil
}

// Add validates p and saves it as a new provider. An id already in use is an error.
func Add(p Provider) error {
	if err := validate(p); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	ps, err := load()
	if err != nil {
		return err
	}
	for _, q := range ps {
		if q.ID == p.ID {
			return fmt.Errorf("provider %q already exists", p.ID)
		}
	}
	return save(append(ps, p))
}

// Update validates p and replaces the saved provider with the same id.
func Update(p Provider) error {
	if err := validate(p); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	ps, err := load()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(ps, func(q Provider) bool { return q.ID == p.ID })
	if i < 0 {
		return fmt.Errorf("provider %q not found", p.ID)
	}
	ps[i] = p
	return save(ps)
}

// Remove deletes the provider with the given id.
func Remove(id string) error {
	mu.Lock()
	defer mu.Unlock()
	ps, err := load()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(ps, func(q Provider) bool { return q.ID == id })
	if i < 0 {
		return fmt.Errorf("provider %q not found", id)
	}
	return save(slices.Delete(ps, i, i+1))
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidID reports whether id is a usable provider id: lowercase letters,
// digits and '-', 1 to 32 characters, starting with a letter.
func ValidID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid provider id %q: use 1-32 lowercase letters, digits or '-', starting with a letter", id)
	}
	return nil
}

// validate checks everything Add and Update need before saving.
func validate(p Provider) error {
	if err := ValidID(p.ID); err != nil {
		return err
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("provider %s: name is required", p.ID)
	}
	a := p.APIs
	if !a.Responses && !a.Chat && a.Anthropic == "" {
		return fmt.Errorf("provider %s: enable at least one API (responses, chat or anthropic)", p.ID)
	}
	if (a.Responses || a.Chat) && p.BaseURL == "" {
		return fmt.Errorf("provider %s: baseURL is required for the responses or chat API", p.ID)
	}
	if p.BaseURL != "" {
		if err := checkURL(p.ID+": baseURL", p.BaseURL); err != nil {
			return err
		}
	}
	if a.Anthropic != "" {
		if err := checkURL(p.ID+": apis.anthropic", a.Anthropic); err != nil {
			return err
		}
	}
	for k, v := range p.Headers {
		if k == "" || strings.ContainsAny(k, " \t\r\n:") {
			return fmt.Errorf("provider %s: invalid header name %q", p.ID, k)
		}
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("provider %s: header %s has a line break in its value", p.ID, k)
		}
	}
	seen := make(map[string]bool, len(p.Models))
	for _, m := range p.Models {
		if m.ID == "" || strings.ContainsAny(m.ID, " \t\r\n") {
			return fmt.Errorf("provider %s: invalid model id %q", p.ID, m.ID)
		}
		if seen[m.ID] {
			return fmt.Errorf("provider %s: model %q listed twice", p.ID, m.ID)
		}
		seen[m.ID] = true
		if m.Context < 0 {
			return fmt.Errorf("provider %s: model %s has a negative context size", p.ID, m.ID)
		}
		for _, e := range m.Efforts {
			if strings.TrimSpace(e) == "" {
				return fmt.Errorf("provider %s: model %s has an empty effort level", p.ID, m.ID)
			}
		}
	}
	return nil
}

func checkURL(field, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s: %q is not an http(s) URL", field, raw)
	}
	return nil
}

// Model returns the model with the given id, if the provider lists it.
func (p Provider) Model(id string) (Model, bool) {
	for _, m := range p.Models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// ParseRef splits a "provider/model" reference at its first slash, so model
// ids may contain more slashes: "cpa/openai/gpt-5" gives ("cpa", "openai/gpt-5").
// ok is false when either part is empty or there is no slash.
func ParseRef(ref string) (providerID, modelID string, ok bool) {
	providerID, modelID, ok = strings.Cut(ref, "/")
	if !ok || providerID == "" || modelID == "" {
		return "", "", false
	}
	return providerID, modelID, true
}

var oSeries = regexp.MustCompile(`^o[0-9]`)

// DefaultFast says whether a model should start with Fast ticked: its last
// path segment starts with "gpt-" or is an o-series id such as "o3" (any case).
func DefaultFast(modelID string) bool {
	seg := modelID
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	seg = strings.ToLower(seg)
	return strings.HasPrefix(seg, "gpt-") || oSeries.MatchString(seg)
}

// MaskKey hides the middle of an API key for display, e.g. "sk-abcd…wxyz".
// Keys shorter than 16 characters become "****" so little of them shows.
func MaskKey(k string) string {
	if k == "" {
		return ""
	}
	r := []rune(k)
	if len(r) < 16 {
		return "****"
	}
	return string(r[:7]) + "…" + string(r[len(r)-4:])
}

// Templates are the starting points offered when adding a provider. They are
// form defaults: the user still sets the id, name and key.
func Templates() []Provider {
	return []Provider{
		{
			ID:      "cpa",
			Name:    "CPA (CLIProxyAPI)",
			BaseURL: "http://127.0.0.1:8317/v1",
			APIs: APIs{
				Responses: true,
				Chat:      true,
				Anthropic: "http://127.0.0.1:8317",
			},
		},
		{
			ID:   "openai-compatible",
			Name: "OpenAI-compatible relay",
			APIs: APIs{Responses: true, Chat: true},
		},
		{
			ID:   "anthropic-compatible",
			Name: "Anthropic-compatible relay",
			APIs: APIs{Anthropic: "https://api.anthropic.com"},
		},
	}
}

// errNoBaseURL is returned by FetchModels for a provider without a base URL.
var errNoBaseURL = errors.New("provider has no base URL to fetch models from")
