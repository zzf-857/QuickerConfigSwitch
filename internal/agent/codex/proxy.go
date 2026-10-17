// Environment matching adapted from yetone/magpie internal/agent/codex_proxy.go
// (MIT, Copyright (c) 2026 yetone), commit
// 7660399a121af67db4efd56ecb6bd4fb04dc53f1; see third_party/magpie/LICENSE.
package codex

import (
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
)

// environmentProxy follows reqwest's environment precedence, which unlike
// Go's ProxyFromEnvironment does not automatically bypass loopback. It only
// examines this process's environment: desktop login shells and platform
// proxy settings may differ, and are never read or changed here.
func environmentProxy(base string, env func(string) string) (proxy, from string) {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return "", ""
	}
	first := func(keys ...string) (string, string) {
		for _, key := range keys {
			if value := strings.TrimSpace(env(key)); value != "" {
				return value, key
			}
		}
		return "", ""
	}
	no, _ := first("NO_PROXY", "no_proxy")
	if reqwestNoProxy(u.Hostname(), no) {
		return "", ""
	}
	if u.Scheme == "https" {
		proxy, from = first("HTTPS_PROXY", "https_proxy")
	} else if u.Scheme == "http" {
		proxy, from = first("HTTP_PROXY", "http_proxy")
	} else {
		return "", ""
	}
	if proxy == "" {
		proxy, from = first("ALL_PROXY", "all_proxy")
	}
	return proxy, from
}

// reqwestNoProxy matches hyper-util's NoProxy.contains: an IP is bypassed
// only by the same IP or a containing CIDR. "localhost", "127.*" and "*"
// do not match 127.0.0.1; a hostname accepts itself, a parent domain or "*".
func reqwestNoProxy(host, list string) bool {
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	for _, entry := range strings.Split(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if ip != nil {
			if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
				return true
			}
			if other := net.ParseIP(entry); other != nil && other.Equal(ip) {
				return true
			}
			continue
		}
		domain := strings.TrimPrefix(entry, ".")
		if entry == "*" || strings.EqualFold(host, domain) || strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(domain)) {
			return true
		}
	}
	return false
}

// proxyAddress never displays credentials, paths, queries or fragments from
// a proxy URL. Invalid values are not echoed either.
func proxyAddress(raw string) string {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "(configured address)"
	}
	return u.Scheme + "://" + u.Host
}

func loopbackProxyProblem(cfg config) *agent.Problem {
	base, _ := cfg.str("openai_base_url")
	if id, _ := cfg.str("model_provider"); id != "" && id != "openai" {
		base, _ = cfg.providers()[id]["base_url"].(string)
	}
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if !strings.EqualFold(u.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
		return nil
	}
	if features, _ := cfg.doc["features"].(map[string]any); features["respect_system_proxy"] == true {
		return &agent.Problem{Level: "info", Message: "Codex uses respect_system_proxy with a loopback endpoint; check the platform proxy's local-address bypass. This check covers process environment proxies only and cannot verify system proxy settings or a desktop login shell."}
	}
	proxy, from := environmentProxy(base, os.Getenv)
	if proxy == "" {
		return nil
	}
	return &agent.Problem{Level: "warn", Message: "Codex may send its loopback endpoint through " + from + " at " + proxyAddress(proxy) + "; reqwest does not automatically bypass loopback. Allow the local address directly or add " + u.Hostname() + " to NO_PROXY where Codex is launched, then restart Codex. This checks this process's environment only; desktop login-shell and system proxies may differ."}
}
