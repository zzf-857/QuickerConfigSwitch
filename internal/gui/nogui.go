//go:build nogui

// Package gui is the desktop app. This build was made with -tags nogui and
// has none.
package gui

import "errors"

// Available is false for a CLI/Web-only build: registering it to open
// an unavailable desktop on login would leave an unusable startup entry.
const Available = false

// Run reports that this build has no GUI.
func Run(hidden bool) error {
	return errors.New("这个构建不含桌面界面 (nogui); 请用 qcs web 在浏览器中打开")
}
