//go:build linux

package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/autostart"
	"github.com/zzf-857/quickerconfigswitch/internal/gui"
)

func TestAutostartCLI(t *testing.T) {
	setup(t)
	m, err := autostart.New(true)
	if err != nil {
		t.Fatal(err)
	}
	st, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if out := mustRun(t, "", "autostart"); !strings.Contains(out, "登录自启: no") {
		t.Fatalf("status: %s", out)
	}
	if _, err := os.Lstat(st.Path); !os.IsNotExist(err) {
		t.Fatal("status wrote registration")
	}
	if gui.Available {
		mustRun(t, "", "autostart", "enable")
	} else {
		if code, _, _ := run(t, "", "autostart", "enable"); code != 1 {
			t.Fatalf("headless enable code %d", code)
		}
		if _, err := m.Enable(); err != nil {
			t.Fatal(err)
		} // fixture of a previous desktop build
	}
	if out := mustRun(t, "", "autostart", "status"); !strings.Contains(out, "登录自启: yes") {
		t.Fatalf("enabled: %s", out)
	}
	mustRun(t, "", "autostart", "off")
	if _, err := os.Lstat(st.Path); !os.IsNotExist(err) {
		t.Fatal("CLI did not remove its registration")
	}
	for _, args := range [][]string{{"autostart", "wat"}, {"autostart", "on", "off"}} {
		if code, _, _ := run(t, "", args...); code != 2 {
			t.Fatalf("%v: %d", args, code)
		}
	}
}
