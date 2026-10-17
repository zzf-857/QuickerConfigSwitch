// Adapted from yetone/magpie internal/autostart (MIT, Copyright (c) 2026 yetone).

// Package autostart opens QuickerConfigSwitch at user login, in the tray.
// The system's registration is the source of truth; registration never starts
// the program immediately or enables a relay.
package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zzf-857/quickerconfigswitch/internal/filelock"
)

const identifier = "io.github.zzf-857.quickerconfigswitch"

// Status describes this user's registration. Current means that a managed
// registration names this copy of qcs. Operating-system policy may still
// prevent login items from running.
type Status struct {
	Supported  bool   `json:"supported"`
	Enabled    bool   `json:"enabled"`
	Managed    bool   `json:"managed"`
	Current    bool   `json:"current"`
	Path       string `json:"path"`
	Executable string `json:"executable"`
	Problem    string `json:"problem,omitempty"`
	CanEnable  bool   `json:"canEnable"`
}

// Manager controls one user's registration for the current executable.
// Separate processes serialize changes with an OS-held file lock.
type Manager struct {
	mu         sync.Mutex
	platform   string
	executable string
	path       string
	lockPath   string
	canEnable  bool
	resolveErr error
	registry   registryOps
}

// New resolves this executable and the user's native registration location.
// canEnable must be false for a build without a desktop interface; such a
// build can still inspect and disable an older desktop registration.
func New(canEnable bool) (*Manager, error) {
	exe, resolveErr := self()
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	m := &Manager{platform: runtime.GOOS, executable: exe, canEnable: canEnable, resolveErr: resolveErr}
	switch runtime.GOOS {
	case "linux":
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(home, ".config")
		}
		if !filepath.IsAbs(cfg) {
			return nil, errors.New("autostart: XDG_CONFIG_HOME must be absolute")
		}
		m.path = filepath.Join(cfg, "autostart", identifier+".desktop")
		m.lockPath = m.path + ".lock"
	case "darwin":
		m.path = filepath.Join(home, "Library", "LaunchAgents", identifier+".plist")
		m.lockPath = m.path + ".lock"
	case "windows":
		m.path = `HKCU\` + runKey + `\` + valueName
		cfg, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		m.lockPath = filepath.Join(cfg, "QuickerConfigSwitch", "autostart.lock")
		m.registry = nativeRegistry()
	}
	return m, nil
}

// self follows Magpie's executable resolution: an AppImage's mounted binary
// disappears at logout, and a translocated macOS application must first move
// to a permanent location. Unlike a shell command, this path needs no PATH.
func self() (string, error) {
	exe := ""
	if runtime.GOOS == "linux" {
		exe = os.Getenv("APPIMAGE")
	}
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	exe, err := filepath.Abs(exe)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe, fmt.Errorf("autostart executable: %w", err)
	}
	return resolved, nil
}

func (m *Manager) base() Status {
	supported := m.platform == "linux" || m.platform == "darwin" || m.platform == "windows"
	return Status{Supported: supported, Path: m.path, Executable: m.executable,
		CanEnable: supported && m.canEnable && m.enableProblem() == nil}
}

// Status inspects the native registration without changing it.
func (m *Manager) Status() (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status()
}

func (m *Manager) status() (Status, error) {
	st := m.base()
	if !st.Supported {
		st.Problem = "此平台不支持开机自启"
		return st, nil
	}
	var err error
	if m.platform == "windows" {
		st, err = m.registryStatus()
	} else {
		st, err = m.fileStatus()
	}
	if problem := m.enableProblem(); st.Problem == "" && problem != nil {
		st.Problem = problem.Error()
	}
	return st, err
}

// Enable registers this executable as `qcs gui --hidden`, for the next login.
// A registration that qcs does not recognize is left untouched.
func (m *Manager) Enable() (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.base().Supported {
		return m.base(), errors.New("此平台不支持开机自启")
	}
	if !m.canEnable {
		return m.base(), errors.New("此构建不含桌面界面，无法启用开机自启")
	}
	if err := m.enableProblem(); err != nil {
		return m.base(), err
	}
	if m.platform == runtime.GOOS {
		fi, err := os.Stat(m.executable)
		if err != nil {
			return m.base(), fmt.Errorf("autostart executable: %w", err)
		}
		if !fi.Mode().IsRegular() || runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
			return m.base(), errors.New("autostart: path is not an executable file")
		}
	}
	return m.change(true)
}

func (m *Manager) enableProblem() error {
	if m.resolveErr != nil {
		return m.resolveErr
	}
	if err := validExecutable(m.platform, m.executable); err != nil {
		return err
	}
	if m.platform == "linux" && strings.Contains(m.executable, "%") {
		// GLib's desktop launcher checks the executable before expanding %%
		// and refuses this otherwise-valid entry. Do not register a login
		// command known to fail on common desktops, or add a shell wrapper.
		return errors.New("Linux 桌面启动器不支持含 % 的程序路径，请将 qcs 移到不含 % 的固定路径后启用")
	}
	if m.platform == "darwin" && strings.Contains(m.executable, "/AppTranslocation/") {
		return errors.New("请先将 QuickerConfigSwitch 移至 Applications，再启用开机自启")
	}
	return nil
}

// Disable removes only an unmodified registration owned by qcs. It does not
// stop an already-running desktop application.
func (m *Manager) Disable() (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.base().Supported {
		return m.base(), errors.New("此平台不支持开机自启")
	}
	return m.change(false)
}

func (m *Manager) change(on bool) (Status, error) {
	unlock, held := filelock.Lock(m.lockPath, 2*time.Second)
	defer unlock()
	if !held {
		return m.base(), errors.New("autostart: could not lock registration")
	}
	var err error
	if m.platform == "windows" {
		err = m.setRegistry(on)
	} else {
		err = m.setFile(on)
	}
	st, serr := m.status()
	return st, errors.Join(err, serr)
}

func validExecutable(platform, exe string) error {
	if !utf8.ValidString(exe) {
		return errors.New("autostart: executable path must be valid UTF-8")
	}
	absolute := filepath.IsAbs(exe)
	if platform == "windows" {
		absolute = len(exe) >= 3 && exe[1] == ':' && (exe[2] == '\\' || exe[2] == '/') || strings.HasPrefix(exe, `\\`)
		if strings.Contains(exe, `"`) {
			return errors.New("autostart: invalid Windows executable path")
		}
	}
	if !absolute {
		return errors.New("autostart: executable path must be absolute")
	}
	for _, r := range exe {
		if r < 0x20 || r == 0x7f {
			return errors.New("autostart: control character in executable path")
		}
	}
	return nil
}
