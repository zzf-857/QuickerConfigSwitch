package safekeep

import (
	"slices"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/edit"
	"github.com/zzf-857/quickerconfigswitch/internal/plan"
)

// Older Codex plans kept the entire provider, including its credentials.
// Migrate only recorded QCS tables whose merge explicitly wrote the key;
// a matching table name alone is not evidence that the key belongs to us.
func migrateKeptCredentials(agent string, f *fileApplied) {
	if agent != "codex" || f.Format != plan.TOML {
		return
	}
	for _, wr := range f.Slots {
		if wr.Kind != TOMLTable || !wr.Keep || wr.Deleted || !strings.HasPrefix(wr.Table, "model_providers.qcs-") {
			continue
		}
		// Token-only restore policies also need upgrading: credentials can
		// be supplied through Authorization, X-Api-Key or Cookie headers.
		for _, key := range []string{"experimental_bearer_token", "http_headers"} {
			if slices.Contains(wr.Keys, key) && !slices.Contains(wr.RestoreKeys, key) {
				wr.RestoreKeys = append(wr.RestoreKeys, key)
			}
		}
	}
}

func ownedRestoreKeys(op plan.Op) []string {
	var keys []string
	for _, key := range op.RestoreKeys {
		for _, kv := range op.KVs {
			if kv.Path == key && !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// seedRestoreKeys recovers per-key originals from historical table records.
// Current records already have them, including a fresh original captured
// if an inactive provider was selected again.
func seedRestoreKeys(fs *fileStash, fa *fileApplied) error {
	if fs == nil || fa == nil {
		return nil
	}
	for id, wr := range fa.Slots {
		table := fs.Slots[id]
		if table == nil {
			continue
		}
		for _, key := range wr.RestoreKeys {
			if !slices.Contains(wr.Keys, key) || table.Restore[key] != nil {
				continue
			}
			o := &Original{Slot: Slot{Kind: TOMLKey, Table: wr.Table, Key: key, LiteralKeys: wr.LiteralKeys}}
			if table.Existed {
				var err error
				o.Raw, o.Existed, err = edit.TOMLKeyRaw(table.Raw, wr.Table, key)
				if err != nil {
					return err
				}
			}
			if table.Restore == nil {
				table.Restore = map[string]*Original{}
			}
			table.Restore[key] = o
		}
	}
	return nil
}

func captureRestoreKeys(path string, op plan.Op, table *Original) {
	for _, key := range ownedRestoreKeys(op) {
		if table.Restore[key] != nil {
			continue
		}
		o := capture(path, Slot{Kind: TOMLKey, Table: op.Table, Key: key, LiteralKeys: table.LiteralKeys})
		o.Created = "" // the table must survive even when this was its only key
		if table.Restore == nil {
			table.Restore = map[string]*Original{}
		}
		table.Restore[key] = &o
	}
}

func keptKeyRestores(path string, fs *fileStash, wr *written, keys []string, res *FileResult) []*Original {
	var out []*Original
	for _, key := range keys {
		if !slices.Contains(wr.Keys, key) {
			continue
		}
		sl := Slot{Kind: TOMLKey, Table: wr.Table, Key: key, LiteralKeys: wr.LiteralKeys}
		var o *Original
		if fs != nil && fs.Slots[wr.ID()] != nil {
			o = fs.Slots[wr.ID()].Restore[key]
		}
		raw, ok, err := edit.TOMLKeyRaw(wr.Raw, wr.Table, key)
		deleted := slices.Contains(wr.DeletedKeys, key)
		if err != nil || !ok && !deleted || o == nil || !unchanged(path, &written{Slot: sl, Raw: raw, Deleted: deleted}) {
			res.Left = append(res.Left, sl.ID())
			continue
		}
		out = append(out, o)
	}
	return out
}

func retireRestoreKeys(fs *fileStash, wr *written, keys []string) {
	for _, key := range keys {
		wr.Keys = slices.DeleteFunc(wr.Keys, func(k string) bool { return k == key })
		wr.RestoreKeys = slices.DeleteFunc(wr.RestoreKeys, func(k string) bool { return k == key })
		wr.DeletedKeys = slices.DeleteFunc(wr.DeletedKeys, func(k string) bool { return k == key })
		if fs != nil && fs.Slots[wr.ID()] != nil {
			delete(fs.Slots[wr.ID()].Restore, key)
		}
	}
}
