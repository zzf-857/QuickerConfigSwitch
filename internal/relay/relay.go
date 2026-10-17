// Package relay forwards Codex's HTTP requests to its official backend or a
// Responses-compatible provider. The caller must serve the handler on loopback.
// ChatGPT credentials are only forwarded with their original request; the
// relay never reads or saves login files.
package relay

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/zzf-857/quickerconfigswitch/internal/codexcat"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
)

const (
	codexPath       = "/backend-api/codex"
	officialURL     = "https://chatgpt.com/backend-api/codex"
	maxRequestBytes = 32 << 20 // includes image inputs
	maxModelsBytes  = 8 << 20
)

// Options configure one provider's relay. All sends plain model IDs to the
// provider too; otherwise only its listed provider/model aliases use it.
type Options struct {
	Provider    provider.Provider
	All         bool
	OfficialURL string            // default: https://chatgpt.com/backend-api/codex
	CodexHome   string            // optional models_cache.json for codexcat
	Transport   http.RoundTripper // nil uses http.DefaultTransport
}

type server struct {
	prefix   string
	models   map[string]bool
	all      bool
	official http.Handler
	catalog  http.Handler
	custom   http.Handler
}

// New validates the provider and returns a handler. It copies the provider's
// routing data so concurrent requests cannot change the caller's configuration.
// HTTP redirects are never followed with either API key. Provider redirects
// are refused so Codex cannot follow them with its original account headers.
func New(o Options) (http.Handler, error) {
	p := o.Provider
	if err := provider.ValidID(p.ID); err != nil {
		return nil, err
	}
	if !p.APIs.Responses {
		return nil, errors.New("relay requires a provider with the Responses API")
	}
	base, err := parseURL(p.BaseURL)
	if err != nil {
		return nil, errors.New("relay provider baseURL must be an http(s) URL without credentials, query or fragment")
	}
	if o.OfficialURL == "" {
		o.OfficialURL = officialURL
	}
	official, err := parseURL(o.OfficialURL)
	if err != nil {
		return nil, errors.New("relay official URL must be an http(s) URL without credentials, query or fragment")
	}
	headers := make(http.Header, len(p.Headers))
	for k, v := range p.Headers {
		if !headerName(k) || strings.ContainsAny(v, "\r\n\x00") {
			return nil, errors.New("relay provider has an invalid header")
		}
		if hopHeader(k) || strings.EqualFold(k, "Host") || strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Content-Encoding") {
			return nil, fmt.Errorf("relay provider header %q cannot be overridden", k)
		}
		headers.Set(k, v)
	}
	if strings.ContainsAny(p.APIKey, "\r\n\x00") {
		return nil, errors.New("relay provider has an invalid API key")
	}
	s := &server{prefix: p.ID + "/", models: map[string]bool{}, all: o.All}
	models := make([]codexcat.Model, 0, len(p.Models))
	for _, m := range p.Models {
		if m.ID == "" || strings.ContainsAny(m.ID, " \t\r\n") || s.models[m.ID] {
			return nil, errors.New("relay provider has an invalid or duplicate model ID")
		}
		s.models[m.ID] = true
		models = append(models, codexcat.Model{Slug: m.ID, Name: m.Name,
			Efforts: m.Efforts, Fast: m.Fast, Context: m.Context, Images: m.Images})
	}
	// Resolve the real slug first: giving Catalog an alias would discard the
	// model's own instructions and capabilities in favor of a generic entry.
	raw, err := codexcat.Catalog(models, o.CodexHome)
	if err != nil {
		return nil, fmt.Errorf("relay catalog: %w", err)
	}
	var catalog struct {
		Models []map[string]json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return nil, fmt.Errorf("relay catalog: %w", err)
	}
	aliases := make([]json.RawMessage, 0, len(catalog.Models))
	for i, entry := range catalog.Models {
		entry["slug"], _ = json.Marshal(s.prefix + p.Models[i].ID)
		var name string
		if json.Unmarshal(entry["display_name"], &name) != nil || name == "" {
			name = p.Models[i].ID
		}
		entry["display_name"], _ = json.Marshal(s.prefix + name)
		raw, err := json.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("relay catalog: %w", err)
		}
		aliases = append(aliases, raw)
	}
	toOfficial := func(r *httputil.ProxyRequest) {
		setTarget(r, official, strings.TrimPrefix(r.In.URL.Path, codexPath))
	}
	s.official = proxy(o.Transport, toOfficial, nil)
	s.catalog = proxy(o.Transport, func(r *httputil.ProxyRequest) {
		toOfficial(r)
		// The response includes local entries, so official cache validators and
		// byte ranges cannot describe it. Request a whole uncompressed list.
		for _, k := range []string{"If-None-Match", "If-Modified-Since", "If-Match", "If-Unmodified-Since", "If-Range", "Range"} {
			r.Out.Header.Del(k)
		}
		r.Out.Header.Set("Accept-Encoding", "identity")
	}, func(r *http.Response) error { return mergeModels(r, aliases) })
	s.custom = proxy(o.Transport, func(r *httputil.ProxyRequest) {
		setTarget(r, base, strings.TrimPrefix(r.In.URL.Path, codexPath))
		// The Responses endpoint needs no ChatGPT query parameters. Keep
		// account data out of the provider's URL as well as its headers.
		r.Out.URL.RawQuery, r.Out.URL.ForceQuery = "", false
		// Start fresh so future ChatGPT-specific headers cannot silently carry
		// account data to an unrelated upstream. Provider headers are explicit.
		h := make(http.Header)
		for _, k := range []string{"Content-Type", "Accept", "OpenAI-Beta"} {
			if values := r.Out.Header.Values(k); len(values) > 0 {
				h[k] = append([]string(nil), values...)
			}
		}
		for k, v := range headers {
			h[k] = append([]string(nil), v...)
		}
		if p.APIKey != "" {
			h.Set("Authorization", "Bearer "+p.APIKey)
		}
		r.Out.Header = h
		r.Out.Trailer = nil
	}, func(r *http.Response) error {
		if r.StatusCode >= 300 && r.StatusCode <= 399 {
			return errors.New("relay provider redirects are not supported")
		}
		return nil
	})
	return s, nil
}

func parseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("invalid upstream URL")
	}
	return u, nil
}

func headerName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c) {
			continue
		}
		return false
	}
	return true
}

func hopHeader(k string) bool {
	switch strings.ToLower(k) {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}

func setTarget(r *httputil.ProxyRequest, target *url.URL, suffix string) {
	r.Out.URL.Scheme, r.Out.URL.Host = target.Scheme, target.Host
	r.Out.URL.User, r.Out.URL.Opaque = nil, ""
	r.Out.URL.Path = strings.TrimRight(target.Path, "/") + suffix
	r.Out.URL.RawPath = ""
	if r.In.URL.RawPath != "" || target.RawPath != "" {
		r.Out.URL.RawPath = strings.TrimRight(target.EscapedPath(), "/") + strings.TrimPrefix(r.In.URL.EscapedPath(), codexPath)
	}
	r.Out.URL.RawQuery = r.In.URL.RawQuery
	r.Out.Host = target.Host
}

func proxy(transport http.RoundTripper, rewrite func(*httputil.ProxyRequest), modify func(*http.Response) error) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: rewrite, Transport: transport, ModifyResponse: modify,
		FlushInterval: -1, // flush SSE events immediately, including custom transports
		ErrorLog:      log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Transport errors can contain URLs, keys or login headers.
			http.Error(w, "relay upstream unavailable", http.StatusBadGateway)
		},
	}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !localHost(r.Host) || !sameOrigin(r) || strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		http.Error(w, "relay requires a local same-origin request", http.StatusForbidden)
		return
	}
	if r.Header.Get("Upgrade") != "" {
		http.Error(w, "relay requires HTTP; WebSocket is not supported", http.StatusUpgradeRequired)
		return
	}
	switch r.URL.Path {
	case codexPath + "/models":
		if !method(w, r, http.MethodGet) {
			return
		}
		s.catalog.ServeHTTP(w, r)
	case codexPath + "/responses", codexPath + "/responses/compact":
		if !method(w, r, http.MethodPost) {
			return
		}
		s.responses(w, r)
	default:
		if !strings.HasPrefix(r.URL.Path, codexPath+"/") {
			http.NotFound(w, r)
			return
		}
		if !method(w, r, http.MethodGet, http.MethodHead, http.MethodPost) {
			return
		}
		s.official.ServeHTTP(w, r)
	}
}

