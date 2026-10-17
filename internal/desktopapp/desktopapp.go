// Package desktopapp recognizes the desktop clients that share an agent's
// configuration. Adapted from yetone/magpie internal/agent/desktopapp.go and
// desktopapp_windows.go (MIT, Copyright (c) 2026 yetone), commit
// 7660399a121af67db4efd56ecb6bd4fb04dc53f1; see third_party/magpie/LICENSE.
package desktopapp

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// sandboxMarker matches testenv.Marker without importing its testing helpers
// into the application. No test in another package may read real Store apps
// or /Applications merely because it calls an agent's Detect method.
const sandboxMarker = "QCS_TEST_SANDBOX"

type app struct {
	id        string
	names     []string
	store     string
	publisher string
}

var apps = map[string]app{
	// ChatGPT.app can be the Codex client or the unrelated chat client;
	// its bundle identifier, rather than its name, distinguishes the two.
	"codex": {id: "com.openai.codex", names: []string{"ChatGPT.app", "Codex.app"}, store: "OpenAI.Codex", publisher: "2p2nqsd0c76g0"},
}

var appFolders = func() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	global := "/Applications"
	if sandbox := os.Getenv(sandboxMarker); sandbox != "" {
		global = filepath.Join(sandbox, "Applications")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{global}
	}
	return []string{global, filepath.Join(home, "Applications")}
}

var windowsStorePackages = func() []string {
	if os.Getenv(sandboxMarker) != "" {
		return nil
	}
	return installedWindowsStorePackages()
}

var plistID = regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>([^<]+)</string>`)

// Installed recognizes an app by its bundle or Store identity. Unknown apps
// and an unreadable identity return false. It reads no credentials, starts
// no shell or app, and does not scan arbitrary folders or other users.
func Installed(id string) bool {
	a, ok := apps[id]
	if !ok {
		return false
	}
	for _, bundle := range inAppFolders(appFolders(), a.names...) {
		b, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
		if err == nil {
			if match := plistID.FindSubmatch(b); match != nil && strings.TrimSpace(string(match[1])) == a.id {
				return true
			}
		}
	}
	return a.store != "" && storeAppVersion(windowsStorePackages(), a.store, a.publisher) != ""
}

// inAppFolders also finds bundles one level down, such as /Applications/AI.
// Hidden folders and other app bundles are not traversed.
func inAppFolders(dirs []string, names ...string) []string {
	var out []string
	for _, dir := range dirs {
		for _, name := range names {
			out = append(out, filepath.Join(dir, name))
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasSuffix(e.Name(), ".app") || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			for _, name := range names {
				out = append(out, filepath.Join(dir, e.Name(), name))
			}
		}
	}
	return out
}

// storeAppVersion ignores resource-only packages left after an uninstall or
// update, and matches both the package name and publisher, as Magpie does.
func storeAppVersion(packages []string, name, publisher string) string {
	prefix, suffix := name+"_", "_"+publisher
	var newest [4]uint64
	found := ""
	for _, pkg := range packages {
		if !strings.HasPrefix(pkg, prefix) || !strings.HasSuffix(pkg, suffix) {
			continue
		}
		fields := strings.Split(strings.TrimSuffix(strings.TrimPrefix(pkg, prefix), suffix), "_")
		if len(fields) != 3 || fields[2] != "" {
			continue
		}
		parts := strings.Split(fields[0], ".")
		if len(parts) != len(newest) {
			continue
		}
		var version [4]uint64
		valid := true
		for i, part := range parts {
			var err error
			version[i], err = strconv.ParseUint(part, 10, 16)
			if err != nil {
				valid = false
				break
			}
		}
		if valid && (found == "" || slices.Compare(version[:], newest[:]) > 0) {
			newest, found = version, fields[0]
		}
	}
	return found
}
