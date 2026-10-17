// Package safekeep carries out a plan.Plan on an agent's config files and
// guarantees the user's original config can always be put back. Adapters
// never write files; they hand a Plan to Apply, and Restore undoes
// everything this tool did for an agent.
//
// # Records
//
// Every Op addresses a slot of one file (see Slot): a TOML top-level key
// (toml-top:<key>), a key of a TOML table (toml-key:<table>:<key>), a
// whole TOML table (toml-table:<table>, for SetTable and DelTable), a JSON
// path (json:<path>) or the whole file (file, for WriteFile). Slots are per
// agent and file.
//
//   - state/stash.json (0600): each slot's Original, captured the first time
//     this tool changes it — whether it existed and its raw text as written
//     (TOML value text, a table's block with its header, a JSON value, or
//     the file's bytes in base64). It is never overwritten once recorded;
//     only putting the slot back (Restore, or an Apply that no longer sets
//     it) forgets it.
//   - state/applied.json: per slot what was last written (or that it was
//     deleted), per file the sha256 right after the last write and whether
//     someone else changed it between two of our writes (Dirty), and the
//     plan.State of the agent's last Apply.
//   - backups/<agent>/<file>/original-<time>: the whole file the first time
//     it is touched (original-<time>.absent when it did not exist), and
//     <time>.bak before every write, the latest KeepBackups kept.
//
// Both records are changed under a mutex and the state/lock file lock, and
// written whole (temp file + rename). A record that does not parse is an
// error rather than an empty start, so originals are never lost to it.
//
// # Apply
//
// The plan is the full desired set for its agent: a slot that the agent's
// last Apply set and this plan does not is put back to its original (a
// switch from one provider to another leaves no stale model_catalog_json
// behind), unless the user has changed it since, when it is left as theirs,
// or it was set with Keep, when it stays in place and stays recorded. When
// the file is still exactly what this tool last wrote and no one else
// changed it between our writes, putting slots back starts from the
// original snapshot (byte for byte) with the slots kept re-added, then the
// plan is applied on that; otherwise slots are put back one by one.
//
// All files change together or none do (edit.Atomically). Each edit goes
// through internal/edit, which keeps comments, BOM, CRLF, links and mode;
// a file is checked to still parse after its edits, and a plan that leaves
// one invalid is refused with every file as it was.
//
// A merging SetTable (Whole false) sets its keys in the table the file
// has, keeping the user's keys there, and takes out keys an earlier
// SetTable of ours set and this one no longer does.
//
// # Restore
//
// Per file: when the file is what we last wrote and no one changed it
// between our writes, the original snapshot is written back whole (or the
// file removed when it did not exist) and Keep slots that did not exist
// originally are added again. Otherwise each slot still holding what we
// wrote gets its original back, one the user changed since is left and
// listed in Report.Left, and Keep slots stay. A file this tool created
// that ends up holding nothing (whitespace, {}) is removed. Then the
// agent's records are forgotten.
//
// Slots of one file are expected not to overlap (a SetTable and a
// SetTableKey of the same table): each is put back on its own.
package safekeep

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
)

// Report says what Restore did.
type Report struct {
	Agent string       `json:"agent"`
	Files []FileResult `json:"files,omitempty"`
}

// FileResult is what Restore did to one file.
type FileResult struct {
	Path string `json:"path"`
	// Exact: the file was put back byte for byte from its original
	// snapshot (Keep slots added again after).
	Exact bool `json:"exact,omitempty"`
	// Removed: the file did not exist before this tool and is gone again.
	Removed  bool     `json:"removed,omitempty"`
	Reverted []string `json:"reverted,omitempty"` // slots given their original back
	Left     []string `json:"left,omitempty"`     // slots changed by someone else since, left as they are
	Kept     []string `json:"kept,omitempty"`     // Keep slots left in place
}

// Left reports whether any slot was left as someone else changed it.
func (r *Report) Left() bool {
	for _, f := range r.Files {
		if len(f.Left) > 0 {
			return true
		}
	}
	return false
}

