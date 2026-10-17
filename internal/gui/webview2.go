//go:build !nogui

package gui

import "errors"

var errNoWebView2 = errors.New("缺少 Microsoft Edge WebView2 运行时, 桌面界面无法打开; 请安装它 (winget install Microsoft.EdgeWebView2Runtime), 或用 qcs web 在浏览器中打开")

// webView2Missing reads Wails' own answer, Env.Info().PlatformInfo["WebView2"]:
// the runtime's version, "" when its lookup failed. A key Wails doesn't set
// says nothing and is read as present.
func webView2Missing(info map[string]any) bool {
	v, ok := info["WebView2"]
	return ok && (v == nil || v == "")
}
