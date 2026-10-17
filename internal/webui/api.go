package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/core"
	"github.com/zzf-857/quickerconfigswitch/internal/importer"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

func (s *server) routes(m *http.ServeMux) {
	m.HandleFunc("GET /api/state", s.getState)
	m.HandleFunc("POST /api/preview", s.locked(s.preview))
	m.HandleFunc("POST /api/use", s.locked(s.use))
	m.HandleFunc("GET /api/doctor", s.doctor)

	m.HandleFunc("GET /api/templates", s.templates)
	m.HandleFunc("POST /api/providers", s.locked(s.addProvider))
	m.HandleFunc("PUT /api/providers/{id}", s.locked(s.updateProvider))
	m.HandleFunc("DELETE /api/providers/{id}", s.locked(s.removeProvider))
	m.HandleFunc("POST /api/providers/{id}/fetch", s.locked(s.fetchModels))

	m.HandleFunc("POST /api/profiles", s.locked(s.saveProfile))
	m.HandleFunc("PUT /api/profiles/{name}", s.locked(s.putProfile))
	m.HandleFunc("DELETE /api/profiles/{name}", s.locked(s.deleteProfile))
	m.HandleFunc("POST /api/profiles/{name}/preview", s.locked(s.previewProfile))
	m.HandleFunc("POST /api/profiles/{name}/apply", s.locked(s.applyProfile))

	m.HandleFunc("GET /api/import", s.scanImport)
	m.HandleFunc("POST /api/import", s.locked(s.doImport))

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "no such endpoint")
	})
}

// locked runs h with the server's lock held: previews read the same files
// switches write, and nothing else here guards against two at once.
func (s *server) locked(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		h(w, r)
	}
}

// ---- responses ----

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: maskText(scrubKeys(msg))})
}

// fail writes err with a status picked from what it says.
func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	msg := err.Error()
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, core.ErrUnknownAgent),
		strings.Contains(msg, "not found"), strings.HasPrefix(msg, "no profile"):
		status = http.StatusNotFound
	case strings.Contains(msg, "already exists"):
		status = http.StatusConflict
	}
	writeErr(w, status, msg)
}

// scrubKeys removes every stored API key and secret header value from s, so
// an error that quotes a provider can't leak one.
func scrubKeys(s string) string {
	ps, err := provider.List()
	if err != nil {
		return s
	}
	for _, p := range ps {
		if len(p.APIKey) >= 4 {
			s = strings.ReplaceAll(s, p.APIKey, "***")
		}
		for k, v := range p.Headers {
			if secretHeader(k) && len(v) >= 4 {
				s = strings.ReplaceAll(s, v, "***")
			}
		}
	}
	return s
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return err
		}
		if errors.Is(err, io.EOF) {
			return errors.New("empty request body")
		}
		return fmt.Errorf("bad JSON: %v", err)
	}
	return nil
}

// ---- views ----

// providerView is a provider as the page sees it: the key masked.
type providerView struct {
	provider.Provider
	HasKey bool `json:"hasKey"`
}

// secretHeader: a header whose value is a credential and is masked like
// the key.
func secretHeader(name string) bool { return core.SecretHeader(name) }

func maskProvider(p provider.Provider) providerView {
	v := providerView{Provider: p, HasKey: p.APIKey != ""}
	v.APIKey = provider.MaskKey(p.APIKey)
	if len(p.Headers) > 0 {
		h := make(map[string]string, len(p.Headers))
		for k, val := range p.Headers {
			if secretHeader(k) {
				val = provider.MaskKey(val)
			}
			h[k] = val
		}
		v.Headers = h
	}
	return v
}

type problem struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

func problems(ps []agent.Problem) []problem {
	out := make([]problem, 0, len(ps))
	for _, p := range ps {
		out = append(out, problem{Level: p.Level, Message: p.Message})
	}
	return out
}