// Drift kinds.
const (
	Changed  = "changed"  // the slot holds something other than what we wrote
	Removed  = "removed"  // a slot we set is gone
	Replaced = "replaced" // the file was rewritten whole: most of our slots are gone or changed
	Missing  = "missing"  // the file is gone
)

// Drift is a difference between what this tool last wrote and what is
// there now.
type Drift struct {
	Path string `json:"path"`
	Slot string `json:"slot,omitempty"` // "" for Replaced and Missing
	Kind string `json:"kind"`
	Want string `json:"want,omitempty"` // what we wrote
	Got  string `json:"got,omitempty"`  // what is there
}

// Apply carries out p: the full set of changes its agent should have.
func Apply(p *plan.Plan) error {
	if err := check(p); err != nil {
		return err
	}
	unlock := lockAll()
	defer unlock()
	s, err := load()
	if err != nil {
		return err
	}
	e := &engine{agent: p.Agent, s: s}
	jobs, err := e.jobs(p)
	if err != nil {
		return err
	}
	if _, err := e.sync(jobs, false); err != nil {
		return err
	}
	a := e.appliedAgent()
	a.State = nil
	if len(p.State) > 0 {
		a.State = map[string]string{}
		for k, v := range p.State {
			a.State[k] = v
		}
	}
	s.prune(p.Agent)
	// applied first: a stash entry it no longer names is dropped later,
	// while an applied slot without its original could not be put back
	if err := s.saveApplied(); err != nil {
		return err
	}
	return s.saveStash()
}

// Preview is the unified diff of what Apply(p) would do, writing nothing:
// the plan is carried out on copies in a temporary folder.
func Preview(p *plan.Plan) (string, error) {
	if err := check(p); err != nil {
		return "", err
	}
	return preview(p, false)
}

// Restore undoes everything this tool did for agent; see the package
// documentation.
func Restore(agent string) (*Report, error) {
	unlock := lockAll()
	defer unlock()
	s, err := load()
	if err != nil {
		return nil, err
	}
	e := &engine{agent: agent, s: s}
	jobs, err := e.jobs(&plan.Plan{Agent: agent})
	if err != nil {
		return nil, err
	}
	rep, err := e.sync(jobs, true)
	if err != nil {
		return nil, err
	}
	delete(s.applied.Agents, agent)
	delete(s.stash.Agents, agent)
	if err := s.saveApplied(); err != nil {
		return nil, err
	}
	return rep, s.saveStash()
}

// RestorePreview is the unified diff of what Restore(agent) would do.
func RestorePreview(agent string) (string, error) {
	return preview(&plan.Plan{Agent: agent}, true)
}

// Check lists what changed in agent's files since this tool last wrote
// them. Nil means nothing did (or nothing is recorded).
func Check(agent string) []Drift {
	mu.Lock()
	s, err := load()
	mu.Unlock()
	if err != nil {
		return nil
	}
	a := s.applied.Agents[agent]
	if a == nil {
		return nil
	}
	var out []Drift
	for _, path := range sortedKeys(a.Files) {
		fa := a.Files[path]
		b, exists, _ := readFile(path)
		if !exists && fa.Hash != "" {
			out = append(out, Drift{Path: path, Kind: Missing})
			continue
		}
		if hashOf(b, exists) == fa.Hash {
			continue
		}
		var ds []Drift
		for _, id := range sortedKeys(fa.Slots) {
			wr := fa.Slots[id]
			raw, ok := readSlot(path, wr.Slot)
			switch {
			case wr.Deleted && ok:
				ds = append(ds, Drift{Path: path, Slot: id, Kind: Changed, Got: raw})
			case !wr.Deleted && !ok:
				ds = append(ds, Drift{Path: path, Slot: id, Kind: Removed, Want: wr.Raw})
			case !wr.Deleted && !same(wr.Slot, raw, wr.Raw):
				ds = append(ds, Drift{Path: path, Slot: id, Kind: Changed, Want: wr.Raw, Got: raw})
			}
		}
		if len(ds) >= 2 && 2*len(ds) > len(fa.Slots) {
			out = append(out, Drift{Path: path, Kind: Replaced})
			continue
		}
		out = append(out, ds...)
	}
	return out
}

