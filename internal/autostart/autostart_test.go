// Adapted from yetone/magpie internal/autostart (MIT, Copyright (c) 2026 yetone).

package autostart

import (
	"bytes"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func fileManager(t *testing.T, platform string) *Manager {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "中文 apps", "qcs")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("test executable\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "autostart", "qcs.desktop")
	if platform == "darwin" {
		path = filepath.Join(dir, "LaunchAgents", "qcs.plist")
	}
	return &Manager{platform: platform, executable: exe, path: path, lockPath: path + ".lock", canEnable: true}
}

func TestFileLifecycle(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			m := fileManager(t, platform)
			st, err := m.Status()
			if err != nil || st.Enabled || st.Managed || !st.CanEnable {
				t.Fatalf("initial: %+v %v", st, err)
			}
			if _, err := os.Stat(filepath.Dir(m.path)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("status created startup directory")
			}
			st, err = m.Enable()
			if err != nil || !st.Enabled || !st.Managed || !st.Current || st.Executable != m.executable {
				t.Fatalf("enable: %+v %v", st, err)
			}
			before, _ := os.ReadFile(m.path)
			if _, err := m.Enable(); err != nil {
				t.Fatal(err)
			}
			if after, _ := os.ReadFile(m.path); !bytes.Equal(before, after) {
				t.Fatal("repeat enable changed registration")
			}
			m.executable += "-moved"
			if err := os.WriteFile(m.executable, []byte("moved executable\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			st, err = m.Status()
			if err != nil || !st.Managed || st.Current || !st.Enabled {
				t.Fatalf("moved: %+v %v", st, err)
			}
			st, err = m.Enable()
			if err != nil || !st.Current || st.Executable != m.executable {
				t.Fatalf("refresh: %+v %v", st, err)
			}
			m.canEnable = false
			if _, err := m.Enable(); err == nil {
				t.Fatal("nogui enabled startup")
			}
			for range 2 {
				st, err = m.Disable()
				if err != nil || st.Enabled || st.Managed {
					t.Fatalf("disable: %+v %v", st, err)
				}
			}
		})
	}
}

func TestUnknownAndEditedEntriesPreserved(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		for _, variant := range []string{"unknown", "edited", "truncated", "empty", "directory", "symlink"} {
			t.Run(platform+"/"+variant, func(t *testing.T) {
				m := fileManager(t, platform)
				if _, err := m.Enable(); err != nil {
					t.Fatal(err)
				}
				body, _ := os.ReadFile(m.path)
				switch variant {
				case "unknown":
					body = []byte("user startup file\n")
				case "edited":
					body = append(body, []byte("\nuser note\n")...)
				case "truncated":
					body = body[:len(body)/2]
				case "empty":
					body = nil
				case "directory":
					os.Remove(m.path)
					if err := os.Mkdir(m.path, 0o755); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					target := m.path + ".user"
					if err := os.Rename(m.path, target); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, m.path); err != nil {
						t.Skipf("symlink unavailable: %v", err)
					}
				}
				if variant != "directory" && variant != "symlink" {
					if err := os.WriteFile(m.path, body, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				st, err := m.Status()
				if err != nil || st.Managed || st.Enabled || st.Problem == "" {
					t.Fatalf("unrecognized: %+v %v", st, err)
				}
				if _, err := m.Enable(); err == nil {
					t.Fatal("enable overwrote unknown entry")
				}
				if _, err := m.Disable(); err == nil {
					t.Fatal("disable removed unknown entry")
				}
				if variant != "directory" {
					if after, _ := os.ReadFile(m.path); !bytes.Equal(after, body) {
						t.Fatal("unknown contents changed")
					}
				}
			})
		}
	}
}

