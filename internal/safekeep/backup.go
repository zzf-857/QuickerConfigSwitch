package safekeep

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
)

// KeepBackups is how many rolling backups are kept per file.
const KeepBackups = 20

// backupDir is where an agent's copies of the file at path go.
func backupDir(agent, path string) string {
	return filepath.Join(appdir.BackupDir(), sanitize(agent), sanitize(path))
}

// sanitize turns a path into one folder name.
func sanitize(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return strings.TrimLeft(r.Replace(p), "_.")
}

// stamp is a time as a name that sorts in time order.
func stamp(t time.Time) string { return t.UTC().Format("20060102T150405.000000000Z") }

// freeName is dir/prefix<stamp>suffix for the first stamp from t on that is
// not taken.
func freeName(dir, prefix, suffix string, t time.Time) string {
	for {
		p := filepath.Join(dir, prefix+stamp(t)+suffix)
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			return p
		}
		t = t.Add(time.Nanosecond)
	}
}

// snapshot copies the file as it is now, the first time this tool touches
// it for agent; a file that is not there is recorded by an empty
// ".absent" marker. It returns the copy's path.
func snapshot(agent, path string, b []byte, exists bool) (string, error) {
	dir := backupDir(agent, path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	suffix := ""
	if !exists {
		suffix, b = ".absent", nil
	}
	p := freeName(dir, "original-", suffix, time.Now())
	return p, os.WriteFile(p, b, 0o600)
}

// rollingBackup copies the file as it is now before a write and keeps the
// latest KeepBackups copies. A copy the same as the newest one is not
// made again.
func rollingBackup(agent, path string, b []byte) error {
	dir := backupDir(agent, path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var baks []string
	for _, e := range es {
		if strings.HasSuffix(e.Name(), ".bak") && !strings.HasPrefix(e.Name(), "original-") {
			baks = append(baks, e.Name())
		}
	}
	sort.Strings(baks)
	// the same as the newest copy: one is enough
	if n := len(baks); n > 0 {
		if last, err := os.ReadFile(filepath.Join(dir, baks[n-1])); err == nil && bytes.Equal(last, b) {
			return nil
		}
	}
	name := freeName(dir, "", ".bak", time.Now())
	if err := os.WriteFile(name, b, 0o600); err != nil {
		return err
	}
	baks = append(baks, filepath.Base(name))
	for len(baks) > KeepBackups {
		os.Remove(filepath.Join(dir, baks[0]))
		baks = baks[1:]
	}
	return nil
}
