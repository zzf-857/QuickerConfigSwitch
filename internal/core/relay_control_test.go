package core

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/agent/codex"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func testRelayController(t *testing.T) *RelayController {
	t.Helper()
	c := NewRelayController()
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func controllerPreview(t *testing.T, c *RelayController) RelayPreview {
	t.Helper()
	pv, err := c.Preview(relayTarget(), 0)
	if err != nil {
		t.Fatal(err)
	}
	return pv
}

func awaitRelayStatus(t *testing.T, c *RelayController, state string) RelayStatus {
	t.Helper()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		st := c.Status()
		if st.State == state {
			return st
		}
		select {
		case <-deadline:
			t.Fatalf("relay status %+v; want %s", st, state)
		case <-tick.C:
		}
	}
}

func TestRelayControllerPreviewStartStop(t *testing.T) {
	cfg := setup(t)
	c := testRelayController(t)
	if st := c.Status(); st.State != "idle" || c.Busy() {
		t.Fatalf("new controller %+v", st)
	}
	if _, err := os.Stat(appdir.StateDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("status created state directory: %v", err)
	}
	pv := controllerPreview(t, c)
	if pv.ID == "" || pv.URL == "" || !strings.Contains(pv.Diff, pv.URL) || pv.Notice == "" ||
		time.Until(pv.ExpiresAt) < time.Minute || time.Until(pv.ExpiresAt) > relayPreviewTTL {
		t.Fatalf("preview %+v", pv)
	}
	if readRelayFile(t, cfg) != codexCfg || safekeep.Touched("codex") {
		t.Fatal("preview wrote config")
	}
	st := c.Status()
	if st.State != "preview" || st.URL != pv.URL || st.Provider != "cpa" || st.Model != "alias" ||
		st.ExpiresAt == nil || !st.ExpiresAt.Equal(pv.ExpiresAt) || !c.Busy() {
		t.Fatalf("preview state %+v", st)
	}
	if _, err := c.Preview(relayTarget(), 0); !errors.Is(err, ErrRelayRunning) {
		t.Fatalf("replaced preview: %v", err)
	}
	if err := c.Start("wrong"); err == nil || c.Status().URL != pv.URL {
		t.Fatal("invalid confirmation changed preview")
	}
	if _, err := c.Stop(); !errors.Is(err, ErrRelayRunning) {
		t.Fatalf("stop discarded preview: %v", err)
	}
	if _, err := c.Recover(); !errors.Is(err, ErrRelayRunning) {
		t.Fatalf("recovery discarded preview: %v", err)
	}
	if err := c.Start(pv.ID); err != nil {
		t.Fatal(err)
	}
	if u, _ := edit.GetTOMLTop(cfg, "openai_base_url"); u != pv.URL {
		t.Fatalf("did not use reserved URL: %q", u)
	}
	if st := c.Status(); st.State != "running" || st.URL != pv.URL || st.ExpiresAt != nil || !c.Busy() {
		t.Fatalf("running state %+v", st)
	}
	if err := c.Start(pv.ID); err == nil {
		t.Fatal("replayed confirmation")
	}
	if err := c.Cancel(pv.ID); err == nil || c.Status().State != "running" {
		t.Fatal("late cancel stopped confirmed relay")
	}
	if _, err := c.Preview(relayTarget(), 0); !errors.Is(err, ErrRelayRunning) {
		t.Fatalf("replaced active relay: %v", err)
	}
	report, err := c.Stop()
	if err != nil || report == nil || report.Left() {
		t.Fatalf("stop %+v %v", report, err)
	}
	if c.Busy() || c.Status().State != "idle" || readRelayFile(t, cfg) != codexCfg || safekeep.Touched("codex") {
		t.Fatal("stop left relay configured")
	}
	if report, err := c.Stop(); report != nil || err != nil {
		t.Fatalf("repeated stop %+v %v", report, err)
	}
	controllerPreview(t, c) // stop released the process lock
}

