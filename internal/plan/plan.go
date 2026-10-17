// Package plan describes a change to agent config files without making it.
// Adapters (internal/agent/...) compute a Plan; safekeep.Apply carries it
// out (snapshot, backup, stash originals, atomic write, record) and
// safekeep.Restore undoes it. Adapters never write files themselves.
package plan

import "github.com/zzf-857/quickerconfigswitch/internal/edit"

// Format is how a file is edited.
type Format string

const (
	TOML Format = "toml"
	JSON Format = "json" // JSON or JSONC
	Raw  Format = "raw"  // a whole file this tool generates and owns
)

// Kind is one edit operation.
type Kind string

const (
	// TOML
	SetTop      Kind = "set-top"       // Key = Value (top-level, before any table)
	DelTop      Kind = "del-top"       // remove top-level Key
	SetTable    Kind = "set-table"     // replace/append table Table with KVs (user keys in it kept unless Whole)
	SetTableKey Kind = "set-table-key" // Table.Key = Value, other keys kept
	DelTableKey Kind = "del-table-key" // remove Table.Key
	DelTable    Kind = "del-table"     // remove table Table
	// JSON
	SetJSON Kind = "set-json" // Key is a dot path
	DelJSON Kind = "del-json"
	// Raw
	WriteFile Kind = "write-file" // the file's whole content is Data
)

// Op is one change. Value is what edit accepts: string, bool, int, float64,
// edit.Raw (literal TOML/JSON text), edit.Inline (TOML inline table).
type Op struct {
	Kind  Kind      `json:"kind"`
	Table string    `json:"table,omitempty"`
	Key   string    `json:"key,omitempty"`
	Value any       `json:"value,omitempty"`
	KVs   []edit.KV `json:"kvs,omitempty"`
	Data  []byte    `json:"-"`
	// Whole replaces a SetTable's table entirely instead of merging over
	// the user's keys in it.
	Whole bool `json:"whole,omitempty"`
	// Keep marks what Restore must leave in place even though this tool
	// added it: Codex's [model_providers.qcs-*] table, which threads
	// started on it need to be reopened.
	Keep bool `json:"keep,omitempty"`
}

// FileEdit is every change to one file.
type FileEdit struct {
	Path   string `json:"path"`
	Format Format `json:"format"`
	Ops    []Op   `json:"ops"`
}

// Plan is the change one switch makes.
type Plan struct {
	Agent  string     `json:"agent"`
	Edits  []FileEdit `json:"edits"`
	Notice string     `json:"notice,omitempty"`
	// State is the agent's own bookkeeping recorded with the change (its
	// mode, provider and model), read back by the adapter's Current.
	State map[string]string `json:"state,omitempty"`
}

// Empty reports whether the plan changes nothing.
func (p *Plan) Empty() bool {
	for _, e := range p.Edits {
		if len(e.Ops) > 0 {
			return false
		}
	}
	return true
}
