//go:build !nogui

// Package gui is the desktop app: a tray icon and a window that show the
// page internal/webui serves. Builds with -tags nogui leave it out.
package gui

import (
	_ "embed"
	"runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

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

// focusMain brings the main window up; set once the window exists.
var focusMain atomic.Value

// Run starts the desktop app: a tray icon whose click toggles a compact
// panel, and a main window that hides on close. The main window opens at
// start unless hidden (an autostart): many Linux desktops (GNOME without an
// extension) show no tray icons at all, and the app must still be usable
// there. It returns when the user quits from the tray menu.
func Run(hidden bool) error {
	app := application.New(application.Options{
		Name:        appName,
		Description: "Switch Codex / Claude Code between official setup and custom providers",
		Icon:        trayPNG,
		// The page is served in-process, so it needs no API token.
		Assets: application.AssetOptions{Handler: webui.Handler(webui.Options{})},
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