// Parse the two distinct escaping layers the desktop standard specifies.
// An escaped percent must remain a literal percent, never a field code.
func parseDesktopExec(t *testing.T, body []byte) string {
	t.Helper()
	line := ""
	for _, s := range strings.Split(string(body), "\n") {
		if p, ok := strings.CutPrefix(s, "Exec="); ok {
			line = p
		}
	}
	var value strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' {
			i++
			if i >= len(line) || line[i] != '\\' {
				t.Fatal("invalid desktop string escape")
			}
		}
		value.WriteByte(line[i])
	}
	s := value.String()
	if !strings.HasPrefix(s, `"`) {
		t.Fatal("executable not quoted")
	}
	var exe strings.Builder
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
			if i >= len(s) || !strings.ContainsRune("\\\"`$", rune(s[i])) {
				t.Fatal("invalid Exec escape")
			}
			exe.WriteByte(s[i])
		case '%':
			i++
			if i >= len(s) || s[i] != '%' {
				t.Fatal("unescaped Exec field code")
			}
			exe.WriteByte('%')
		case '"':
			if s[i+1:] != " gui --hidden" {
				t.Fatalf("unexpected startup arguments %q", s[i+1:])
			}
			return exe.String()
		default:
			exe.WriteByte(s[i])
		}
	}
	t.Fatal("unterminated executable")
	return ""
}

func TestDesktopExecEscaping(t *testing.T) {
	for _, exe := range []string{"/opt/qcs", "/opt/中文 apps/qcs", `/tmp/a%b/%f/qcs`, "/tmp/a\\b\"c$d`e'qcs", "/tmp/qcs#;&()<>|*?~"} {
		if got := parseDesktopExec(t, desktop(exe)); got != exe {
			t.Fatalf("parsed %q, want %q", got, exe)
		}
		m := &Manager{platform: "linux"}
		if got, ok := m.recognize(desktop(exe)); !ok || got != exe {
			t.Fatalf("recognize %q: %q %v", exe, got, ok)
		}
	}
}

