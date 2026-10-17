package edit

import (
	"os"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestSetTOMLKeyQuotedKeepsDottedHeaderScalar(t *testing.T) {
	const input = `# user configuration
[model_providers.qcs-cpa]
name = "CPA" # parent stays

[model_providers.qcs-cpa.http_headers] # header table stays
"X.Existing" = "old"  # existing spelling stays
X-Keep = "mine"

# sibling stays
[model_providers.other]
http_headers = { "X.User" = "mine" }
`
	p := tmpFile(t, "config.toml", input)
	const table = "model_providers.qcs-cpa.http_headers"
	for range 2 {
		if err := SetTOMLKeyQuoted(p, table, "X.Existing", "new"); err != nil {
			t.Fatal(err)
		}
		if err := SetTOMLKeyQuoted(p, table, "X.New.Header", "added"); err != nil {
			t.Fatal(err)
		}
	}
	want := strings.Replace(input, `"old"`, `"new"`, 1)
	want = strings.Replace(want, "X-Keep = \"mine\"\n", "X-Keep = \"mine\"\n\"X.New.Header\" = \"added\"\n", 1)
	assertTOMLContent(t, p, want)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Providers map[string]struct {
			Headers map[string]string `toml:"http_headers"`
		} `toml:"model_providers"`
	}
	if err := toml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	headers := document.Providers["qcs-cpa"].Headers
	if len(headers) != 3 || headers["X.Existing"] != "new" || headers["X.New.Header"] != "added" || headers["X-Keep"] != "mine" {
		t.Fatalf("header keys = %#v", headers)
	}
}

func TestSetTOMLKeyQuotedAppendsMissingTable(t *testing.T) {
	p := tmpFile(t, "config.toml", "# keep\nmodel = \"mine\"\n")
	if err := SetTOMLKeyQuoted(p, "provider.http_headers", "X.Header", "value"); err != nil {
		t.Fatal(err)
	}
	assertTOMLContent(t, p, "# keep\nmodel = \"mine\"\n\n[provider.http_headers]\n\"X.Header\" = \"value\"\n")
}
