package store

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

type state struct {
	Name  string   `json:"name"`
	Items []string `json:"items,omitempty"`
}

func statePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(testenv.Home(t), "sub", "dir", "state.json")
}

func mustJSON(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLoadMissingLeavesValue(t *testing.T) {
	testenv.Home(t)
	p := statePath(t)
	v := state{Name: "keep"}
	if err := Load(p, &v); err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if v.Name != "keep" {
		t.Errorf("value changed: %+v", v)
	}
	if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load created a file: %v", err)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	testenv.Home(t)
	p := statePath(t)
	in := state{Name: "a", Items: []string{"x", "y"}}
	if err := Save(p, in, 0o600); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(p)); err != nil {
		t.Fatalf("parent folder not created: %v", err)
	}
	var out state
	if err := Load(p, &out); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Name != in.Name || len(out.Items) != 2 || out.Items[1] != "y" {
		t.Errorf("roundtrip: got %+v want %+v", out, in)
	}
	want := "{\n  \"name\": \"a\",\n  \"items\": [\n    \"x\",\n    \"y\"\n  ]\n}\n"
	if got := string(mustJSON(t, p)); got != want {
		t.Errorf("file content:\n%s\nwant:\n%s", got, want)
	}
}

func TestSaveNewFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not meaningful on windows")
	}
	testenv.Home(t)
	p := statePath(t)
	if err := Save(p, state{Name: "a"}, 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", fi.Mode().Perm())
	}
	// a second save keeps the mode of the file it replaces
	if err := Save(p, state{Name: "b"}, 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode after resave = %o, want 600", fi.Mode().Perm())
	}
	bi, err := os.Stat(p + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if bi.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %o, want 600", bi.Mode().Perm())
	}
}

func TestSaveKeepsPreviousAsBackup(t *testing.T) {
	testenv.Home(t)
	p := statePath(t)
	if err := Save(p, state{Name: "one"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".bak"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("backup made for a new file: %v", err)
	}
	first := mustJSON(t, p)
	if err := Save(p, state{Name: "two"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, p+".bak"); !bytes.Equal(got, first) {
		t.Errorf("backup = %q, want previous content %q", got, first)
	}
}

func TestSaveDoesNotBackupCorruptFile(t *testing.T) {
	testenv.Home(t)
	p := statePath(t)
	if err := Save(p, state{Name: "one"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(p, state{Name: "two"}, 0o600); err != nil {
		t.Fatal(err)
	}
	good := mustJSON(t, p+".bak") // holds "one"
	if err := os.WriteFile(p, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(p, state{Name: "three"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, p+".bak"); !bytes.Equal(got, good) {
		t.Errorf("corrupt file replaced the good backup: %q", got)
	}
	var v state
	if err := Load(p, &v); err != nil || v.Name != "three" {
		t.Errorf("Load after save: %+v, %v", v, err)
	}
}

func TestLoadFallsBackOnEmptyFile(t *testing.T) {
	testenv.Home(t)
	p := statePath(t)
	if err := Save(p, state{Name: "one"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(p, state{Name: "two"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var v state
	if err := Load(p, &v); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if v.Name != "one" {
		t.Errorf("got %q from backup, want %q", v.Name, "one")
	}
}

func TestLoadFallsBackOnZeroBytes(t *testing.T) {
	testenv.Home(t)
	p := statePath(t)
	if err := Save(p, state{Name: "one"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(p, state{Name: "two"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, 64), 0o600); err != nil {
		t.Fatal(err)
	}
	var v state
	if err := Load(p, &v); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if v.Name != "one" {
		t.Errorf("got %q from backup, want %q", v.Name, "one")
	}
}

func TestLoadFallsBackOnTruncatedFile(t *testing.T) {
	testenv.Home(t)
	p := statePath(t)
	if err := Save(p, state{Name: "one"}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(p, state{Name: "two", Items: []string{"a", "b", "c"}}, 0o600); err != nil {
		t.Fatal(err)
	}
	full := mustJSON(t, p)
	if err := os.WriteFile(p, full[:len(full)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	var v state
	if err := Load(p, &v); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if v.Name != "one" {
		t.Errorf("got %q from backup, want %q", v.Name, "one")
	}
}

func TestLoadCorruptWithoutBackupIsError(t *testing.T) {
	testenv.Home(t)
	p := statePath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := state{Name: "untouched"}
	err := Load(p, &v)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Load error = %v, want ErrCorrupt", err)
	}
	if v.Name != "untouched" {
		t.Errorf("value changed on error: %+v", v)
	}
}

func TestLoadCorruptWithBadBackupIsError(t *testing.T) {
	testenv.Home(t)
	p := statePath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p+".bak", []byte("["), 0o600); err != nil {
		t.Fatal(err)
	}
	var v state
	if err := Load(p, &v); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Load error = %v, want ErrCorrupt", err)
	}
}

func TestLoadAcceptsBOM(t *testing.T) {
	testenv.Home(t)
	p := statePath(t)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	data := append([]byte("\xef\xbb\xbf"), []byte(`{"name":"bom"}`)...)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var v state
	if err := Load(p, &v); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if v.Name != "bom" {
		t.Errorf("name = %q", v.Name)
	}
}
