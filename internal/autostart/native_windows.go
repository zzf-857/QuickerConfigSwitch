// Adapted from yetone/magpie internal/autostart (MIT, Copyright (c) 2026 yetone).

package autostart

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

type windowsRegistry struct{}

func nativeRegistry() registryOps { return windowsRegistry{} }

func registryRead(key, name string) (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	return v, err == nil, err
}

func registryWrite(key, name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

func registryDelete(key, name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

func (windowsRegistry) readRun() (string, bool, error) { return registryRead(runKey, valueName) }
func (windowsRegistry) writeRun(v string) error        { return registryWrite(runKey, valueName, v) }
func (windowsRegistry) deleteRun() error               { return registryDelete(runKey, valueName) }
func (windowsRegistry) readReceipt() (string, bool, error) {
	return registryRead(receiptKey, receiptName)
}
func (windowsRegistry) writeReceipt(v string) error {
	return registryWrite(receiptKey, receiptName, v)
}
func (windowsRegistry) deleteReceipt() error { return registryDelete(receiptKey, receiptName) }

func (windowsRegistry) switchedOff() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, approvedKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer k.Close()
	b, _, err := k.GetBinaryValue(valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	return err == nil && len(b) > 0 && b[0]&1 == 1, err
}

func (windowsRegistry) clearApprovedMarker() error {
	return registryDelete(approvedKey, valueName)
}
