//go:build !nogui

// Package gui is the desktop app: a tray icon and a window that show the
// page internal/webui serves. Builds with -tags nogui leave it out.
package gui

import "github.com/wailsapp/wails/v3/pkg/application"

var _ = application.New
