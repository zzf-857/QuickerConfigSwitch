package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	fetchTimeout = 15 * time.Second
	maxListBytes = 8 << 20 // a model list is never this big
	maxErrBody   = 200     // bytes of an error response quoted in the error
)

// FetchModels lists the models p's upstream offers, from GET {BaseURL}/models.
// It sends the API key as a Bearer token and p.Headers with the request. When
// ctx has no deadline, the request gets 15 seconds. Models come back sorted by
// id, named by id, and Fast is set by DefaultFast when p speaks Responses.
//
// The returned error never contains the API key.
func FetchModels(ctx context.Context, p Provider) ([]Model, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		return nil, errNoBaseURL
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, fetchTimeout)
		defer cancel()
	}
	u := base + "/models"
	fail := func(format string, args ...any) error {
		return errors.New(scrub(fmt.Sprintf(format, args...), p.APIKey))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fail("GET %s: %v", u, err)
	}
	req.Header.Set("Accept", "application/json")
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fail("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes))
	if err != nil {
		return nil, fail("GET %s: reading response: %v", u, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		text := scrub(string(body), p.APIKey)
		if len(text) > maxErrBody {
			text = text[:maxErrBody]
		}
		return nil, fmt.Errorf("GET %s: %s: %s", u, resp.Status, strings.TrimSpace(text))
	}

	ids, err := parseModelIDs(body)
	if err != nil {
		return nil, fail("GET %s: unrecognized model list: %v", u, err)
	}
	models := make([]Model, 0, len(ids))
	for _, id := range ids {
		models = append(models, Model{
			ID:   id,
			Name: id,
			Fast: p.APIs.Responses && DefaultFast(id),
		})
	}
	return models, nil
}

// parseModelIDs reads the ids out of an OpenAI-style {"data":[...]} list, a
// top-level array, or {"models":[...]}. Entries are objects with "id", "name"
// or "slug", or plain strings. The result is sorted and without duplicates.
func parseModelIDs(body []byte) ([]string, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, errors.New("not JSON")
	}
	var items []any
	switch v := raw.(type) {
	case []any:
		items = v
	case map[string]any:
		if d, ok := v["data"].([]any); ok {
			items = d
		} else if m, ok := v["models"].([]any); ok {
			items = m
		} else {
			return nil, errors.New("no data or models array")
		}
	default:
		return nil, errors.New("neither an object nor an array")
	}

	set := make(map[string]bool, len(items))
	for _, it := range items {
		var id string
		switch x := it.(type) {
		case string:
			id = x
		case map[string]any:
			for _, k := range []string{"id", "name", "slug"} {
				if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
					id = s
					break
				}
			}
		}
		if id = strings.TrimSpace(id); id != "" {
			set[id] = true
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// scrub removes every occurrence of key from s.
func scrub(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "***")
}
