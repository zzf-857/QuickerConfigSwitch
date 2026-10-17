package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/core"
)

// Tests replace the signal source so a foreground session can finish
// without sending a signal to the test process.
var relayStop = func() (<-chan struct{}, func()) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx.Done(), cancel
}

func (e *env) useRelay(t agent.Target, port int, yes, dry bool) (err error) {
	s, err := core.PrepareRelay(t, port)
	if err != nil {
		return err
	}
	started := false
	defer func() {
		rep, closeErr := s.Close()
		err = errors.Join(err, closeErr)
		if started && closeErr == nil {
			if rep != nil && rep.Left() {
				fmt.Fprintln(e.out, "路由已停止；被其它工具修改的配置已保留，请运行 qcs doctor codex 检查。")
			} else {
				fmt.Fprintln(e.out, "路由已停止，已还原本次路由配置；请重启 Codex。")
			}
		}
	}()
	fmt.Fprintln(e.out, core.Redact(s.Preview))
	fmt.Fprintln(e.out, s.Notice)
	if dry {
		return nil
	}
	if !yes && !e.confirm("应用修改并启动本地路由?") {
		fmt.Fprintln(e.out, "已取消。")
		return errSilent
	}
	stop, cancel := relayStop()
	defer cancel()
	if err := s.Start(); err != nil {
		return err
	}
	started = true
	fmt.Fprintf(e.out, "本地路由已启动: %s\n按 Ctrl+C 停止并还原。\n", s.URL)
	select {
	case <-stop:
		return nil
	case err := <-s.Done():
		return err
	}
}

func (e *env) cmdRelay(args []string) error {
	if len(args) != 1 || args[0] != "recover" {
		return usagef("用法: qcs relay recover")
	}
	rep, err := core.RecoverRelay()
	if err != nil {
		return err
	}
	if rep == nil {
		fmt.Fprintln(e.out, "没有需要恢复的路由配置。")
	} else if rep.Left() {
		fmt.Fprintln(e.out, "已恢复路由配置；被其它工具修改的值已保留，请运行 qcs doctor codex 检查。")
	} else {
		fmt.Fprintln(e.out, "已恢复异常退出前的配置；请重启 Codex。")
	}
	return nil
}
