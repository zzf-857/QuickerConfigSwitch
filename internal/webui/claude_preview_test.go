package webui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
)

func TestClaudePreviewHidesOriginalCustomAuthentication(t *testing.T) {
	setup(t)
	addCPA(t, "http://127.0.0.1:8317/v1")
	if err := os.MkdirAll(appdir.ClaudeHome(), 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(map[string]any{"env": map[string]string{
		"ANTHROPIC_CUSTOM_HEADERS": "Authorization: Bearer old-unlisted-credential\nX-Private: escaped\"secret\"",
	}})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(appdir.ClaudeHome(), "settings.json")
	if err := os.WriteFile(file, before, 0o600); err != nil {
		t.Fatal(err)
	}
	c := newClient(t, Options{})
	w := c.raw("POST", "/api/preview", map[string]string{"agent": "claude", "ref": "cpa/gpt-5.5"}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "ANTHROPIC_CUSTOM_HEADERS") {
		t.Fatalf("preview %d: %s", w.Code, w.Body)
	}
	for _, secret := range []string{"old-unlisted-credential", "escaped", "Authorization: Bearer"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("original custom authentication leaked through API: %s", secret)
		}
	}
	if after, err := os.ReadFile(file); err != nil || string(after) != string(before) {
		t.Fatalf("preview changed original config: %v", err)
	}
}
