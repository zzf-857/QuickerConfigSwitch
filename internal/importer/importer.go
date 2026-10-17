// Package importer finds providers the user already set up elsewhere — CC
// Switch, Codex's own config.toml, Claude Code's settings.json — so they can
// be added here in one click. It only reads those files.
package importer

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

// Candidate is one provider found elsewhere.
type Candidate struct {
	Source   string            `json:"source"`           // "cc-switch", "codex", "claude"
	Origin   string            `json:"origin"`           // where in the source: a CC Switch entry's name, a table name, a file
	Provider provider.Provider `json:"provider"`         // ready to add; ID suggested, unique among current providers
	Exists   string            `json:"exists,omitempty"` // id of a current provider with the same base URL and key, if any
	Note     string            `json:"note,omitempty"`
}

// Scan lists every candidate found. A source that isn't there is skipped;
// one that can't be read is reported in errs without stopping the others.
func Scan() (cands []Candidate, errs []error) {
	current, err := provider.List()
	if err != nil {
		errs = append(errs, fmt.Errorf("current providers: %w", err))
	}
	for _, src := range []func() ([]Candidate, []error){scanCCSwitch, scanCodex, scanClaude} {
		cs, es := src()
		cands = append(cands, cs...)
		errs = append(errs, es...)
	}
	cands = dedupe(cands)
	cands = dropApplied(cands, current)
	finish(cands, current)
	return cands, errs
}

// Add adds a candidate as a provider under id ("" keeps its suggested id).
func Add(c Candidate, id string) (provider.Provider, error) {
	p := c.Provider
	if id != "" {
		p.ID = id
	}
	return p, provider.Add(p)
}

// ---- shared helpers --------------------------------------------------------

// normURL is a base URL compared loosely: no trailing slash, lowercase scheme and host.
func normURL(s string) string {
	s = strings.TrimSpace(s)
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return strings.TrimRight(s, "/")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/")
}

func hostOf(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func urlsOf(p provider.Provider) []string {
	var out []string
	if p.BaseURL != "" && (p.APIs.Responses || p.APIs.Chat) {
		out = append(out, normURL(p.BaseURL))
	}
	if p.APIs.Anthropic != "" {
		out = append(out, normURL(p.APIs.Anthropic))
	}
	return out
}

func sameURLAndKey(a, b provider.Provider) bool {
	if a.APIKey != b.APIKey {
		return false
	}
	for _, x := range urlsOf(a) {
		for _, y := range urlsOf(b) {
			if x == y {
				return true
			}
		}
	}
	return false
}

// dedupe drops candidates identical to an earlier one: same source kind, URLs and key.
func dedupe(cs []Candidate) []Candidate {
	var out []Candidate
	for _, c := range cs {
		dup := false
		for _, o := range out {
			if o.Source == c.Source && sameURLAndKey(o.Provider, c.Provider) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, c)
		}
	}
	return out
}

var nonID = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(nonID.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// baseID is a valid id (before uniqueness) from a name, else the host.
func baseID(name, rawURL, fallback string) string {
	s := slug(name)
	if s == "" {
		h := hostOf(rawURL)
		h = strings.TrimPrefix(strings.TrimPrefix(h, "www."), "api.")
		s = slug(h)
	}
	if s == "" {
		s = slug(fallback)
	}
	if s == "" {
		s = "provider"
	}
	if s[0] < 'a' || s[0] > 'z' {
		s = "p-" + s
	}
	if len(s) > 32 {
		s = strings.TrimRight(s[:32], "-")
	}
	return s
}

func uniqueID(base string, taken map[string]bool) string {
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		suf := fmt.Sprintf("-%d", n)
		b := base
		if len(b)+len(suf) > 32 {
			b = strings.TrimRight(b[:32-len(suf)], "-")
		}
		if id := b + suf; !taken[id] {
			return id
		}
	}
}

