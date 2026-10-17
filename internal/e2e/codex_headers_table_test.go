package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/agent/codex"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func TestCodexDirectExplicitHeaderTable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		crlf  bool
		dirty bool
	}{
		{name: "LF"},
		{name: "BOM-CRLF", crlf: true},
		{name: "user-added-header", dirty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := setup(t)
			cfg := filepath.Join(home, "config.toml")
			auth := filepath.Join(home, "auth.json")
			const fixture = `# original config
model = "gpt-5.5"
service_tier = "fast"

[features]
fast_mode = true

[model_providers.qcs-cpa]
name = "User CPA" # keep name comment
base_url = "https://user.example/v1"
wire_api = "responses"
experimental_bearer_token = "user-original" # keep token comment
request_max_retries = 7 # keep retry comment

[model_providers.qcs-cpa.http_headers] # keep explicit table
Authorization = "Bearer user-original" # keep auth comment
"X.Trace" = "user-trace" # keep dotted header comment
X-User = "mine" # keep unrelated header comment

[model_providers.other]
name = "Other" # keep sibling
`
			original := fixture
			if tc.crlf {
				original = "\ufeff" + strings.ReplaceAll(original, "\n", "\r\n")
			}
			write(t, cfg, original)
			write(t, auth, authJSON)
			p, _, err := provider.Get("cpa")
			if err != nil {
				t.Fatal(err)
			}
			p.Headers = map[string]string{
				"Authorization": "Bearer provider-only", "X-Api-Key": "provider-key", "X.Trace": "provider-trace",
			}
			if err := provider.Update(p); err != nil {
				t.Fatal(err)
			}
			cx, _ := agent.Get("codex")
			target := agent.Target{Mode: agent.Direct, Provider: "cpa", Model: "gpt-5.5"}
			planned, err := cx.Plan(target)
			if err != nil {
				t.Fatal(err)
			}
			if diff, err := safekeep.Preview(planned); err != nil || diff == "" {
				t.Fatalf("preview = %q, %v", diff, err)
			}
			if read(t, cfg) != original || read(t, auth) != authJSON || safekeep.Touched("codex") {
				t.Fatal("preview changed user files or management state")
			}
			if _, err := os.Stat(codex.CatalogPath("cpa")); !os.IsNotExist(err) {
				t.Fatalf("preview left a generated catalog: %v", err)
			}

			use(t, cx, target)
			doc := codexReads(t, cfg)
			table := doc["model_providers"].(map[string]any)["qcs-cpa"].(map[string]any)
			headers := table["http_headers"].(map[string]any)
			if doc["service_tier"] != "fast" || table["experimental_bearer_token"] != "sk-cpa" || table["request_max_retries"] != int64(7) {
				t.Fatalf("provider or original settings were not preserved: %#v", table)
			}
			if len(headers) != 4 || headers["Authorization"] != "Bearer provider-only" || headers["X-Api-Key"] != "provider-key" || headers["X.Trace"] != "provider-trace" || headers["X-User"] != "mine" {
				t.Fatalf("header table = %#v", headers)
			}
			written := read(t, cfg)
			if strings.Count(written, "[model_providers.qcs-cpa.http_headers]") != 1 || strings.Contains(written, "http_headers =") {
				t.Fatal("explicit header table was duplicated or replaced with an inline table")
			}
			for _, kept := range []string{"# keep name comment", "# keep token comment", "# keep retry comment", "# keep auth comment", "# keep dotted header comment", "# keep unrelated header comment", "# keep sibling"} {
				if !strings.Contains(written, kept) {
					t.Fatalf("switch lost %s", kept)
				}
			}
			if read(t, auth) != authJSON {
				t.Fatal("direct switch changed auth.json")
			}
			if tc.dirty {
				written = strings.Replace(written, "X-User = \"mine\" # keep unrelated header comment\n", "X-User = \"mine\" # keep unrelated header comment\n\"X.User.Added\" = \"later\" # keep later header\n", 1)
				write(t, cfg, written)
			}
			if _, err := safekeep.Restore("codex"); err != nil {
				t.Fatal(err)
			}
			if read(t, auth) != authJSON {
				t.Fatal("restore changed auth.json")
			}
			if !tc.dirty {
				if got := read(t, cfg); got != original {
					t.Fatalf("original config not restored byte for byte:\n%q", got)
				}
				return
			}
			restored := codexReads(t, cfg)
			table = restored["model_providers"].(map[string]any)["qcs-cpa"].(map[string]any)
			headers = table["http_headers"].(map[string]any)
			if len(headers) != 4 || headers["Authorization"] != "Bearer user-original" || headers["X.Trace"] != "user-trace" || headers["X-User"] != "mine" || headers["X.User.Added"] != "later" {
				t.Fatalf("restore lost original/later headers or retained provider key: %#v", headers)
			}
			if table["experimental_bearer_token"] != "user-original" || restored["service_tier"] != "fast" {
				t.Fatalf("original credential/settings not restored: %#v", table)
			}
			if !strings.Contains(read(t, cfg), "# keep later header") {
				t.Fatal("later user header comment lost")
			}
		})
	}
}
