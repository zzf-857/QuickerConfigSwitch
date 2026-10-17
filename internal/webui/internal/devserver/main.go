// Command devserver serves the web UI over fixture data in a throwaway
// home, for looking at the page in a browser:
//
//	go run ./internal/webui/internal/devserver -addr 127.0.0.1:8765 -token dev [-switched]
//
// It never touches the real home: HOME, CODEX_HOME, CLAUDE_CONFIG_DIR and
// the app folder all point into a temporary directory.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/core"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/testenv"
	"github.com/zzf-857/quickerconfigswitch/internal/webui"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:8765", "listen address")
	token := flag.String("token", "dev", "API token (\"\" for none)")
	switched := flag.Bool("switched", false, "switch Codex to cpa/gpt-5.5 first")
	flag.Parse()

	dir, err := os.MkdirTemp("", "qcs-devserver-")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	testenv.Setup(dir)
	cx, cl := filepath.Join(dir, ".codex"), filepath.Join(dir, ".claude")
	os.Setenv("CODEX_HOME", cx)
	os.Setenv("CLAUDE_CONFIG_DIR", cl)
	os.MkdirAll(cx, 0o700)
	os.MkdirAll(cl, 0o700)
	must(os.WriteFile(filepath.Join(cx, "config.toml"), []byte("# my codex\nmodel = \"gpt-5.5\"\nmodel_reasoning_effort = \"medium\"\n\n[features]\nweb_search = true\n"), 0o600))
	must(os.WriteFile(filepath.Join(cx, "auth.json"), []byte(`{"tokens":{"access_token":"fixture"}}`), 0o600))
	must(os.WriteFile(filepath.Join(cl, "settings.json"), []byte("{\n  \"theme\": \"dark\"\n}\n"), 0o600))
	must(os.WriteFile(filepath.Join(cl, ".credentials.json"), []byte(`{}`), 0o600))
	must(provider.Add(provider.Provider{
		ID: "cpa", Name: "CPA (CLIProxyAPI)", BaseURL: "http://127.0.0.1:8317/v1", APIKey: "sk-fixture-0123456789abcdef",
		APIs: provider.APIs{Responses: true, Chat: true, Anthropic: "http://127.0.0.1:8317"},
		Models: []provider.Model{
			{ID: "gpt-5.5", Fast: true, Efforts: []string{"low", "medium", "high", "xhigh"}},
			{ID: "gpt-5.5-mini", Fast: true},
			{ID: "claude-opus-4.6"},
			{ID: "claude-sonnet-4.6"},
		},
	}))
	must(provider.Add(provider.Provider{
		ID: "relay", Name: "Anthropic relay", APIKey: "short",
		APIs:   provider.APIs{Anthropic: "https://relay.example.com"},
		Models: []provider.Model{{ID: "claude-opus-4.6"}},
	}))
	if *switched {
		on := true
		if _, err := core.Use(core.Request{Agent: "codex", Ref: "cpa/gpt-5.5", Fast: &on, Effort: "high"}); err != nil {
			log.Fatal(err)
		}
		if _, err := core.SaveProfile("CPA 全家桶"); err != nil {
			log.Fatal(err)
		}
	}
	control := core.NewRelayController()
	defer control.Close()
	srv := &http.Server{Addr: *addr, Handler: webui.Handler(webui.Options{Token: *token, Version: "dev", Relay: control}), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		control.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			srv.Close()
		}
	}()
	fmt.Printf("http://%s/?token=%s\n", *addr, *token)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
