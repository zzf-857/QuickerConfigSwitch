// Adapted from yetone/magpie internal/autostart (MIT, Copyright (c) 2026 yetone).

package autostart

import (
	"errors"
	"fmt"
	"strings"
)

const (
	runKey      = `Software\Microsoft\Windows\CurrentVersion\Run`
	approvedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`
	receiptKey  = `Software\QuickerConfigSwitch`
	valueName   = "QuickerConfigSwitch"
	receiptName = "AutostartCommand"
)

// Native registry access is behind this small boundary so tests on every OS
// exercise the same ownership and rollback paths without registering startup.
type registryOps interface {
	readRun() (string, bool, error)
	writeRun(string) error
	deleteRun() error
	readReceipt() (string, bool, error)
	writeReceipt(string) error
	deleteReceipt() error
	switchedOff() (bool, error)
	clearApprovedMarker() error
}

func windowsCommand(exe string) string { return `"` + exe + `" gui --hidden` }

func commandExecutable(command string) (string, bool) {
	p, ok := strings.CutSuffix(command, `" gui --hidden`)
	if !ok || !strings.HasPrefix(p, `"`) {
		return "", false
	}
	exe := strings.TrimPrefix(p, `"`)
	return exe, validExecutable("windows", exe) == nil && windowsCommand(exe) == command
}

func (m *Manager) registryStatus() (Status, error) {
	st := m.base()
	run, present, err := m.registry.readRun()
	if err != nil {
		return st, err
	}
	receipt, recorded, err := m.registry.readReceipt()
	if err != nil {
		return st, err
	}
	if !present && !recorded {
		return st, nil
	}
	exe, valid := commandExecutable(receipt)
	if !recorded || !valid || present && run != receipt {
		st.Problem = "启动项未知或已被修改，已保留原状"
		return st, nil
	}
	st.Managed = true
	st.Executable = exe
	if !present {
		return st, nil // a stale receipt is safe to clean up or refresh
	}
	st.Current = strings.EqualFold(exe, m.executable)
	off, err := m.registry.switchedOff()
	if err != nil {
		return st, err
	}
	st.Enabled = !off
	if off {
		st.Problem = "开机自启已在 Windows 启动应用中禁用，可显式开启"
	}
	return st, nil
}

func (m *Manager) setRegistry(on bool) error {
	st, err := m.registryStatus()
	if err != nil {
		return err
	}
	if !st.Managed && st.Problem != "" {
		return errors.New(st.Problem)
	}
	oldRun, runPresent, err := m.registry.readRun()
	if err != nil {
		return err
	}
	oldReceipt, receiptPresent, err := m.registry.readReceipt()
	if err != nil {
		return err
	}
	// Run values and their receipts must match before either is changed. An
	// unrelated entry with our name is never adopted simply because it exists.
	_, validReceipt := commandExecutable(oldReceipt)
	if runPresent && (!receiptPresent || oldRun != oldReceipt) || receiptPresent && !validReceipt {
		return errors.New("启动项已被修改，已保留原状")
	}
	rollback := func(cause error) error {
		rerr := restoreValue(runPresent, oldRun, m.registry.writeRun, m.registry.deleteRun)
		oerr := restoreValue(receiptPresent, oldReceipt, m.registry.writeReceipt, m.registry.deleteReceipt)
		if rerr != nil || oerr != nil {
			return errors.Join(cause, fmt.Errorf("autostart rollback: %w", errors.Join(rerr, oerr)))
		}
		return cause
	}
	if !on {
		if !st.Managed {
			return nil
		}
		if err := m.registry.deleteRun(); err != nil {
			return err
		}
		if err := m.registry.deleteReceipt(); err != nil {
			return rollback(err)
		}
		return nil
	}
	want := windowsCommand(m.executable)
	if err := m.registry.writeReceipt(want); err != nil {
		return err
	}
	if err := m.registry.writeRun(want); err != nil {
		return rollback(err)
	}
	// As in Magpie, explicitly turning it on also clears Task Manager's
	// StartupApproved marker; otherwise the Run value would never run.
	if err := m.registry.clearApprovedMarker(); err != nil {
		return rollback(err)
	}
	return nil
}

func restoreValue(present bool, value string, write func(string) error, remove func() error) error {
	if present {
		return write(value)
	}
	return remove()
}
