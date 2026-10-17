//go:build windows && !nogui

package gui

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// webView2Check says whether the app's windows can be made. Without the
// runtime Wails would exit on the first window with no console to say why,
// so the reader is told in a message box of Windows' own, and Run ends.
func webView2Check(app *application.App) bool {
	if !webView2Missing(app.Env.Info().PlatformInfo) {
		return true
	}
	logf("%v", errNoWebView2)
	msgBox("QuickerConfigSwitch 需要 WebView2",
		"桌面界面需要 Microsoft Edge WebView2 运行时, 这台电脑上没有安装。\r\n\r\n"+
			"请安装: winget install Microsoft.EdgeWebView2Runtime\r\n"+
			"或从 https://developer.microsoft.com/microsoft-edge/webview2/ 下载。\r\n\r\n"+
			"装好之前, 可以在终端运行 qcs web 在浏览器中使用。")
	return false
}

var procMessageBoxW = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

func msgBox(title, body string) {
	const iconError, setForeground, topmost = 0x10, 0x10000, 0x40000
	procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(body))),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(title))),
		iconError|setForeground|topmost)
}
