package core

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/filelock"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

const relayPreviewTTL = 2 * time.Minute

// ErrRelayBusy means this controller owns an active relay or a preview.
// It also matches ErrRelayRunning for callers handling both owner kinds.
var ErrRelayBusy = fmt.Errorf("%w; cancel the preview or stop this app's relay first", ErrRelayRunning)

// ErrRelayClosed means process shutdown has permanently closed the owner.
var ErrRelayClosed = errors.New("codex relay: controller is closed")

// RelayPreview identifies one reserved address and the change to confirm.
// Start accepts its ID, so it cannot apply a different plan or port.
type RelayPreview struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Diff      string    `json:"diff"`
	Notice    string    `json:"notice,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// RelayStatus describes the process that owns the relay, or a stale record
// that can be recovered. Error explains a serving, cleanup or probe failure.
// A controller that has been closed reports disabled and cannot restart.
type RelayStatus struct {
	State     string     `json:"state"` // idle, preview, running, external, stale, error, disabled
	URL       string     `json:"url,omitempty"`
	Provider  string     `json:"provider,omitempty"`
	Model     string     `json:"model,omitempty"`
	All       bool       `json:"all"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type relayPending struct {
	preview RelayPreview
	session *RelaySession
	timer   *time.Timer
}

// RelayController owns a desktop or browser process's relay. The main
// window and tray panel must share it. Close belongs in the process's
// shutdown hook, even when no relay has been started.
type RelayController struct {
	mu       sync.Mutex
	pending  *relayPending
	active   *RelaySession
	lastErr  error
	closed   bool
	closeErr error
	ttl      time.Duration
}

// NewRelayController creates a controller with two-minute previews. A
// preview that is abandoned releases its listener and process lock even
// when the page is closed before it can send Cancel.
func NewRelayController() *RelayController {
	return &RelayController{ttl: relayPreviewTTL}
}

// Preview reserves the address and lock without changing Codex. It does
// not replace an existing preview: another window's confirmation stays
// valid until that preview is canceled or expires.
func (c *RelayController) Preview(t agent.Target, port int) (RelayPreview, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.available(); err != nil {
		return RelayPreview{}, err
	}
	s, err := PrepareRelay(t, port)
	if err != nil {
		return RelayPreview{}, err
	}
	pv := RelayPreview{
		ID: rand.Text(), URL: s.URL, Diff: s.Preview, Notice: s.Notice,
		ExpiresAt: time.Now().Add(c.ttl),
	}
	pending := &relayPending{preview: pv, session: s}
	c.pending, c.lastErr = pending, nil
	pending.timer = time.AfterFunc(c.ttl, func() { c.expire(pv.ID) })
	return pv, nil
}

// available runs with c.mu held.
func (c *RelayController) available() error {
	if c.closed {
		return ErrRelayClosed
	}
	if c.pending != nil || c.active != nil {
		return ErrRelayBusy
	}
	return nil
}

func (c *RelayController) expire(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil || c.pending.preview.ID != id {
		return
	}
	c.lastErr = c.cancelPending()
}

// cancelPending runs with c.mu held and releases only the pending session.
func (c *RelayController) cancelPending() error {
	pending := c.pending
	c.pending = nil
	pending.timer.Stop()
	_, err := pending.session.Close()
	return err
}

// Start confirms the exact reserved preview. Config and provider changes
// are rechecked by the session; failure releases the preview and its lock.
func (c *RelayController) Start(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrRelayClosed
	}
	if c.pending == nil || id == "" || c.pending.preview.ID != id {
		return errors.New("codex relay: preview expired or no longer exists; preview again")
	}
	if !time.Now().Before(c.pending.preview.ExpiresAt) {
		err := c.cancelPending()
		c.lastErr = err
		return errors.Join(errors.New("codex relay: preview expired; preview again"), err)
	}
	pending := c.pending
	c.pending = nil
	pending.timer.Stop()
	if err := pending.session.Start(); err != nil {
		c.lastErr = err
		return err
	}
	c.active, c.lastErr = pending.session, nil
	go c.watch(pending.session)
	return nil
}

func (c *RelayController) watch(s *RelaySession) {
	err := <-s.Done()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == s {
		c.active, c.lastErr = nil, err
	}
}

