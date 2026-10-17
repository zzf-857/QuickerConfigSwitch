//go:build !nogui

// Package gui is the desktop app: a tray icon and a window that show the
// page internal/webui serves. Builds with -tags nogui leave it out.
package gui

import (
	_ "embed"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/zzf-857/quickerconfigswitch/internal/autostart"
	"github.com/zzf-857/quickerconfigswitch/internal/core"
	"github.com/zzf-857/quickerconfigswitch/internal/webui"
)

//go:embed tray.png
var trayPNG []byte

//go:embed tray_template.png
var trayTemplatePNG []byte

const (
	appName    = "QuickerConfigSwitch"
	panelW     = 420
	panelH     = 640
	mainW      = 1040
	mainH      = 720
	trayOffset = 6
)

// Available says this build can launch the desktop app at login.
const Available = true

// focusMain brings the main window up; set once the window exists.
var focusMain atomic.Value

// Run starts the desktop app: a tray icon whose click toggles a compact
// panel, and a main window that hides on close. The main window opens at
// start unless hidden (an autostart): many Linux desktops (GNOME without an
// extension) show no tray icons at all, and the app must still be usable
// there. It returns when the user quits from the tray menu.
func Run(hidden bool) error {
	login, err := autostart.New(Available)
	if err != nil {
		// Login startup is optional; its discovery must not prevent the
		// user from opening the app and fixing their configuration.
		logf("autostart: %s", core.Redact(err.Error()))
	}
	control := core.NewRelayController()
	done := make(chan struct{})
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			close(done)
			if err := control.Close(); err != nil {
				logf("relay shutdown: %s", core.Redact(err.Error()))
			}
		})
	}
	defer cleanup()
	app := application.New(application.Options{
		Name:        appName,
		Description: "Switch Codex / Claude Code between official setup and custom providers",
		Icon:        trayPNG,
		// The page is served in-process, so it needs no API token.
		Assets: application.AssetOptions{Handler: webui.Handler(webui.Options{Relay: control, Autostart: login})},
		// Some platforms exit without returning from Run. Wails invokes
		// OnShutdown synchronously before destroying the process.
		OnShutdown: cleanup,
		// A tray app: no Dock icon on macOS.
		Mac:     application.MacOptions{ActivationPolicy: application.ActivationPolicyAccessory},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		Linux:   application.LinuxOptions{DisableQuitOnLastWindowClosed: true, ProgramName: "quickerconfigswitch"},
		// A second launch hands over to this one and quits; this one shows
		// its main window.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "io.github.zzf-857.quickerconfigswitch",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if f, _ := focusMain.Load().(func()); f != nil {
					f()
				}
			},
		},
		ErrorHandler: func(err error) {
			logf("%v", err)
		},
	})
	if !webView2Check(app) {
		return errNoWebView2
	}

	panel := app.Window.NewWithOptions(panelOptions())
	main := app.Window.NewWithOptions(mainOptions(hidden))

	showMain := func() {
		application.InvokeAsync(func() {
			if main.IsMinimised() {
				main.UnMinimise()
			}
			main.Show().Focus()
		})
	}
	focusMain.Store(showMain)
	// Closing the main window hides it; the tray keeps the app alive.
	main.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		main.Hide()
		e.Cancel()
	})

	menu := app.NewMenu()
	menu.Add("打开主窗口").OnClick(func(*application.Context) {
		panel.Hide()
		showMain()
	})
	menu.AddSeparator()
	relayLabel := menu.Add("路由：未启动").SetEnabled(false)
	stopRelay := menu.Add("停止路由并还原").SetEnabled(false)
	stopRelay.OnClick(func(*application.Context) {
		go func() {
			if _, err := control.Stop(); err != nil {
				logf("stop relay: %s", core.Redact(err.Error()))
			}
		}()
	})
	recoverRelay := menu.Add("恢复异常退出的路由配置").SetEnabled(false)
	recoverRelay.OnClick(func(*application.Context) {
		go func() {
			if _, err := control.Recover(); err != nil {
				logf("recover relay: %s", core.Redact(err.Error()))
			}
		}()
	})
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(*application.Context) { app.Quit() })

	tray := app.SystemTray.New()
	tray.SetTooltip(appName)
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayTemplatePNG)
	} else {
		tray.SetIcon(trayPNG)
	}
	if runtime.GOOS == "linux" {
		tray.SetLabel(appName)
	}
	tray.SetMenu(menu)
	// With a window attached, a click on the icon toggles it by the icon.
	tray.AttachWindow(panel).WindowOffset(trayOffset)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				st := control.Status()
				application.InvokeAsync(func() {
					select {
					case <-done:
						return
					default:
					}
					label := "路由：未启动"
					switch st.State {
					case "preview":
						label = "路由：等待确认"
					case "running":
						label = "路由：" + st.Provider + "/" + st.Model
					case "external":
						label = "路由：由其它进程运行"
					case "stale":
						label = "路由：异常退出，待恢复"
					case "error":
						label = "路由：发生错误，请打开主窗口"
					}
					relayLabel.SetLabel(label)
					stopRelay.SetEnabled(st.State == "running")
					recoverRelay.SetEnabled(st.State == "stale")
					tray.SetTooltip(appName + " · " + label)
				})
			}
		}
	}()

	return app.Run()
}

func panelOptions() application.WebviewWindowOptions {
	o := application.WebviewWindowOptions{
		Name:            "panel",
		Title:           appName,
		URL:             "/?mode=panel",
		Width:           panelW,
		Height:          panelH,
		Hidden:          true,
		Frameless:       true,
		AlwaysOnTop:     true,
		DisableResize:   true,
		HideOnEscape:    true,
		HideOnFocusLost: true,
		Windows:         application.WindowsWindow{HiddenOnTaskbar: true},
	}
	if runtime.GOOS == "windows" {
		o.MinimiseButtonState = application.ButtonHidden
		o.MaximiseButtonState = application.ButtonHidden
		o.CloseButtonState = application.ButtonHidden
	}
	return o
}

func mainOptions(hidden bool) application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:      "main",
		Title:     appName,
		URL:       "/",
		Width:     mainW,
		Height:    mainH,
		MinWidth:  720,
		MinHeight: 480,
		Hidden:    hidden,
	}
}
