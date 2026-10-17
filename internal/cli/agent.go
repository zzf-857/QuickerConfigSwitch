package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/agent"
	"github.com/zzf-857/quickerconfigswitch/internal/core"
	"github.com/zzf-857/quickerconfigswitch/internal/provider"
	"github.com/zzf-857/quickerconfigswitch/internal/safekeep"
)

func lookupAgent(id string) (agent.Agent, error) {
	a, ok := agent.Get(id)
	if !ok {
		var ids []string
		for _, x := range agent.All() {
			ids = append(ids, x.ID())
		}
		return nil, usagef("未知 agent %q (可用: %s)", id, strings.Join(ids, ", "))
	}
	return a, nil
}

func (e *env) cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	pos, err := e.parse(fs, "qcs ls", args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("ls 不接受参数")
	}
	for _, a := range agent.All() {
		d := a.Detect()
		fmt.Fprintf(e.out, "%s  (%s)\n", a.ID(), a.Name())
		fmt.Fprintf(e.out, "  installed: %s\n", yesNo(d.Installed))
		si := d.SignIn
		if si == "" {
			si = "-"
		}
		fmt.Fprintf(e.out, "  sign-in:   %s\n", si)
		st, err := a.Current()
		if err != nil {
			fmt.Fprintf(e.out, "  current:   读取失败: %v\n", err)
		} else {
			line := string(st.Mode)
			if st.Provider != "" && st.Mode != agent.Relay {
				line += " " + st.Provider
			}
			if st.Model != "" {
				if st.Mode == agent.Relay {
					// Relay preserves Codex's actual model. A plain official
					// slug must not look like a provider-prefixed model.
					line += " " + st.Model
				} else {
					line += "/" + st.Model
				}
			}
			if st.Detail != "" {
				line += "  " + st.Detail
			}
			fmt.Fprintf(e.out, "  current:   %s\n", line)
		}
		if safekeep.Touched(a.ID()) {
			fmt.Fprintln(e.out, "  managed by qcs")
		}
		if n := len(safekeep.Check(a.ID())); n > 0 {
			fmt.Fprintf(e.out, "  drift:     %d\n", n)
		}
	}
	return nil
}

func (e *env) cmdUse(args []string, diffOnly bool) error {
	name := "use"
	if diffOnly {
		name = "diff"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fast := fs.Bool("fast", false, "开启 Fast")
	noFast := fs.Bool("no-fast", false, "关闭 Fast")
	effort := fs.String("effort", "", "推理强度")
	yes := fs.Bool("yes", false, "不再确认")
	dry := fs.Bool("dry-run", false, "只显示变更")
	relay := fs.Bool("relay", false, "保留官方登录，前台运行本地路由 (仅 Codex)")
	relayAll := fs.Bool("relay-all", false, "路由中所有模型都发给 provider (需要 --relay)")
	port := fs.Int("port", 17321, "路由端口 (需要 --relay，0 = 任意空闲端口)")
	pos, err := e.parse(fs, "qcs "+name+" <agent> <provider>/<model>|official [flags]", args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return usagef("%s 需要 <agent> 和 <provider>/<model> 或 official", name)
	}
	if *fast && *noFast {
		return usagef("--fast 与 --no-fast 不能同时使用")
	}
	portSet := false
	fs.Visit(func(f *flag.Flag) { portSet = portSet || f.Name == "port" })
	if !*relay && (*relayAll || portSet) {
		return usagef("--relay-all 和 --port 需要 --relay")
	}
	a, err := lookupAgent(pos[0])
	if err != nil {
		return err
	}
	if pos[1] == "official" {
		if *relay {
			return usagef("official 不接受 --relay")
		}
		return e.restore(a, *yes, *dry || diffOnly)
	}
	pid, mid, ok := provider.ParseRef(pos[1])
	if !ok {
		return usagef("%q 不是 <provider>/<model> 形式", pos[1])
	}
	t := agent.Target{Mode: agent.Direct, Provider: pid, Model: mid, Effort: *effort}
	if *fast {
		b := true
		t.Fast = &b
	} else if *noFast {
		b := false
		t.Fast = &b
	}
	if *relay {
		if a.ID() != "codex" {
			return usagef("--relay 仅支持 Codex")
		}
		t.Mode, t.RelayAll = agent.Relay, *relayAll
		return e.useRelay(t, *port, *yes, *dry || diffOnly)
	}
	p, err := a.Plan(t)
	if err != nil {
		return err
	}
	diff, err := safekeep.Preview(p)
	if err != nil {
		return err
	}
	if p.Empty() || strings.TrimSpace(diff) == "" {
		fmt.Fprintln(e.out, "没有需要修改的内容。")
	} else {
		fmt.Fprintln(e.out, core.Redact(diff))
	}
	if *dry || diffOnly {
		return nil
	}
	if !*yes && !e.confirm("应用这些修改?") {
		fmt.Fprintln(e.out, "已取消。")
		return errSilent
	}
	if err := safekeep.Apply(p); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "已切换 %s 到 %s/%s\n", a.ID(), pid, mid)
	if p.Notice != "" {
		fmt.Fprintln(e.out, p.Notice)
	}
	return nil
}

