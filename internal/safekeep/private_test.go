package safekeep

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
)

func TestNewConfigurationFilesArePrivate(t *testing.T) {
	const secret = "fixture-provider-secret"
	for _, format := range []plan.Format{plan.TOML, plan.JSON, plan.Raw} {
		for _, existing := range []bool{false, true} {
			name := string(format) + "/new"
			if existing {
				name = string(format) + "/existing"
			}
			t.Run(name, func(t *testing.T) {
				e := setup(t)
				path := filepath.Join(e.home, "private", "config")
				original := "# original\nuser = true\n"
				op := plan.Op{Kind: plan.SetTop, Key: "api_key", Value: secret}
				if format == plan.JSON {
					original = "{\"original\":true}\n"
					op = plan.Op{Kind: plan.SetJSON, Key: "apiKey", Value: secret}
				} else if format == plan.Raw {
					original = "original raw data"
					op = plan.Op{Kind: plan.WriteFile, Data: []byte(secret)}
				}
				wantMode := os.FileMode(0o600)
				if existing {
					write(t, path, original)
					wantMode = 0o640
					must(t, os.Chmod(path, wantMode))
				}
				p := &plan.Plan{Agent: "private", Edits: []plan.FileEdit{{Path: path, Format: format, Ops: []plan.Op{op}}}}
				writes := 0
				must(t, edit.Filtered(func(written string, data []byte) []byte {
					if written == path && bytes.Contains(data, []byte(secret)) {
						writes++
						assertFileMode(t, path, wantMode)
					}
					return data
				}, func() error { return Apply(p) }))
				if writes == 0 {
					t.Fatal("did not observe a secret-bearing write")
				}
				assertFileMode(t, path, wantMode)
				_, err := Restore("private")
				must(t, err)
				if existing {
					if read(t, path) != original {
						t.Fatal("original configuration was not restored")
					}
					assertFileMode(t, path, wantMode)
				} else {
					absent(t, path)
				}
			})
		}
	}
}

// Clean restoration of an originally absent file can re-add a kept provider
// before revoking its credentials. That write must be private as well.
func TestNewKeptConfigurationStaysPrivateOnRestore(t *testing.T) {
	e := setup(t)
	p := planA(e, true)
	writes := 0
	must(t, edit.Filtered(func(path string, data []byte) []byte {
		if path == e.cfg && bytes.Contains(data, []byte("sk-cpa")) {
			writes++
			assertFileMode(t, e.cfg, 0o600)
		}
		return data
	}, func() error {
		if err := Apply(p); err != nil {
			return err
		}
		_, err := Restore("codex")
		return err
	}))
	if writes < 2 {
		t.Fatal("did not observe both the initial and kept-provider writes")
	}
	assertFileMode(t, e.cfg, 0o600)
	assertKeptToken(t, e.cfg, "")
}

func TestFailedApplyRemovesPrivatePlaceholders(t *testing.T) {
	e := setup(t)
	generated := filepath.Join(e.home, "generated-secret")
	p := &plan.Plan{Agent: "private", Edits: []plan.FileEdit{
		{Path: generated, Format: plan.Raw, Ops: []plan.Op{{Kind: plan.WriteFile, Data: []byte("fixture-secret")}}},
		{Path: e.cfg, Format: plan.TOML, Ops: []plan.Op{{Kind: plan.SetTop, Key: "api_key", Value: edit.Raw(`"unterminated`)}}},
	}}
	if err := Apply(p); err == nil {
		t.Fatal("invalid apply unexpectedly succeeded")
	}
	absent(t, generated)
	absent(t, e.cfg)
	absent(t, stashPath())
	absent(t, appliedPath())
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	must(t, err)
	if runtime.GOOS != "windows" && info.Mode().Perm() != want {
		t.Fatalf("%s: mode %o, want %o", path, info.Mode().Perm(), want)
	}
}
