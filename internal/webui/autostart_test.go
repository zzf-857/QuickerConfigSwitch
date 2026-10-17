//go:build linux

package webui

import (
	"net/http"
	"os"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/autostart"
)

func TestAutostartAPIExplicitChangesOnly(t *testing.T) {
	setup(t)
	m, err := autostart.New(true)
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t, Options{Token: "dev", Autostart: m})
	c.hdr[TokenHeader] = "dev"
	st := c.do("GET", "/api/autostart", nil, http.StatusOK)
	path, _ := st["path"].(string)
	if path == "" || st["enabled"] != false || st["canEnable"] != true {
		t.Fatalf("initial status: %+v", st)
	}
	c.do("GET", "/api/state", nil, http.StatusOK)
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("inspection registered startup")
	}
	for _, enabled := range []bool{true, false} {
		if w := c.raw("PUT", "/api/autostart", map[string]bool{"enabled": enabled}, map[string]string{TokenHeader: ""}); w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated mutation: %d", w.Code)
		}
		if w := c.raw("PUT", "/api/autostart", map[string]bool{"enabled": enabled}, map[string]string{"Origin": "https://foreign.example"}); w.Code != http.StatusForbidden {
			t.Fatalf("foreign mutation: %d", w.Code)
		}
	}
	c.do("PUT", "/api/autostart", map[string]any{}, http.StatusBadRequest)
	res := c.do("PUT", "/api/autostart", map[string]bool{"enabled": true}, http.StatusOK)
	if get(res, "state", "autostart", "enabled") != true || get(res, "autostart", "managed") != true {
		t.Fatalf("enabled state: %+v", res)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	c.do("PUT", "/api/autostart", map[string]bool{"enabled": true}, http.StatusOK)
	res = c.do("PUT", "/api/autostart", map[string]bool{"enabled": false}, http.StatusOK)
	if get(res, "state", "autostart", "enabled") != false {
		t.Fatalf("disable: %+v", res)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("disable left registration")
	}
}

func TestAutostartAPIHeadlessDisableAndCollision(t *testing.T) {
	setup(t)
	desktop, err := autostart.New(true)
	if err != nil {
		t.Fatal(err)
	}
	headless, err := autostart.New(false)
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t, Options{Autostart: headless})
	c.do("PUT", "/api/autostart", map[string]bool{"enabled": true}, http.StatusBadRequest)
	st, err := desktop.Enable()
	if err != nil {
		t.Fatal(err)
	}
	c.do("PUT", "/api/autostart", map[string]bool{"enabled": false}, http.StatusOK)
	if _, err := desktop.Enable(); err != nil {
		t.Fatal(err)
	}
	const other = "[Desktop Entry]\nType=Application\nExec=/bin/true\n"
	if err := os.WriteFile(st.Path, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	c.do("PUT", "/api/autostart", map[string]bool{"enabled": false}, http.StatusBadRequest)
	if got, err := os.ReadFile(st.Path); err != nil || string(got) != other {
		t.Fatal("overwrote another startup entry")
	}
	nilHost := newClient(t, Options{})
	nilHost.do("GET", "/api/autostart", nil, http.StatusServiceUnavailable)
	nilHost.do("PUT", "/api/autostart", map[string]bool{"enabled": true}, http.StatusServiceUnavailable)
}