func TestRelayControllerCancelAndExpiration(t *testing.T) {
	cfg := setup(t)
	c := testRelayController(t)
	pv := controllerPreview(t, c)
	if err := c.Cancel("wrong"); err == nil || !c.Busy() {
		t.Fatal("wrong cancel discarded preview")
	}
	if err := c.Cancel(pv.ID); err != nil {
		t.Fatal(err)
	}
	if c.Busy() || c.Status().State != "idle" || readRelayFile(t, cfg) != codexCfg {
		t.Fatal("cancel changed config or kept lock")
	}
	next := controllerPreview(t, c)
	if next.ID == pv.ID {
		t.Fatal("reused preview ID")
	}
	c.expire(pv.ID)
	if c.Status().State != "preview" || c.Status().URL != next.URL {
		t.Fatal("old timer discarded newer preview")
	}
	if err := c.Cancel(pv.ID); err == nil {
		t.Fatal("old dialog canceled newer preview")
	}
	// Start checks the deadline itself even if a timer callback is delayed.
	c.mu.Lock()
	c.pending.preview.ExpiresAt = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if err := c.Start(next.ID); err == nil || !strings.Contains(err.Error(), "expired") || c.Busy() {
		t.Fatalf("expired preview accepted: %v", err)
	}
	// The timer also releases abandoned previews without another API call.
	c.ttl = 20 * time.Millisecond
	last := controllerPreview(t, c)
	awaitRelayStatus(t, c, "idle")
	if err := c.Start(last.ID); err == nil || c.Busy() {
		t.Fatal("timer did not invalidate confirmation")
	}
	s := prepareTestRelay(t)
	if s.URL == "" || readRelayFile(t, cfg) != codexCfg {
		t.Fatal("expiry did not release process lock cleanly")
	}
}

