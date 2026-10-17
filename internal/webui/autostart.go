package webui

import "net/http"

func (s *server) getAutostart(w http.ResponseWriter, r *http.Request) {
	if s.o.Autostart == nil {
		writeErr(w, http.StatusServiceUnavailable, "this host cannot manage login startup")
		return
	}
	st, err := s.o.Autostart.Status()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *server) setAutostart(w http.ResponseWriter, r *http.Request) {
	if s.o.Autostart == nil {
		writeErr(w, http.StatusServiceUnavailable, "this host cannot manage login startup")
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "enabled is required")
		return
	}
	var err error
	if *in.Enabled {
		_, err = s.o.Autostart.Enable()
	} else {
		_, err = s.o.Autostart.Disable()
	}
	if err != nil {
		fail(w, err)
		return
	}
	st, err := s.o.Autostart.Status()
	if err != nil {
		fail(w, err)
		return
	}
	s.withState(w, map[string]any{"autostart": st})
}
