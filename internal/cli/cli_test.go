package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func mustRun(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	code, out, errs := run(t, stdin, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d\nout: %s\nerr: %s", args, code, out, errs)
	}
	return out
}

const codexCfg = "# mine\nmodel = \"gpt-5\"\n\n[features]\nfast_mode = true\n"

func setup(t *testing.T) (codexDir, claudeDir string) {
	home := testenv.Home(t)
	codexDir = filepath.Join(home, "codexhome")
	claudeDir = filepath.Join(home, "claudehome")
	t.Setenv("CODEX_HOME", codexDir)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	for _, d := range []string{codexDir, claudeDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func addCPA(t *testing.T) {
	mustRun(t, "", "provider", "add", "cpa", "--template", "cpa", "--key", "sk-supersecretkey-1234567890", "--header", "X-A=b")
	mustRun(t, "", "provider", "models", "cpa", "--add", "gpt-5.5,claude-sonnet")
}

func TestProviderLifecycle(t *testing.T) {
	setup(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "gpt-5.5"}, {"id": "o3"}}})
	}))
	defer srv.Close()
	mustRun(t, "", "provider", "add", "x", "--base", srv.URL+"/v1", "--key", "sk-abcdefghijklmnopqrstuvwxyz", "--name", "X")
	out := mustRun(t, "", "provider", "ls")
	if !strings.Contains(out, "x") || strings.Contains(out, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("ls: %s", out)
	}
	mustRun(t, "", "provider", "models", "x", "--add", "gpt-5.5")
	mustRun(t, "", "provider", "models", "x", "--fast", "gpt-5.5=false")
	out = mustRun(t, "", "provider", "models", "x", "--fetch")
	if !strings.Contains(out, "新增 1") || !strings.Contains(out, "gpt-5.5  fast=no") || !strings.Contains(out, "o3  fast=yes") {
		t.Fatalf("models: %s", out)
	}
	mustRun(t, "", "provider", "models", "x", "--rm", "o3")
	if out = mustRun(t, "", "provider", "models", "x"); strings.Contains(out, "o3") {
		t.Fatal(out)
	}
	if out = mustRun(t, "", "provider", "templates"); !strings.Contains(out, "cpa") {
		t.Fatal(out)
	}
	mustRun(t, "", "provider", "rm", "x")
	if code, _, _ := run(t, "", "provider", "rm", "x"); code != 1 {
		t.Fatalf("code %d", code)
	}
	if out = mustRun(t, "", "provider", "ls"); strings.Contains(out, "X") && strings.Contains(out, "base:") {
		t.Fatal(out)
	}
}

func TestUseCodexAndRestore(t *testing.T) {
	codexDir, _ := setup(t)
	cfg := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(cfg, []byte(codexCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	addCPA(t)

	// dry-run writes nothing
	out := mustRun(t, "", "use", "codex", "cpa/gpt-5.5", "--fast", "--dry-run")
	if !strings.Contains(out, "qcs-cpa") {
		t.Fatalf("diff: %s", out)
	}
	if b, _ := os.ReadFile(cfg); string(b) != codexCfg {
		t.Fatal("dry-run wrote")
	}
	// "n" aborts
	code, out, _ := run(t, "n\n", "use", "codex", "cpa/gpt-5.5", "--fast")
	if code != 1 || !strings.Contains(out, "应用这些修改? [y/N]") {
		t.Fatalf("code %d out %s", code, out)
	}
	if b, _ := os.ReadFile(cfg); string(b) != codexCfg {
		t.Fatal("abort wrote")
	}
	// flags after positionals, confirm with y
	mustRun(t, "y\n", "use", "codex", "cpa/gpt-5.5", "--fast")
	b, _ := os.ReadFile(cfg)
	if !strings.Contains(string(b), `model_provider = "qcs-cpa"`) || !strings.Contains(string(b), "[model_providers.qcs-cpa]") {
		t.Fatalf("config:\n%s", b)
	}
	out = mustRun(t, "", "ls")
	if !strings.Contains(out, "managed by qcs") || !strings.Contains(out, "direct") {
		t.Fatalf("ls: %s", out)
	}
	if strings.Contains(out, "supersecret") {
		t.Fatal("key leaked")
	}
	if code, _, _ := run(t, "", "doctor", "codex"); code != 0 {
		t.Fatalf("doctor code %d", code)
	}
	// restore
	out = mustRun(t, "", "use", "codex", "official", "--yes")
	if !strings.Contains(out, "kept") {
		t.Fatalf("restore: %s", out)
	}
	// the qcs-cpa table is deliberately kept (threads started on it); all else is original
	b, _ = os.ReadFile(cfg)
	if !strings.HasPrefix(string(b), codexCfg) || strings.Contains(string(b), "model_provider =") {
		t.Fatalf("not restored:\n%s", b)
	}
	if out = mustRun(t, "", "restore", "codex", "--yes"); !strings.Contains(out, "无需还原") {
		t.Fatal(out)
	}
}

func TestUseClaude(t *testing.T) {
	_, claudeDir := setup(t)
	addCPA(t)
	mustRun(t, "", "use", "claude", "cpa/claude-sonnet", "--yes")
	b, err := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	if err != nil || !strings.Contains(string(b), "claude-sonnet") {
		t.Fatalf("settings: %v %s", err, b)
	}
	mustRun(t, "", "restore", "claude", "--yes")
	if mustRun(t, "", "diff", "claude", "official") == "" {
		t.Fatal("no output")
	}
}

func TestDoctorDrift(t *testing.T) {
	codexDir, _ := setup(t)
	os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte(codexCfg), 0o644)
	addCPA(t)
	mustRun(t, "", "use", "codex", "cpa/gpt-5.5", "--yes")
	os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("model = \"other\"\n"), 0o644)
	out := mustRun(t, "", "doctor", "codex")
	if !strings.Contains(out, "漂移") {
		t.Fatalf("doctor: %s", out)
	}
}

func TestUsageErrors(t *testing.T) {
	setup(t)
	for _, args := range [][]string{
		{"bogus"},
		{"use"},
		{"use", "codex"},
		{"use", "nope", "official"},
		{"use", "codex", "noslash"},
		{"use", "codex", "a/b", "--fast", "--no-fast"},
		{"provider"},
		{"provider", "add"},
		{"provider", "add", "x", "--nope"},
		{"provider", "add", "x", "--template", "zzz"},
		{"restore"},
	} {
		if code, _, errs := run(t, "", args...); code != 2 || errs == "" {
			t.Errorf("%v: code %d err %q", args, code, errs)
		}
	}
	for _, args := range [][]string{{"help"}, {"version"}, {"use", "-h"}, {"provider", "add", "-h"}} {
		if code, out, _ := run(t, "", args...); code != 0 || out == "" {
			t.Errorf("%v: code %d", args, code)
		}
	}
}
