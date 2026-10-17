package safekeep

import (
	"slices"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
)

const keptTable = "model_providers.qcs-cpa"
const keptToken = "experimental_bearer_token"

func TestKeptCredentialRestore(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, tc := range []struct {
			name, original, change, want string
			dirty, left                  bool
		}{
			{name: "added"},
			{name: "unrelated edit", dirty: true},
			{name: "original", original: "'user-before'", want: "'user-before'"},
			{name: "original dirty", original: "'user-before'", want: "'user-before'", dirty: true},
			{name: "changed token", change: "user-after", want: `"user-after"`, left: true},
			{name: "changed original token", original: "'user-before'", change: "user-after", want: `"user-after"`, left: true},
			{name: "removed token", change: "remove", left: true},
		} {
			name := tc.name
			if legacy {
				name += " legacy"
			}
			t.Run(name, func(t *testing.T) {
				e := setup(t)
				orig := crlf(codexConfig)
				if tc.original != "" {
					orig += "\r\n[" + keptTable + "]\r\nname = 'original'\r\n" + keptToken + " = " + tc.original + " # keep comment\r\n"
				}
				write(t, e.cfg, orig)
				must(t, Apply(planA(e, true)))
				if legacy {
					makeLegacy(t, e)
				}
				if tc.dirty {
					must(t, edit.SetTOMLKey(e.cfg, keptTable, "request_max_retries", 7))
				}
				switch tc.change {
				case "":
				case "remove":
					must(t, edit.DelTOMLTableKey(e.cfg, keptTable, keptToken))
				default:
					must(t, edit.SetTOMLKey(e.cfg, keptTable, keptToken, tc.change))
				}
				before, stash, applied := read(t, e.cfg), read(t, stashPath()), read(t, appliedPath())
				diff, err := RestorePreview("codex")
				must(t, err)
				if tc.want == "" && !tc.left && !strings.Contains(diff, "-experimental_bearer_token") {
					t.Error("preview does not remove the owned token")
				}
				if read(t, e.cfg) != before || read(t, stashPath()) != stash || read(t, appliedPath()) != applied {
					t.Fatal("preview changed config or records")
				}
				rep, err := Restore("codex")
				must(t, err)
				assertKeptToken(t, e.cfg, tc.want)
				var left []string
				for _, f := range rep.Files {
					left = append(left, f.Left...)
				}
				if got := slices.Contains(left, "toml-key:"+keptTable+":"+keptToken); got != tc.left {
					t.Errorf("token left %v, want %v", got, tc.left)
				}
				if tc.dirty {
					if got, _ := edit.GetTOMLKeyRaw(e.cfg, keptTable, "request_max_retries"); got != "7" {
						t.Error("user's provider setting was lost")
					}
				} else if tc.original != "" && tc.change == "" && read(t, e.cfg) != orig {
					t.Error("clean preexisting table was not restored byte for byte")
				}
				got := read(t, e.cfg)
				if strings.Count(got, "\n") != strings.Count(got, "\r\n") {
					t.Error("restore changed CRLF line endings")
				}
			})
		}
	}
}

func makeLegacy(t *testing.T, e env) {
	t.Helper()
	s, err := load()
	must(t, err)
	s.applied.Agents["codex"].Files[e.cfg].Slots["toml-table:"+keptTable].RestoreKeys = nil
	s.stash.Agents["codex"][e.cfg].Slots["toml-table:"+keptTable].Restore = nil
	must(t, s.saveApplied())
	must(t, s.saveStash())
}

func assertKeptToken(t *testing.T, path, want string) {
	t.Helper()
	if _, ok := edit.GetTOMLTableBlock(path, keptTable); !ok {
		t.Fatal("provider definition removed")
	}
	got, ok := edit.GetTOMLKeyRaw(path, keptTable, keptToken)
	if ok != (want != "") || got != want {
		t.Fatal("provider credential was not restored as expected")
	}
}

func TestKeptCredentialReleasedBetweenSelections(t *testing.T) {
	e := setup(t)
	write(t, e.cfg, codexConfig)
	must(t, Apply(planA(e, true)))
	// An inactive table survives an official or another provider plan;
	// none of its previously managed credentials may be brought back.
	official := &plan.Plan{Agent: "codex", State: map[string]string{"mode": "official"}}
	must(t, Apply(official))
	assertKeptToken(t, e.cfg, "")
	must(t, edit.SetTOMLKey(e.cfg, keptTable, keptToken, "user-between"))
	must(t, Apply(planA(e, true)))
	_, err := Restore("codex")
	must(t, err)
	assertKeptToken(t, e.cfg, `"user-between"`)
}

