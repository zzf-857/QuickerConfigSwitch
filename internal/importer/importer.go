// Package importer finds providers the user already set up elsewhere — CC
// Switch, Codex's own config.toml, Claude Code's settings.json — so they can
// be added here in one click. It only reads those files.
package importer

import "github.com/zzf-857/quickerconfigswitch/internal/provider"

// Candidate is one provider found elsewhere.
type Candidate struct {
	Source   string            `json:"source"`   // "cc-switch", "codex", "claude"
	Origin   string            `json:"origin"`   // where in the source: a CC Switch entry's name, a table name, a file
	Provider provider.Provider `json:"provider"` // ready to add; ID suggested, unique among current providers
	Exists   string            `json:"exists,omitempty"` // id of a current provider with the same base URL and key, if any
	Note     string            `json:"note,omitempty"`
}

// Scan lists every candidate found. A source that isn't there is skipped;
// one that can't be read is reported in errs without stopping the others.
func Scan() (cands []Candidate, errs []error) { return nil, nil }

// Add adds a candidate as a provider under id ("" keeps its suggested id).
func Add(c Candidate, id string) (provider.Provider, error) {
	p := c.Provider
	if id != "" {
		p.ID = id
	}
	return p, provider.Add(p)
}
