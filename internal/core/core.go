// Package core is what the GUI, `qcs web` and the CLI share: the agents'
// state, switching with a preview first, switching back, and profiles. It
// only puts together the adapters (internal/agent/...), safekeep and
// provider; it writes no config itself.
package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	_ "github.com/zzf-857/quickerconfigswitch/internal/agent/claude" // registers Claude Code
	_ "github.com/zzf-857/quickerconfigswitch/internal/agent/codex"  // registers Codex
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

// Official is the ref that means the agent's own setup.
const Official = "official"

// AgentView is one agent as a page shows it.
type AgentView struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Installed bool             `json:"installed"`
	SignedIn  bool             `json:"signedIn"`
	SignIn    string           `json:"signIn,omitempty"`
	Dir       string           `json:"dir,omitempty"`
	Modes     []agent.Mode     `json:"modes"`
	Mode      agent.Mode       `json:"mode"`
	Provider  string           `json:"provider,omitempty"`
	Model     string           `json:"model,omitempty"`
	Fast      string           `json:"fast,omitempty"`   // "true", "false" or "" (left as it was)
	Effort    string           `json:"effort,omitempty"` // as picked, "" = left as it was
	Detail    string           `json:"detail,omitempty"`
	Managed   bool             `json:"managed"` // this tool changed its config and hasn't switched back
	Drift     []safekeep.Drift `json:"drift,omitempty"`
	Error     string           `json:"error,omitempty"`
}

// Agents lists every registered agent with where it points now.
func Agents() []AgentView {
	var out []AgentView
	for _, a := range agent.All() {
		out = append(out, View(a))
	}
	return out
}

// View is one agent's AgentView.
func View(a agent.Agent) AgentView {
	d := a.Detect()
	v := AgentView{
		ID: a.ID(), Name: a.Name(), Installed: d.Installed, SignedIn: d.SignedIn,
		SignIn: d.SignIn, Dir: d.Dir, Modes: a.Modes(), Managed: safekeep.Touched(a.ID()),
	}
	st, err := a.Current()
	if err != nil {
		v.Error = err.Error()
	}
	v.Mode, v.Provider, v.Model, v.Detail = st.Mode, st.Provider, st.Model, st.Detail
	if v.Managed {
		s := safekeep.State(a.ID())
		v.Fast, v.Effort = s["fast"], s["effort"]
		v.Drift = safekeep.Check(a.ID())
	}
	return v
}

// Request is a switch the user asked for.
type Request struct {
	Agent  string     `json:"agent"`
	Ref    string     `json:"ref"`              // "<provider>/<model>" or "official"
	Fast   *bool      `json:"fast,omitempty"`   // nil leaves the user's own choice alone
	Effort string     `json:"effort,omitempty"` // "" leaves it alone
	Mode   agent.Mode `json:"mode,omitempty"`   // relay needs a foreground owner, not an ordinary switch
}

// Preview is what a switch would change, written nowhere.
type Preview struct {
	Diff    string `json:"diff"`
	Notice  string `json:"notice,omitempty"`
	Nothing bool   `json:"nothing"` // nothing would change
}

// Result is what a switch did.
type Result struct {
	Notice string           `json:"notice,omitempty"`
	Report *safekeep.Report `json:"report,omitempty"` // for a switch back to official
}

// ErrUnknownAgent is an agent id no adapter has.
var ErrUnknownAgent = errors.New("unknown agent")

func lookup(id string) (agent.Agent, error) {
	a, ok := agent.Get(id)
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownAgent, id)
	}
	return a, nil
}

// planFor is the adapter's plan for r, with the choices it doesn't record
// itself put in its state (profiles read them back).
func planFor(r Request) (*plan.Plan, error) {
	if r.Mode != "" && r.Mode != agent.Direct {
		return nil, errors.New("relay needs a foreground process; use qcs use codex <provider>/<model> --relay")
	}
	a, err := lookup(r.Agent)
	if err != nil {
		return nil, err
	}
	pid, mid, ok := provider.ParseRef(r.Ref)
	if !ok {
		return nil, fmt.Errorf("%q is not <provider>/<model> or %s", r.Ref, Official)
	}
	p, err := a.Plan(agent.Target{Mode: agent.Direct, Provider: pid, Model: mid, Fast: r.Fast, Effort: r.Effort})
	if err != nil {
		return nil, err
	}
	if p.State == nil {
		p.State = map[string]string{}
	}
	if r.Fast != nil {
		p.State["fast"] = fmt.Sprint(*r.Fast)
	}
	if r.Effort != "" {
		p.State["effort"] = r.Effort
	}
	return p, nil
}

// PreviewUse says what Use(r) would change.
func PreviewUse(r Request) (Preview, error) {
	if r.Ref == Official {
		if _, err := lookup(r.Agent); err != nil {
			return Preview{}, err
		}
		if !safekeep.Touched(r.Agent) {
			return Preview{Nothing: true}, nil
		}
		d, err := safekeep.RestorePreview(r.Agent)
		return Preview{Diff: d, Nothing: strings.TrimSpace(d) == ""}, err
	}
	p, err := planFor(r)
	if err != nil {
		return Preview{}, err
	}
	d, err := safekeep.Preview(p)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Diff: d, Notice: p.Notice, Nothing: p.Empty() || strings.TrimSpace(d) == ""}, nil
}

// Use switches an agent: to a provider's model, or back to official.
func Use(r Request) (Result, error) {
	if r.Ref == Official {
		if _, err := lookup(r.Agent); err != nil {
			return Result{}, err
		}
		if !safekeep.Touched(r.Agent) {
			return Result{}, nil
		}
		rep, err := safekeep.Restore(r.Agent)
		return Result{Report: rep}, err
	}
	p, err := planFor(r)
	if err != nil {
		return Result{}, err
	}
	if err := safekeep.Apply(p); err != nil {
		return Result{}, err
	}
	return Result{Notice: p.Notice}, nil
}

// Doctor is an agent's problems, its drift since this tool last wrote
// included.
func Doctor(id string) ([]agent.Problem, error) {
	a, err := lookup(id)
	if err != nil {
		return nil, err
	}
	ps := a.Doctor()
	for _, d := range safekeep.Check(id) {
		ps = append(ps, agent.Problem{Level: "warn", Message: fmt.Sprintf("%s: %s 在 qcs 写入后被改动（%s）", d.Path, d.Slot, d.Kind)})
	}
	return ps, nil
}

// Current is the request that would bring the agent back to where it is
// now: official, or the provider/model (with Fast and effort) safekeep
// recorded for it.
func Current(id string) Request {
	if !safekeep.Touched(id) {
		return Request{Agent: id, Ref: Official}
	}
	s := safekeep.State(id)
	r := Request{Agent: id, Ref: s["provider"] + "/" + s["model"], Effort: s["effort"]}
	if s["mode"] == string(agent.Relay) {
		r.Mode = agent.Relay
	}
	if s["provider"] == "" || s["model"] == "" {
		r.Ref = Official
	}
	switch s["fast"] {
	case "true":
		b := true
		r.Fast = &b
	case "false":
		b := false
		r.Fast = &b
	}
	return r
}