func (e *env) cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "不再确认")
	dry := fs.Bool("dry-run", false, "只显示变更")
	pos, err := e.parse(fs, "qcs restore <agent> [flags]", args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("restore 需要一个 <agent>")
	}
	a, err := lookupAgent(pos[0])
	if err != nil {
		return err
	}
	return e.restore(a, *yes, *dry)
}

func (e *env) restore(a agent.Agent, yes, dry bool) error {
	if !safekeep.Touched(a.ID()) {
		fmt.Fprintf(e.out, "%s 没有被 qcs 修改过, 无需还原。\n", a.ID())
		return nil
	}
	diff, err := safekeep.RestorePreview(a.ID())
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		fmt.Fprintln(e.out, "没有需要修改的内容。")
	} else {
		fmt.Fprintln(e.out, core.Redact(diff))
	}
	if dry {
		return nil
	}
	if !yes && !e.confirm("应用这些修改?") {
		fmt.Fprintln(e.out, "已取消。")
		return errSilent
	}
	rep, err := safekeep.Restore(a.ID())
	if err != nil {
		return err
	}
	fmt.Fprintf(e.out, "已还原 %s 到官方配置\n", a.ID())
	for _, f := range rep.Files {
		switch {
		case f.Removed:
			fmt.Fprintf(e.out, "  %s: 已删除 (原本不存在)\n", f.Path)
		case f.Exact:
			fmt.Fprintf(e.out, "  %s: 逐字节还原 (exact)\n", f.Path)
		default:
			fmt.Fprintf(e.out, "  %s:\n", f.Path)
		}
		if len(f.Reverted) > 0 {
			fmt.Fprintf(e.out, "    reverted: %s\n", strings.Join(f.Reverted, ", "))
		}
		if len(f.Left) > 0 {
			fmt.Fprintf(e.out, "    left (被他人修改, 未动): %s\n", strings.Join(f.Left, ", "))
		}
		if len(f.Kept) > 0 {
			fmt.Fprintf(e.out, "    kept (保留): %s\n", strings.Join(f.Kept, ", "))
		}
	}
	return nil
}

func (e *env) cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	pos, err := e.parse(fs, "qcs doctor [agent]", args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usagef("doctor 最多一个 agent")
	}
	agents := agent.All()
	if len(pos) == 1 {
		a, err := lookupAgent(pos[0])
		if err != nil {
			return err
		}
		agents = []agent.Agent{a}
	}
	bad := false
	for _, a := range agents {
		fmt.Fprintf(e.out, "%s:\n", a.ID())
		n := 0
		for _, p := range a.Doctor() {
			fmt.Fprintf(e.out, "  [%s] %s\n", p.Level, p.Message)
			n++
			if p.Level == "error" {
				bad = true
			}
		}
		for _, d := range safekeep.Check(a.ID()) {
			what := d.Path
			if d.Slot != "" {
				what += " " + d.Slot
			}
			fmt.Fprintf(e.out, "  [warn] 漂移 (%s): %s\n", d.Kind, what)
			n++
		}
		if n == 0 {
			fmt.Fprintln(e.out, "  一切正常")
		}
	}
	if bad {
		return errSilent
	}
	return nil
}