// State is the plan.State the agent's last Apply recorded.
func State(agent string) map[string]string {
	mu.Lock()
	defer mu.Unlock()
	s, err := load()
	if err != nil || s.applied.Agents[agent] == nil {
		return nil
	}
	return s.applied.Agents[agent].State
}

// Touched reports whether this tool has changed something for agent that
// Restore has not undone yet.
func Touched(agent string) bool {
	mu.Lock()
	defer mu.Unlock()
	s, err := load()
	return err == nil && s.applied.Agents[agent] != nil && len(s.applied.Agents[agent].Files) > 0
}

func check(p *plan.Plan) error {
	if p == nil || p.Agent == "" {
		return errors.New("safekeep: a plan needs its agent")
	}
	return nil
}

// preview runs Apply (or Restore) of p on copies and diffs them.
func preview(p *plan.Plan, restore bool) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	s, err := load()
	if err != nil {
		return "", err
	}
	e := &engine{agent: p.Agent, s: s, dry: true, work: map[string]string{}}
	jobs, err := e.jobs(p)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "qcs-preview-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	type side struct {
		b      []byte
		exists bool
	}
	before := make([]side, len(jobs))
	for i, j := range jobs {
		b, exists, err := readFile(j.real)
		if err != nil {
			return "", err
		}
		before[i] = side{b, exists}
		dir := filepath.Join(tmp, strconv.Itoa(i))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		w := filepath.Join(dir, filepath.Base(j.real))
		if exists {
			if err := os.WriteFile(w, b, 0o600); err != nil {
				return "", err
			}
		}
		e.work[j.real] = w
	}
	if _, err := e.sync(jobs, restore); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, j := range jobs {
		b, exists, err := readFile(e.work[j.real])
		if err != nil {
			return "", err
		}
		if j.format == plan.Raw {
			// a whole file this tool generates (Codex's model catalog):
			// a line saying so, not thousands of lines of JSON
			sb.WriteString(generatedLine(j.real, before[i].b, b, before[i].exists, exists))
			continue
		}
		sb.WriteString(unifiedDiff(j.real, before[i].b, b, before[i].exists, exists))
	}
	return sb.String(), nil
}

// engine carries out a sync for one agent. With dry it takes no snapshot
// or backup and saves no record, and every path is edited at its copy in
// work.
type engine struct {
	agent string
	s     *state
	dry   bool
	work  map[string]string
}

// job is everything to do to one file.
type job struct {
	real   string
	format plan.Format
	ops    []plan.Op
	slots  []Slot // of each op
}

func (e *engine) at(real string) string {
	if w, ok := e.work[real]; ok {
		return w
	}
	return real
}

func (e *engine) stashFiles() map[string]*fileStash {
	files := e.s.stash.Agents[e.agent]
	if files == nil {
		files = map[string]*fileStash{}
		e.s.stash.Agents[e.agent] = files
	}
	return files
}

func (e *engine) appliedAgent() *agentApplied {
	a := e.s.applied.Agents[e.agent]
	if a == nil {
		a = &agentApplied{}
		e.s.applied.Agents[e.agent] = a
	}
	if a.Files == nil {
		a.Files = map[string]*fileApplied{}
	}
	return a
}