// Cancel discards one preview. Its ID prevents a late cancel from an old
// dialog from releasing a newer preview or stopping a confirmed relay.
func (c *RelayController) Cancel(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrRelayClosed
	}
	if c.pending == nil || id == "" || c.pending.preview.ID != id {
		return errors.New("codex relay: preview expired or no longer exists")
	}
	c.lastErr = c.cancelPending()
	return c.lastErr
}

// Stop closes only this controller's active relay. An external process's
// relay is left running; stale records need the explicit Recover action.
func (c *RelayController) Stop() (*safekeep.Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrRelayClosed
	}
	if c.pending != nil {
		return nil, ErrRelayBusy
	}
	if c.active == nil {
		st := relayStatus()
		if st.State == "external" {
			return nil, ErrRelayRunning
		}
		if st.State == "error" {
			return nil, errors.New(st.Error)
		}
		return nil, nil
	}
	s := c.active
	c.active = nil
	report, err := s.Close()
	c.lastErr = err
	return report, err
}

// Recover restores a stale relay record. The shared process lock prevents
// recovery while a CLI process or another desktop instance owns a relay.
func (c *RelayController) Recover() (*safekeep.Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.available(); err != nil {
		return nil, err
	}
	report, err := RecoverRelay()
	c.lastErr = err
	return report, err
}

// Busy reports whether this controller has a pending or active relay.
// Callers use it to require an explicit stop before an ordinary switch.
func (c *RelayController) Busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pending != nil || c.active != nil
}

// WithIdle serializes an ordinary Codex switch with this controller's
// lifecycle, including across handlers sharing the owner. The callback
// must not call the controller again while this lock is held.
func (c *RelayController) WithIdle(fn func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.available(); err != nil {
		return err
	}
	return fn()
}

// Status reads the current owner and saved record without writing config,
// creating a lock file or recovering anything. Lock-probe errors are
// reported as error, never as a stale record that is safe to recover.
func (c *RelayController) Status() RelayStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return RelayStatus{State: "disabled"}
	}
	if c.pending != nil {
		st := sessionStatus("preview", c.pending.session)
		expires := c.pending.preview.ExpiresAt
		st.ExpiresAt = &expires
		return st
	}
	if c.active != nil {
		return sessionStatus("running", c.active)
	}
	st := relayStatus()
	if c.lastErr != nil {
		st.Error = errors.Join(c.lastErr, errorFromString(st.Error)).Error()
		if st.State == "idle" {
			st.State = "error"
		}
	}
	return st
}

func errorFromString(s string) error {
	if s == "" {
		return nil
	}
	return errors.New(s)
}

func sessionStatus(state string, s *RelaySession) RelayStatus {
	return RelayStatus{
		State: state, URL: s.URL, Provider: s.target.Provider, Model: s.target.Model, All: s.target.RelayAll,
	}
}

// relayStatus probes only an existing lock. Hold the probe lock through
// reading the record so another relay cannot start between those reads.
func relayStatus() RelayStatus {
	st := RelayStatus{State: "idle"}
	f, err := os.OpenFile(filepath.Join(appdir.StateDir(), "relay.lock"), os.O_RDWR, 0)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return RelayStatus{State: "error", Error: fmt.Sprintf("codex relay: cannot inspect relay lock: %v", err)}
	}
	if err == nil {
		defer f.Close()
		held, err := filelock.TryLock(f)
		if err != nil {
			return RelayStatus{State: "error", Error: fmt.Sprintf("codex relay: cannot inspect relay lock: %v", err)}
		}
		if held {
			defer filelock.Unlock(f)
		} else {
			st.State = "external"
		}
	}
	state := safekeep.State("codex")
	if state["mode"] == string(agent.Relay) {
		st.URL, st.Provider, st.Model = state["relay_url"], state["provider"], state["model"]
		st.All = state["relay_all"] == "true"
		if st.State == "idle" {
			st.State = "stale"
		}
	}
	return st
}

// Close permanently closes the controller before releasing its sessions,
// so a simultaneous request cannot restart it during process shutdown.
// Repeated calls return the same cleanup error.
func (c *RelayController) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return c.closeErr
	}
	c.closed = true
	if c.pending != nil {
		c.closeErr = c.cancelPending()
	}
	if c.active != nil {
		s := c.active
		c.active = nil
		_, err := s.Close()
		c.closeErr = errors.Join(c.closeErr, err)
	}
	return c.closeErr
}
