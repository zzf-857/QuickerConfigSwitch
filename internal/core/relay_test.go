package core

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func relayTarget() agent.Target {
	return agent.Target{Mode: agent.Relay, Provider: "cpa", Model: "alias"}
}

func prepareTestRelay(t *testing.T) *RelaySession {
	t.Helper()
	s, err := PrepareRelay(relayTarget(), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func readRelayFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRelayPreviewStartAndRestore(t *testing.T) {
	cfg := setup(t)
	auth := filepath.Join(appdir.CodexHome(), "auth.json")
	const login = `{"tokens":{"access_token":"test-login"}}`
	if err := os.WriteFile(auth, []byte(login), 0o600); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer k" ||
			!strings.Contains(string(b), `"model":"alias"`) || !strings.Contains(string(b), `"service_tier":"priority"`) {
			t.Errorf("upstream request path %s, body %s", r.URL.Path, b)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"response"}`)
	}))
	defer upstream.Close()
	p, _, _ := provider.Get("cpa")
	p.BaseURL = upstream.URL + "/v1"
	if err := provider.Update(p); err != nil {
		t.Fatal(err)
	}
	s := prepareTestRelay(t)
	if !strings.Contains(s.Preview, s.URL) || s.Notice == "" || readRelayFile(t, cfg) != codexCfg || safekeep.Touched("codex") {
		t.Fatalf("preview changed config or missed address: %s", s.Preview)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err == nil {
		t.Fatal("started twice")
	}
	if u, _ := edit.GetTOMLTop(cfg, "openai_base_url"); u != s.URL {
		t.Fatalf("wrong listening URL %q", u)
	}
	if st := safekeep.State("codex"); st["mode"] != "relay" || st["relay_session"] == "" {
		t.Fatalf("missing session state: %+v", st)
	}
	if current := Current("codex"); current.Mode != agent.Relay {
		t.Fatalf("current mode %+v", current)
	}
	if _, err := SaveProfile("relay"); err == nil {
		t.Fatal("saved a live relay as a direct profile")
	}
	if err := PutProfile(Profile{Name: "relay", Agents: []Request{Current("codex")}}); err == nil {
		t.Fatal("accepted a profile without a relay owner")
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Post(s.URL+"/responses", "application/json", strings.NewReader(`{"model":"cpa/alias","service_tier":"priority"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(b) != `{"id":"response"}` {
		t.Fatalf("relay response %d: %s", res.StatusCode, b)
	}
	rep, err := s.Close()
	if err != nil || rep == nil || rep.Left() {
		t.Fatalf("close %+v %v", rep, err)
	}
	if again, err := s.Close(); err != nil || again != rep {
		t.Fatal("close was not idempotent")
	}
	select {
	case err := <-s.Done():
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not stop")
	}
	if readRelayFile(t, cfg) != codexCfg || readRelayFile(t, auth) != login || safekeep.Touched("codex") {
		t.Fatal("original config/login not restored")
	}
	if err := s.Start(); err == nil {
		t.Fatal("restarted closed session")
	}
}

func TestRelayDeclinedPreviewAndExclusiveLock(t *testing.T) {
	cfg := setup(t)
	s := prepareTestRelay(t)
	for _, start := range []func() error{
		func() error { _, err := PrepareRelay(relayTarget(), 0); return err },
		func() error { _, err := RecoverRelay(); return err },
	} {
		if err := start(); !errors.Is(err, ErrRelayRunning) {
			t.Fatalf("live owner not protected: %v", err)
		}
	}
	if rep, err := s.Close(); err != nil || rep != nil {
		t.Fatalf("decline %+v %v", rep, err)
	}
	if readRelayFile(t, cfg) != codexCfg || safekeep.Touched("codex") {
		t.Fatal("declined preview changed config")
	}
	if _, err := os.Stat(filepath.Join(appdir.StateDir(), "applied.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview wrote an applied record: %v", err)
	}
	prepareTestRelay(t) // Close released the lifetime lock.
}

func TestRelayPortSelection(t *testing.T) {
	setup(t)
	busy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port
	if port == DefaultRelayPort {
		t.Skip("ephemeral port happened to be the default")
	}
	if _, err := PrepareRelay(relayTarget(), port); err == nil {
		t.Fatal("silently replaced an occupied explicit port")
	}
	for _, port := range []int{-1, 65536} {
		if _, err := PrepareRelay(relayTarget(), port); err == nil {
			t.Fatal("accepted invalid port")
		}
	}
	// The default may already be occupied by another program; both cases
	// exercise the fallback, without taking that other listener away.
	defaultBusy, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultRelayPort)))
	if err == nil {
		defer defaultBusy.Close()
	}
	s, err := PrepareRelay(relayTarget(), DefaultRelayPort)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.listener.Addr().(*net.TCPAddr).Port == DefaultRelayPort {
		t.Fatal("did not fall back from occupied default")
	}
}