func TestKeptCredentialProviderSwitch(t *testing.T) {
	e := setup(t)
	write(t, e.cfg, codexConfig)
	must(t, Apply(planA(e, true)))
	p := planB(e, true)
	p.Edits[0].Ops[2].Table = "model_providers.qcs-other"
	must(t, Apply(p))
	assertKeptToken(t, e.cfg, "")
	if _, ok := edit.GetTOMLKeyRaw(e.cfg, "model_providers.qcs-other", keptToken); !ok {
		t.Fatal("active provider token missing")
	}
	_, err := Restore("codex")
	must(t, err)
	assertKeptToken(t, e.cfg, "")
	if _, ok := edit.GetTOMLKeyRaw(e.cfg, "model_providers.qcs-other", keptToken); ok {
		t.Fatal("second provider token left")
	}
}

func TestKeptCredentialOnlyOwnedKeys(t *testing.T) {
	for _, dropped := range []bool{false, true} {
		t.Run(map[bool]string{false: "never written", true: "dropped from plan"}[dropped], func(t *testing.T) {
			e := setup(t)
			write(t, e.cfg, codexConfig+"\n["+keptTable+"]\n"+keptToken+" = 'user-original'\n")
			p := planA(e, true)
			if dropped {
				must(t, Apply(p))
			}
			p.Edits[0].Ops[3].KVs = p.Edits[0].Ops[3].KVs[:3]
			must(t, Apply(p))
			assertKeptToken(t, e.cfg, "'user-original'")
			_, err := Restore("codex")
			must(t, err)
			assertKeptToken(t, e.cfg, "'user-original'")
		})
	}
}

func TestKeptCredentialDeletedTable(t *testing.T) {
	e := setup(t)
	write(t, e.cfg, codexConfig)
	must(t, Apply(planA(e, true)))
	must(t, edit.PutTOMLTableBlock(e.cfg, keptTable, ""))
	_, err := Restore("codex")
	must(t, err)
	if _, ok := edit.GetTOMLTableBlock(e.cfg, keptTable); ok {
		t.Fatal("user-deleted table was recreated")
	}
}

func TestLegacyKeptCredentialNeedsOwnership(t *testing.T) {
	for _, tc := range []struct{ name, agent, table string }{
		{"other agent", "other", keptTable},
		{"other table", "codex", "model_providers.custom"},
		{"unwritten token", "codex", keptTable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			write(t, e.cfg, codexConfig+"\n["+keptTable+"]\n"+keptToken+" = 'user-owned'\n")
			p := planA(e, true)
			p.Agent = tc.agent
			op := &p.Edits[0].Ops[3]
			op.Table, op.RestoreKeys = tc.table, nil
			if tc.name == "unwritten token" {
				op.KVs = op.KVs[:3]
			}
			must(t, Apply(p))
			// Make the restore take the per-key path so this tests migration
			// ownership, not an original table supplied by the snapshot.
			must(t, edit.SetTOMLKey(e.cfg, tc.table, "request_max_retries", 7))
			_, err := Restore(tc.agent)
			must(t, err)
			if _, ok := edit.GetTOMLKeyRaw(e.cfg, tc.table, keptToken); !ok {
				t.Fatal("migration removed an unowned credential")
			}
		})
	}
}

func TestRestoreIfState(t *testing.T) {
	e := setup(t)
	write(t, e.cfg, codexConfig)
	p := planA(e, true)
	p.State["relay_session"] = "current"
	must(t, Apply(p))
	before, applied, stash := read(t, e.cfg), read(t, appliedPath()), read(t, stashPath())
	for _, tc := range []struct{ key, value string }{{"relay_session", "older"}, {"missing", ""}} {
		rep, err := RestoreIfState("codex", tc.key, tc.value)
		must(t, err)
		if rep != nil || read(t, e.cfg) != before || read(t, appliedPath()) != applied || read(t, stashPath()) != stash {
			t.Fatal("mismatched state changed the active selection")
		}
	}
	rep, err := RestoreIfState("codex", "relay_session", "current")
	must(t, err)
	if rep == nil || Touched("codex") {
		t.Fatal("matching state was not restored")
	}
	assertKeptToken(t, e.cfg, "")
}
