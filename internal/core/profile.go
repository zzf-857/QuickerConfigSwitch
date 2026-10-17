package core

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
	"github.com/zzf-857/quickerconfigswitch/internal/store"
)

// A Profile is every agent's pick under one name ("省钱", "work"): applying
// it switches each agent it names. "official" is built in and switches
// every agent back.
type Profile struct {
	Name   string    `json:"name"`
	Agents []Request `json:"agents"`
}

type profileFile struct {
	Profiles []Profile `json:"profiles"`
}

var profileMu sync.Mutex

func profilePath() string { return appdir.Path("profiles.json") }

// Profiles lists the saved profiles by name, the built-in official first.
func Profiles() ([]Profile, error) {
	profileMu.Lock()
	defer profileMu.Unlock()
	f, err := loadProfiles()
	if err != nil {
		return nil, err
	}
	out := []Profile{officialProfile()}
	return append(out, f.Profiles...), nil
}

func officialProfile() Profile {
	p := Profile{Name: Official}
	for _, a := range agent.All() {
		p.Agents = append(p.Agents, Request{Agent: a.ID(), Ref: Official})
	}
	return p
}

func loadProfiles() (profileFile, error) {
	var f profileFile
	if err := store.Load(profilePath(), &f); err != nil {
		return f, err
	}
	sort.Slice(f.Profiles, func(i, j int) bool { return f.Profiles[i].Name < f.Profiles[j].Name })
	return f, nil
}

func validName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" || n != name || len(n) > 64 {
		return fmt.Errorf("profile name %q: 1-64 characters, no surrounding spaces", name)
	}
	if strings.EqualFold(n, Official) {
		return fmt.Errorf("%q is built in", Official)
	}
	return nil
}

// SaveProfile saves every installed agent's current pick under name,
// replacing a profile of that name.
func SaveProfile(name string) (Profile, error) {
	if err := validName(name); err != nil {
		return Profile{}, err
	}
	p := Profile{Name: name}
	for _, a := range agent.All() {
		if a.Detect().Installed || safekeep.Touched(a.ID()) {
			r := Current(a.ID())
			if r.Mode == agent.Relay {
				return Profile{}, fmt.Errorf("%s uses a foreground relay; stop it before saving a profile", a.ID())
			}
			p.Agents = append(p.Agents, r)
		}
	}
	return p, putProfile(p)
}

// PutProfile saves a profile as given (the page's editor).
func PutProfile(p Profile) error {
	if err := validName(p.Name); err != nil {
		return err
	}
	for _, r := range p.Agents {
		if r.Mode != "" && r.Mode != agent.Direct {
			return fmt.Errorf("profiles cannot start foreground relay processes")
		}
		if _, err := lookup(r.Agent); err != nil {
			return err
		}
	}
	return putProfile(p)
}

func putProfile(p Profile) error {
	profileMu.Lock()
	defer profileMu.Unlock()
	f, err := loadProfiles()
	if err != nil {
		return err
	}
	kept := f.Profiles[:0]
	for _, q := range f.Profiles {
		if q.Name != p.Name {
			kept = append(kept, q)
		}
	}
	f.Profiles = append(kept, p)
	return store.Save(profilePath(), f, 0o600)
}

// DeleteProfile removes a saved profile.
func DeleteProfile(name string) error {
	profileMu.Lock()
	defer profileMu.Unlock()
	f, err := loadProfiles()
	if err != nil {
		return err
	}
	kept := f.Profiles[:0]
	found := false
	for _, q := range f.Profiles {
		if q.Name == name {
			found = true
			continue
		}
		kept = append(kept, q)
	}
	if !found {
		return fmt.Errorf("no profile %q", name)
	}
	f.Profiles = kept
	return store.Save(profilePath(), f, 0o600)
}

func findProfile(name string) (Profile, error) {
	ps, err := Profiles()
	if err != nil {
		return Profile{}, err
	}
	for _, p := range ps {
		if p.Name == name {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("no profile %q", name)
}

// ProfileStep is one agent's part of applying a profile.
type ProfileStep struct {
	Request
	Preview Preview `json:"preview"`
	Result  *Result `json:"result,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// PreviewProfile says what ApplyProfile would change, agent by agent.
func PreviewProfile(name string) ([]ProfileStep, error) {
	p, err := findProfile(name)
	if err != nil {
		return nil, err
	}
	var out []ProfileStep
	for _, r := range p.Agents {
		s := ProfileStep{Request: r}
		if pv, err := PreviewUse(r); err != nil {
			s.Error = err.Error()
		} else {
			s.Preview = pv
		}
		out = append(out, s)
	}
	return out, nil
}

// ApplyProfile switches each agent the profile names. An agent that fails
// doesn't stop the others; its step says why.
func ApplyProfile(name string) ([]ProfileStep, error) {
	p, err := findProfile(name)
	if err != nil {
		return nil, err
	}
	return ApplyProfileSnapshot(p), nil
}

// ApplyProfileSnapshot applies the requests already inspected by a caller.
// It does not reload a named profile, which could have changed between a
// lifecycle guard and execution. Failures stay attached to their own step.
func ApplyProfileSnapshot(p Profile) []ProfileStep {
	var out []ProfileStep
	for _, r := range p.Agents {
		s := ProfileStep{Request: r}
		if res, err := Use(r); err != nil {
			s.Error = err.Error()
		} else {
			s.Result = &res
		}
		out = append(out, s)
	}
	return out
}