func TestRelayControllerStartRevalidatesAndReleases(t *testing.T) {
	for _, change := range []string{"config", "provider"} {
		t.Run(change, func(t *testing.T) {
			cfg := setup(t)
			c := testRelayController(t)
			pv := controllerPreview(t, c)
			want := codexCfg
			if change == "config" {
				want += "# changed after preview\n"
				if err := os.WriteFile(cfg, []byte(want), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				p, _, _ := provider.Get("cpa")
				p.APIKey = "changed-key"
				if err := provider.Update(p); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Start(pv.ID); err == nil || c.Busy() {
				t.Fatalf("changed input accepted or lock leaked: %v", err)
			}
			if st := c.Status(); st.State != "error" || st.Error == "" {
				t.Fatalf("missing validation error %+v", st)
			}
			if readRelayFile(t, cfg) != want || safekeep.Touched("codex") {
				t.Fatal("failed confirmation wrote config")
			}
			controllerPreview(t, c)
			if st := c.Status(); st.State != "preview" || st.Error != "" {
				t.Fatalf("new preview did not clear failure %+v", st)
			}
		})
	}
}

func TestRelayControllerExternalOwnerAndStaleRecovery(t *testing.T) {
	cfg := setup(t)
	c := testRelayController(t)
	external := prepareTestRelay(t)
	if st := c.Status(); st.State != "external" || st.URL != "" || c.Busy() {
		t.Fatalf("external preview %+v", st)
	}
	if _, err := c.Stop(); !errors.Is(err, ErrRelayRunning) {
		t.Fatalf("stopped external preview: %v", err)
	}
	if _, err := c.Recover(); !errors.Is(err, ErrRelayRunning) {
		t.Fatalf("recovered external preview: %v", err)
	}
	if _, err := c.Preview(relayTarget(), 0); !errors.Is(err, ErrRelayRunning) {
		t.Fatalf("took external preview: %v", err)
	}
	if err := external.Start(); err != nil {
		t.Fatal(err)
	}
	if st := c.Status(); st.State != "external" || st.URL != external.URL || st.Provider != "cpa" {
		t.Fatalf("external running state %+v", st)
	}
	if _, err := c.Stop(); !errors.Is(err, ErrRelayRunning) {
		t.Fatalf("stopped external relay: %v", err)
	}
	if _, err := c.Recover(); !errors.Is(err, ErrRelayRunning) {
		t.Fatalf("recovered external relay: %v", err)
	}
	if _, err := external.Close(); err != nil {
		t.Fatal(err)
	}
	const staleURL = "http://127.0.0.1:17321/backend-api/codex"
	target := relayTarget()
	target.RelayURL, target.RelayAll = staleURL, true
	p, err := codex.New().Plan(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := safekeep.Apply(p); err != nil {
		t.Fatal(err)
	}
	before := readRelayFile(t, cfg)
	if st := c.Status(); st.State != "stale" || st.URL != staleURL || !st.All {
		t.Fatalf("stale state %+v", st)
	}
	if readRelayFile(t, cfg) != before || !safekeep.Touched("codex") {
		t.Fatal("status recovered stale config")
	}
	if rep, err := c.Stop(); rep != nil || err != nil || readRelayFile(t, cfg) != before {
		t.Fatalf("stop recovered without explicit request: %+v %v", rep, err)
	}
	if rep, err := c.Recover(); err != nil || rep == nil || rep.Left() {
		t.Fatalf("recovery %+v %v", rep, err)
	}
	if c.Status().State != "idle" || readRelayFile(t, cfg) != codexCfg {
		t.Fatal("recovery did not restore original config")
	}
}

func TestRelayControllerStatusProbeFailure(t *testing.T) {
	setup(t)
	c := testRelayController(t)
	// A directory at the lock path fails on every platform, including root.
	if err := os.MkdirAll(filepath.Join(appdir.StateDir(), "relay.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if st := c.Status(); st.State != "error" || st.Error == "" {
		t.Fatalf("failed lock probe mislabeled safe %+v", st)
	}
	if _, err := c.Stop(); err == nil {
		t.Fatal("stop ignored inaccessible lock")
	}
}

func TestRelayControllerUnexpectedFailureAndNewSession(t *testing.T) {
	cfg := setup(t)
	c := testRelayController(t)
	pv := controllerPreview(t, c)
	if err := c.Start(pv.ID); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	old := c.active
	old.listener.Close()
	c.mu.Unlock()
	if st := awaitRelayStatus(t, c, "error"); st.Error == "" || c.Busy() {
		t.Fatalf("failure not reported %+v", st)
	}
	if readRelayFile(t, cfg) != codexCfg || safekeep.Touched("codex") {
		t.Fatal("failed server left config behind")
	}
	next := controllerPreview(t, c)
	if err := c.Start(next.ID); err != nil {
		t.Fatal(err)
	}
	// A late observer of the old completion cannot clear a newer session.
	c.watch(old)
	if st := c.Status(); st.State != "running" || st.URL != next.URL || st.Error != "" {
		t.Fatalf("old completion clobbered new session %+v", st)
	}
}

func TestRelayControllerCloseAndShutdownRace(t *testing.T) {
	for _, start := range []bool{false, true} {
		t.Run(map[bool]string{false: "preview", true: "running"}[start], func(t *testing.T) {
			cfg := setup(t)
			c := testRelayController(t)
			pv := controllerPreview(t, c)
			if start {
				if err := c.Start(pv.ID); err != nil {
					t.Fatal(err)
				}
			}
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Go(func() {
					_ = c.Start(pv.ID)
					_ = c.Status()
					if err := c.Close(); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if c.Busy() || c.Status().State != "disabled" || readRelayFile(t, cfg) != codexCfg || safekeep.Touched("codex") {
				t.Fatal("shutdown raced with restart or failed to restore")
			}
			if _, err := c.Preview(relayTarget(), 0); err == nil {
				t.Fatal("closed controller prepared a relay")
			}
			if err := c.Cancel(pv.ID); err == nil {
				t.Fatal("closed controller accepted cancel")
			}
			if _, err := c.Stop(); err == nil {
				t.Fatal("closed controller accepted stop")
			}
			if _, err := c.Recover(); err == nil {
				t.Fatal("closed controller accepted recovery")
			}
			prepareTestRelay(t) // closing released the process lock
		})
	}
}

func TestRelayControllerStopPreservesLaterSwitch(t *testing.T) {
	cfg := setup(t)
	c := testRelayController(t)
	pv := controllerPreview(t, c)
	if err := c.Start(pv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Use(Request{Agent: "codex", Ref: "cpa/alias"}); err != nil {
		t.Fatal(err)
	}
	before := readRelayFile(t, cfg)
	if report, err := c.Stop(); report != nil || err != nil || readRelayFile(t, cfg) != before {
		t.Fatalf("stop undid later switch: %+v %v", report, err)
	}
}

func TestRelayControllerAllRoutesAndClosesPort(t *testing.T) {
	setup(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	p, _, _ := provider.Get("cpa")
	p.BaseURL = upstream.URL + "/v1"
	if err := provider.Update(p); err != nil {
		t.Fatal(err)
	}
	c := testRelayController(t)
	target := agent.Target{Mode: agent.Relay, Provider: "cpa", Model: "alias", RelayAll: true}
	pv, err := c.Preview(target, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(pv.ID); err != nil {
		t.Fatal(err)
	}
	if !c.Status().All {
		t.Fatal("all flag missing")
	}
	client := &http.Client{Timeout: time.Second}
	res, err := client.Post(pv.URL+"/responses", "application/json", strings.NewReader(`{"model":"gpt-5.5"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("unprefixed model did not route to provider: %d", res.StatusCode)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	address := strings.TrimSuffix(strings.TrimPrefix(pv.URL, "http://"), "/backend-api/codex")
	ln, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatalf("close left listener occupied: %v", err)
	}
	ln.Close()
}

func TestRelayControllerWithIdleGuardsSwitches(t *testing.T) {
	setup(t)
	c := testRelayController(t)
	wantErr := errors.New("switch failed")
	if err := c.WithIdle(func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("lost switch failure: %v", err)
	}
	pv := controllerPreview(t, c)
	called := false
	use := func() error { called = true; return nil }
	if err := c.WithIdle(use); !errors.Is(err, ErrRelayBusy) || called {
		t.Fatalf("ordinary switch ran while preview held: %v", err)
	}
	if err := c.Start(pv.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.WithIdle(use); !errors.Is(err, ErrRelayBusy) || called {
		t.Fatalf("ordinary switch ran while relay active: %v", err)
	}
	if _, err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := c.WithIdle(use); err != nil || !called {
		t.Fatalf("idle switch failed: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	called = false
	if err := c.WithIdle(use); !errors.Is(err, ErrRelayClosed) || called {
		t.Fatalf("ordinary switch ran after shutdown: %v", err)
	}
}

func TestRelayControllerWithIdleSerializesPreview(t *testing.T) {
	setup(t)
	c := testRelayController(t)
	entered, release := make(chan struct{}), make(chan struct{})
	switched := make(chan error, 1)
	go func() {
		switched <- c.WithIdle(func() error {
			close(entered)
			<-release
			_, err := Use(Request{Agent: "codex", Ref: "cpa/alias"})
			return err
		})
	}()
	<-entered
	previewed := make(chan error, 1)
	go func() {
		_, err := c.Preview(relayTarget(), 0)
		previewed <- err
	}()
	close(release)
	if err := <-switched; err != nil {
		t.Fatal(err)
	}
	// The relay sees the committed direct config, rather than slipping its
	// preview between the switch's availability check and configuration write.
	if err := <-previewed; err == nil || !strings.Contains(err.Error(), "official first") {
		t.Fatalf("preview did not observe the serialized switch: %v", err)
	}
	if c.Busy() || Current("codex").Ref != "cpa/alias" {
		t.Fatal("concurrent preview disrupted the ordinary switch")
	}
}
