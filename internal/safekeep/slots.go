package safekeep

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/tidwall/jsonc"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
)

// Slot kinds.
const (
	TOMLTop   = "toml-top"   // a top-level key: Key
	TOMLKey   = "toml-key"   // Table.Key
	TOMLTable = "toml-table" // a whole table: Table
	JSONPath  = "json"       // a dot path: Key
	WholeFile = "file"       // the whole file
)

// Slot is one place in a file an Op changes.
type Slot struct {
	Kind  string `json:"kind"`
	Table string `json:"table,omitempty"`
	Key   string `json:"key,omitempty"`
	// LiteralKeys distinguishes header names such as X.Trace from dotted
	// TOML paths. On a table it applies to that table's owned keys.
	LiteralKeys bool `json:"literal_keys,omitempty"`
}

// ID is the slot's name: toml-top:<key>, toml-key:<table>:<key>,
// toml-table:<table>, json:<path> or file.
func (s Slot) ID() string {
	switch s.Kind {
	case TOMLTop, JSONPath:
		return s.Kind + ":" + s.Key
	case TOMLKey:
		return s.Kind + ":" + s.Table + ":" + s.Key
	case TOMLTable:
		return s.Kind + ":" + s.Table
	}
	return s.Kind
}

// slotOf is the slot op changes in a file of format f.
func slotOf(f plan.Format, op plan.Op) (Slot, error) {
	toml := f == plan.TOML
	need := func(ok bool, s Slot, what string) (Slot, error) {
		if !ok {
			return Slot{}, fmt.Errorf("%s op on a %s file", op.Kind, f)
		}
		if (s.Kind != TOMLTable && s.Kind != WholeFile && s.Key == "") || ((s.Kind == TOMLKey || s.Kind == TOMLTable) && s.Table == "") {
			return Slot{}, fmt.Errorf("%s op without %s", op.Kind, what)
		}
		return s, nil
	}
	switch op.Kind {
	case plan.SetTop, plan.DelTop:
		return need(toml, Slot{Kind: TOMLTop, Key: op.Key}, "a key")
	case plan.SetTableKey, plan.DelTableKey:
		return need(toml, Slot{Kind: TOMLKey, Table: op.Table, Key: op.Key}, "a table and key")
	case plan.SetTable, plan.DelTable:
		return need(toml, Slot{Kind: TOMLTable, Table: op.Table}, "a table")
	case plan.SetJSON, plan.DelJSON:
		return need(f == plan.JSON, Slot{Kind: JSONPath, Key: op.Key}, "a key path")
	case plan.WriteFile:
		return Slot{Kind: WholeFile}, nil
	}
	return Slot{}, fmt.Errorf("unknown op %q", op.Kind)
}

