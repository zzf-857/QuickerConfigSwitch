//go:build unix

package e2e

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/cli"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

// Run the actual CLI in another process, without another Go build. TestMain
// first gives that process its own home; the helper then selects the parent's
// fixture home. Its temporary directories also stay under the parent test.
func TestRelayForegroundProcessHelper(t *testing.T) {
	home := os.Getenv("QCS_E2E_RELAY_HOME")
	if home == "" {
		return
	}
	testenv.Setup(home)
	for i, arg := range os.Args {
		if arg == "--" {
			if code := cli.Run(os.Args[i+1:], os.Stdin, os.Stdout, os.Stderr); code != 0 {
				t.Fatalf("qcs exit code %d", code)
			}
			return
		}
	}
	t.Fatal("missing helper CLI arguments")
}

type relayProcess struct {
	cmd    *exec.Cmd
	done   chan struct{}
	err    error // read only after done closes
	output string
	url    string
}

func startRelayProcess(t *testing.T, home string) *relayProcess {
	t.Helper()
	return startCLIProcess(t, home, []string{"use", "codex", "cpa/cpa-gpt-alias", "--relay", "--port", "0", "--yes"}, "本地路由已启动: ", "/backend-api/codex")
}

func startCLIProcess(t *testing.T, home string, args []string, announcement, path string) *relayProcess {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	out, err := os.Create(filepath.Join(dir, "relay.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, append([]string{"-test.run=^TestRelayForegroundProcessHelper$", "-test.timeout=45s", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "QCS_E2E_RELAY_HOME="+home, "TMPDIR="+dir)
	cmd.Stdout, cmd.Stderr = out, out
	p := &relayProcess{cmd: cmd, done: make(chan struct{}), output: out.Name()}
	if err := cmd.Start(); err != nil {
		out.Close()
		t.Fatal(err)
	}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		defer out.Close()
		select {
		case <-p.done:
			return
		default:
		}
		_ = cmd.Process.Kill()
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			t.Error("relay helper did not exit after kill")
		}
	})
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-p.done:
			t.Fatalf("relay exited before startup: %v\n%s", p.err, read(t, p.output))
		case <-timer.C:
			t.Fatalf("relay did not announce startup:\n%s", read(t, p.output))
		case <-tick.C:
			for _, line := range strings.Split(read(t, p.output), "\n") {
				if !strings.HasPrefix(line, announcement) {
					continue
				}
				p.url = strings.TrimSpace(strings.TrimPrefix(line, announcement))
				u, err := url.Parse(p.url)
				if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Port() == "0" || u.Path != path {
					t.Fatalf("invalid announced relay URL %q", p.url)
				}
				return p
			}
		}
	}
}

func stopRelayProcess(t *testing.T, p *relayProcess, signal syscall.Signal) {
	t.Helper()
	if err := p.cmd.Process.Signal(signal); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		t.Fatalf("relay did not exit after %v:\n%s", signal, read(t, p.output))
	}
	if signal == syscall.SIGKILL {
		var exit *exec.ExitError
		if !errors.As(p.err, &exit) {
			t.Fatalf("killed relay returned %v", p.err)
		}
		status, ok := exit.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
			t.Fatalf("relay was not killed: %v", p.err)
		}
	} else if p.err != nil {
		t.Fatalf("graceful relay shutdown: %v\n%s", p.err, read(t, p.output))
	}
}