func method(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for _, m := range allowed {
		if r.Method == m {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func localHost(host string) bool {
	h := host
	if name, _, err := net.SplitHostPort(host); err == nil {
		h = name
	}
	h = strings.Trim(h, "[]")
	return strings.EqualFold(h, "localhost") || h == "127.0.0.1" || h == "::1"
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return err == nil && u.Scheme == scheme && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && strings.EqualFold(u.Host, r.Host)
}

func (s *server) responses(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	wire, err := io.ReadAll(r.Body)
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "cannot read relay request body", status)
		return
	}
	body, err := decodeBody(wire, strings.Join(r.Header.Values("Content-Encoding"), ","))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		} else if errors.Is(err, errEncoding) {
			status = http.StatusUnsupportedMediaType
		}
		http.Error(w, "cannot decode relay request body", status)
		return
	}
	var fields map[string]json.RawMessage
	var model string
	if json.Unmarshal(body, &fields) != nil || json.Unmarshal(fields["model"], &model) != nil || strings.TrimSpace(model) == "" {
		http.Error(w, "request must be a JSON object with a model string", http.StatusBadRequest)
		return
	}
	custom := s.all
	if strings.HasPrefix(model, s.prefix) {
		model = strings.TrimPrefix(model, s.prefix)
		if !s.models[model] {
			http.Error(w, "unknown relay provider model", http.StatusBadRequest)
			return
		}
		custom = true
		fields["model"], _ = json.Marshal(model)
		body, err = json.Marshal(fields)
		if err != nil {
			http.Error(w, "cannot rewrite relay request", http.StatusBadRequest)
			return
		}
	}
	if custom {
		r.Header.Del("Content-Encoding")
	} else {
		// Codex uses zstd for ChatGPT Responses. Inspect a bounded decoded
		// copy to route it, then preserve its exact wire bytes for official.
		body = wire
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	if custom {
		s.custom.ServeHTTP(w, r)
	} else {
		s.official.ServeHTTP(w, r)
	}
}

var (
	errBodyTooLarge = errors.New("decoded request body is too large")
	errEncoding     = errors.New("unsupported request encoding")
)

// decodeBody bounds both decompressed output and zstd's working window.
// The caller has already bounded the encoded body, including extra frames.
func decodeBody(wire []byte, encoding string) ([]byte, error) {
	var reader io.Reader
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		return wire, nil
	case "zstd":
		decoder, err := zstd.NewReader(bytes.NewReader(wire), zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxMemory(maxRequestBytes), zstd.WithDecoderMaxWindow(maxRequestBytes))
		if err != nil {
			return nil, err
		}
		defer decoder.Close()
		reader = decoder
	case "gzip":
		decoder, err := gzip.NewReader(bytes.NewReader(wire))
		if err != nil {
			return nil, err
		}
		defer decoder.Close()
		reader = decoder
	default:
		return nil, errEncoding
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxRequestBytes+1))
	if len(body) > maxRequestBytes || errors.Is(err, zstd.ErrDecoderSizeExceeded) || errors.Is(err, zstd.ErrWindowSizeExceeded) {
		return nil, errBodyTooLarge
	}
	return body, err
}

func mergeModels(r *http.Response, aliases []json.RawMessage) error {
	if r.StatusCode != http.StatusOK {
		return nil
	}
	defer r.Body.Close()
	var reader io.Reader = r.Body
	switch strings.ToLower(r.Header.Get("Content-Encoding")) {
	case "", "identity":
	case "gzip":
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return err
		}
		defer gz.Close()
		reader = gz
	default:
		return errors.New("unsupported catalog encoding")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxModelsBytes+1))
	if err != nil || len(body) > maxModelsBytes {
		return errors.New("cannot read official catalog")
	}
	var fields map[string]json.RawMessage
	var models []json.RawMessage
	if json.Unmarshal(body, &fields) != nil || json.Unmarshal(fields["models"], &models) != nil || bytes.Equal(bytes.TrimSpace(fields["models"]), []byte("null")) {
		return errors.New("invalid official catalog")
	}
	models = append(models, aliases...)
	fields["models"], err = json.Marshal(models)
	if err != nil {
		return err
	}
	body, err = json.Marshal(fields)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cache-Control", "no-store")
	for _, k := range []string{"Content-Encoding", "ETag", "Last-Modified", "Content-MD5", "Digest", "Content-Range", "Accept-Ranges"} {
		r.Header.Del(k)
	}
	return nil
}