// readFile is the file's bytes as they are on disk, byte order mark and
// all, through a symlink.
func readFile(path string) ([]byte, bool, error) {
	t, err := edit.Target(path)
	if err != nil {
		return nil, false, err
	}
	b, err := os.ReadFile(t)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

func hashOf(b []byte, exists bool) string {
	if !exists {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// readSlot is the slot's value in the file at path as written; a whole
// file is its sha256.
func readSlot(path string, s Slot) (string, bool) {
	switch s.Kind {
	case TOMLTop:
		return edit.GetTOMLTopRaw(path, s.Key)
	case TOMLKey:
		return edit.GetTOMLKeyRaw(path, s.Table, s.Key)
	case TOMLTable:
		return edit.GetTOMLTableBlock(path, s.Table)
	case JSONPath:
		return edit.GetJSONRaw(path, s.Key)
	case WholeFile:
		b, ok, _ := readFile(path)
		if !ok {
			return "", false
		}
		return "sha256:" + hashOf(b, true), true
	}
	return "", false
}

// capture is the slot's original in the file at path.
func capture(path string, s Slot) Original {
	o := Original{Slot: s}
	if s.Kind == WholeFile {
		b, ok, _ := readFile(path)
		o.Existed = ok
		if ok {
			o.Raw = base64.StdEncoding.EncodeToString(b)
		}
		return o
	}
	o.Raw, o.Existed = readSlot(path, s)
	if o.Existed {
		return o
	}
	switch s.Kind {
	case TOMLKey:
		if _, ok := edit.GetTOMLTableBlock(path, s.Table); !ok {
			o.Created = s.Table
		}
	case JSONPath:
		parts := splitPath(s.Key)
		for i := 1; i < len(parts); i++ {
			p := strings.Join(parts[:i], ".")
			if _, ok := edit.GetJSONRaw(path, p); !ok {
				o.Created = p
				break
			}
		}
	}
	return o
}

// splitPath splits a JSON key path at its unescaped dots.
func splitPath(p string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			i++
		case '.':
			parts = append(parts, p[start:i])
			start = i + 1
		}
	}
	return append(parts, p[start:])
}

// same reports whether two raw values of a slot are the same value as
// written: JSON ignoring whitespace, the rest ignoring line ends.
func same(s Slot, a, b string) bool {
	if s.Kind == JSONPath {
		var x, y bytes.Buffer
		if json.Compact(&x, []byte(a)) == nil && json.Compact(&y, []byte(b)) == nil {
			return x.String() == y.String()
		}
	}
	norm := func(v string) string { return strings.TrimRight(strings.ReplaceAll(v, "\r\n", "\n"), "\r\n \t") }
	return norm(a) == norm(b)
}

// unchanged reports whether the slot still holds what wr says we wrote.
func unchanged(path string, wr *written) bool {
	raw, ok := readSlot(path, wr.Slot)
	if wr.Deleted {
		return !ok
	}
	return ok && same(wr.Slot, raw, wr.Raw)
}

// putOriginal puts o back in the file at path: the value as it was, or
// no value when there was none.
func putOriginal(path string, o *Original) error {
	if !o.Existed {
		if err := removeSlot(path, o.Slot); err != nil {
			return err
		}
		return dropCreated(path, o)
	}
	if err := ensurePrivateFile(path); err != nil {
		return err
	}
	if o.Kind == WholeFile {
		b, err := base64.StdEncoding.DecodeString(o.Raw)
		if err != nil {
			return fmt.Errorf("%s: stashed original: %w", path, err)
		}
		return edit.WriteAtomic(path, b)
	}
	return setRaw(path, o.Slot, o.Raw)
}

// putWritten writes what wr records again (a Keep slot after the file was
// put back whole).
func putWritten(path string, wr *written) error {
	if wr.Deleted {
		return removeSlot(path, wr.Slot)
	}
	if err := ensurePrivateFile(path); err != nil {
		return err
	}
	if wr.Kind == WholeFile {
		return fmt.Errorf("%s: a whole file can't be written again from its hash", path)
	}
	return setRaw(path, wr.Slot, wr.Raw)
}

func setRaw(path string, s Slot, raw string) error {
	switch s.Kind {
	case TOMLTop:
		return edit.SetTOMLTopPreserving(path, edit.KV{Path: s.Key, Value: edit.Raw(raw)})
	case TOMLKey:
		if s.LiteralKeys {
			return edit.SetTOMLKeyQuoted(path, s.Table, s.Key, edit.Raw(raw))
		}
		return edit.SetTOMLKey(path, s.Table, s.Key, edit.Raw(raw))
	case TOMLTable:
		return edit.PutTOMLTableBlock(path, s.Table, raw)
	case JSONPath:
		return edit.SetJSON(path, edit.KV{Path: s.Key, Value: json.RawMessage(raw)})
	}
	return fmt.Errorf("unknown slot %q", s.ID())
}

func removeSlot(path string, s Slot) error {
	switch s.Kind {
	case TOMLTop:
		return edit.DelTOMLTop(path, s.Key)
	case TOMLKey:
		return edit.DelTOMLTableKey(path, s.Table, s.Key)
	case TOMLTable:
		return edit.PutTOMLTableBlock(path, s.Table, "")
	case JSONPath:
		return edit.DelJSON(path, s.Key)
	case WholeFile:
		if _, ok, _ := readFile(path); ok {
			return edit.Remove(path)
		}
		return nil
	}
	return fmt.Errorf("unknown slot %q", s.ID())
}

// dropCreated takes away what o.Created names when it is left empty.
func dropCreated(path string, o *Original) error {
	if o.Created == "" {
		return nil
	}
	switch o.Kind {
	case TOMLKey:
		block, ok := edit.GetTOMLTableBlock(path, o.Created)
		if !ok {
			return nil
		}
		for _, line := range strings.Split(block, "\n")[1:] {
			if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
				return nil
			}
		}
		return edit.PutTOMLTableBlock(path, o.Created, "")
	case JSONPath:
		raw, ok := edit.GetJSONRaw(path, o.Created)
		var c bytes.Buffer
		if ok && json.Compact(&c, []byte(raw)) == nil && c.String() == "{}" {
			return edit.DelJSON(path, o.Created)
		}
	}
	return nil
}

