package safekeep

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
)

type tableOperation struct {
	op          plan.Op
	literalKeys bool
}

// Magpie's SetTOMLTable merges an Inline value into an existing child table
// rather than defining that key twice (internal/edit/toml.go). Track that
// child's keys separately here so its original values and later user edits
// participate in safekeep's restore and drift checks too.
func tableOperations(path string, op plan.Op) []tableOperation {
	if op.Kind != plan.SetTable || op.Whole {
		return []tableOperation{{op: op}}
	}
	parent := op
	parent.KVs = nil
	parent.RestoreKeys = slices.Clone(op.RestoreKeys)
	parts := []tableOperation{{}}
	for _, kv := range op.KVs {
		in, inline := kv.Value.(edit.Inline)
		child := op.Table + "." + kv.Path
		if _, exists := edit.GetTOMLTableBlock(path, child); !inline || !exists {
			parent.KVs = append(parent.KVs, kv)
			continue
		}
		next := plan.Op{Kind: plan.SetTable, Table: child, KVs: slices.Clone(in), Keep: op.Keep}
		if slices.Contains(op.RestoreKeys, kv.Path) {
			for _, header := range in {
				next.RestoreKeys = append(next.RestoreKeys, header.Path)
			}
		}
		parent.RestoreKeys = slices.DeleteFunc(parent.RestoreKeys, func(key string) bool { return key == kv.Path })
		parts = append(parts, tableOperation{op: next, literalKeys: true})
	}
	parts[0].op = parent
	return parts
}

// A user may spell previously managed inline headers as a child table.
// Retain the recorded originals for unchanged owned values; changed or new
// user values become the originals of a subsequent explicit switch. Never
// capture an old QCS credential as a new original just because its spelling
// moved. Inline maps that have not changed shape keep their whole-key policy.
func migrateHeaderTables(path string, fs *fileStash, fa *fileApplied) error {
	if fs == nil || fa == nil || fa.Format != plan.TOML {
		return nil
	}
	for _, id := range sortedKeys(fa.Slots) {
		wr := fa.Slots[id]
		if wr.Kind != TOMLTable || wr.LiteralKeys || !wr.Keep || !strings.HasPrefix(wr.Table, "model_providers.qcs-") ||
			!slices.Contains(wr.RestoreKeys, "http_headers") {
			continue
		}
		child := Slot{Kind: TOMLTable, Table: wr.Table + ".http_headers", LiteralKeys: true}
		if _, exists := edit.GetTOMLTableBlock(path, child.Table); !exists {
			continue
		}
		raw, exists, err := edit.TOMLKeyRaw(wr.Raw, wr.Table, "http_headers")
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		was, err := headerValues(raw)
		if err != nil {
			return err
		}
		original := fs.Slots[id]
		if original == nil || original.Restore["http_headers"] == nil {
			return fmt.Errorf("%s: missing original headers", wr.Table)
		}
		before := map[string]string{}
		if o := original.Restore["http_headers"]; o.Existed {
			before, err = headerValues(o.Raw)
			if err != nil {
				return err
			}
		}
		if fa.Slots[child.ID()] != nil || fs.Slots[child.ID()] != nil {
			return fmt.Errorf("%s: overlapping header ownership", child.Table)
		}
		o := &Original{Slot: child, Existed: true, Raw: headerBlock(child.Table, before), Restore: map[string]*Original{}}
		next := &written{Slot: child, Raw: headerBlock(child.Table, was), Keep: true}
		owned := map[string]bool{}
		for key := range was {
			owned[key] = true
		}
		for key := range before {
			owned[key] = true
		}
		for _, key := range sortedKeys(owned) {
			sl := Slot{Kind: TOMLKey, Table: child.Table, Key: key, LiteralKeys: true}
			value := capture(path, sl)
			last, writtenBefore := was[key]
			if !writtenBefore {
				next.DeletedKeys = append(next.DeletedKeys, key)
			}
			if unchanged(path, &written{Slot: sl, Raw: edit.TOMLLiteral(last), Deleted: !writtenBefore}) {
				old, existed := before[key]
				value = Original{Slot: sl, Existed: existed}
				if existed {
					value.Raw = edit.TOMLLiteral(old)
				}
			}
			o.Restore[key] = &value
			next.Keys = append(next.Keys, key)
			next.RestoreKeys = append(next.RestoreKeys, key)
		}
		fs.Slots[child.ID()], fa.Slots[child.ID()] = o, next
		retireRestoreKeys(fs, wr, []string{"http_headers"})
	}
	return nil
}

func headerValues(raw string) (map[string]string, error) {
	var doc struct {
		Headers map[string]string `toml:"headers"`
	}
	if err := toml.Unmarshal([]byte("headers = "+raw), &doc); err != nil || doc.Headers == nil {
		return nil, fmt.Errorf("recorded http_headers is not a string table")
	}
	return doc.Headers, nil
}

func headerBlock(table string, values map[string]string) string {
	lines := []string{"[" + table + "]"}
	for _, key := range sortedKeys(values) {
		lines = append(lines, edit.TOMLLiteral(key)+" = "+edit.TOMLLiteral(values[key]))
	}
	return strings.Join(lines, "\n")
}