type state struct {
	Agents    []core.AgentView `json:"agents"`
	Providers []providerView   `json:"providers"`
	Profiles  []core.Profile   `json:"profiles"`
	Version   string           `json:"version"`
}

func (s *server) state() (state, error) {
	st := state{Agents: core.Agents(), Version: s.o.Version}
	if st.Agents == nil {
		st.Agents = []core.AgentView{}
	}
	ps, err := provider.List()
	if err != nil {
		return st, err
	}
	st.Providers = make([]providerView, 0, len(ps))
	for _, p := range ps {
		st.Providers = append(st.Providers, maskProvider(p))
	}
	if st.Profiles, err = core.Profiles(); err != nil {
		return st, err
	}
	return st, nil
}

func (s *server) getState(w http.ResponseWriter, r *http.Request) {
	st, err := s.state()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// withState answers v merged with a fresh "state", so the page redraws
// from one response after a change.
func (s *server) withState(w http.ResponseWriter, fields map[string]any) {
	st, err := s.state()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	fields["state"] = st
	writeJSON(w, http.StatusOK, fields)
}

// ---- switching ----

func (s *server) preview(w http.ResponseWriter, r *http.Request) {
	var req core.Request
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	pv, err := core.PreviewUse(req)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, maskPreview(pv))
}

func (s *server) use(w http.ResponseWriter, r *http.Request) {
	var req core.Request
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	res, err := core.Use(req)
	if err != nil {
		fail(w, err)
		return
	}
	res.Notice = maskText(res.Notice)
	s.withState(w, map[string]any{"result": res})
}

func (s *server) doctor(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("agent")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "agent is required")
		return
	}
	ps, err := core.Doctor(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": id, "problems": problems(ps)})
}

// ---- providers ----

func (s *server) templates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, provider.Templates())
}

func (s *server) addProvider(w http.ResponseWriter, r *http.Request) {
	var p provider.Provider
	if err := decode(r, &p); err != nil {
		fail(w, err)
		return
	}
	if err := provider.Add(p); err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{"provider": maskProvider(p)})
}

// providerEdit is the body of an update: the provider, and whether to drop
// the stored key (an empty or masked apiKey keeps it).
type providerEdit struct {
	provider.Provider
	ClearKey bool `json:"clearKey,omitempty"`
}

// keepSecrets puts the stored key and secret header values back where the
// page sent them empty or masked.
func keepSecrets(in providerEdit, old provider.Provider) provider.Provider {
	p := in.Provider
	switch {
	case in.ClearKey:
		p.APIKey = ""
	case p.APIKey == "" || p.APIKey == provider.MaskKey(old.APIKey):
		p.APIKey = old.APIKey
	}
	for k, v := range p.Headers {
		if ov, ok := old.Headers[k]; ok && secretHeader(k) && (v == "" || v == provider.MaskKey(ov)) {
			p.Headers[k] = ov
		}
	}
	return p
}

func (s *server) updateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in providerEdit
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.ID == "" {
		in.ID = id
	}
	if in.ID != id {
		writeErr(w, http.StatusBadRequest, "the id in the body differs from the URL; ids can't be renamed")
		return
	}
	old, ok, err := provider.Get(id)
	if err != nil {
		fail(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("provider %q not found", id))
		return
	}
	p := keepSecrets(in, old)
	if err := provider.Update(p); err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{"provider": maskProvider(p)})
}

func (s *server) removeProvider(w http.ResponseWriter, r *http.Request) {
	if err := provider.Remove(r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{})
}

// mergeModels adds the fetched models old doesn't list yet, keeping old's
// models (and their Fast, efforts, ...) as they are and in their order.
func mergeModels(old, fetched []provider.Model) (out []provider.Model, added []string) {
	have := make(map[string]bool, len(old))
	out = append(out, old...)
	for _, m := range old {
		have[m.ID] = true
	}
	for _, m := range fetched {
		if !have[m.ID] {
			have[m.ID] = true
			out = append(out, m)
			added = append(added, m.ID)
		}
	}
	return out, added
}

