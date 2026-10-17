package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/agent/codex"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func TestRelayPreviewCancelAndForeground(t *testing.T) {
	dir, _ := setup(t)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(codexCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	addCPA(t)
	for _, cmd := range [][]string{
		{"use", "codex", "cpa/gpt-5.5", "--relay", "--port", "0", "--dry-run"},
		{"diff", "codex", "cpa/gpt-5.5", "--relay", "--port", "0"},
	} {
		out := mustRun(t, "", cmd...)
		if !strings.Contains(out, "openai_base_url") || strings.Contains(out, cpaKey) {
			t.Fatalf("preview: %s", out)
		}
		if b, _ := os.ReadFile(path); string(b) != codexCfg || safekeep.Touched("codex") {
			t.Fatal("preview modified config")
		}
	}
	if code, _, _ := run(t, "n\n", "use", "codex", "cpa/gpt-5.5", "--relay", "--port", "0"); code != 1 {
		t.Fatalf("cancel code %d", code)
	}
	old := relayStop
	t.Cleanup(func() { relayStop = old })
	relayStop = func() (<-chan struct{}, func()) {
		stop := make(chan struct{})
		close(stop)
		return stop, func() {}
	}
	out := mustRun(t, "", "use", "codex", "cpa/gpt-5.5", "--relay", "--relay-all", "--port", "0", "--yes")
	if !strings.Contains(out, "本地路由已启动") || !strings.Contains(out, "已还原") {
		t.Fatalf("foreground: %s", out)
	}
	if b, _ := os.ReadFile(path); string(b) != codexCfg || safekeep.Touched("codex") {
		t.Fatalf("did not restore: %s", b)
	}
}

func TestRelayFlagsAndRecovery(t *testing.T) {
	dir, _ := setup(t)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(codexCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	addCPA(t)
	for _, args := range [][]string{
		{"use", "codex", "cpa/gpt-5.5", "--relay-all"},
		{"use", "codex", "cpa/gpt-5.5", "--port", "0"},
		{"use", "claude", "cpa/gpt-5.5", "--relay"},
		{"use", "codex", "official", "--relay"},
		{"relay", "unknown"},
	} {
		if code, _, _ := run(t, "", args...); code != 2 {
			t.Fatalf("%v: code %d", args, code)
		}
	}
	for _, args := range [][]string{
		{"use", "codex", "cpa/gpt-5.5", "--relay", "--port", "-1"},
		{"use", "codex", "cpa/gpt-5.5", "--relay", "--fast", "--dry-run"},
	} {
		if code, _, _ := run(t, "", args...); code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
	p, err := codex.New().Plan(agent.Target{Mode: agent.Relay, Provider: "cpa", Model: "gpt-5.5", RelayURL: "http://127.0.0.1:17321/backend-api/codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err := safekeep.Apply(p); err != nil {
		t.Fatal(err)
	}
	if out := mustRun(t, "", "ls"); !strings.Contains(out, "current:   relay gpt-5") || strings.Contains(out, "current:   relay cpa/") {
		t.Fatalf("relay target was presented as Codex's selected model: %s", out)
	}
	out := mustRun(t, "", "relay", "recover")
	if !strings.Contains(out, "已恢复异常退出") || safekeep.Touched("codex") {
		t.Fatalf("recover: %s", out)
	}
	if out := mustRun(t, "", "relay", "recover"); !strings.Contains(out, "没有需要恢复") {
		t.Fatal(out)
	}
}