// dropApplied drops Claude Code env candidates that sit on a current
// provider's Anthropic URL with another key: that is most likely what applying
// the provider wrote, not a relay of the user's own. Same URL and key stay,
// marked Exists by finish.
func dropApplied(cs []Candidate, current []provider.Provider) []Candidate {
	var out []Candidate
	for _, c := range cs {
		drop := false
		if c.Source == "claude" {
			for _, q := range current {
				if q.APIs.Anthropic != "" && normURL(q.APIs.Anthropic) == normURL(c.Provider.APIs.Anthropic) && q.APIKey != c.Provider.APIKey {
					drop = true
				}
			}
		}
		if !drop {
			out = append(out, c)
		}
	}
	return out
}

// finish assigns unique ids and marks candidates a current provider already has.
func finish(cs []Candidate, current []provider.Provider) {
	taken := map[string]bool{}
	for _, p := range current {
		taken[p.ID] = true
	}
	for i := range cs {
		p := &cs[i].Provider
		u := p.BaseURL
		if u == "" {
			u = p.APIs.Anthropic
		}
		p.ID = uniqueID(baseID(p.Name, u, cs[i].Source), taken)
		taken[p.ID] = true
		for _, q := range current {
			if sameURLAndKey(*p, q) {
				cs[i].Exists = q.ID
				break
			}
		}
	}
}

func addModel(p *provider.Provider, id string, codex bool) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, " \t\r\n") {
		return
	}
	for _, m := range p.Models {
		if m.ID == id {
			return
		}
	}
	p.Models = append(p.Models, provider.Model{ID: id, Fast: codex && provider.DefaultFast(id)})
}

func isQCS(s string) bool { return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), "qcs-") }

// ---- CC Switch -------------------------------------------------------------

type ccEntry struct {
	id, app, name string
	settings      map[string]any
}

func ccSwitchDir() string {
	if d := os.Getenv("CC_SWITCH_DIR"); d != "" {
		return d
	}
	return filepath.Join(appdir.Home(), ".cc-switch")
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func sqliteURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows drive letter
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro&_pragma=busy_timeout(2000)"}
	return u.String()
}

func readCCDB(path string) ([]ccEntry, error) {
	db, err := sql.Open("sqlite", sqliteURI(path))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return nil, errors.New("CC Switch database can't be opened: " + err.Error())
	}
	rows, err := db.Query(`SELECT id, app_type, name, settings_config FROM providers ORDER BY app_type, COALESCE(sort_index, 0), created_at`)
	if err != nil {
		return nil, errors.New("CC Switch database can't be read: " + err.Error())
	}
	defer rows.Close()
	var out []ccEntry
	for rows.Next() {
		var e ccEntry
		var raw sql.NullString
		if rows.Scan(&e.id, &e.app, &e.name, &raw) != nil {
			continue
		}
		json.Unmarshal([]byte(raw.String), &e.settings)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("CC Switch database can't be read: " + err.Error())
	}
	return out, nil
}

func readCCJSON(path string) ([]ccEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &cfg); err != nil {
		return nil, errors.New("CC Switch config.json can't be parsed: " + err.Error())
	}
	var out []ccEntry
	for app, sec := range cfg {
		var s struct {
			Providers map[string]struct {
				Name     string         `json:"name"`
				Settings map[string]any `json:"settingsConfig"`
			} `json:"providers"`
		}
		if json.Unmarshal(sec, &s) != nil {
			continue
		}
		for id, p := range s.Providers {
			out = append(out, ccEntry{id: id, app: app, name: p.Name, settings: p.Settings})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].app+"\x00"+out[i].id < out[j].app+"\x00"+out[j].id })
	return out, nil
}

func mapOf(v any) map[string]any { m, _ := v.(map[string]any); return m }

