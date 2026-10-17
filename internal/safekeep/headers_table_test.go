package safekeep

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
)

const headersTable = keptTable + ".http_headers"

func childHeadersConfig() string {
	return codexConfig + "\n[" + keptTable + "]\n" +
		"# original provider\nname = 'original'\nrequest_max_retries = 7 # user\n" +
		keptToken + " = 'original-token'\n\n[" + headersTable + "]\n" +
		"# my proxy\nAuthorization = 'user-before' # original auth\n" +
		"'X-User' = 'keep'\n\"X.Trace\" = 'trace-before' # dotted header\n"
}

func childHeadersPlan(e env) *plan.Plan {
	p := planWithHeaders(e)
	op := &p.Edits[0].Ops[3]
	in := op.KVs[len(op.KVs)-1].Value.(edit.Inline)
	op.KVs[len(op.KVs)-1].Value = append(in, edit.KV{Path: "X.Trace", Value: "qcs-trace"})
	return p
}

func TestKeptHeadersChildTableRoundTrip(t *testing.T) {
	for _, format := range []string{"lf", "bom-crlf"} {
		for _, change := range []string{"clean", "unrelated", "added header", "changed header", "removed header", "removed table", "drop headers"} {
			t.Run(format+"/"+change, func(t *testing.T) {
				e := setup(t)
				original := childHeadersConfig()
				if format == "bom-crlf" {
					original = bom(crlf(original))
				}
				write(t, e.cfg, original)
				p := childHeadersPlan(e)
				if _, err := Preview(p); err != nil {
					t.Fatal(err)
				}
				if read(t, e.cfg) != original || Touched("codex") {
					t.Fatal("preview changed configuration or records")
				}
				must(t, Apply(p))
				for _, text := range []string{"# original provider", "request_max_retries = 7 # user", "# my proxy", "# original auth", "# dotted header", "'X-User' = 'keep'"} {
					if !strings.Contains(read(t, e.cfg), text) {
						t.Fatalf("lost original text %q", text)
					}
				}
				if _, exists := edit.GetTOMLKeyRaw(e.cfg, keptTable, keptHeaders); exists {
					t.Fatal("wrote inline headers beside the existing child table")
				}
				if drift := Check("codex"); len(drift) != 0 {
					t.Fatalf("fresh header table drift: %+v", drift)
				}
				assertChildHeader(t, e.cfg, "X.Trace", `"qcs-trace"`)
				switch change {
				case "unrelated":
					must(t, edit.SetTOMLKey(e.cfg, "mcp_servers.mine", "command", "mine"))
				case "added header":
					must(t, edit.SetTOMLKeyQuoted(e.cfg, headersTable, "X.User.Added", "mine"))
				case "changed header":
					must(t, edit.SetTOMLKeyQuoted(e.cfg, headersTable, "Authorization", "user-after"))
				case "removed header":
					must(t, edit.DelTOMLTableKey(e.cfg, headersTable, "Authorization"))
				case "removed table":
					must(t, edit.PutTOMLTableBlock(e.cfg, headersTable, ""))
				case "drop headers":
					must(t, Apply(planA(e, true)))
					assertChildHeader(t, e.cfg, "Authorization", "'user-before'")
					assertChildHeader(t, e.cfg, "X-Api-Key", "")
				}
				if change == "added header" || change == "changed header" || change == "removed header" || change == "removed table" {
					if drift := Check("codex"); len(drift) == 0 {
						t.Fatal("header table change was not detected")
					}
				}
				before := read(t, e.cfg)
				if _, err := RestorePreview("codex"); err != nil {
					t.Fatal(err)
				}
				if read(t, e.cfg) != before {
					t.Fatal("restore preview wrote configuration")
				}
				report, err := Restore("codex")
				must(t, err)
				assertKeptToken(t, e.cfg, "'original-token'")
				if change == "removed table" {
					if _, exists := edit.GetTOMLTableBlock(e.cfg, headersTable); exists {
						t.Fatal("recreated user-removed header table")
					}
				} else {
					want := "'user-before'"
					if change == "changed header" {
						want = `"user-after"`
					} else if change == "removed header" {
						want = ""
					}
					assertChildHeader(t, e.cfg, "Authorization", want)
					assertChildHeader(t, e.cfg, "X.Trace", "'trace-before'")
					assertChildHeader(t, e.cfg, "X-User", "'keep'")
					assertChildHeader(t, e.cfg, "X-Api-Key", "")
					assertChildHeader(t, e.cfg, "Cookie", "")
				}
				if change == "added header" {
					assertChildHeader(t, e.cfg, "X.User.Added", `"mine"`)
				}
				if change == "unrelated" {
					if value, _ := edit.GetTOMLKeyRaw(e.cfg, "mcp_servers.mine", "command"); value != `"mine"` {
						t.Fatal("lost unrelated user edit")
					}
				}
				wantLeft := change == "changed header" || change == "removed header" || change == "removed table"
				if report.Left() != wantLeft {
					t.Fatalf("unexpected restore conflicts: %+v", report)
				}
				if (change == "clean" || change == "drop headers") && read(t, e.cfg) != original {
					t.Fatal("original header table was not restored byte for byte")
				}
				if format == "bom-crlf" {
					got := read(t, e.cfg)
					if !strings.HasPrefix(got, "\xef\xbb\xbf") || strings.Count(got, "\n") != strings.Count(got, "\r\n") {
						t.Fatal("BOM or CRLF changed")
					}
				}
			})
		}
	}
}

