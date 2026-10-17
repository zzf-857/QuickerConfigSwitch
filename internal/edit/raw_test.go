package edit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

const rawFixture = `# my codex config
model = "gpt-5.5" # the one I like
model_reasoning_effort = "high"
notify = [
  "say",
  "done",
]

[features]
fast_mode = true
web_search = false # off

# CC Switch's provider
[model_providers.custom]
name = "custom"
base_url = "https://example.com/v1"

[model_providers.custom.http_headers]
X-Foo = "bar"

[profiles.work]
model = "o3"
`

func writeRaw(t *testing.T, name, text string) string {
	t.Helper()
	p := filepath.Join(testenv.Home(t), name)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// variants are the fixture as saved by different tools.
func rawVariants() map[string]func(string) string {
	return map[string]func(string) string{
		"lf":   func(s string) string { return s },
		"crlf": func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") },
		"bom":  func(s string) string { return "\xef\xbb\xbf" + s },
		"bom+crlf": func(s string) string {
			return "\xef\xbb\xbf" + strings.ReplaceAll(s, "\n", "\r\n")
		},
	}
}

func TestGetTOMLRaw(t *testing.T) {
	for name, v := range rawVariants() {
		t.Run(name, func(t *testing.T) {
			p := writeRaw(t, "config.toml", v(rawFixture))
			cases := []struct {
				got  func() (string, bool)
				want string
				ok   bool
			}{
				{func() (string, bool) { return GetTOMLTopRaw(p, "model") }, `"gpt-5.5"`, true},
				{func() (string, bool) { return GetTOMLTopRaw(p, "notify") }, "[\n  \"say\",\n  \"done\",\n]", true},
				{func() (string, bool) { return GetTOMLTopRaw(p, "missing") }, "", false},
				{func() (string, bool) { return GetTOMLTopRaw(p, "fast_mode") }, "", false},
				{func() (string, bool) { return GetTOMLKeyRaw(p, "features", "web_search") }, "false", true},
				{func() (string, bool) { return GetTOMLKeyRaw(p, "features", "nope") }, "", false},
				{func() (string, bool) { return GetTOMLKeyRaw(p, "nope", "x") }, "", false},
				{func() (string, bool) { return GetTOMLTableBlock(p, "features") }, "[features]\nfast_mode = true\nweb_search = false # off", true},
				{func() (string, bool) { return GetTOMLTableBlock(p, "model_providers.custom") }, "[model_providers.custom]\nname = \"custom\"\nbase_url = \"https://example.com/v1\"", true},
				{func() (string, bool) { return GetTOMLTableBlock(p, "profiles.work") }, "[profiles.work]\nmodel = \"o3\"", true},
				{func() (string, bool) { return GetTOMLTableBlock(p, "model_providers") }, "", false},
			}
			for i, c := range cases {
				got, ok := c.got()
				if got != c.want || ok != c.ok {
					t.Errorf("case %d: got %q %v, want %q %v", i, got, ok, c.want, c.ok)
				}
			}
		})
	}
}

func TestTOMLKeyRawRecordedText(t *testing.T) {
	for name, variant := range rawVariants() {
		t.Run(name, func(t *testing.T) {
			raw := variant("[provider]\ntoken = '''line one\nline two''' # comment\nother = [1, 2]\n")
			got, ok, err := TOMLKeyRaw(raw, "provider", "token")
			if err != nil || !ok || got != "'''line one\nline two'''" {
				t.Fatalf("raw token was not preserved: %q %v %v", got, ok, err)
			}
			if _, ok, err := TOMLKeyRaw(raw, "provider", "missing"); err != nil || ok {
				t.Fatalf("missing key: %v %v", ok, err)
			}
		})
	}
	if _, _, err := TOMLKeyRaw("[provider]\ntoken = 'unterminated", "provider", "token"); err == nil {
		t.Fatal("invalid recorded TOML treated as a missing key")
	}
}

// What is read raw and put back leaves the file byte for byte as it was,
// whatever its line ends and byte order mark.
func TestRawRoundTrip(t *testing.T) {
	for name, v := range rawVariants() {
		t.Run(name, func(t *testing.T) {
			orig := v(rawFixture)
			p := writeRaw(t, "config.toml", orig)
			model, _ := GetTOMLTopRaw(p, "model")
			notify, _ := GetTOMLTopRaw(p, "notify")
			ws, _ := GetTOMLKeyRaw(p, "features", "web_search")
			features, _ := GetTOMLTableBlock(p, "features")
			custom, _ := GetTOMLTableBlock(p, "model_providers.custom")

			must(t, SetTOMLTopPreserving(p, KV{"model", "other"}, KV{"notify", Raw(`["x"]`)}))
			must(t, SetTOMLKey(p, "features", "web_search", true))
			must(t, PutTOMLTableBlock(p, "model_providers.custom", "[model_providers.custom]\nname = \"changed\""))
			must(t, PutTOMLTableBlock(p, "features", ""))
			must(t, PutTOMLTableBlock(p, "model_providers.qcs", "[model_providers.qcs]\nname = \"qcs\""))
			if got, _ := GetTOMLTableBlock(p, "model_providers.qcs"); got != "[model_providers.qcs]\nname = \"qcs\"" {
				t.Fatalf("added block: %q", got)
			}

			must(t, PutTOMLTableBlock(p, "model_providers.qcs", ""))
			must(t, PutTOMLTableBlock(p, "model_providers.custom", custom))
			must(t, SetTOMLTopPreserving(p, KV{"model", Raw(model)}, KV{"notify", Raw(notify)}))
			// the table is gone, so it comes back at the end: the order is
			// not the original's, which is what a snapshot is for
			must(t, PutTOMLTableBlock(p, "features", features))
			must(t, SetTOMLKey(p, "features", "web_search", Raw(ws)))
			b, _ := os.ReadFile(p)
			for _, want := range []string{"# the one I like", "[features]", "web_search = false # off", "\"say\","} {
				if !strings.Contains(string(b), want) {
					t.Errorf("lost %q:\n%s", want, b)
				}
			}
			if strings.Contains(orig, "\r\n") && strings.Count(string(b), "\n") != strings.Count(string(b), "\r\n") {
				t.Errorf("mixed line ends:\n%q", b)
			}
			if strings.HasPrefix(orig, "\xef\xbb\xbf") != strings.HasPrefix(string(b), "\xef\xbb\xbf") {
				t.Errorf("byte order mark not kept: %q", b[:8])
			}

			// replaced in place, the file is byte for byte the original
			p2 := writeRaw(t, "two.toml", orig)
			must(t, PutTOMLTableBlock(p2, "model_providers.custom", "[model_providers.custom]\nname = \"changed\"\n"))
			must(t, SetTOMLTopPreserving(p2, KV{"model", "other"}))
			must(t, PutTOMLTableBlock(p2, "model_providers.custom", custom))
			must(t, SetTOMLTopPreserving(p2, KV{"model", Raw(model)}))
			if b, _ := os.ReadFile(p2); string(b) != orig {
				t.Errorf("not the original:\n%q\nwant\n%q", b, orig)
			}
		})
	}
}

func TestPutTOMLTableBlock(t *testing.T) {
	cases := []struct {
		name, in, table, block, want string
		err                          bool
	}{
		{"append to empty", "", "a", "[a]\nx = 1", "[a]\nx = 1\n", false},
		{"append", "k = 1\n", "a", "[a]\nx = 1", "k = 1\n\n[a]\nx = 1\n", false},
		{"remove last", "k = 1\n\n[a]\nx = 1\n", "a", "", "k = 1\n", false},
		{"remove only", "[a]\nx = 1\n", "a", "", "\n", false},
		{"remove middle keeps next comment", "[a]\nx = 1\n\n# about b\n[b]\ny = 2\n", "a", "", "# about b\n[b]\ny = 2\n", false},
		{"remove between", "[z]\nq = 0\n\n[a]\nx = 1\n\n[b]\ny = 2\n", "a", "", "[z]\nq = 0\n\n[b]\ny = 2\n", false},
		{"remove keeps child", "[a]\nx = 1\n\n[a.b]\ny = 2\n", "a", "", "[a.b]\ny = 2\n", false},
		{"remove absent", "k = 1\n", "a", "", "k = 1\n", false},
		{"replace keeps child", "[a]\nx = 1\n\n[a.b]\ny = 2\n", "a", "[a]\nx = 3\nz = 4", "[a]\nx = 3\nz = 4\n\n[a.b]\ny = 2\n", false},
		{"wrong table", "", "a", "[b]\nx = 1", "", true},
		{"two tables", "", "a", "[a]\n[b]", "", true},
		{"top key", "", "a", "k = 1\n[a]", "", true},
		{"invalid", "", "a", "[a]\nx = ", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeRaw(t, "c.toml", c.in)
			err := PutTOMLTableBlock(p, c.table, c.block)
			if c.err {
				if err == nil {
					t.Fatal("no error")
				}
				if b, _ := os.ReadFile(p); string(b) != c.in {
					t.Fatalf("changed on error: %q", b)
				}
				return
			}
			must(t, err)
			if b, _ := os.ReadFile(p); string(b) != c.want {
				t.Errorf("got %q, want %q", b, c.want)
			}
		})
	}
}

func TestDelTOMLTableKey(t *testing.T) {
	p := writeRaw(t, "c.toml", "[features]\r\nfast_mode = true # mine\r\n\r\n[b]\r\nx = 1\r\n")
	must(t, DelTOMLTableKey(p, "features", "fast_mode"))
	if b, _ := os.ReadFile(p); string(b) != "[features]\r\n\r\n[b]\r\nx = 1\r\n" {
		t.Errorf("got %q", b)
	}
	must(t, DelTOMLTableKey(p, "features", "absent"))
	must(t, DelTOMLTableKey(p, "nope", "absent"))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
