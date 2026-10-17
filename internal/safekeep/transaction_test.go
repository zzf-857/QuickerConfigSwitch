package safekeep

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRecordWriteFailureRollsBackConfigs(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions as a non-root user")
	}
	for _, action := range []string{"first apply", "apply again", "restore"} {
		t.Run(action, func(t *testing.T) {
			e := setup(t)
			write(t, e.cfg, codexConfig)
			if action != "first apply" {
				must(t, Apply(planA(e, true)))
			} else {
				write(t, appliedPath(), "{}\n")
			}
			cfgBefore := read(t, e.cfg)
			appliedBefore := read(t, appliedPath())
			stashBefore, stashExists, err := readFile(stashPath())
			must(t, err)
			catalogBefore, catalogExists, err := readFile(e.catalog)
			must(t, err)

			// load can read the state, but recording the new state fails
			// after config writes. The state itself stays readable for rollback.
			dir := filepath.Join(t.TempDir(), "read-only")
			must(t, os.MkdirAll(dir, 0o700))
			target := filepath.Join(dir, "applied.json")
			write(t, target, appliedBefore)
			must(t, os.Remove(appliedPath()))
			must(t, os.Symlink(target, appliedPath()))
			must(t, os.Chmod(dir, 0o500))
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

			switch action {
			case "first apply":
				err = Apply(planA(e, true))
			case "apply again":
				err = Apply(planB(e, true))
			case "restore":
				_, err = Restore("codex")
			}
			if err == nil {
				t.Fatal("record write unexpectedly succeeded")
			}
			if read(t, e.cfg) != cfgBefore || read(t, appliedPath()) != appliedBefore {
				t.Fatal("config or applied state changed despite record failure")
			}
			gotStash, gotStashExists, err := readFile(stashPath())
			must(t, err)
			gotCatalog, gotCatalogExists, err := readFile(e.catalog)
			must(t, err)
			if string(gotStash) != string(stashBefore) || gotStashExists != stashExists ||
				string(gotCatalog) != string(catalogBefore) || gotCatalogExists != catalogExists {
				t.Fatal("stash or generated file changed despite record failure")
			}
		})
	}
}
