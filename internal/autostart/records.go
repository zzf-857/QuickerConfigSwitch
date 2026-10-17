// Adapted from yetone/magpie internal/autostart (MIT, Copyright (c) 2026 yetone).

package autostart

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
)

const desktopOwner = "X-QuickerConfigSwitch-Executable="

// Desktop Entry values are unescaped before Exec's quoted arguments are
// parsed. Escape both layers, and double percent signs to suppress field
// codes even in quoted executable names. No shell is involved.
func desktop(exe string) []byte {
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`, `%`, `%%`).Replace(exe)
	q = strings.ReplaceAll(q, `\`, `\\`)
	return []byte("[Desktop Entry]\nType=Application\nName=QuickerConfigSwitch\n" +
		"Comment=Switch coding agent configurations\nExec=\"" + q + "\" gui --hidden\n" +
		"Terminal=false\nX-GNOME-Autostart-enabled=true\n" + desktopOwner +
		base64.RawURLEncoding.EncodeToString([]byte(exe)) + "\n")
}

// This is Magpie's LaunchAgent template, with QCS's identity and command.
// RunAtLoad takes effect at the next login; there is no launchctl bootstrap.
func plist(exe, bundle string) []byte {
	associated := ""
	if bundle != "" {
		associated = "\t<key>AssociatedBundleIdentifiers</key>\n\t<array><string>" + html.EscapeString(bundle) + "</string></array>\n"
	}
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Managed by QuickerConfigSwitch autostart v1 -->
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array><string>%s</string><string>gui</string><string>--hidden</string></array>
	<key>RunAtLoad</key><true/>
	<key>LimitLoadToSessionType</key><string>Aqua</string>
	<key>ProcessType</key><string>Interactive</string>
	<key>AbandonProcessGroup</key><true/>
%s</dict>
</plist>
`, identifier, html.EscapeString(exe), associated))
}

var (
	program    = regexp.MustCompile(`<key>ProgramArguments</key>\s*<array><string>([^<]+)</string>`)
	bundleID   = regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>([^<]+)</string>`)
	associated = regexp.MustCompile(`<key>AssociatedBundleIdentifiers</key>\s*<array><string>([^<]+)</string></array>`)
)

// associatedBundle follows Magpie: Login Items should name the containing app,
// rather than its signing developer. A bare executable has no bundle ID.
func associatedBundle(exe string) string {
	macos := filepath.Dir(exe)
	if filepath.Base(macos) != "MacOS" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(macos), "Info.plist"))
	if err != nil {
		return ""
	}
	m := bundleID.FindSubmatch(b)
	if m == nil {
		return ""
	}
	return html.UnescapeString(string(m[1]))
}

func (m *Manager) fileStatus() (Status, error) {
	st := m.base()
	fi, err := os.Lstat(m.path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if !fi.Mode().IsRegular() {
		st.Problem = "启动项不是普通文件，已保留原状"
		return st, nil
	}
	b, err := os.ReadFile(m.path)
	if err != nil {
		return st, err
	}
	exe, valid := m.recognize(b)
	if !valid {
		st.Problem = "启动项未知或已被修改，已保留原状"
		return st, nil
	}
	st.Enabled, st.Managed = true, true
	st.Executable = exe
	st.Current = exe == m.executable
	return st, nil
}

// Recognize only our complete output. The executable is read from the record,
// so an app moved or upgraded can still remove its older registration. A
// changed Info.plist does not invalidate an existing LaunchAgent's ownership.
func (m *Manager) recognize(b []byte) (string, bool) {
	var exe string
	var want []byte
	if m.platform == "linux" {
		for _, line := range strings.Split(string(b), "\n") {
			if encoded, ok := strings.CutPrefix(line, desktopOwner); ok {
				p, err := base64.RawURLEncoding.DecodeString(encoded)
				if err != nil {
					return "", false
				}
				exe = string(p)
			}
		}
		want = desktop(exe)
	} else {
		p := program.FindSubmatch(b)
		if p == nil {
			return "", false
		}
		exe = html.UnescapeString(string(p[1]))
		bundle := ""
		if a := associated.FindSubmatch(b); a != nil {
			bundle = html.UnescapeString(string(a[1]))
		}
		want = plist(exe, bundle)
	}
	return exe, validExecutable(m.platform, exe) == nil && bytes.Equal(b, want)
}

func (m *Manager) setFile(on bool) error {
	st, err := m.fileStatus()
	if err != nil {
		return err
	}
	if st.Problem != "" {
		return errors.New(st.Problem)
	}
	if !on {
		if !st.Managed {
			return nil
		}
		if err := os.Remove(m.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if st.Current {
		return nil
	}
	b := desktop(m.executable)
	if m.platform == "darwin" {
		b = plist(m.executable, associatedBundle(m.executable))
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	return edit.WriteAtomic(m.path, b)
}
