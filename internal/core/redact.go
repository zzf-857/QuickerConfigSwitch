package core

import (
	"regexp"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

// A preview's diff quotes config lines, and those hold API keys (Codex's
// experimental_bearer_token, Claude's ANTHROPIC_AUTH_TOKEN, a provider's
// secret headers, or a key the user had before). maskText masks them:
// every stored provider secret, any quoted value of a key-like name, and
// anything shaped like an "sk-" key.
var (
	secretAssign = regexp.MustCompile(`(?i)((?:token|key|secret|auth\w*|password|bearer)"?\s*[=:]\s*")([^"\\\n]{4,})(")`)
	skLike       = regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{8,}`)
)

// Redact masks API keys in text shown to the user: previews, notices and
// errors, in the page and the CLI alike.
func Redact(s string) string {
	if s == "" {
		return s
	}
	if ps, err := provider.List(); err == nil {
		for _, p := range ps {
			if len(p.APIKey) >= 4 {
				s = strings.ReplaceAll(s, p.APIKey, provider.MaskKey(p.APIKey))
			}
			for k, v := range p.Headers {
				if SecretHeader(k) && len(v) >= 4 {
					s = strings.ReplaceAll(s, v, provider.MaskKey(v))
				}
			}
		}
	}
	s = secretAssign.ReplaceAllStringFunc(s, func(m string) string {
		g := secretAssign.FindStringSubmatch(m)
		if strings.Contains(g[2], "…") || g[2] == "****" {
			return m // masked already
		}
		return g[1] + provider.MaskKey(g[2]) + g[3]
	})
	return skLike.ReplaceAllStringFunc(s, func(k string) string {
		if strings.Contains(k, "…") {
			return k
		}
		return provider.MaskKey(k)
	})
}

// SecretHeader reports whether a header's value is a secret by its name.
func SecretHeader(name string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"auth", "key", "token", "secret", "cookie", "password"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}