// jobs is p's files in its order, then every other file recorded for the
// agent, whose slots all go back.
func (e *engine) jobs(p *plan.Plan) ([]*job, error) {
	byPath := map[string]*job{}
	var jobs []*job
	for _, fe := range p.Edits {
		path, err := filepath.Abs(fe.Path)
		if err != nil {
			return nil, err
		}
		j := byPath[path]
		if j == nil {
			j = &job{real: path, format: fe.Format}
			byPath[path] = j
			jobs = append(jobs, j)
		} else if j.format != fe.Format {
			return nil, fmt.Errorf("%s: edited as both %s and %s", path, j.format, fe.Format)
		}
		for _, op := range fe.Ops {
			sl, err := slotOf(fe.Format, op)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			j.ops = append(j.ops, op)
			j.slots = append(j.slots, sl)
		}
	}
	a := e.s.applied.Agents[e.agent]
	tracked := map[string]plan.Format{}
	if a != nil {
		for path, fa := range a.Files {
			tracked[path] = fa.Format
		}
	}
	for path := range e.s.stash.Agents[e.agent] {
		if _, ok := tracked[path]; !ok {
			tracked[path] = plan.Raw
		}
	}
	var out []*job
	for _, j := range jobs {
		if _, ok := tracked[j.real]; ok || len(j.ops) > 0 {
			if f, ok := tracked[j.real]; ok && a != nil && a.Files[j.real] != nil && f != j.format {
				return nil, fmt.Errorf("%s: recorded as %s, now edited as %s", j.real, f, j.format)
			}
			out = append(out, j)
		}
	}
	for _, path := range sortedKeys(tracked) {
		if byPath[path] == nil {
			out = append(out, &job{real: path, format: tracked[path]})
		}
	}
	return out, nil
}

// sync brings every job's file to what its ops say, putting back what the
// agent's records hold and the ops no longer set. With restore every slot
// but Keep ones goes back (jobs then hold no ops) and Keep slots that
// existed originally are left at their original when the file is put back
// whole.
func (e *engine) sync(jobs []*job, restore bool) (*Report, error) {
	files := e.stashFiles()
	a := e.appliedAgent()
	// first every original, recorded before anything is written
	for _, j := range jobs {
		w := e.at(j.real)
		cur, exists, err := readFile(w)
		if err != nil {
			return nil, err
		}
		fs := files[j.real]
		switch {
		case fs == nil && len(j.ops) == 0:
		case fs == nil:
			fs = &fileStash{Absent: !exists, Slots: map[string]*Original{}, snap: append([]byte(nil), cur...), hasSnap: true}
			if !e.dry {
				if fs.Snapshot, err = snapshot(e.agent, j.real, cur, exists); err != nil {
					return nil, fmt.Errorf("snapshot of %s: %w", j.real, err)
				}
			}
			files[j.real] = fs
		case !fs.hasSnap && fs.Absent:
			fs.hasSnap = true
		case !fs.hasSnap && fs.Snapshot != "":
			if b, err := os.ReadFile(fs.Snapshot); err == nil {
				fs.snap, fs.hasSnap = b, true
			}
		}
		if fa := a.Files[j.real]; fa != nil && fa.Hash != hashOf(cur, exists) {
			fa.Dirty = true
		}
		for _, sl := range j.slots {
			if fs.Slots[sl.ID()] == nil {
				o := capture(w, sl)
				fs.Slots[sl.ID()] = &o
			}
		}
	}
	if !e.dry {
		if err := e.s.saveStash(); err != nil {
			return nil, err
		}
	}
	rep := &Report{Agent: e.agent}
	var paths []string
	for _, j := range jobs {
		paths = append(paths, e.at(j.real))
	}
	err := edit.Atomically(func() error {
		for _, j := range jobs {
			fr, err := e.syncFile(j, restore)
			if err != nil {
				return err
			}
			if fr != nil {
				rep.Files = append(rep.Files, *fr)
			}
		}
		return nil
	}, paths...)
	if err != nil {
		if !e.dry {
			err = errors.Join(err, e.s.unsaveStash())
		}
		return nil, err
	}
	return rep, nil
}