func (s *server) fetchModels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok, err := provider.Get(id)
	if err != nil {
		fail(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("provider %q not found", id))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	ms, err := provider.FetchModels(ctx, p)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	var added []string
	p.Models, added = mergeModels(p.Models, ms)
	if added == nil {
		added = []string{}
	}
	if len(added) > 0 {
		if err := provider.Update(p); err != nil {
			fail(w, err)
			return
		}
	}
	s.withState(w, map[string]any{"provider": maskProvider(p), "added": added, "offered": len(ms)})
}

// ---- profiles ----

func (s *server) saveProfile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	p, err := core.SaveProfile(in.Name)
	if err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{"profile": p})
}

func (s *server) putProfile(w http.ResponseWriter, r *http.Request) {
	var p core.Profile
	if err := decode(r, &p); err != nil {
		fail(w, err)
		return
	}
	name := r.PathValue("name")
	if p.Name == "" {
		p.Name = name
	}
	if p.Name != name {
		writeErr(w, http.StatusBadRequest, "the name in the body differs from the URL")
		return
	}
	if err := core.PutProfile(p); err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{"profile": p})
}

func (s *server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	if err := core.DeleteProfile(r.PathValue("name")); err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{})
}

func (s *server) previewProfile(w http.ResponseWriter, r *http.Request) {
	steps, err := core.PreviewProfile(r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	if steps == nil {
		steps = []core.ProfileStep{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"steps": maskSteps(steps)})
}

func (s *server) applyProfile(w http.ResponseWriter, r *http.Request) {
	steps, err := core.ApplyProfile(r.PathValue("name"))
	if err != nil {
		fail(w, err)
		return
	}
	if steps == nil {
		steps = []core.ProfileStep{}
	}
	s.withState(w, map[string]any{"steps": maskSteps(steps)})
}

// ---- import ----

type candidateView struct {
	Index    int          `json:"index"`
	Source   string       `json:"source"`
	Origin   string       `json:"origin"`
	Provider providerView `json:"provider"`
	Exists   string       `json:"exists,omitempty"`
	Note     string       `json:"note,omitempty"`
}

func (s *server) scanImport(w http.ResponseWriter, r *http.Request) {
	cands, errs := importer.Scan()
	out := struct {
		Candidates []candidateView `json:"candidates"`
		Errors     []string        `json:"errors"`
	}{Candidates: []candidateView{}, Errors: []string{}}
	for i, c := range cands {
		out.Candidates = append(out.Candidates, candidateView{
			Index: i, Source: c.Source, Origin: c.Origin,
			Provider: maskProvider(c.Provider), Exists: c.Exists, Note: c.Note,
		})
	}
	for _, e := range errs {
		msg := e.Error()
		for _, c := range cands {
			if len(c.Provider.APIKey) >= 4 {
				msg = strings.ReplaceAll(msg, c.Provider.APIKey, "***")
			}
		}
		out.Errors = append(out.Errors, scrubKeys(msg))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) doImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Index  *int   `json:"index"`
		ID     string `json:"id"`
		Source string `json:"source,omitempty"` // when given, must match the candidate
		Origin string `json:"origin,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.Index == nil {
		writeErr(w, http.StatusBadRequest, "index is required")
		return
	}
	cands, _ := importer.Scan()
	i := *in.Index
	if i < 0 || i >= len(cands) {
		writeErr(w, http.StatusNotFound, "no such import candidate; scan again")
		return
	}
	c := cands[i]
	if (in.Source != "" && in.Source != c.Source) || (in.Origin != "" && in.Origin != c.Origin) {
		writeErr(w, http.StatusConflict, "the candidates changed since the scan; scan again")
		return
	}
	p, err := importer.Add(c, in.ID)
	if err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{"provider": maskProvider(p)})
}
