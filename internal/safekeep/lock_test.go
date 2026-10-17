package safekeep

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A writable state directory is not proof that its process lock can be
// taken. No mutation, including backups or records, may proceed without it.
func TestMutationsRequireStateLock(t *testing.T) {
	for _, action := range []string{"first apply", "apply again", "restore", "conditional restore"} {
		t.Run(action, func(t *testing.T) {
			e := setup(t)
			write(t, e.cfg, codexConfig)
			p := planA(e, false)
			p.State["relay_session"] = "current"
			if action != "first apply" {
				must(t, Apply(p))
				must(t, os.Remove(lockPath()))
			}
			must(t, os.MkdirAll(lockPath(), 0o700))
			before := lockTestFiles(t, e.home)
			operate := func() (*Report, error) {
				switch action {
				case "first apply":
					return nil, Apply(p)
				case "apply again":
					return nil, Apply(planB(e, false))
				case "restore":
					return Restore("codex")
				default:
					return RestoreIfState("codex", "relay_session", "current")
				}
			}
			report, err := operate()
			if err == nil || !strings.Contains(err.Error(), "cannot acquire state lock") {
				t.Fatalf("mutation without process lock: %v", err)
			}
			if report != nil || !reflect.DeepEqual(lockTestFiles(t, e.home), before) {
				t.Fatal("failed lock changed configuration, generated files, backups or state")
			}
			// A failed attempt must release the in-process mutex too. Once
			// the lock path is repaired, the same operation can complete.
			must(t, os.Remove(lockPath()))
			report, err = operate()
			must(t, err)
			if action == "restore" || action == "conditional restore" {
				if report == nil || Touched("codex") || read(t, e.cfg) != codexConfig {
					t.Fatal("restore did not complete after lock recovery")
				}
			} else if !Touched("codex") {
				t.Fatal("apply did not complete after lock recovery")
			}
		})
	}
}

func lockTestFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	must(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(data)
		return nil
	}))
	return files
}