func assertChildHeader(t *testing.T, path, key, want string) {
	t.Helper()
	got, exists := edit.GetTOMLKeyRaw(path, headersTable, key)
	if got != want || exists != (want != "") {
		t.Fatalf("header %s: %q, exists %v, want %q", key, got, exists, want)
	}
}

func TestKeptHeadersChildProviderSwitch(t *testing.T) {
	e := setup(t)
	write(t, e.cfg, childHeadersConfig())
	must(t, Apply(childHeadersPlan(e)))
	next := planWithHeaders(e)
	other := "model_providers.qcs-other"
	next.Edits[0].Ops[0].Value = "qcs-other"
	next.Edits[0].Ops[3].Table = other
	next.State["provider"] = "other"
	must(t, Apply(next))
	assertKeptToken(t, e.cfg, "'original-token'")
	assertChildHeader(t, e.cfg, "Authorization", "'user-before'")
	assertChildHeader(t, e.cfg, "X.Trace", "'trace-before'")
	assertChildHeader(t, e.cfg, "X-Api-Key", "")
	assertChildHeader(t, e.cfg, "Cookie", "")
	if _, exists := edit.GetTOMLKeyRaw(e.cfg, other, keptHeaders); !exists {
		t.Fatal("new provider headers were not written")
	}
	_, err := Restore("codex")
	must(t, err)
	assertHeaders(t, e.cfg, other, "")
	assertChildHeader(t, e.cfg, "Authorization", "'user-before'")
}

func TestKeptHeadersInlineMovedToChild(t *testing.T) {
	for _, version := range []string{"current", "keep only", "token only"} {
		for _, action := range []string{"restore", "reapply", "drop headers"} {
			for _, changed := range []bool{false, true} {
				t.Run(version+"/"+action+"/"+strconv.FormatBool(changed), func(t *testing.T) {
					e := setup(t)
					original := codexConfig + "\n[" + keptTable + "]\nhttp_headers = " + originalHeaders + "\n"
					write(t, e.cfg, original)
					must(t, Apply(planWithHeaders(e)))
					headerRecordVersion(t, e, version)
					must(t, edit.DelTOMLTableKey(e.cfg, keptTable, keptHeaders))
					block := "[" + headersTable + "]\n# user moved the map\n" +
						"Authorization = \"qcs-authorization\" # auth\n\"X-Api-Key\" = \"qcs-api-key\"\nCookie = \"qcs-cookie\"\n\"X.User.Added\" = 'mine'\n"
					if changed {
						block = strings.Replace(block, `"qcs-authorization"`, `'user-after'`, 1)
						block += "'X-User' = 'user-header-after'\n"
					}
					must(t, edit.PutTOMLTableBlock(e.cfg, headersTable, block))
					if len(Check("codex")) == 0 {
						t.Fatal("inline-to-table drift was not detected")
					}
					switch action {
					case "reapply":
						must(t, Apply(planWithHeaders(e)))
					case "drop headers":
						must(t, Apply(planA(e, true)))
					}
					report, err := Restore("codex")
					must(t, err)
					if _, exists := edit.GetTOMLKeyRaw(e.cfg, keptTable, keptHeaders); exists {
						t.Fatal("recreated old inline headers beside the child table")
					}
					want := `"user-before"`
					if changed {
						want = `'user-after'`
					}
					assertChildHeader(t, e.cfg, "Authorization", want)
					assertChildHeader(t, e.cfg, "X-Api-Key", "")
					assertChildHeader(t, e.cfg, "Cookie", "")
					assertChildHeader(t, e.cfg, "X.User.Added", "'mine'")
					userHeader := `"keep"`
					if changed {
						userHeader = "'user-header-after'"
					}
					assertChildHeader(t, e.cfg, "X-User", userHeader)
					if !strings.Contains(read(t, e.cfg), "# user moved the map") {
						t.Fatal("user header comments changed")
					}
					var left []string
					for _, file := range report.Files {
						left = append(left, file.Left...)
					}
					if action == "restore" && changed && !slices.Contains(left, "toml-key:"+headersTable+":Authorization") {
						t.Fatalf("changed user header not reported: %+v", report)
					}
				})
			}
		}
	}
}
