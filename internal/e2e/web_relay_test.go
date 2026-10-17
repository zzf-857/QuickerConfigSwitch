//go:build unix

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func TestWebRelaySIGTERMCleansPreviewAndActive(t *testing.T) {
	for _, start := range []bool{false, true} {
		name := "pending"
		if start {
			name = "running"
		}
		t.Run(name, func(t *testing.T) {
			codexHome, _ := setup(t)
			home := filepath.Dir(codexHome)
			cfg := filepath.Join(codexHome, "config.toml")
			write(t, cfg, codexConfig)
			original := read(t, cfg)
			p := startCLIProcess(t, home, []string{"web", "--no-open", "--port", "0"}, "QuickerConfigSwitch 已启动: ", "/")
			u, err := url.Parse(p.url)
			if err != nil {
				t.Fatal(err)
			}
			token := u.Query().Get("token")
			base := u.Scheme + "://" + u.Host
			client := &http.Client{Timeout: 5 * time.Second}
			t.Cleanup(client.CloseIdleConnections)
			call := func(path string, body any) map[string]any {
				t.Helper()
				raw, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				req, err := http.NewRequest(http.MethodPost, base+"/api/relay/"+path, bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-QCS-Token", token)
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				if res.StatusCode != http.StatusOK {
					t.Fatalf("%s: %d", path, res.StatusCode)
				}
				var result map[string]any
				if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			pv := call("preview", map[string]any{"provider": "cpa", "model": "cpa-gpt-alias", "port": 0})
			if start {
				call("start", map[string]any{"id": pv["id"]})
				if codexReads(t, cfg)["openai_base_url"] != pv["url"] {
					t.Fatal("web relay did not apply preview")
				}
			} else if read(t, cfg) != original {
				t.Fatal("web preview changed config")
			}
			stopRelayProcess(t, p, syscall.SIGTERM)
			if read(t, cfg) != original || safekeep.Touched("codex") {
				t.Fatal("web shutdown did not restore config")
			}
			// Also prove the pending or active process lock was released.
			next := startRelayProcess(t, home)
			stopRelayProcess(t, next, syscall.SIGTERM)
		})
	}
}
