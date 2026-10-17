// Package store keeps small JSON state files that must survive a crash.
//
// Save keeps the previous good copy of a file at path+".bak" and writes the
// new one atomically (see edit.WriteAtomic). Load falls back to that copy
// when the file is present but is not valid JSON (empty, zeroed or cut short).
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
)

// ErrCorrupt is returned by Load when a file is not valid JSON and no usable
// backup exists either.
var ErrCorrupt = errors.New("store: file is not valid JSON")

// Load decodes the JSON file at path into v. A missing file leaves v untouched
// and returns nil. A file that is present but not valid JSON is replaced by
// path+".bak" when that is valid; otherwise Load returns an error wrapping
// ErrCorrupt and leaves v untouched.
func Load(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	data = edit.TrimBOM(data)
	if json.Valid(data) {
		return json.Unmarshal(data, v)
	}
	bak, berr := os.ReadFile(path + ".bak")
	if berr == nil {
		bak = edit.TrimBOM(bak)
		if json.Valid(bak) {
			return json.Unmarshal(bak, v)
		}
	}
	return fmt.Errorf("%w: %s (no usable backup)", ErrCorrupt, path)
}

// Save writes v as indented JSON to path, creating the parent folder (0700)
// when needed. When the current file is valid JSON it is first copied to
// path+".bak", so a damaged file never replaces a good backup. A file that is
// created new gets mode; an existing file keeps its own mode.
func Save(path string, v any, mode os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	cur, err := os.ReadFile(path)
	switch {
	case err == nil:
		if json.Valid(edit.TrimBOM(cur)) {
			// the copy gets the permissions of the file it copies, so a
			// secret file's backup is as private as the file itself
			bakMode := mode
			if fi, err := os.Stat(path); err == nil {
				bakMode = fi.Mode().Perm()
			}
			if err := write(path+".bak", cur, bakMode); err != nil {
				return fmt.Errorf("back up %s: %w", path, err)
			}
		}
	case errors.Is(err, fs.ErrNotExist):
	default:
		return err
	}
	return write(path, data, mode)
}

// write replaces path with data via edit.WriteAtomic. A file that does not
// exist yet is created with mode first, because WriteAtomic keeps the mode of
// the file it replaces; if the write fails, that new empty file is removed.
func write(path string, data []byte, mode os.FileMode) error {
	created := false
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		if err := createEmpty(path, mode); err != nil {
			return err
		}
		created = true
	}
	if err := edit.WriteAtomic(path, data); err != nil {
		if created {
			_ = os.Remove(path)
		}
		return err
	}
	return nil
}

// createEmpty creates an empty file at path with exactly mode (the umask
// would otherwise strip bits from it).
func createEmpty(path string, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