// apply carries out op on the file at path. prev are the keys an earlier
// merging SetTable of the same table set.
func apply(path string, op plan.Op, prev []string, literalKeys bool) error {
	if err := ensurePrivateFile(path); err != nil {
		return err
	}
	switch op.Kind {
	case plan.SetTop:
		return edit.SetTOMLTopPreserving(path, edit.KV{Path: op.Key, Value: op.Value})
	case plan.DelTop:
		return edit.DelTOMLTop(path, op.Key)
	case plan.SetTableKey:
		return edit.SetTOMLKey(path, op.Table, op.Key, op.Value)
	case plan.DelTableKey:
		return edit.DelTOMLTableKey(path, op.Table, op.Key)
	case plan.SetTable:
		if _, ok := edit.GetTOMLTableBlock(path, op.Table); op.Whole || !ok {
			lines := []string{"[" + op.Table + "]"}
			for _, kv := range op.KVs {
				key := kv.Path
				if literalKeys {
					key = edit.TOMLLiteral(key)
				}
				lines = append(lines, key+" = "+edit.TOMLLiteral(kv.Value))
			}
			return edit.PutTOMLTableBlock(path, op.Table, strings.Join(lines, "\n"))
		}
		set := map[string]bool{}
		for _, kv := range op.KVs {
			set[kv.Path] = true
		}
		for _, k := range prev {
			if !set[k] {
				if err := edit.DelTOMLTableKey(path, op.Table, k); err != nil {
					return err
				}
			}
		}
		for _, kv := range op.KVs {
			set := edit.SetTOMLKey
			if literalKeys {
				set = edit.SetTOMLKeyQuoted
			}
			if err := set(path, op.Table, kv.Path, kv.Value); err != nil {
				return err
			}
		}
		return nil
	case plan.DelTable:
		return edit.PutTOMLTableBlock(path, op.Table, "")
	case plan.SetJSON:
		return edit.SetJSON(path, edit.KV{Path: op.Key, Value: op.Value})
	case plan.DelJSON:
		return edit.DelJSON(path, op.Key)
	case plan.WriteFile:
		return edit.WriteAtomic(path, op.Data)
	}
	return fmt.Errorf("unknown op %q", op.Kind)
}

// Existing modes belong to the user. A new file may contain a provider key,
// so make an empty private file before the editor writes any content. The
// surrounding transaction removes this placeholder if a later edit fails.
func ensurePrivateFile(path string) error {
	_, exists, err := readFile(path)
	if err != nil || exists {
		return err
	}
	if err := edit.WriteAtomic(path, nil); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// verify checks that the file at path still parses as its format.
func verify(path string, f plan.Format) error {
	b, err := edit.Read(path)
	if err != nil || b == nil {
		return err
	}
	switch f {
	case plan.TOML:
		var m map[string]any
		if err := toml.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("%s: the result is not valid TOML: %w", path, err)
		}
	case plan.JSON:
		if len(bytes.TrimSpace(b)) > 0 && !json.Valid(jsonc.ToJSON(b)) {
			return fmt.Errorf("%s: the result is not valid JSON", path)
		}
	}
	return nil
}

// emptyish reports whether a file holds nothing worth keeping: only
// whitespace, or an empty JSON object.
func emptyish(b []byte) bool {
	t := bytes.TrimSpace(edit.TrimBOM(b))
	if len(t) == 0 {
		return true
	}
	var c bytes.Buffer
	return json.Compact(&c, jsonc.ToJSON(t)) == nil && c.String() == "{}"
}
