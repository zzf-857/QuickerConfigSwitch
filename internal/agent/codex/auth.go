package codex

import (
	"fmt"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

// checkDirectAuth protects the credential boundary when a qcs table already
// exists. Its unrelated user keys are kept, so conflicting auth must be
// rejected rather than silently deleted or forwarded to the new endpoint.
//
// Codex model-provider/src/auth.rs bearer_auth_for_provider resolves env_key
// before experimental_bearer_token; requires_openai_auth or command auth can
// fall back to ambient login. model-provider-info build_header_map applies
// env_http_headers after static http_headers (openai/codex ff6aec96).
func checkDirectAuth(table string, existing map[string]any, p provider.Provider) error {
	conflict := func(key string) error {
		return fmt.Errorf("codex: existing model_providers.%s sets %s, which conflicts with provider credential isolation; remove that override or use a separate provider id before switching", table, key)
	}
	if value, exists := existing["requires_openai_auth"]; exists {
		if enabled, valid := value.(bool); !valid || enabled {
			return conflict("requires_openai_auth")
		}
	}
	for _, key := range []string{"env_key", "auth", "gateway_oauth", "aws"} {
		if _, exists := existing[key]; exists {
			return conflict(key)
		}
	}
	if value, exists := existing["experimental_bearer_token"]; exists && p.APIKey == "" {
		if token, valid := value.(string); !valid || token != "" {
			return conflict("experimental_bearer_token")
		}
	}
	// An inline map is replaced as a whole when a provider supplies headers.
	// A child table is merged instead, so omitted keys would otherwise keep
	// sending an old credential to the newly selected endpoint. TOML keys
	// are case-sensitive while HTTP names are not: differently cased keys
	// would coexist and Codex could choose either credential when building
	// its HTTP HeaderMap. Require an exact replacement of each owned name.
	_, child := edit.GetTOMLTableBlock(ConfigPath(), "model_providers."+table+".http_headers")
	if raw, exists := existing["http_headers"]; exists && (child || len(p.Headers) == 0) {
		headers, valid := raw.(map[string]any)
		if !valid {
			return conflict("http_headers")
		}
		for name := range headers {
			if credentialHeader(name) {
				if _, replaced := p.Headers[name]; !replaced {
					return conflict("http_headers")
				}
			}
		}
	}
	if raw, exists := existing["env_http_headers"]; exists {
		headers, valid := raw.(map[string]any)
		if !valid {
			return conflict("env_http_headers")
		}
		for name, value := range headers {
			if _, valid := value.(string); !valid || credentialHeader(name) {
				return conflict("env_http_headers")
			}
			for explicit := range p.Headers {
				if strings.EqualFold(name, explicit) {
					return conflict("env_http_headers")
				}
			}
		}
	}
	return nil
}

func credentialHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "cookie", "x-api-key", "api-key",
		"chatgpt-account-id", "openai-account-id", "openai-organization", "openai-project", "x-openai-actor-authorization":
		return true
	}
	return false
}