func TestRelayPreviewRejectsChangedInputs(t *testing.T) {
	for _, change := range []string{"config", "provider", "state"} {
		t.Run(change, func(t *testing.T) {
			cfg := setup(t)
			s := prepareTestRelay(t)
			address := s.listener.Addr().String()
			switch change {
			case "config":
				if err := edit.SetTOMLTop(cfg, edit.KV{Path: "openai_base_url", Value: "https://example.invalid"}); err != nil {
					t.Fatal(err)
				}
			case "provider":
				p, _, _ := provider.Get("cpa")
				p.APIKey = "changed"
				if err := provider.Update(p); err != nil {
					t.Fatal(err)
				}
			case "state":
				p := *s.plan
				p.State = map[string]string{"mode": "relay", "relay_session": "other"}
				if err := safekeep.Apply(&p); err != nil {
					t.Fatal(err)
				}
			}
			before := readRelayFile(t, cfg)
			if err := s.Start(); err == nil {
				t.Fatal("started with changed inputs")
			}
			if readRelayFile(t, cfg) != before {
				t.Fatal("failed start changed config")
			}
			if conn, err := net.DialTimeout("tcp", address, time.Second); err == nil {
				conn.Close()
				t.Fatal("failed start leaked listener")
			}
			prepareTestRelay(t) // failed Start released the lock too
		})
	}
}

func TestRelayClosePreservesLaterSwitch(t *testing.T) {
	cfg := setup(t)
	s := prepareTestRelay(t)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := Use(Request{Agent: "codex", Ref: "cpa/alias"}); err != nil {
		t.Fatal(err)
	}
	before := readRelayFile(t, cfg)
	if rep, err := s.Close(); err != nil || rep != nil {
		t.Fatalf("old session restored new choice: %+v %v", rep, err)
	}
	if readRelayFile(t, cfg) != before || safekeep.State("codex")["mode"] != "direct" {
		t.Fatal("close overwrote later direct mode")
	}
	if rep, err := RecoverRelay(); err != nil || rep != nil || readRelayFile(t, cfg) != before {
		t.Fatalf("recovery overwrote direct mode: %+v %v", rep, err)
	}
	if _, err := PrepareRelay(relayTarget(), 0); err == nil || !strings.Contains(err.Error(), "official") {
		t.Fatalf("accepted active direct config: %v", err)
	}
}

func TestRelayClosePreservesUserChanges(t *testing.T) {
	cfg := setup(t)
	s := prepareTestRelay(t)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if err := edit.SetTOMLTop(cfg, edit.KV{Path: "openai_base_url", Value: "https://user.invalid"}); err != nil {
		t.Fatal(err)
	}
	before := readRelayFile(t, cfg)
	rep, err := s.Close()
	if err != nil || rep == nil || !rep.Left() || readRelayFile(t, cfg) != before {
		t.Fatalf("user edit not retained: %+v %v", rep, err)
	}
}

func staleRelay(t *testing.T) {
	t.Helper()
	target := relayTarget()
	target.RelayURL = "http://127.0.0.1:1/backend-api/codex"
	p, err := mustAgent(t, "codex").Plan(target)
	if err != nil {
		t.Fatal(err)
	}
	p.State["relay_session"] = "dead-session"
	if err := safekeep.Apply(p); err != nil {
		t.Fatal(err)
	}
}

