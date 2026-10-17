// Package webui serves the app's page and its JSON API: the window of the
// desktop app (internal/gui) and `qcs web` in a browser load the same
// handler.
//
// The page (assets/) is plain HTML, CSS and JavaScript modules embedded in
// the binary: no build step, nothing loaded from the network, so the
// desktop window works offline.
package webui

import (
	"crypto/subtle"
	"embed"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/zzf-857/quickerconfigswitch/internal/autostart"
	"github.com/zzf-857/quickerconfigswitch/internal/core"
)

//go:embed assets
var assets embed.FS

// Options configure the handler.
type Options struct {
	// Token, when set, is required on every /api request (header
	// X-QCS-Token or ?token= on the first page load, then a cookie), so
	// another site in the browser can't drive the API. The desktop window
	// passes "" since it loads the page in-process.
	Token string
	// Version is shown in the page's footer ("dev" when empty).
	Version string
	// AllowOrigins are extra Origin values (scheme://host[:port]) accepted
	// besides the server's own host, e.g. the desktop webview's scheme.
	AllowOrigins []string
	// Relay belongs to the serving process, shared by all its windows.
	// The caller must Close it during shutdown. Nil disables relay controls.
	Relay *core.RelayController
	// Autostart inspects/registers only this host's current-user login
	// entry. Nil disables the setting; page loads never register it.
	Autostart *autostart.Manager
}

// CookieName is the cookie that carries Options.Token after the first page
// load.
const CookieName = "qcs_token"

// TokenHeader is the request header that may carry Options.Token.
const TokenHeader = "X-QCS-Token"

type server struct {
	o  Options
	mu sync.Mutex // serializes everything that writes config or app files
}

// Handler serves the embedded page under / and the API under /api/.
func Handler(o Options) http.Handler {
	if o.Version == "" {
		o.Version = "dev"
	}
	s := &server{o: o}
	mux := http.NewServeMux()
	s.routes(mux)

	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(sub)
	// "/" without a method: "GET /" would conflict with the "/api/" fallback.
	mux.Handle("/", s.page(files))

	return s.guard(mux)
}

// page serves the static files. A load of the page with ?token=<token>
// sets the cookie and redirects to the same URL without the token, so it
// doesn't stay in the address bar or history.
func (s *server) page(files http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if strings.HasSuffix(r.URL.Path, ".test.mjs") {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Has("token") {
			if s.o.Token != "" && tokenEq(q.Get("token"), s.o.Token) {
				http.SetCookie(w, &http.Cookie{
					Name: CookieName, Value: s.o.Token, Path: "/",
					HttpOnly: true, SameSite: http.SameSiteStrictMode,
				})
			}
			q.Del("token")
			u := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
			http.Redirect(w, r, u.String(), http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

// guard applies the checks every request gets: security headers, the
// Host and Origin checks, and for /api the token and the JSON content type
// of a mutating request.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")

		if s.o.Token != "" && !localHost(r.Host) {
			writeErr(w, http.StatusForbidden, "host not allowed")
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !s.originOK(o, r.Host) {
			writeErr(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api" {
			next.ServeHTTP(w, r)
			return
		}
		h.Set("Cache-Control", "no-store")
		if s.o.Token != "" && !s.authorized(r) {
			writeErr(w, http.StatusUnauthorized, "missing or wrong token")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
		case http.MethodPost, http.MethodPut, http.MethodDelete:
			if !isJSON(r.Header.Get("Content-Type")) {
				writeErr(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) authorized(r *http.Request) bool {
	if t := r.Header.Get(TokenHeader); t != "" {
		return tokenEq(t, s.o.Token)
	}
	if c, err := r.Cookie(CookieName); err == nil {
		return tokenEq(c.Value, s.o.Token)
	}
	return false
}

func tokenEq(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// originOK: the Origin names the host the request was sent to (same
// origin), or one of AllowOrigins.
func (s *server) originOK(origin, host string) bool {
	for _, a := range s.o.AllowOrigins {
		if strings.EqualFold(strings.TrimRight(a, "/"), origin) {
			return true
		}
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// localHost: the Host header is a loopback name, so a DNS name rebound to
// 127.0.0.1 can't reach the API.
func localHost(host string) bool {
	if host == "" {
		return true
	}
	h := host
	if hp, _, err := net.SplitHostPort(host); err == nil {
		h = hp
	}
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	switch strings.ToLower(h) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func isJSON(ct string) bool {
	mt, _, _ := strings.Cut(ct, ";")
	return strings.EqualFold(strings.TrimSpace(mt), "application/json")
}
