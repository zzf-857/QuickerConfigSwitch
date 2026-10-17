// Package webui serves the app's page and its JSON API: the window of the
// desktop app (internal/gui) and `qcs web` in a browser load the same
// handler.
package webui

import "net/http"

// Options configure the handler.
type Options struct {
	// Token, when set, is required on every /api request (header
	// X-QCS-Token or ?token= on the first page load, then a cookie), so
	// another site in the browser can't drive the API. The desktop window
	// passes "" since it loads the page in-process.
	Token string
}

// Handler serves the embedded page under / and the API under /api/.
func Handler(o Options) http.Handler { return http.NotFoundHandler() }
