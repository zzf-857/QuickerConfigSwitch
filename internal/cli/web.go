package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"time"

	"github.com/zzf-857/quickerconfigswitch/internal/gui"
	"github.com/zzf-857/quickerconfigswitch/internal/webui"
)

const defaultWebPort = 17320

// Hooks the tests replace.
var (
	openBrowser = defaultOpenBrowser
	// webStop yields a channel closed when `qcs web` should stop.
	webStop = func() (<-chan struct{}, func()) {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		return ctx.Done(), cancel
	}
	webHandler = webui.Handler
	runGUI     = gui.Run
	hasDisplay = func() bool {
		if runtime.GOOS != "linux" {
			return true
		}
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	}
)

func defaultOpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (e *env) cmdWeb(args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	port := fs.Int("port", defaultWebPort, "端口 (0 = 任意空闲端口)")
	host := fs.String("host", "127.0.0.1", "监听地址")
	noOpen := fs.Bool("no-open", false, "不自动打开浏览器")
	remote := fs.Bool("allow-remote", false, "允许监听非本机地址 (token 是唯一保护)")
	pos, err := e.parse(fs, "qcs web [--port N] [--host 127.0.0.1] [--no-open]", args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("web 不接受参数")
	}
	if *port < 0 || *port > 65535 {
		return usagef("--port 超出范围: %d", *port)
	}
	if !isLoopback(*host) {
		if !*remote {
			return usagef("--host %s 不是本机回环地址; 如确需远程访问请加 --allow-remote", *host)
		}
		fmt.Fprintf(e.err, "警告: 监听 %s, 此时只有 token 保护 API, 且流量未加密。\n", *host)
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "port" })

	ln, err := net.Listen("tcp", net.JoinHostPort(*host, strconv.Itoa(*port)))
	if err != nil && !explicit {
		ln, err = net.Listen("tcp", net.JoinHostPort(*host, "0"))
	}
	if err != nil {
		return err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		ln.Close()
		return err
	}
	token := hex.EncodeToString(raw[:])
	srv := &http.Server{Handler: webHandler(webui.Options{Token: token}), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()

	showHost := *host
	if ip := net.ParseIP(showHost); ip != nil && ip.IsUnspecified() {
		showHost = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s/?token=%s", net.JoinHostPort(showHost, strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)), token)
	fmt.Fprintf(e.out, "QuickerConfigSwitch 已启动: %s\n按 Ctrl+C 停止。\n", url)
	if !*noOpen {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(e.err, "无法打开浏览器 (%v), 请手动打开上面的地址。\n", err)
		}
	}

	stop, cancel := webStop()
	defer cancel()
	select {
	case <-stop:
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	_ = srv.Shutdown(ctx)
	fmt.Fprintln(e.out, "已停止。")
	return nil
}

// cmdGUI runs the desktop app. It fails with a hint when this build has
// none or there is no display.
func (e *env) cmdGUI(args []string) error {
	fs := flag.NewFlagSet("gui", flag.ContinueOnError)
	pos, err := e.parse(fs, "qcs [gui]", args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("gui 不接受参数")
	}
	var gerr error
	if !hasDisplay() {
		gerr = errors.New("没有图形显示 (DISPLAY / WAYLAND_DISPLAY 为空)")
	} else {
		gerr = runGUI()
	}
	if gerr != nil {
		fmt.Fprintf(e.err, "qcs: 无法打开桌面界面: %v\n", gerr)
		fmt.Fprintln(e.err, "可以用 qcs web 在浏览器中使用, 或 qcs help 查看命令行用法。")
		return errSilent
	}
	return nil
}
