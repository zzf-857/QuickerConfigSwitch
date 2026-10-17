// Adapted from yetone/magpie internal/autostart (MIT, Copyright (c) 2026 yetone).

package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeRegistry struct {
	run, receipt               string
	runPresent, receiptPresent bool
	off                        bool
	fail                       map[string]error
	changes                    int
}

func (r *fakeRegistry) failure(op string) error {
	err := r.fail[op]
	delete(r.fail, op)
	return err
}
func (r *fakeRegistry) readRun() (string, bool, error) {
	return r.run, r.runPresent, r.failure("readRun")
}
func (r *fakeRegistry) readReceipt() (string, bool, error) {
	return r.receipt, r.receiptPresent, r.failure("readReceipt")
}
func (r *fakeRegistry) writeRun(s string) error {
	if err := r.failure("writeRun"); err != nil {
		return err
	}
	r.changes++
	r.run, r.runPresent = s, true
	return nil
}
func (r *fakeRegistry) writeReceipt(s string) error {
	if err := r.failure("writeReceipt"); err != nil {
		return err
	}
	r.changes++
	r.receipt, r.receiptPresent = s, true
	return nil
}
func (r *fakeRegistry) deleteRun() error {
	if err := r.failure("deleteRun"); err != nil {
		return err
	}
	r.changes++
	r.run, r.runPresent = "", false
	return nil
}
func (r *fakeRegistry) deleteReceipt() error {
	if err := r.failure("deleteReceipt"); err != nil {
		return err
	}
	r.changes++
	r.receipt, r.receiptPresent = "", false
	return nil
}
func (r *fakeRegistry) switchedOff() (bool, error) {
	return r.off, r.failure("switchedOff")
}
func (r *fakeRegistry) clearApprovedMarker() error {
	if err := r.failure("clearApprovedMarker"); err != nil {
		return err
	}
	r.changes++
	r.off = false
	return nil
}

