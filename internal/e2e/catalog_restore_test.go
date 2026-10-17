package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/agent/codex"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func TestCodexPreservesOriginalCustomCatalog(t *testing.T) {
	home, _ := setup(t)
	cfg := filepath.Join(home, "config.toml")
	auth := filepath.Join(home, "auth.json")
	originalCatalog := filepath.Join(home, "original-models.json")
	const catalog = `{"models":[{"slug":"other-model","original_note":"keep me"}]}`
	write(t, originalCatalog, catalog)
	before, err := os.Stat(originalCatalog)
	if err != nil {
		t.Fatal(err)
	}
	original := strings.Replace(codexConfig, "[features]", fmt.Sprintf("model_catalog_json = %q\r\n[features]", originalCatalog), 1)
	write(t, cfg, original)
	write(t, auth, authJSON)
	p, _, _ := provider.Get("cpa")
	p.Models = []provider.Model{{ID: "gpt-5.5", Fast: true}}
	if err := provider.Update(p); err != nil {
		t.Fatal(err)
	}
	cx, _ := agent.Get("codex")
	use(t, cx, agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"})
	m := codexReads(t, cfg)
	if m["model_catalog_json"] != codex.CatalogPath("cpa") || m["service_tier"] != "fast" || !fastTier(t, codex.CatalogPath("cpa"), "gpt-5.5") {
		t.Fatal("original catalog hid the selected provider model or Fast")
	}
	if _, err := safekeep.Restore("codex"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(read(t, cfg), original) || read(t, auth) != authJSON || read(t, originalCatalog) != catalog {
		t.Fatal("original config, login or catalog was not preserved")
	}
	after, err := os.Stat(originalCatalog)
	if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("original catalog identity/permissions/mtime changed: %v", err)
	}
	if _, err := os.Stat(codex.CatalogPath("cpa")); !os.IsNotExist(err) {
		t.Fatalf("generated provider catalog not removed on restore: %v", err)
	}
}
