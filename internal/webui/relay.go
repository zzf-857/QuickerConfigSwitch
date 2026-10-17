package webui

import (
	"net/http"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/core"
)

// withRelayIdle prevents ordinary configuration changes from racing with
// a preview or live relay owned by this process. Other agents remain usable.
func (s *server) withRelayIdle(codex bool, fn func() error) error {
	if codex && s.o.Relay != nil {
		return s.o.Relay.WithIdle(fn)
	}
	return fn()
}

func (s *server) relayReady(w http.ResponseWriter) bool {
	if s.o.Relay == nil {
		writeErr(w, http.StatusServiceUnavailable, "this server does not manage relays")
		return false
	}
	return true
}

func (s *server) previewRelay(w http.ResponseWriter, r *http.Request) {
	if !s.relayReady(w) {
		return
	}
	var in struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		All      bool   `json:"all"`
		Port     *int   `json:"port,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	port := core.DefaultRelayPort
	if in.Port != nil {
		port = *in.Port
	}
	pv, err := s.o.Relay.Preview(agent.Target{
		Mode: agent.Relay, Provider: in.Provider, Model: in.Model, RelayAll: in.All,
	}, port)
	if err != nil {
		fail(w, err)
		return
	}
	pv.Diff, pv.Notice = maskText(pv.Diff), maskText(pv.Notice)
	writeJSON(w, http.StatusOK, pv)
}

func (s *server) startRelay(w http.ResponseWriter, r *http.Request) {
	if !s.relayReady(w) {
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.o.Relay.Start(in.ID); err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{})
}

func (s *server) cancelRelay(w http.ResponseWriter, r *http.Request) {
	if !s.relayReady(w) {
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.o.Relay.Cancel(in.ID); err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{})
}

func (s *server) stopRelay(w http.ResponseWriter, r *http.Request) {
	if !s.relayReady(w) {
		return
	}
	if err := decode(r, &struct{}{}); err != nil {
		fail(w, err)
		return
	}
	rep, err := s.o.Relay.Stop()
	if err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{"result": core.Result{Report: rep}})
}

func (s *server) recoverRelay(w http.ResponseWriter, r *http.Request) {
	if !s.relayReady(w) {
		return
	}
	if err := decode(r, &struct{}{}); err != nil {
		fail(w, err)
		return
	}
	rep, err := s.o.Relay.Recover()
	if err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{"result": core.Result{Report: rep}})
}
