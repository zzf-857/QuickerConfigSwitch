package cli

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/importer"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/webui"
)

func TestProfileLifecycle(t *testing.T) {
	codexDir, _ := setup(t)
	cfg := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(cfg, []byte(codexCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	addCPA(t)
	if out := mustRun(t, "", "profile", "ls"); !strings.Contains(out, "official") {
		t.Fatalf("ls: %s", out)
	}
	mustRun(t, "", "use", "codex", "cpa/gpt-5.5", "--yes")
	out := mustRun(t, "", "profile", "save", "work")
	if !strings.Contains(out, "codex: cpa/gpt-5.5") {
		t.Fatalf("save: %s", out)
	}
	if out = mustRun(t, "", "profile", "ls"); !strings.Contains(out, "work") {
		t.Fatalf("ls: %s", out)
	}
	mustRun(t, "", "use", "codex", "official", "--yes")
	after, _ := os.ReadFile(cfg)

	// dry-run shows the diff and writes nothing
	out = mustRun(t, "", "profile", "apply", "work", "--dry-run")
	if !strings.Contains(out, "qcs-cpa") {
		t.Fatalf("dry-run: %s", out)
	}
	if b, _ := os.ReadFile(cfg); string(b) != string(after) {
		t.Fatal("dry-run wrote")
	}
	// "n" aborts
	if code, out, _ := run(t, "n\n", "profile", "apply", "work"); code != 1 || !strings.Contains(out, "[y/N]") {
		t.Fatalf("code %d out %s", code, out)
	}
	if b, _ := os.ReadFile(cfg); string(b) != string(after) {
		t.Fatal("abort wrote")
	}
	// confirmed
	out = mustRun(t, "y\n", "profile", "apply", "work")
	if !strings.Contains(out, "已应用 codex") {
		t.Fatalf("apply: %s", out)
	}
	if b, _ := os.ReadFile(cfg); !strings.Contains(string(b), `model_provider = "qcs-cpa"`) {
		t.Fatalf("not applied:\n%s", b)
	}
	// official is built in and can't be removed
	mustRun(t, "", "profile", "apply", "official", "--yes")
	if code, _, _ := run(t, "", "profile", "rm", "official"); code != 1 {
		t.Fatalf("rm official: %d", code)
	}
	mustRun(t, "", "profile", "rm", "work")
	if code, _, _ := run(t, "", "profile", "rm", "work"); code != 1 {
		t.Fatalf("rm twice: %d", code)
	}
	if code, _, _ := run(t, "", "profile", "apply", "work", "--yes"); code != 1 {
		t.Fatalf("apply missing: %d", code)
	}
}

func TestProfileApplyFailureExits1(t *testing.T) {
	codexDir, _ := setup(t)
	os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(codexCfg), 0o644)
	addCPA(t)
	mustRun(t, "", "use", "codex", "cpa/gpt-5.5", "--yes")
	mustRun(t, "", "profile", "save", "p")
	mustRun(t, "", "provider", "rm", "cpa") // the profile now names a provider that's gone
	code, out, errs := run(t, "", "profile", "apply", "p", "--yes")
	if code != 1 || !strings.Contains(out+errs, "失败") {
		t.Fatalf("code %d out %s err %s", code, out, errs)
	}
}

func TestProfileUsageErrors(t *testing.T) {
	setup(t)
	for _, args := range [][]string{{"profile"}, {"profile", "zzz"}, {"profile", "save"}, {"profile", "apply"}, {"profile", "rm"}, {"profile", "ls", "x"}} {
		if code, _, errs := run(t, "", args...); code != 2 || errs == "" {
			t.Errorf("%v: code %d", args, code)
		}
	}
}

func fakeScan(t *testing.T, cands []importer.Candidate, errs []error) {
	old := scanImports
	scanImports = func() ([]importer.Candidate, []error) { return cands, errs }
	t.Cleanup(func() { scanImports = old })
}

func TestImportEmpty(t *testing.T) {
	setup(t)
	fakeScan(t, nil, nil)
	if out := mustRun(t, "", "import"); !strings.Contains(out, "没有发现") {
		t.Fatal(out)
	}
	if code, _, _ := run(t, "", "import", "--add", "1"); code != 1 {
		t.Fatalf("code %d", code)
	}
}

func TestImportListAndAdd(t *testing.T) {
	setup(t)
	fakeScan(t, []importer.Candidate{
		{Source: "codex", Origin: "table x", Provider: provider.Provider{ID: "x", Name: "X", BaseURL: "https://a/v1", APIKey: "sk-abcdefghijklmnopqrstuvwxyz", APIs: provider.APIs{Responses: true}, Models: []provider.Model{{ID: "m"}}}},
		{Source: "cc-switch", Origin: "Y", Provider: provider.Provider{ID: "y", Name: "Y", BaseURL: "https://b/v1", APIs: provider.APIs{Chat: true}}, Exists: "old", Note: "dup"},
	}, []error{errors.New("boom")})
	code, out, errs := run(t, "", "import")
	if code != 0 || !strings.Contains(out, "1. x  (codex: table x)") || !strings.Contains(out, "2. y") ||
		!strings.Contains(out, "已存在: old") || !strings.Contains(out, "note:   dup") || strings.Contains(out, "abcdefghijklmnopqrstuvwxyz") ||
		!strings.Contains(errs, "boom") {
		t.Fatalf("code %d\nout: %s\nerr: %s", code, out, errs)
	}
	var added []string
	old := addImport
	addImport = func(c importer.Candidate, id string) (provider.Provider, error) {
		p := c.Provider
		if id != "" {
			p.ID = id
		}
		added = append(added, p.ID)
		return p, nil
	}
	t.Cleanup(func() { addImport = old })
	mustRun(t, "", "import", "--add", "1", "--id", "mine")
	mustRun(t, "", "import", "--add", "all")
	if strings.Join(added, ",") != "mine,x" {
		t.Fatalf("added %v", added)
	}
	for _, args := range [][]string{{"import", "--add", "3"}, {"import", "--add", "zero"}, {"import", "--id", "q"}, {"import", "--add", "all", "--id", "q"}, {"import", "extra"}} {
		if code, _, _ := run(t, "", args...); code != 2 {
			t.Errorf("%v: code %d", args, code)
		}
	}
}

func TestWebFlags(t *testing.T) {
	setup(t)
	opened := 0
	oldOpen := openBrowser
	openBrowser = func(string) error { opened++; return nil }
	t.Cleanup(func() { openBrowser = oldOpen })

	code, _, errs := run(t, "", "web", "--host", "0.0.0.0", "--no-open")
	if code != 2 || !strings.Contains(errs, "--allow-remote") {
		t.Fatalf("code %d err %s", code, errs)
	}
	for _, args := range [][]string{{"web", "--port", "70000"}, {"web", "x"}, {"web", "--host", "example.com"}} {
		if code, _, _ := run(t, "", args...); code != 2 {
			t.Errorf("%v: code %d", args, code)
		}
	}
	if opened != 0 {
		t.Fatal("browser opened on bad flags")
	}

	// a real start on a free port, stopped at once
	oldStop := webStop
	webStop = func() (<-chan struct{}, func()) {
		c := make(chan struct{})
		close(c)
		return c, func() {}
	}
	t.Cleanup(func() { webStop = oldStop })
	oldH := webHandler
	webHandler = func(webui.Options) http.Handler { return http.NotFoundHandler() }
	t.Cleanup(func() { webHandler = oldH })
	code, out, errs := run(t, "", "web", "--port", "0")
	if code != 0 || !strings.Contains(out, "http://127.0.0.1:") || !strings.Contains(out, "?token=") || opened != 1 {
		t.Fatalf("code %d out %s err %s opened %d", code, out, errs, opened)
	}
	code, out, errs = run(t, "", "web", "--no-open", "--port", "0")
	if code != 0 || opened != 1 {
		t.Fatalf("code %d out %s err %s", code, out, errs)
	}
}

func TestNoArgsRunsGUI(t *testing.T) {
	setup(t)
	oldRun, oldDisp := runGUI, hasDisplay
	t.Cleanup(func() { runGUI, hasDisplay = oldRun, oldDisp })

	hasDisplay = func() bool { return true }
	runGUI = func() error { return nil }
	if code, _, _ := run(t, ""); code != 0 {
		t.Fatalf("code %d", code)
	}
	runGUI = func() error { return errors.New("nope") }
	code, _, errs := run(t, "", "gui")
	if code != 1 || !strings.Contains(errs, "nope") || !strings.Contains(errs, "qcs web") {
		t.Fatalf("code %d err %s", code, errs)
	}
	hasDisplay = func() bool { return false }
	runGUI = func() error { t.Fatal("gui started without a display"); return nil }
	code, _, errs = run(t, "")
	if code != 1 || !strings.Contains(errs, "qcs web") {
		t.Fatalf("code %d err %s", code, errs)
	}
}
