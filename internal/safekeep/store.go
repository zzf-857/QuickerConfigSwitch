package safekeep

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/zzf-857/quickerconfigswitch/internal/appdir"
	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/filelock"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
)

// Original is what a slot held before this tool first changed it.
type Original struct {
	Slot
	Existed bool `json:"existed"`
	// Raw is the value as written: TOML value text, a TOML table's block
	// (header and body), a JSON value, or a whole file's bytes in base64.
	Raw string `json:"raw,omitempty"`
	// Created is the part of the slot's surroundings that did not exist
	// either and goes again on restore when it is left empty: for a JSON
	// path the outermost missing parent object, for a TOML table key its
	// table.
	Created string `json:"created,omitempty"`
	// Restore holds originals of keys excepted from a kept table. They
	// are released separately, so using the table again captures any key
	// the user set while it was inactive.
	Restore map[string]*Original `json:"restore,omitempty"`
}

// fileStash is one agent's originals for one file.
type fileStash struct {
	// Absent: the file did not exist before this tool first wrote it.
	Absent bool `json:"absent,omitempty"`
	// Snapshot is the copy of the whole file taken then.
	Snapshot string               `json:"snapshot,omitempty"`
	Slots    map[string]*Original `json:"slots"`

	snap    []byte // the snapshot's bytes, when they could be read
	hasSnap bool
}

type stashDB struct {
	Version int                              `json:"version"`
	Agents  map[string]map[string]*fileStash `json:"agents"`
}

// written is what this tool last wrote in a slot.
type written struct {
	Slot
	Deleted bool `json:"deleted,omitempty"`
	// Raw is as for Original, except that a whole file is its sha256.
	Raw  string `json:"raw,omitempty"`
	Keep bool   `json:"keep,omitempty"`
	// Keys are the keys a merging SetTable set in its table, so a later
	// one that no longer sets one takes it out.
	Keys        []string `json:"keys,omitempty"`
	RestoreKeys []string `json:"restore_keys,omitempty"`
}

type fileApplied struct {
	Format plan.Format `json:"format"`
	// Hash is the sha256 of the file right after this tool's last write,
	// "" when it was left absent.
	Hash string `json:"hash"`
	// Dirty: someone else changed the file between two of this tool's
	// writes, so the original snapshot no longer is "the file minus our
	// changes" and Restore must go key by key.
	Dirty bool                `json:"dirty,omitempty"`
	Slots map[string]*written `json:"slots"`
}

type agentApplied struct {
	State map[string]string       `json:"state,omitempty"`
	Files map[string]*fileApplied `json:"files,omitempty"`
}

type appliedDB struct {
	Version int                      `json:"version"`
	Agents  map[string]*agentApplied `json:"agents"`
}

func stashPath() string   { return filepath.Join(appdir.StateDir(), "stash.json") }
func appliedPath() string { return filepath.Join(appdir.StateDir(), "applied.json") }
func lockPath() string    { return filepath.Join(appdir.StateDir(), "lock") }

// mu keeps one change at a time in this process; the lock file across
// processes (a GUI and a CLI run at once).
var mu sync.Mutex

func lockAll() (unlock func()) {
	mu.Lock()
	release, _ := filelock.Lock(lockPath(), filelock.Wait)
	return func() { release(); mu.Unlock() }
}

// readJSON reads path into v. A missing file leaves v as it is; one that
// does not parse is an error: what it holds may be the only record of the
// user's original config, so it is never quietly written over.
func readJSON(path string, v any) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return b, fmt.Errorf("%s is unreadable (%v); move it aside to start over", path, err)
	}
	return b, nil
}

// writeJSON writes v to path whole (temp file + rename), mode 0600: the
// stash holds originals that may include keys.
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeBytes(path, append(b, '\n'))
}

func writeBytes(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
		f.Close()
	} else if !errors.Is(err, fs.ErrExist) {
		return err
	}
	return edit.WriteAtomic(path, b)
}

// state is both records as loaded, with the stash's bytes as they were on
// disk to put back when a change fails.
type state struct {
	stash      *stashDB
	applied    *appliedDB
	stashBytes []byte
}

func load() (*state, error) {
	s := &state{
		stash:   &stashDB{Version: 1, Agents: map[string]map[string]*fileStash{}},
		applied: &appliedDB{Version: 1, Agents: map[string]*agentApplied{}},
	}
	b, err := readJSON(stashPath(), s.stash)
	if err != nil {
		return nil, err
	}
	s.stashBytes = b
	if _, err := readJSON(appliedPath(), s.applied); err != nil {
		return nil, err
	}
	if s.stash.Agents == nil {
		s.stash.Agents = map[string]map[string]*fileStash{}
	}
	if s.applied.Agents == nil {
		s.applied.Agents = map[string]*agentApplied{}
	}
	for _, files := range s.stash.Agents {
		for _, f := range files {
			if f.Slots == nil {
				f.Slots = map[string]*Original{}
			}
		}
	}
	for agent, a := range s.applied.Agents {
		for _, f := range a.Files {
			if f.Slots == nil {
				f.Slots = map[string]*written{}
			}
			migrateKeptCredentials(agent, f)
		}
	}
	return s, nil
}

func (s *state) saveStash() error { return writeJSON(stashPath(), s.stash) }

// unsaveStash puts stash.json back as it was loaded.
func (s *state) unsaveStash() error {
	if s.stashBytes == nil {
		err := os.Remove(stashPath())
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return writeBytes(stashPath(), s.stashBytes)
}

func (s *state) saveApplied() error { return writeJSON(appliedPath(), s.applied) }

// prune drops empty entries so the records only hold what is in force.
func (s *state) prune(agent string) {
	if files := s.stash.Agents[agent]; len(files) == 0 {
		delete(s.stash.Agents, agent)
	}
	if a := s.applied.Agents[agent]; a != nil && len(a.Files) == 0 && len(a.State) == 0 {
		delete(s.applied.Agents, agent)
	}
}
