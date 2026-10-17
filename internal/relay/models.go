// Model-list timeout and cache fallback adapted from yetone/magpie (MIT,
// Copyright (c) 2026 yetone), commit 7660399a121af67db4efd56ecb6bd4fb04dc53f1:
// internal/gateway/codex_backend.go: codexModelsWait and codexModels;
// internal/codexcat/codexcat.go: CacheEntries.
// See third_party/magpie/LICENSE.

package relay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Codex gives model refreshes five seconds. A slow or unreachable official
// backend must leave time for a reply that still lists the local aliases.
const modelsWait = 3 * time.Second

func catalogHandler(upstream *httputil.ReverseProxy, aliases []json.RawMessage, home string, wait time.Duration) (http.Handler, error) {
	local, err := modelList(aliases)
	if err != nil || len(local) > maxModelsBytes {
		return nil, errors.New("relay model catalog is too large")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), wait)
		defer cancel()
		// The proxy's configuration is immutable. This request's error
		// handler keeps the parent context separate from the upstream budget:
		// timing out the upstream may fall back; losing the client must not.
		p := *upstream
		p.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
			if r.Context().Err() != nil {
				return
			}
			body, source := local, "local"
			if cached := cachedModels(home); len(cached) > 0 {
				if merged, err := modelList(append(cached, aliases...)); err == nil && len(merged) <= maxModelsBytes {
					body, source = merged, "cache"
				}
			}
			if r.Context().Err() != nil {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-QCS-Catalog-Source", source)
			w.Write(body)
		}
		p.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}

func modelList(models []json.RawMessage) ([]byte, error) {
	return json.Marshal(struct {
		Models []json.RawMessage `json:"models"`
	}{Models: models})
}

// cachedModels reads only the existing model entries, never auth.json or
// cache metadata. Namespaced entries belong to old relay catalogs and must
// not be reintroduced as official models. It preserves order and raw fields.
func cachedModels(home string) []json.RawMessage {
	if home == "" {
		return nil
	}
	path := filepath.Join(home, "models_cache.json")
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxModelsBytes+1))
	if err != nil || len(data) > maxModelsBytes {
		return nil
	}
	var cache struct {
		Models []json.RawMessage `json:"models"`
	}
	if json.Unmarshal(data, &cache) != nil {
		return nil
	}
	own := make([]json.RawMessage, 0, len(cache.Models))
	for _, entry := range cache.Models {
		var model struct {
			Slug string `json:"slug"`
		}
		if json.Unmarshal(entry, &model) != nil || strings.TrimSpace(model.Slug) == "" {
			return nil
		}
		if !strings.Contains(model.Slug, "/") {
			own = append(own, entry)
		}
	}
	return own
}