func TestRelayRestartAdoptsOriginalSnapshot(t *testing.T) {
	cfg := setup(t)
	if err := edit.SetTOMLTop(cfg, edit.KV{Path: "openai_base_url", Value: "https://original.invalid"}); err != nil {
		t.Fatal(err)
	}
	original := readRelayFile(t, cfg)
	staleRelay(t)
	stale := readRelayFile(t, cfg)
	s := prepareTestRelay(t)
	if readRelayFile(t, cfg) != stale {
		t.Fatal("restart preparation restored before confirmation")
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if marker := safekeep.State("codex")["relay_session"]; marker == "dead-session" || marker == "" {
		t.Fatal("restart did not own its state")
	}
	if _, err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if readRelayFile(t, cfg) != original {
		t.Fatal("restart lost the pre-relay snapshot")
	}
}

func TestRecoverStaleRelay(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(strconv.FormatBool(changed), func(t *testing.T) {
			cfg := setup(t)
			staleRelay(t)
			want := codexCfg
			if changed {
				if err := edit.SetTOMLTop(cfg, edit.KV{Path: "openai_base_url", Value: "https://user.invalid"}); err != nil {
					t.Fatal(err)
				}
				want = readRelayFile(t, cfg)
			}
			rep, err := RecoverRelay()
			if err != nil || rep == nil || rep.Left() != changed || readRelayFile(t, cfg) != want || safekeep.Touched("codex") {
				t.Fatalf("recovery %+v %v", rep, err)
			}
			if rep, err := RecoverRelay(); err != nil || rep != nil {
				t.Fatalf("repeated recovery %+v %v", rep, err)
			}
		})
	}
}

func TestRelayUnexpectedServerFailureRestores(t *testing.T) {
	cfg := setup(t)
	s := prepareTestRelay(t)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	s.listener.Close() // simulate an unexpected failure of Serve
	select {
	case err := <-s.Done():
		if err == nil {
			t.Fatal("server failure not reported")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server failure did not clean up")
	}
	if readRelayFile(t, cfg) != codexCfg || safekeep.Touched("codex") {
		t.Fatal("server failure left a dead relay address")
	}
	prepareTestRelay(t)
}

func TestRelayStopsStreamingAndSupportsUnlistedModel(t *testing.T) {
	cfg := setup(t)
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(canceled)
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"model":"alias"`) {
			t.Errorf("chosen unlisted model not routed: %s", b)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	p, _, _ := provider.Get("cpa")
	p.BaseURL, p.Models = upstream.URL, nil
	if err := provider.Update(p); err != nil {
		t.Fatal(err)
	}
	s := prepareTestRelay(t)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Post(s.URL+"/responses", "application/json", strings.NewReader(`{"model":"cpa/alias","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unlisted model response %d", res.StatusCode)
	}
	if _, err := io.ReadFull(res.Body, make([]byte, len("data: ready\n\n"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("closing relay did not cancel upstream stream")
	}
	if readRelayFile(t, cfg) != codexCfg {
		t.Fatal("streaming shutdown did not restore config")
	}
}

func TestRelayStartRecordFailureRollsBackAndReleases(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions as a non-root user")
	}
	cfg := setup(t)
	s := prepareTestRelay(t)
	address := s.listener.Addr().String()
	dir := filepath.Join(t.TempDir(), "read-only")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, "applied.json")
	if err := os.WriteFile(record, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(record, filepath.Join(appdir.StateDir(), "applied.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := s.Start(); err == nil {
		t.Fatal("started despite failing to record config change")
	}
	if readRelayFile(t, cfg) != codexCfg || safekeep.Touched("codex") || readRelayFile(t, record) != "{}\n" {
		t.Fatal("failed start left config changes or a partial state record")
	}
	if conn, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		conn.Close()
		t.Fatal("record-save failure leaked listener")
	}
	prepareTestRelay(t) // Record failure also released the lifetime lock.
}
