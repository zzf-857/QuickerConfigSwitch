package core

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/filelock"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/relay"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

// DefaultRelayPort is preferred when the caller has not chosen a port.
// An occupied default falls back to an available port; an explicit port
// other than this one is used as given. Port zero asks the OS to choose.
const DefaultRelayPort = 17321

// ErrRelayRunning means another process owns the Codex relay. Its lock
// must be acquired before either replacing or restoring the relay URL.
var ErrRelayRunning = errors.New("codex relay is already running")

// RelaySession owns a loopback listener and its temporary Codex config.
// PrepareRelay reserves the listener; Start applies the previewed plan.
// Close must be called even when the preview is declined.
type RelaySession struct {
	URL     string
	Preview string
	Notice  string

	mu       sync.Mutex
	target   agent.Target
	provider provider.Provider
	plan     *plan.Plan
	state    map[string]string
	id       string
	listener net.Listener
	server   *http.Server
	unlock   func()
	started  bool
	applied  bool
	closed   bool
	report   *safekeep.Report
	closeErr error
	done     chan error
	finish   sync.Once
}

// PrepareRelay reserves the loopback address and previews the adapter's
// plan without changing Codex. A stale relay record can be adopted after
// acquiring the lock: safekeep keeps its original snapshot across the
// restart, so closing the new session still restores the user's address.
func PrepareRelay(t agent.Target, port int) (*RelaySession, error) {
	if port < 0 || port > 65535 {
		return nil, fmt.Errorf("codex relay: invalid port %d", port)
	}
	unlock, err := lockRelay()
	if err != nil {
		return nil, err
	}
	s := &RelaySession{unlock: unlock, done: make(chan error, 1)}
	ready := false
	defer func() {
		if !ready {
			s.Close()
		}
	}()
	s.state = safekeep.State("codex")
	if safekeep.Touched("codex") && s.state["mode"] != string(agent.Relay) {
		return nil, errors.New("codex relay: switch back with qcs use codex official first")
	}
	s.listener, err = listenRelay(port)
	if err != nil {
		return nil, err
	}
	s.URL = "http://" + s.listener.Addr().String() + "/backend-api/codex"
	t.Mode, t.RelayURL = agent.Relay, s.URL
	if t.Fast != nil {
		b := *t.Fast
		t.Fast = &b
	}
	s.target = t
	a, err := lookup("codex")
	if err != nil {
		return nil, err
	}
	s.plan, err = a.Plan(t)
	if err != nil {
		return nil, err
	}
	s.Preview, err = safekeep.Preview(s.plan)
	if err != nil {
		return nil, err
	}
	s.Notice = s.plan.Notice
	s.provider, _, err = provider.Get(t.Provider)
	if err != nil {
		return nil, err
	}
	p := s.provider
	if len(p.Models) == 0 {
		p.Models = []provider.Model{{ID: t.Model, Fast: provider.DefaultFast(t.Model)}}
	}
	handler, err := relay.New(relay.Options{Provider: p, All: t.RelayAll, CodexHome: appdir.CodexHome()})
	if err != nil {
		return nil, err
	}
	s.server = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second}
	s.id = rand.Text()
	ready = true
	return s, nil
}

// StartRelay is PrepareRelay followed by Start, for callers that have
// already confirmed the change. Interactive callers should preview first.
func StartRelay(t agent.Target, port int) (*RelaySession, error) {
	s, err := PrepareRelay(t, port)
	if err != nil {
		return nil, err
	}
	if err := s.Start(); err != nil {
		return nil, err
	}
	return s, nil
}

// Start revalidates the preview, applies the plan and starts serving.
// Any failure releases the listener and lock, including a changed config
// or provider while the user was reading the preview.
func (s *RelaySession) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("codex relay: session is closed")
	}
	if s.started {
		return errors.New("codex relay: session already started")
	}
	if err := s.start(); err != nil {
		_, closeErr := s.close()
		err = errors.Join(err, closeErr)
		s.complete(err)
		return err
	}
	s.started = true
	go func() {
		err := s.server.Serve(s.listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		_, closeErr := s.Close()
		s.complete(errors.Join(err, closeErr))
	}()
	return nil
}

func (s *RelaySession) start() error {
	a, err := lookup("codex")
	if err != nil {
		return err
	}
	p, err := a.Plan(s.target)
	if err != nil {
		return err
	}
	diff, err := safekeep.Preview(p)
	if err != nil {
		return err
	}
	upstream, found, err := provider.Get(s.target.Provider)
	if err != nil {
		return err
	}
	if !found || !reflect.DeepEqual(upstream, s.provider) || !reflect.DeepEqual(p, s.plan) ||
		diff != s.Preview || !reflect.DeepEqual(safekeep.State("codex"), s.state) {
		return errors.New("codex relay: config or provider changed since preview; run the command again")
	}
	if p.State == nil {
		p.State = map[string]string{}
	}
	p.State["relay_session"] = s.id
	// A failed Apply also takes the guarded cleanup path; no other
	// session's state can be restored by that attempt.
	s.applied = true
	return safekeep.Apply(p)
}

// Done reports a serving or cleanup error, then closes. A normal close
// reports nil. A declined preview closes it without starting a server.
func (s *RelaySession) Done() <-chan error { return s.done }

func (s *RelaySession) complete(err error) {
	s.finish.Do(func() {
		s.done <- err
		close(s.done)
	})
}

// Close stops requests, restores only this session's recorded changes,
// and releases its lock. A later switch is left in place. Repeated calls
// return the same report and error.
func (s *RelaySession) Close() (*safekeep.Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	report, err := s.close()
	if !s.started {
		s.complete(err)
	}
	return report, err
}

// close runs with s.mu held. The serve goroutine never needs the mutex
// until Serve has returned, so shutdown does not wait for itself.
func (s *RelaySession) close() (*safekeep.Report, error) {
	if s.closed {
		return s.report, s.closeErr
	}
	s.closed = true
	defer s.unlock()
	if s.started {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := s.server.Shutdown(ctx)
		cancel()
		if err != nil {
			// Streaming responses can outlive the grace period. Cancel
			// them before taking away the address Codex still points to.
			s.closeErr = s.server.Close()
			if !errors.Is(err, context.DeadlineExceeded) {
				s.closeErr = errors.Join(s.closeErr, err)
			}
		}
	}
	if s.listener != nil {
		if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.closeErr = errors.Join(s.closeErr, err)
		}
	}
	if s.applied {
		var err error
		s.report, err = safekeep.RestoreIfState("codex", "relay_session", s.id)
		s.closeErr = errors.Join(s.closeErr, err)
	}
	return s.report, s.closeErr
}

// RecoverRelay restores a stale relay without starting another one. A
// live or previewing owner holds the same lock, so it cannot be disrupted.
func RecoverRelay() (*safekeep.Report, error) {
	unlock, err := lockRelay()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return safekeep.RestoreIfState("codex", "mode", string(agent.Relay))
}

func lockRelay() (func(), error) {
	path := filepath.Join(appdir.StateDir(), "relay.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	held, err := filelock.TryLock(f)
	if err != nil || !held {
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("codex relay: cannot lock %s: %w", path, err)
		}
		return nil, ErrRelayRunning
	}
	return func() { filelock.Unlock(f); f.Close() }, nil
}

func listenRelay(port int) (net.Listener, error) {
	l, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil && port == DefaultRelayPort {
		return net.Listen("tcp4", "127.0.0.1:0")
	}
	return l, err
}
