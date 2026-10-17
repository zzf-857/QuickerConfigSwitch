package core

import (
	"strings"
	"testing"
)

func TestRedactCustomHeadersFromOriginalConfig(t *testing.T) {
	setup(t)
	for _, input := range []string{
		`-    "ANTHROPIC_CUSTOM_HEADERS": "Authorization: Bearer original-login\nX-Api-Key: original-api"`,
		`+    "ANTHROPIC_CUSTOM_HEADERS": "X-Private: a\"quoted\"value\nCookie: session=fixture"`,
		`ANTHROPIC_CUSTOM_HEADERS = "X-Token: x"`,
	} {
		got := Redact(input)
		if !strings.Contains(got, `"****"`) || strings.Contains(got, "original-") || strings.Contains(got, "quoted") || strings.Contains(got, "fixture") || strings.Contains(got, "X-Token: x") {
			t.Fatalf("custom header credentials visible in %q", got)
		}
	}
	const empty = `+    "ANTHROPIC_CUSTOM_HEADERS": ""`
	if Redact(empty) != empty {
		t.Fatal("empty custom headers were changed")
	}
	const ordinary = `"model": "claude-opus-4.6"`
	if Redact(ordinary) != ordinary {
		t.Fatal("unrelated config was masked")
	}
}