// A real foreground process must restore on SIGTERM, release its lock even
// on SIGKILL, and preserve the original snapshot when the next owner starts.
func TestRelayForegroundSignalsAndCrashRecovery(t *testing.T) {
	codexHome, _ := setup(t)
	home := filepath.Dir(codexHome)
	cfg := filepath.Join(codexHome, "config.toml")
	auth := filepath.Join(codexHome, "auth.json")
	original := strings.Replace(codexConfig, "model = ", "openai_base_url = \"http://127.0.0.1:9/backend-api/codex\"\r\nmodel = ", 1)
	write(t, cfg, original)
	write(t, auth, authJSON)
	authBefore, err := os.Stat(auth)
	if err != nil {
		t.Fatal(err)
	}
	checkAuth := func() {
		t.Helper()
		after, err := os.Stat(auth)
		if err != nil {
			t.Fatal(err)
		}
		if read(t, auth) != authJSON || !os.SameFile(authBefore, after) || !authBefore.ModTime().Equal(after.ModTime()) || authBefore.Mode() != after.Mode() {
			t.Fatal("relay touched auth.json")
		}
	}
	checkRestored := func() {
		t.Helper()
		if read(t, cfg) != original || safekeep.Touched("codex") {
			t.Fatal("relay did not restore the original config and clear its state")
		}
		checkAuth()
	}

	const firstEvent = "event: response.created\ndata: {\"id\":\"response-local\"}\n\n"
	const lastEvent = "event: response.completed\ndata: [DONE]\n\n"
	const jsonResponse = `{"id":"response-local","service_tier":"priority"}`
	finishStream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model       string `json:"model"`
			ServiceTier string `json:"service_tier"`
			Stream      bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("provider request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || body.Model != "cpa-gpt-alias" || body.ServiceTier != "priority" {
			t.Errorf("provider request %s %s: %+v", r.Method, r.URL.Path, body)
		}
		if r.Header.Get("Authorization") != "Bearer sk-cpa" || r.Header.Get("ChatGPT-Account-Id") != "" || r.Header.Get("Cookie") != "" {
			t.Error("provider received the wrong credential headers")
		}
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, firstEvent)
			w.(http.Flusher).Flush()
			select {
			case <-finishStream:
				io.WriteString(w, lastEvent)
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, jsonResponse)
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(func() {
		select {
		case <-finishStream:
		default:
			close(finishStream)
		}
	})
	p, _, err := provider.Get("cpa")
	if err != nil {
		t.Fatal(err)
	}
	p.BaseURL = upstream.URL + "/v1"
	if err := provider.Update(p); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	request := func(p *relayProcess, stream bool) *http.Response {
		t.Helper()
		body := `{"model":"cpa/cpa-gpt-alias","service_tier":"priority","stream":false}`
		if stream {
			body = strings.Replace(body, "false", "true", 1)
		}
		req, err := http.NewRequest(http.MethodPost, p.url+"/responses", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer test-chatgpt-login")
		req.Header.Set("ChatGPT-Account-Id", "test-account")
		req.Header.Set("Cookie", "session=test-session")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { res.Body.Close() })
		if res.StatusCode != http.StatusOK {
			t.Fatalf("relay response status %d", res.StatusCode)
		}
		return res
	}
	checkLive := func(p *relayProcess) string {
		t.Helper()
		if codexReads(t, cfg)["openai_base_url"] != p.url {
			t.Fatal("Codex config does not point to the live relay")
		}
		state := safekeep.State("codex")
		if state["mode"] != "relay" || state["relay_session"] == "" {
			t.Fatalf("missing relay ownership record: %+v", state)
		}
		res := request(p, false)
		b, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || string(b) != jsonResponse || res.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("provider JSON was not preserved: %q, %v", b, err)
		}
		checkAuth()
		return state["relay_session"]
	}

	first := startRelayProcess(t, home)
	checkLive(first)
	res := request(first, true)
	if res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("relay lost the SSE content type")
	}
	firstBytes := make([]byte, len(firstEvent))
	if _, err := io.ReadFull(res.Body, firstBytes); err != nil || string(firstBytes) != firstEvent {
		t.Fatalf("relay buffered or changed the first SSE event: %q, %v", firstBytes, err)
	}
	close(finishStream) // The first event arrived before the upstream finished.
	rest, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || string(rest) != lastEvent {
		t.Fatalf("relay changed the remaining SSE events: %q, %v", rest, err)
	}
	stopRelayProcess(t, first, syscall.SIGTERM)
	checkRestored()

	crashed := startRelayProcess(t, home)
	staleSession := checkLive(crashed)
	stopRelayProcess(t, crashed, syscall.SIGKILL)
	if codexReads(t, cfg)["openai_base_url"] != crashed.url || safekeep.State("codex")["relay_session"] != staleSession || !safekeep.Touched("codex") {
		t.Fatal("SIGKILL did not leave the expected stale relay record")
	}
	checkAuth()

	restarted := startRelayProcess(t, home)
	if checkLive(restarted) == staleSession {
		t.Fatal("restarted relay did not claim its own session")
	}
	stopRelayProcess(t, restarted, syscall.SIGTERM)
	checkRestored()
}
