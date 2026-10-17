package safekeep

import (
	"slices"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
)

const keptHeaders = "http_headers"
const originalHeaders = "{ Authorization = 'user-before', 'X-User' = 'keep' }"

func planWithHeaders(e env) *plan.Plan {
	p := planA(e, true)
	op := &p.Edits[0].Ops[3]
	op.RestoreKeys = append(op.RestoreKeys, keptHeaders)
	op.KVs = append(op.KVs, edit.KV{Path: keptHeaders, Value: edit.Inline{
		{Path: "Authorization", Value: "qcs-authorization"},
		{Path: "X-Api-Key", Value: "qcs-api-key"},
		{Path: "Cookie", Value: "qcs-cookie"},
	}})
	return p
}

// Both earlier record formats can explicitly own headers: the original
// Keep-only policy and the later one that restored only bearer tokens.
func headerRecordVersion(t *testing.T, e env, version string) {
	t.Helper()
	if version == "current" {
		return
	}
	s, err := load()
	must(t, err)
	wr := s.applied.Agents["codex"].Files[e.cfg].Slots["toml-table:"+keptTable]
	o := s.stash.Agents["codex"][e.cfg].Slots["toml-table:"+keptTable]
	if version == "keep only" {
		wr.RestoreKeys, o.Restore = nil, nil
	} else {
		wr.RestoreKeys = []string{keptToken}
		delete(o.Restore, keptHeaders)
	}
	must(t, s.saveApplied())
	must(t, s.saveStash())
}

func assertHeaders(t *testing.T, path, table, want string) {
	t.Helper()
	got, ok := edit.GetTOMLKeyRaw(path, table, keptHeaders)
	if got != want || ok != (want != "") {
		t.Fatal("provider headers were not restored as expected")
	}
}

func TestKeptHeadersRestore(t *testing.T) {
	for _, version := range []string{"current", "keep only", "token only"} {
		for _, tc := range []struct {
			name, original, changed, want string
			dirty, left, noToken          bool
		}{
			{name: "added"},
			{name: "header auth only", noToken: true},
			{name: "unrelated edit", dirty: true},
			{name: "original", original: originalHeaders, want: originalHeaders},
			{name: "original dirty", original: originalHeaders, want: originalHeaders, dirty: true},
			{name: "changed headers", changed: "{ Authorization = 'user-after' }", want: "{ Authorization = 'user-after' }", left: true},
			{name: "removed headers", changed: "remove", left: true},
		} {
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				e := setup(t)
				orig, wantToken := codexConfig, ""
				if tc.original != "" {
					wantToken = "'original-token'"
					orig += "\n[" + keptTable + "]\n" + keptHeaders + " = " + tc.original + " # original\n" + keptToken + " = " + wantToken + "\n"
				}
				write(t, e.cfg, orig)
				p := planWithHeaders(e)
				if tc.noToken {
					op := &p.Edits[0].Ops[3]
					op.KVs = slices.DeleteFunc(op.KVs, func(kv edit.KV) bool { return kv.Path == keptToken })
				}
				must(t, Apply(p))
				headerRecordVersion(t, e, version)
				if tc.dirty {
					must(t, edit.SetTOMLKey(e.cfg, keptTable, "request_max_retries", 7))
				}
				switch tc.changed {
				case "":
				case "remove":
					must(t, edit.DelTOMLTableKey(e.cfg, keptTable, keptHeaders))
				default:
					must(t, edit.SetTOMLKey(e.cfg, keptTable, keptHeaders, edit.Raw(tc.changed)))
				}
				rep, err := Restore("codex")
				must(t, err)
				assertKeptToken(t, e.cfg, wantToken)
				assertHeaders(t, e.cfg, keptTable, tc.want)
				var left []string
				for _, f := range rep.Files {
					left = append(left, f.Left...)
				}
				if got := slices.Contains(left, "toml-key:"+keptTable+":"+keptHeaders); got != tc.left {
					t.Errorf("headers left %v, want %v", got, tc.left)
				}
				if tc.dirty {
					if got, _ := edit.GetTOMLKeyRaw(e.cfg, keptTable, "request_max_retries"); got != "7" {
						t.Error("user's provider setting was lost")
					}
				} else if tc.original != "" && tc.changed == "" && read(t, e.cfg) != orig {
					t.Error("original credential spelling or comment was changed")
				}
			})
		}
	}
}

func TestKeptHeadersProviderSwitch(t *testing.T) {
	for _, version := range []string{"current", "keep only", "token only"} {
		t.Run(version, func(t *testing.T) {
			e := setup(t)
			write(t, e.cfg, codexConfig)
			must(t, Apply(planWithHeaders(e)))
			headerRecordVersion(t, e, version)
			p := planWithHeaders(e)
			other := "model_providers.qcs-other"
			p.Edits[0].Ops[0].Value = "qcs-other"
			p.Edits[0].Ops[3].Table = other
			p.State["provider"] = "other"
			must(t, Apply(p))
			assertKeptToken(t, e.cfg, "")
			assertHeaders(t, e.cfg, keptTable, "")
			if _, ok := edit.GetTOMLKeyRaw(e.cfg, other, keptHeaders); !ok {
				t.Fatal("active provider headers missing")
			}
			_, err := Restore("codex")
			must(t, err)
			assertKeptToken(t, e.cfg, "")
			assertHeaders(t, e.cfg, keptTable, "")
			assertHeaders(t, e.cfg, other, "")
		})
	}
}

func TestKeptHeadersOnlyOwnedValues(t *testing.T) {
	for _, version := range []string{"unrecorded", "current", "keep only", "token only"} {
		t.Run(version, func(t *testing.T) {
			e := setup(t)
			orig := codexConfig + "\n[" + keptTable + "]\n" + keptHeaders + " = " + originalHeaders + "\n"
			write(t, e.cfg, orig)
			if version != "unrecorded" {
				must(t, Apply(planA(e, true))) // writes a token, never headers
				headerRecordVersion(t, e, version)
				must(t, edit.SetTOMLKey(e.cfg, keptTable, "request_max_retries", 7))
			}
			_, err := Restore("codex")
			must(t, err)
			assertHeaders(t, e.cfg, keptTable, originalHeaders)
			if version == "unrecorded" && read(t, e.cfg) != orig {
				t.Fatal("unrecorded provider was changed")
			}
		})
	}
}

func TestKeptHeadersInactiveTokenOnlyRecord(t *testing.T) {
	e := setup(t)
	write(t, e.cfg, codexConfig)
	p := planWithHeaders(e)
	must(t, Apply(p))
	headers := p.Edits[0].Ops[3].KVs[4].Value
	must(t, Apply(&plan.Plan{Agent: "codex", State: map[string]string{"mode": "official"}}))
	// Reproduce a token-only version's inactive table: its token was
	// retired, while headers and ownership remained for the old provider.
	must(t, edit.SetTOMLKey(e.cfg, keptTable, keptHeaders, headers))
	s, err := load()
	must(t, err)
	fa := s.applied.Agents["codex"].Files[e.cfg]
	wr := fa.Slots["toml-table:"+keptTable]
	wr.Raw, _ = edit.GetTOMLTableBlock(e.cfg, keptTable)
	wr.Keys = append(wr.Keys, keptHeaders)
	wr.RestoreKeys = nil
	cur, exists, err := readFile(e.cfg)
	must(t, err)
	fa.Hash = hashOf(cur, exists)
	must(t, s.saveApplied())
	_, err = Restore("codex")
	must(t, err)
	assertKeptToken(t, e.cfg, "")
	assertHeaders(t, e.cfg, keptTable, "")
}