func TestPlistAndBundle(t *testing.T) {
	m := fileManager(t, "darwin")
	exe := filepath.Join(t.TempDir(), "中文 & app", "QCS.app", "Contents", "MacOS", "qcs")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("test executable\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	info := filepath.Join(filepath.Dir(filepath.Dir(exe)), "Info.plist")
	if err := os.WriteFile(info, []byte(`<plist><dict><key>CFBundleIdentifier</key><string>example.qcs</string></dict></plist>`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.executable = exe
	if _, err := m.Enable(); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(m.path)
	var doc struct {
		XMLName xml.Name `xml:"plist"`
		Dict    struct {
			Arrays []struct {
				Strings []string `xml:"string"`
			} `xml:"array"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("invalid plist: %v\n%s", err, body)
	}
	args := doc.Dict.Arrays[0].Strings
	if len(args) != 3 || args[0] != exe || args[1] != "gui" || args[2] != "--hidden" || doc.Dict.Arrays[1].Strings[0] != "example.qcs" {
		t.Fatalf("unexpected plist: %+v", doc)
	}
	if err := os.Remove(info); err != nil {
		t.Fatal(err)
	}
	st, err := m.Status()
	if err != nil || !st.Managed || !st.Current {
		t.Fatalf("Info.plist vanished invalidated ownership: %+v %v", st, err)
	}
}

func TestInvalidExecutableDoesNotRegister(t *testing.T) {
	for _, exe := range []string{"relative/qcs", "/tmp/a\nExec=evil", "/tmp/qcs\x00", "/tmp/qcs\t", "/tmp/100%/qcs", "/tmp/invalid\xff/qcs"} {
		m := fileManager(t, "linux")
		m.executable = exe
		if _, err := m.Enable(); err == nil {
			t.Fatalf("accepted invalid path %q", exe)
		}
		if _, err := os.Stat(m.path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid path registered startup")
		}
	}
	m := fileManager(t, "darwin")
	m.executable = "/private/var/folders/AppTranslocation/token/QCS.app/Contents/MacOS/qcs"
	if _, err := m.Enable(); err == nil {
		t.Fatal("accepted translocated app")
	}
}

func TestUnsupportedDesktopPathStillAllowsRemovingOldRegistration(t *testing.T) {
	m := fileManager(t, "linux")
	if _, err := m.Enable(); err != nil {
		t.Fatal(err)
	}
	m.executable = "/tmp/100%/qcs"
	st, err := m.Status()
	if err != nil || !st.Managed || !st.Enabled || st.Current || st.CanEnable || st.Problem == "" {
		t.Fatalf("bad current path: %+v %v", st, err)
	}
	if _, err := m.Enable(); err == nil {
		t.Fatal("GLib-incompatible path enabled")
	}
	st, err = m.Disable()
	if err != nil || st.Managed || st.Enabled {
		t.Fatalf("old registration removal: %+v %v", st, err)
	}
}

func TestNewUsesNativeHomeNotQCSHome(t *testing.T) {
	home := testenv.Home(t)
	t.Setenv("QCS_HOME", filepath.Join(t.TempDir(), "other"))
	t.Setenv("APPIMAGE", "")
	m, err := New(false)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(m.executable) {
		t.Fatal("executable is relative")
	}
	if runtime.GOOS == "windows" {
		m.registry = &fakeRegistry{} // never inspect the user's actual Run key
	}
	if runtime.GOOS != "windows" && !strings.HasPrefix(m.path, home+string(filepath.Separator)) {
		t.Fatalf("native record outside isolated home: %s", m.path)
	}
	if st, err := m.Status(); err != nil || st.Enabled || st.CanEnable {
		t.Fatalf("new manager: %+v %v", st, err)
	}
}

func TestAppImageResolutionAndInvalidTargetStillAllowDisable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("APPIMAGE is a Linux launcher override")
	}
	for _, variant := range []string{"missing", "directory", "nonexec", "symlink"} {
		t.Run(variant, func(t *testing.T) {
			home := testenv.Home(t)
			image := filepath.Join(home, "QCS image.AppImage")
			switch variant {
			case "directory":
				if err := os.Mkdir(image, 0o755); err != nil {
					t.Fatal(err)
				}
			case "nonexec":
				if err := os.WriteFile(image, []byte("image"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(home, "real image.AppImage")
				if err := os.WriteFile(target, []byte("image"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, image); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("APPIMAGE", image)
			m, err := New(true)
			if err != nil {
				t.Fatalf("cannot inspect existing registration: %v", err)
			}
			if variant == "symlink" {
				st, err := m.Enable()
				if err != nil || !st.Current || st.Executable == image {
					t.Fatalf("not resolved: %+v %v", st, err)
				}
			} else {
				if _, err := m.Enable(); err == nil {
					t.Fatal("invalid image enabled startup")
				}
				if _, err := os.Stat(m.path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("invalid image created registration")
				}
				if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(m.path, desktop("/opt/older/qcs"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			st, err := m.Disable()
			if err != nil || st.Managed || st.Enabled {
				t.Fatalf("invalid image blocked old disable: %+v %v", st, err)
			}
		})
	}
}

func TestUnsupportedAndFailedFileWrite(t *testing.T) {
	m := &Manager{platform: "plan9", executable: "/bin/qcs", canEnable: true}
	if st, err := m.Status(); err != nil || st.Supported || st.CanEnable || st.Problem == "" {
		t.Fatalf("unsupported: %+v %v", st, err)
	}
	if _, err := m.Enable(); err == nil {
		t.Fatal("unsupported Enable succeeded")
	}
	if _, err := m.Disable(); err == nil {
		t.Fatal("unsupported Disable succeeded")
	}
	m = fileManager(t, "linux")
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("user file"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.path = filepath.Join(blocker, "qcs.desktop")
	if _, err := m.Enable(); err == nil {
		t.Fatal("invalid destination silently enabled")
	}
	if b, err := os.ReadFile(blocker); err != nil || string(b) != "user file" {
		t.Fatalf("blocker changed: %q %v", b, err)
	}
}

func TestFileManagersSerialize(t *testing.T) {
	m := fileManager(t, "linux")
	n := &Manager{platform: m.platform, executable: m.executable, path: m.path, lockPath: m.lockPath, canEnable: true}
	var wg sync.WaitGroup
	for _, manager := range []*Manager{m, n} {
		wg.Go(func() {
			for range 8 {
				if _, err := manager.Enable(); err != nil {
					t.Error(err)
				}
				if _, err := manager.Disable(); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if _, err := m.Enable(); err != nil {
		t.Fatal(err)
	}
	if st, err := m.Status(); err != nil || !st.Enabled || !st.Managed {
		t.Fatalf("final: %+v %v", st, err)
	}
}
