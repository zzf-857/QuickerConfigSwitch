// This test-only process runs the production relay with fake upstreams. It is
// not a product command: every home must belong to the isolated fixture and
// every outgoing request is restricted to its two loopback HTTP servers.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/core"
)

type fixtureTransport struct {
	official, cpa *url.URL
	next          http.RoundTripper
}

func (t fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	u := *r.URL
	c.URL = &u
	if u.Scheme == "https" && u.Host == "chatgpt.com" {
		c.URL.Scheme, c.URL.Host, c.Host = t.official.Scheme, t.official.Host, t.official.Host
	} else if u.Scheme != "http" || u.Host != t.cpa.Host && u.Host != t.official.Host {
		return nil, errors.New("fixture blocked a non-loopback target")
	}
	return t.next.RoundTrip(c)
}

func fixtureURL(name string) *url.URL {
	u, err := url.Parse(os.Getenv(name))
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		panic("fixture requires explicit loopback HTTP upstreams")
	}
	return u
}

func checkHomes() {
	root := os.Getenv("QCS_RELAY_FIXTURE_ROOT")
	if !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(root), "qcs-codex-relay-contract-") {
		panic("fixture requires an isolated temporary root")
	}
	for name, dir := range map[string]string{"HOME": "home", "QCS_HOME": "qcs", "CODEX_HOME": "codex"} {
		if os.Getenv(name) != filepath.Join(root, dir) {
			panic("fixture home is outside its temporary root")
		}
	}
}

func main() {
	checkHomes()
	official, cpa := fixtureURL("QCS_RELAY_FIXTURE_OFFICIAL"), fixtureURL("QCS_RELAY_FIXTURE_CPA")
	http.DefaultTransport = fixtureTransport{official: official, cpa: cpa, next: &http.Transport{Proxy: nil}}
	s, err := core.StartRelay(agent.Target{
		Mode: agent.Relay, Provider: "fixture", Model: "gpt-5.5", RelayAll: true,
	}, 0)
	if err != nil {
		panic(err)
	}
	defer s.Close()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"url": s.URL}); err != nil {
		panic(err)
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	if _, err := s.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