func strOf(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func scanCCSwitch() (cands []Candidate, errs []error) {
	dir := ccSwitchDir()
	var entries []ccEntry
	db, js := filepath.Join(dir, "cc-switch.db"), filepath.Join(dir, "config.json")
	read := false
	if fileExists(db) {
		read = true
		es, err := readCCDB(db)
		if err != nil {
			errs = append(errs, err)
		}
		entries = es
	}
	if !read || (len(errs) > 0 && fileExists(js)) {
		if fileExists(js) {
			es, err := readCCJSON(js)
			if err != nil {
				errs = append(errs, err)
			}
			entries = append(entries, es...)
		}
	}
	for _, e := range entries {
		if isQCS(e.name) || isQCS(e.id) {
			continue
		}
		c, ok := ccCandidate(e)
		if !ok {
			continue
		}
		if i := mergeable(cands, c); i >= 0 {
			mergeInto(&cands[i], c)
			continue
		}
		cands = append(cands, c)
	}
	return cands, errs
}

// ccCandidate reads one CC Switch entry; ok is false for the official sign-in,
// other apps, and ours.
func ccCandidate(e ccEntry) (Candidate, bool) {
	name := strings.TrimSpace(e.name)
	if name == "" {
		name = e.id
	}
	p := provider.Provider{Name: name}
	c := Candidate{Source: "cc-switch", Origin: fmt.Sprintf("CC Switch: %s (%s)", name, e.app)}
	switch e.app {
	case "claude":
		env := mapOf(e.settings["env"])
		base := strOf(env, "ANTHROPIC_BASE_URL")
		key := strOf(env, "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY")
		if base == "" {
			return c, false // the official sign-in (or a bare key to Anthropic itself)
		}
		p.APIKey = key
		p.APIs.Anthropic = base
		for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
			addModel(&p, strOf(env, k), false)
		}
	case "codex":
		cfg, _ := e.settings["config"].(string)
		top, tables, err := parseTOMLText(cfg)
		if err != nil {
			return c, false
		}
		mp := top["model_provider"]
		if isQCS(mp) {
			return c, false
		}
		t := tables[mp]
		base := t["base_url"]
		key := strOf(mapOf(e.settings["auth"]), "OPENAI_API_KEY")
		if key == "" {
			key = t["experimental_bearer_token"]
		}
		if base == "" {
			return c, false // official sign-in, or no endpoint to import
		}
		p.BaseURL = base
		p.APIKey = key
		if t["wire_api"] == "chat" {
			p.APIs.Chat = true
		} else {
			p.APIs.Responses = true
		}
		addModel(&p, top["model"], true)
		if key == "" && t["env_key"] != "" {
			c.Note = "key comes from env var " + t["env_key"]
		}
	default:
		return c, false
	}
	c.Provider = p
	return c, true
}

// mergeable finds an earlier cc-switch candidate of the other app (Claude vs
// Codex) on the same host with the same non-empty key.
func mergeable(have []Candidate, c Candidate) int {
	for i, h := range have {
		if h.Provider.APIKey == "" || h.Provider.APIKey != c.Provider.APIKey {
			continue
		}
		a, b := h.Provider, c.Provider
		oneClaude := (a.APIs.Anthropic != "") != (b.APIs.Anthropic != "")
		oneOpenAI := (a.BaseURL != "") != (b.BaseURL != "")
		if !oneClaude || !oneOpenAI {
			continue
		}
		ha, hb := hostOf(a.APIs.Anthropic+a.BaseURL), hostOf(b.APIs.Anthropic+b.BaseURL)
		if ha != "" && ha == hb {
			return i
		}
	}
	return -1
}

func mergeInto(dst *Candidate, src Candidate) {
	p := &dst.Provider
	if p.BaseURL == "" {
		p.BaseURL = src.Provider.BaseURL
	}
	if p.APIs.Anthropic == "" {
		p.APIs.Anthropic = src.Provider.APIs.Anthropic
	}
	p.APIs.Responses = p.APIs.Responses || src.Provider.APIs.Responses
	p.APIs.Chat = p.APIs.Chat || src.Provider.APIs.Chat
	for _, m := range src.Provider.Models {
		if _, ok := p.Model(m.ID); !ok {
			p.Models = append(p.Models, m)
		}
	}
	dst.Origin += " + " + strings.TrimPrefix(src.Origin, "CC Switch: ")
	if dst.Note == "" {
		dst.Note = src.Note
	}
}