func (e *engine) syncFile(j *job, restore bool) (*FileResult, error) {
	files, a := e.stashFiles(), e.appliedAgent()
	fs, fa := files[j.real], a.Files[j.real]
	if fs == nil && fa == nil {
		return nil, nil
	}
	w := e.at(j.real)
	cur, exists, err := readFile(w)
	if err != nil {
		return nil, err
	}
	desired := map[string]int{} // slot → its last op
	for i, sl := range j.slots {
		desired[sl.ID()] = i
	}
	res := &FileResult{Path: j.real}
	var revert, keep []string
	keepWhole := false // a Keep whole file can't be written again from its hash
	if fa != nil {
		for _, id := range sortedKeys(fa.Slots) {
			if _, ok := desired[id]; ok {
				continue
			}
			if fa.Slots[id].Keep {
				keep = append(keep, id)
				keepWhole = keepWhole || fa.Slots[id].Kind == WholeFile
			} else {
				revert = append(revert, id)
			}
		}
	}
	touch := len(j.ops) > 0 || len(revert) > 0
	if touch && exists && !e.dry {
		if err := rollingBackup(e.agent, j.real, cur); err != nil {
			return nil, fmt.Errorf("backup of %s: %w", j.real, err)
		}
	}
	clean := fa != nil && !fa.Dirty && fa.Hash == hashOf(cur, exists) && fs != nil && fs.hasSnap && !keepWhole
	rebased := false
	if len(revert) > 0 && clean {
		if fs.Absent {
			if exists {
				if err := edit.Remove(w); err != nil {
					return nil, err
				}
			}
		} else if err := edit.WriteAtomic(w, fs.snap); err != nil {
			return nil, err
		}
		for _, id := range keep {
			if o := fs.Slots[id]; restore && o != nil && o.Existed {
				continue // the original, back with the file, is what stays
			}
			if err := putWritten(w, fa.Slots[id]); err != nil {
				return nil, err
			}
		}
		rebased, res.Exact, res.Reverted = true, true, revert
	} else {
		for _, id := range revert {
			var o *Original
			if fs != nil {
				o = fs.Slots[id]
			}
			if o == nil || !unchanged(w, fa.Slots[id]) {
				res.Left = append(res.Left, id)
				continue
			}
			if err := putOriginal(w, o); err != nil {
				return nil, err
			}
			res.Reverted = append(res.Reverted, id)
		}
	}
	res.Kept = keep
	for i, op := range j.ops {
		var prev []string
		if fa != nil && !rebased {
			if wr := fa.Slots[j.slots[i].ID()]; wr != nil {
				prev = wr.Keys
			}
		}
		if err := apply(w, op, prev); err != nil {
			return nil, err
		}
	}
	if touch {
		if err := verify(w, j.format); err != nil {
			return nil, err
		}
		if b, ok, _ := readFile(w); ok && fs != nil && fs.Absent && emptyish(b) {
			if err := edit.Remove(w); err != nil {
				return nil, err
			}
		}
	}
	after, existsAfter, err := readFile(w)
	if err != nil {
		return nil, err
	}
	res.Removed = exists && !existsAfter && fs != nil && fs.Absent

	slots := map[string]*written{}
	for _, id := range keep {
		slots[id] = fa.Slots[id]
	}
	for id, i := range desired {
		sl, op := j.slots[i], j.ops[i]
		raw, ok := readSlot(w, sl)
		wr := &written{Slot: sl, Deleted: !ok, Raw: raw, Keep: op.Keep}
		if op.Kind == plan.SetTable && !op.Whole {
			for _, kv := range op.KVs {
				wr.Keys = append(wr.Keys, kv.Path)
			}
		}
		slots[id] = wr
	}
	if fs != nil {
		for id := range fs.Slots {
			if slots[id] == nil {
				delete(fs.Slots, id)
			}
		}
	}
	if restore || len(slots) == 0 {
		delete(a.Files, j.real)
		delete(files, j.real)
	} else {
		a.Files[j.real] = &fileApplied{
			Format: j.format,
			Hash:   hashOf(after, existsAfter),
			Dirty:  fa != nil && fa.Dirty && !rebased,
			Slots:  slots,
		}
	}
	if !touch && len(keep) == 0 {
		return nil, nil
	}
	return res, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
