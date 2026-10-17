package desktopapp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func install(t *testing.T, path, id string) {
	t.Helper()
	dir := filepath.Join(path, "Contents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Info.plist"), []byte("<plist><dict><key>CFBundleIdentifier</key>\n<string>"+id+"</string></dict></plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Adapted from Magpie TestCodexAppVersion: identically named ChatGPT apps
// must be distinguished, and the client may be installed without its CLI.
func TestCodexBundleIdentity(t *testing.T) {
	global, user := t.TempDir(), t.TempDir()
	wasFolders, wasStore := appFolders, windowsStorePackages
	appFolders = func() []string { return []string{global, user} }
	windowsStorePackages = func() []string { return nil }
	t.Cleanup(func() { appFolders, windowsStorePackages = wasFolders, wasStore })
	if Installed("codex") || Installed("unknown") {
		t.Fatal("empty application folders detected as installed")
	}
	install(t, filepath.Join(global, "ChatGPT.app"), "com.openai.chat")
	if Installed("codex") {
		t.Fatal("ChatGPT chat client mistaken for Codex")
	}
	install(t, filepath.Join(user, "ChatGPT.app"), "com.openai.codex")
	if !Installed("codex") {
		t.Fatal("ChatGPT app with Codex identity not detected")
	}
	if err := os.RemoveAll(filepath.Join(user, "ChatGPT.app")); err != nil {
		t.Fatal(err)
	}
	install(t, filepath.Join(global, "AI", "Codex.app"), "com.openai.codex")
	if !Installed("codex") {
		t.Fatal("Codex app one directory below Applications not detected")
	}
	if err := os.RemoveAll(filepath.Join(global, "AI")); err != nil {
		t.Fatal(err)
	}
	install(t, filepath.Join(global, ".hidden", "Codex.app"), "com.openai.codex")
	install(t, filepath.Join(global, "Other.app", "Codex.app"), "com.openai.codex")
	if Installed("codex") {
		t.Fatal("hidden or nested application bundle traversed")
	}
}

// Adapted from Magpie TestCodexWindowsStoreAppVersion. A stale resource
// package or a package from another publisher does not prove installation.
func TestCodexStoreIdentity(t *testing.T) {
	const publisher = "2p2nqsd0c76g0"
	packages := []string{
		"OpenAI.Codex_not-a-version_x64__" + publisher,
		"OpenAI.Codex_99.0.0.0_x64__wrong-publisher",
		"OpenAI.ChatGPT_99.0.0.0_x64__" + publisher,
		"OpenAI.Codex_27.0.0.0_neutral_split.scale-125_" + publisher,
	}
	wasFolders, wasStore := appFolders, windowsStorePackages
	appFolders = func() []string { return nil }
	windowsStorePackages = func() []string { return packages }
	t.Cleanup(func() { appFolders, windowsStorePackages = wasFolders, wasStore })
	if Installed("codex") {
		t.Fatal("unrelated or resource package mistaken for installed client")
	}
	packages = append(packages, "OpenAI.Codex_26.928.2636.0_x64__"+publisher, "OpenAI.Codex_26.1002.7124.0_arm64__"+publisher)
	if !Installed("codex") || storeAppVersion(packages, "OpenAI.Codex", publisher) != "26.1002.7124.0" {
		t.Fatal("main Store package not recognized by numeric version")
	}
}

func TestSystemAppReadersAreIsolated(t *testing.T) {
	sandbox := os.Getenv(testenv.Marker)
	if sandbox == "" || sandboxMarker != testenv.Marker {
		t.Fatal("missing or inconsistent sandbox marker")
	}
	if packages := windowsStorePackages(); len(packages) > 0 {
		t.Fatal("tests read the real Windows Store registry")
	}
	if runtime.GOOS == "darwin" {
		for _, dir := range appFolders() {
			rel, err := filepath.Rel(sandbox, dir)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				t.Fatalf("application reader escaped sandbox: %s", dir)
			}
		}
	}
}