// parseTOMLText reads TOML text with the edit package (which works on files)
// through a private temp file, returning top-level keys and, for each
// [model_providers.X] table, X's keys.
func parseTOMLText(text string) (top map[string]string, tables map[string]map[string]string, err error) {
	dir, err := os.MkdirTemp("", "qcs-import-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return nil, nil, err
	}
	return parseTOMLFile(path)
}

func parseTOMLFile(path string) (top map[string]string, tables map[string]map[string]string, err error) {
	names, err := edit.TOMLTables(path)
	if err != nil {
		return nil, nil, err
	}
	top = map[string]string{}
	for _, k := range []string{"model", "model_provider"} {
		if v, ok := edit.GetTOMLTop(path, k); ok {
			top[k] = v
		}
	}
	tables = map[string]map[string]string{}
	for _, n := range names {
		id, ok := strings.CutPrefix(n, "model_providers.")
		if !ok || id == "" {
			continue
		}
		if len(id) >= 2 && (id[0] == '"' || id[0] == '\'') && id[len(id)-1] == id[0] {
			id = id[1 : len(id)-1]
		}
		kv, err := edit.GetTOMLTable(path, n)
		if err != nil || kv == nil || strings.Contains(id, ".") && !strings.HasPrefix(n, "model_providers.\"") {
			continue // a sub-table such as model_providers.x.auth
		}
		tables[id] = kv
	}
	return top, tables, nil
}

// ---- Codex config.toml -----------------------------------------------------

func scanCodex() (cands []Candidate, errs []error) {
	path := filepath.Join(appdir.CodexHome(), "config.toml")
	if !fileExists(path) {
		return nil, nil
	}
	top, tables, err := parseTOMLFile(path)
	if err != nil {
		return nil, []error{errors.New("Codex config.toml can't be read: " + err.Error())}
	}
	var ids []string
	for id := range tables {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t := tables[id]
		if isQCS(id) || strings.TrimSpace(t["base_url"]) == "" {
			continue
		}
		p := provider.Provider{Name: id, BaseURL: strings.TrimSpace(t["base_url"]), APIKey: t["experimental_bearer_token"]}
		if t["wire_api"] == "chat" {
			p.APIs.Chat = true
		} else {
			p.APIs.Responses = true
		}
		if n := strings.TrimSpace(t["name"]); n != "" {
			p.Name = n
		}
		if top["model_provider"] == id {
			addModel(&p, top["model"], true)
		}
		c := Candidate{Source: "codex", Origin: "~/.codex/config.toml [model_providers." + id + "]", Provider: p}
		if p.APIKey == "" && t["env_key"] != "" {
			c.Note = "key comes from env var " + t["env_key"]
		}
		cands = append(cands, c)
	}
	return cands, nil
}

// ---- Claude Code -----------------------------------------------------------

func scanClaude() (cands []Candidate, errs []error) {
	paths := []struct{ path, label string }{
		{filepath.Join(appdir.ClaudeHome(), "settings.json"), "~/.claude/settings.json"},
		{filepath.Join(appdir.Home(), ".claude.json"), "~/.claude.json"},
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		paths = append(paths, struct{ path, label string }{filepath.Join(d, ".claude.json"), "$CLAUDE_CONFIG_DIR/.claude.json"})
	}
	seen := map[string]bool{}
	for _, f := range paths {
		if seen[f.path] || !fileExists(f.path) {
			continue
		}
		seen[f.path] = true
		raw, err := os.ReadFile(f.path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s can't be read: %v", f.label, err))
			continue
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		get := func(k string) string { v, _ := edit.GetJSON(f.path, "env."+k); return strings.TrimSpace(v) }
		base := get("ANTHROPIC_BASE_URL")
		if base == "" {
			continue
		}
		p := provider.Provider{Name: hostOf(base), APIKey: firstNonEmpty(get("ANTHROPIC_AUTH_TOKEN"), get("ANTHROPIC_API_KEY"))}
		if p.Name == "" {
			p.Name = base
		}
		p.APIs.Anthropic = base
		for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
			addModel(&p, get(k), false)
		}
		cands = append(cands, Candidate{Source: "claude", Origin: f.label + " env", Provider: p})
	}
	return cands, errs
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
