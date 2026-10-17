// Adapted from yetone/magpie internal/cli/autostart_cli.go (MIT,
// Copyright (c) 2026 yetone). See third_party/magpie/LICENSE.
package cli

import (
	"flag"
	"fmt"

	"github.com/zzf-857/quickerconfigswitch/internal/autostart"
	"github.com/zzf-857/quickerconfigswitch/internal/gui"
)

func (e *env) cmdAutostart(args []string) error {
	fs := flag.NewFlagSet("autostart", flag.ContinueOnError)
	pos, err := e.parse(fs, "qcs autostart [status|enable|disable]", args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usagef("autostart 最多接受一个操作")
	}
	action := "status"
	if len(pos) == 1 {
		action = pos[0]
	}
	switch action {
	case "status", "enable", "disable", "on", "off":
	default:
		return usagef("用法: qcs autostart [status|enable|disable]")
	}
	m, err := autostart.New(gui.Available)
	if err != nil {
		return err
	}
	var st autostart.Status
	switch action {
	case "enable", "on":
		st, err = m.Enable()
	case "disable", "off":
		st, err = m.Disable()
	default:
		st, err = m.Status()
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(e.out, "登录自启: %s\n", yesNo(st.Enabled))
	if st.Path != "" {
		fmt.Fprintf(e.out, "注册位置: %s\n", st.Path)
	}
	if st.Executable != "" {
		fmt.Fprintf(e.out, "启动程序: %s\n", st.Executable)
	}
	if st.Problem != "" {
		fmt.Fprintf(e.out, "提示: %s\n", st.Problem)
	}
	if st.Enabled {
		fmt.Fprintln(e.out, "下次登录时打开隐藏的桌面应用；不会自动开启路由。")
	}
	return nil
}
