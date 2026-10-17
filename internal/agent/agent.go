// Package agent describes the coding agents this tool switches, and keeps
// their registry. Each adapter (internal/agent/codex, internal/agent/claude)
// reads the agent's config and computes a plan.Plan for a Target; package
// safekeep carries the plan out and undoes it. Switching back to the
// official setup is safekeep.Restore, so adapters have no restore of their
// own: what they must leave behind is marked plan.Op.Keep.
package agent

import (
	"fmt"
	"sort"
	"sync"

	"github.com/zzf-857/quickerconfigswitch/internal/plan"
)

// Mode is how an agent is pointed at a provider.
type Mode string

const (
	// Official is the agent's own setup, as it was before this tool.
	Official Mode = "official"
	// Direct points the agent's config straight at a provider.
	Direct Mode = "direct"
	// Relay keeps the agent's own sign-in and routes through the local
	// relay (Codex only; phase 2).
	Relay Mode = "relay"
)

// Target is what the user picked for an agent.
type Target struct {
	Mode     Mode
	Provider string // provider id
	Model    string // the provider's model id
	// Fast asks for the vendor's fast mode where the agent has one (Codex:
	// service tier "priority"). nil leaves the user's own choice alone.
	Fast   *bool
	Effort string // reasoning effort, "" = leave as is
	// RelayAll sends every model through the provider in Relay mode,
	// official ones too; false keeps official models on the sign-in.
	RelayAll bool
	// RelayURL is the listening relay's loopback base URL. The process
	// that owns the listener supplies it before applying a Relay plan.
	RelayURL string
}

// Detection is what is known about an agent on this machine.
type Detection struct {
	Installed bool
	Dir       string   // its config folder
	Files     []string // the config files this tool may edit
	// SignedIn says the agent has its own sign-in (ChatGPT, claude.ai).
	// Only whether one is there is looked at; tokens are never read.
	SignedIn bool
	SignIn   string // "chatgpt", "apikey", "claudeai", "" when unknown
}

// Status is where an agent points now.
type Status struct {
	Mode     Mode
	Provider string
	Model    string
	Detail   string // a short human-readable line
}

// Problem is something Doctor found.
type Problem struct {
	Level   string // "info", "warn" or "error"
	Message string
}

// Agent is one supported agent.
type Agent interface {
	ID() string
	Name() string
	Detect() Detection
	Modes() []Mode
	// Current reads the config (and safekeep's record) to say where the
	// agent points now.
	Current() (Status, error)
	// Plan computes the change for a Direct or Relay target; it writes
	// nothing. It is the full desired set of changes for the agent.
	Plan(t Target) (*plan.Plan, error)
	// Doctor lists conflicts and pitfalls (another tool's override, an
	// environment variable that wins over the config, ...).
	Doctor() []Problem
}

var (
	mu  sync.Mutex
	all = map[string]Agent{}
)

// Register adds an adapter; adapters call it from init.
func Register(a Agent) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := all[a.ID()]; dup {
		panic(fmt.Sprintf("agent %q registered twice", a.ID()))
	}
	all[a.ID()] = a
}

// All lists the registered agents by id.
func All() []Agent {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Agent, 0, len(all))
	for _, a := range all {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out
}

// Get finds an agent by id.
func Get(id string) (Agent, bool) {
	mu.Lock()
	defer mu.Unlock()
	a, ok := all[id]
	return a, ok
}
