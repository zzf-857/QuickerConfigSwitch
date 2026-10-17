//go:build windows

// Adapted from yetone/magpie internal/agent/desktopapp_windows.go (MIT,
// Copyright (c) 2026 yetone); see third_party/magpie/LICENSE.
package desktopapp

import "golang.org/x/sys/windows/registry"

const appxPackagesKey = `Software\Classes\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppModel\Repository\Packages`

// The current user's AppX registry needs neither WindowsApps access nor
// PowerShell. No registry values are changed.
func installedWindowsStorePackages() []string {
	key, err := registry.OpenKey(registry.CURRENT_USER, appxPackagesKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer key.Close()
	names, _ := key.ReadSubKeyNames(-1)
	return names
}