func registryManager(t *testing.T) (*Manager, *fakeRegistry) {
	t.Helper()
	dir := t.TempDir()
	exe := `C:\中文 apps\qcs.exe`
	if runtime.GOOS == "windows" {
		exe = filepath.Join(dir, "qcs.exe")
		if err := os.WriteFile(exe, []byte("test executable\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := &fakeRegistry{fail: map[string]error{}}
	return &Manager{platform: "windows", executable: exe, path: `HKCU\` + runKey + `\` + valueName,
		lockPath: filepath.Join(dir, "autostart.lock"), canEnable: true, registry: r}, r
}

func TestRegistryLifecycle(t *testing.T) {
	m, r := registryManager(t)
	if st, err := m.Status(); err != nil || st.Enabled || st.Managed || st.Executable != m.executable {
		t.Fatalf("initial: %+v %v", st, err)
	}
	for range 2 {
		st, err := m.Enable()
		if err != nil || !st.Enabled || !st.Managed || !st.Current || r.run != windowsCommand(m.executable) || r.receipt != r.run {
			t.Fatalf("enable: %+v %v registry=%+v", st, err, r)
		}
	}
	r.off = true
	st, err := m.Status()
	if err != nil || st.Enabled || !st.Managed || !st.Current || st.Problem == "" {
		t.Fatalf("StartupApproved off: %+v %v", st, err)
	}
	st, err = m.Enable()
	if err != nil || !st.Enabled || r.off {
		t.Fatalf("explicit on clears marker: %+v %v", st, err)
	}
	m.canEnable = false
	if _, err := m.Enable(); err == nil {
		t.Fatal("nogui enabled startup")
	}
	for range 2 {
		st, err = m.Disable()
		if err != nil || st.Enabled || st.Managed || r.runPresent || r.receiptPresent {
			t.Fatalf("disable: %+v %v registry=%+v", st, err, r)
		}
	}
}

func TestRegistryOldPathCanRefreshOrDisable(t *testing.T) {
	for _, on := range []bool{true, false} {
		m, r := registryManager(t)
		old := windowsCommand(`C:\old apps\qcs.exe`)
		r.run, r.receipt, r.runPresent, r.receiptPresent = old, old, true, true
		st, err := m.Status()
		if err != nil || !st.Managed || !st.Enabled || st.Current {
			t.Fatalf("moved: %+v %v", st, err)
		}
		if on {
			st, err = m.Enable()
			if err != nil || !st.Current || r.run != windowsCommand(m.executable) {
				t.Fatalf("refresh: %+v %v", st, err)
			}
		} else {
			st, err = m.Disable()
			if err != nil || st.Enabled || r.runPresent || r.receiptPresent {
				t.Fatalf("old disable: %+v %v", st, err)
			}
		}
	}
}

func TestRegistryUnknownAndEditedPreserved(t *testing.T) {
	for _, variant := range []string{"unknown", "edited", "invalidReceipt", "invalidOrphanReceipt"} {
		t.Run(variant, func(t *testing.T) {
			m, r := registryManager(t)
			r.run, r.runPresent = `"C:\user.exe" --custom`, true
			switch variant {
			case "edited":
				r.receipt, r.receiptPresent = windowsCommand(m.executable), true
			case "invalidReceipt":
				r.receipt, r.receiptPresent = r.run, true
			case "invalidOrphanReceipt":
				r.run, r.runPresent, r.receipt, r.receiptPresent = "", false, "invalid", true
			}
			original := *r
			st, err := m.Status()
			if err != nil || st.Managed || st.Problem == "" {
				t.Fatalf("unknown: %+v %v", st, err)
			}
			if _, err := m.Enable(); err == nil {
				t.Fatal("overwrote unknown Run value")
			}
			if _, err := m.Disable(); err == nil {
				t.Fatal("deleted unknown Run value")
			}
			if r.changes != 0 || r.run != original.run || r.receipt != original.receipt || r.runPresent != original.runPresent || r.receiptPresent != original.receiptPresent {
				t.Fatalf("unknown registry changed: %+v", r)
			}
		})
	}
}

func TestRegistryOrphanReceiptCanRecover(t *testing.T) {
	for _, on := range []bool{true, false} {
		m, r := registryManager(t)
		r.receipt, r.receiptPresent = windowsCommand(`C:\gone\qcs.exe`), true
		st, err := m.Status()
		if err != nil || !st.Managed || st.Enabled || st.Current {
			t.Fatalf("orphan: %+v %v", st, err)
		}
		if on {
			st, err = m.Enable()
			if err != nil || !st.Current || !st.Enabled {
				t.Fatalf("orphan enable: %+v %v", st, err)
			}
		} else {
			st, err = m.Disable()
			if err != nil || st.Managed || r.receiptPresent {
				t.Fatalf("orphan disable: %+v %v", st, err)
			}
		}
	}
}

func TestRegistryOperationFailuresRollback(t *testing.T) {
	denied := errors.New("access denied")
	for _, existing := range []bool{true, false} {
		for _, op := range []string{"writeReceipt", "writeRun", "clearApprovedMarker"} {
			t.Run(op+"/existing="+map[bool]string{true: "yes", false: "no"}[existing], func(t *testing.T) {
				m, r := registryManager(t)
				old := windowsCommand(`C:\old\qcs.exe`)
				if existing {
					r.run, r.receipt, r.runPresent, r.receiptPresent = old, old, true, true
				}
				r.off = true
				r.fail[op] = denied
				if _, err := m.Enable(); !errors.Is(err, denied) {
					t.Fatalf("expected denial: %v", err)
				}
				if r.runPresent != existing || r.receiptPresent != existing || !r.off || existing && (r.run != old || r.receipt != old) {
					t.Fatalf("partially enabled after failure: %+v", r)
				}
			})
		}
	}
	for _, op := range []string{"deleteRun", "deleteReceipt"} {
		m, r := registryManager(t)
		old := windowsCommand(m.executable)
		r.run, r.receipt, r.runPresent, r.receiptPresent = old, old, true, true
		r.fail[op] = denied
		if _, err := m.Disable(); !errors.Is(err, denied) {
			t.Fatalf("expected denial: %v", err)
		}
		if !r.runPresent || !r.receiptPresent || r.run != old || r.receipt != old {
			t.Fatalf("partially disabled: %+v", r)
		}
	}
}

func TestRegistryReadErrorsDoNotReportOff(t *testing.T) {
	for _, op := range []string{"readRun", "readReceipt", "switchedOff"} {
		m, r := registryManager(t)
		r.run, r.receipt, r.runPresent, r.receiptPresent = windowsCommand(m.executable), windowsCommand(m.executable), true, true
		denied := errors.New("access denied")
		r.fail[op] = denied
		if _, err := m.Status(); !errors.Is(err, denied) {
			t.Fatalf("%s: silently reported off: %v", op, err)
		}
		if r.changes != 0 {
			t.Fatal("status changed registry")
		}
	}
}

func TestWindowsCommandHasNoShell(t *testing.T) {
	for _, exe := range []string{`C:\qcs.exe`, `C:\中文 apps\100% & $name\qcs.exe`, `\\server\share\qcs.exe`} {
		got, ok := commandExecutable(windowsCommand(exe))
		if !ok || got != exe {
			t.Fatalf("command %q parsed %q %v", windowsCommand(exe), got, ok)
		}
	}
	for _, cmd := range []string{`qcs.exe gui --hidden`, `"C:\qcs.exe" gui --hidden & bad`, `"C:\a"b.exe" gui --hidden`} {
		if _, ok := commandExecutable(cmd); ok {
			t.Fatalf("recognized invalid command %q", cmd)
		}
	}
	m, r := registryManager(t)
	cmd := windowsCommand(strings.ToUpper(m.executable))
	r.run, r.receipt, r.runPresent, r.receiptPresent = cmd, cmd, true, true
	if st, err := m.Status(); err != nil || !st.Current {
		t.Fatalf("path case mismatch: %+v %v", st, err)
	}
}
