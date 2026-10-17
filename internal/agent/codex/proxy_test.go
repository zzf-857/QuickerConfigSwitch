package codex

import (
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

// Cases adapted from Magpie TestCodexProxyFor/TestCodexProxyNamesItsVariable.
// The explicit map keeps tests independent of the cloud or developer proxy.
func TestEnvironmentProxy(t *testing.T) {
	for _, tc := range []struct {
		name, base string
		env        map[string]string
		from       string
	}{
		{"none", "http://127.0.0.1:17321", nil, ""},
		{"HTTP proxy", "http://127.0.0.1:17321", map[string]string{"HTTP_PROXY": "http://proxy.example:80"}, "HTTP_PROXY"},
		{"uppercase first", "http://127.0.0.1:17321", map[string]string{"HTTP_PROXY": "http://upper.example", "http_proxy": "http://lower.example", "ALL_PROXY": "http://all.example"}, "HTTP_PROXY"},
		{"lowercase", "http://127.0.0.1:17321", map[string]string{"http_proxy": "http://proxy.example"}, "http_proxy"},
		{"all fallback", "http://127.0.0.1:17321", map[string]string{"all_proxy": "socks5://proxy.example:1080"}, "all_proxy"},
		{"HTTPS ignored for HTTP", "http://127.0.0.1:17321", map[string]string{"HTTPS_PROXY": "http://proxy.example"}, ""},
		{"HTTPS selected", "https://127.0.0.1", map[string]string{"HTTP_PROXY": "http://wrong.example", "HTTPS_PROXY": "http://proxy.example"}, "HTTPS_PROXY"},
		{"IP bypass", "http://127.0.0.1:17321", map[string]string{"HTTP_PROXY": "http://proxy.example", "NO_PROXY": "localhost,127.0.0.1"}, ""},
		{"CIDR bypass", "http://127.0.0.1:17321", map[string]string{"HTTP_PROXY": "http://proxy.example", "NO_PROXY": "127.0.0.0/8"}, ""},
		{"localhost differs", "http://127.0.0.1:17321", map[string]string{"HTTP_PROXY": "http://proxy.example", "NO_PROXY": "localhost"}, "HTTP_PROXY"},
		{"wildcard differs", "http://127.0.0.1:17321", map[string]string{"HTTP_PROXY": "http://proxy.example", "NO_PROXY": "127.*"}, "HTTP_PROXY"},
		{"star names only", "http://127.0.0.1:17321", map[string]string{"HTTP_PROXY": "http://proxy.example", "NO_PROXY": "*"}, "HTTP_PROXY"},
		{"lowercase bypass", "http://127.0.0.1:17321", map[string]string{"HTTP_PROXY": "http://proxy.example", "no_proxy": "127.0.0.1"}, ""},
		{"uppercase bypass wins", "http://127.0.0.1:17321", map[string]string{"HTTP_PROXY": "http://proxy.example", "NO_PROXY": "localhost", "no_proxy": "127.0.0.1"}, "HTTP_PROXY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy, from := environmentProxy(tc.base, func(key string) string { return tc.env[key] })
			if from != tc.from || proxy != tc.env[tc.from] {
				t.Fatalf("proxy=%q from=%q, want %q", proxy, from, tc.from)
			}
		})
	}
}

func TestReqwestNoProxy(t *testing.T) {
	for _, tc := range []struct {
		host, list string
		want       bool
	}{
		{"127.0.0.1", "127.0.0.1", true},
		{"127.0.0.1", "127.0.0.0/8", true},
		{"127.0.0.1", "localhost,127.*,*.local,*", false},
		{"[::1]", "::1", true},
		{"::1", "::1/128", true},
		{"::1", "127.0.0.0/8", false},
		{"localhost", "localhost", true},
		{"localhost", "*", true},
		{"localhost", "127.0.0.1", false},
		{"api.Example.com", " .example.com ", true},
		{"notexample.com", "example.com", false},
	} {
		if got := reqwestNoProxy(tc.host, tc.list); got != tc.want {
			t.Errorf("host=%q list=%q got=%v want=%v", tc.host, tc.list, got, tc.want)
		}
	}
}

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "OPENAI_BASE_URL"} {
		t.Setenv(key, "")
	}
}

func TestDoctorLoopbackProxy(t *testing.T) {
	testenv.Home(t)
	clearProxyEnv(t)
	t.Setenv("http_proxy", "http://fixture-user:fixture-password@proxy.example:7890/private?token=fixture-query")
	writeFile(t, ConfigPath(), "openai_base_url = \"http://127.0.0.1:17321/backend-api/codex\"\n")
	problems := New().Doctor()
	if len(problems) != 1 || problems[0].Level != "warn" || !strings.Contains(problems[0].Message, "http_proxy at http://proxy.example:7890") || !strings.Contains(problems[0].Message, "NO_PROXY") {
		t.Fatalf("missing scoped proxy diagnosis: %+v", problems)
	}
	for _, secret := range []string{"fixture-user", "fixture-password", "fixture-query", "/private"} {
		if strings.Contains(problems[0].Message, secret) {
			t.Fatalf("proxy diagnosis exposed %s", secret)
		}
	}
	t.Setenv("NO_PROXY", "127.0.0.0/8")
	if problems := New().Doctor(); len(problems) != 0 {
		t.Fatalf("diagnosed bypassed loopback: %+v", problems)
	}
	t.Setenv("NO_PROXY", "")
	writeFile(t, ConfigPath(), "openai_base_url = \"http://127.0.0.1:17321/backend-api/codex\"\n[features]\nrespect_system_proxy = true\n")
	if problems := New().Doctor(); len(problems) != 1 || problems[0].Level != "info" || !strings.Contains(problems[0].Message, "cannot verify system proxy") {
		t.Fatalf("environment rule asserted over system proxy: %+v", problems)
	}
	writeFile(t, ConfigPath(), "openai_base_url = \"https://chatgpt.com/backend-api/codex\"\n")
	if problems := New().Doctor(); len(problems) != 0 {
		t.Fatalf("nonlocal official endpoint diagnosed as loopback: %+v", problems)
	}
	writeFile(t, ConfigPath(), "model_provider = \"qcs-cpa\"\n[model_providers.qcs-cpa]\nname = \"CPA\"\nbase_url = \"http://localhost:8317/v1\"\n")
	if problems := New().Doctor(); len(problems) != 1 || !strings.Contains(problems[0].Message, "add localhost to NO_PROXY") {
		t.Fatalf("local direct provider omitted: %+v", problems)
	}
}

func TestDetectDesktopWithoutCLIOrConfig(t *testing.T) {
	testenv.Home(t)
	t.Setenv("PATH", t.TempDir())
	was := desktopInstalled
	desktopInstalled = func(id string) bool { return id == "codex" }
	t.Cleanup(func() { desktopInstalled = was })
	if detection := New().Detect(); !detection.Installed || detection.SignedIn {
		t.Fatalf("desktop without CLI/config not detected: %+v", detection)
	}
	desktopInstalled = func(string) bool { return false }
	if detection := New().Detect(); detection.Installed {
		t.Fatalf("empty home and PATH detected as installed: %+v", detection)
	}
}
