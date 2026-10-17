package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/zzf-857/quickerconfigswitch/internal/core"
)

func (e *env) cmdProfile(args []string) error {
	if len(args) == 0 {
		return usagef("profile 需要子命令: ls, save, apply, rm")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls":
		return e.profileLs(rest)
	case "save":
		return e.profileSave(rest)
	case "apply":
		return e.profileApply(rest)
	case "rm":
		return e.profileRm(rest)
	case "-h", "--help", "help":
		fmt.Fprint(e.out, usageText)
		return nil
	}
	return usagef("未知的 profile 子命令 %q", sub)
}

func requestText(r core.Request) string {
	s := r.Ref
	if r.Fast != nil {
		s += " fast=" + yesNo(*r.Fast)
	}
	if r.Effort != "" {
		s += " effort=" + r.Effort
	}
	return s
}

func (e *env) profileLs(args []string) error {
	fs := flag.NewFlagSet("profile ls", flag.ContinueOnError)
	pos, err := e.parse(fs, "qcs profile ls", args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("profile ls 不接受参数")
	}
	ps, err := core.Profiles()
	if err != nil {
		return err
	}
	for _, p := range ps {
		tag := ""
		if p.Name == core.Official {
			tag = "  (内置)"
		}
		fmt.Fprintf(e.out, "%s%s\n", p.Name, tag)
		for _, r := range p.Agents {
			fmt.Fprintf(e.out, "  %s: %s\n", r.Agent, requestText(r))
		}
	}
	return nil
}

func (e *env) profileSave(args []string) error {
	fs := flag.NewFlagSet("profile save", flag.ContinueOnError)
	pos, err := e.parse(fs, "qcs profile save <name>", args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("profile save 需要一个 <name>")
	}
	p, err := core.SaveProfile(pos[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(e.out, "已保存 profile %s\n", p.Name)
	for _, r := range p.Agents {
		fmt.Fprintf(e.out, "  %s: %s\n", r.Agent, requestText(r))
	}
	return nil
}

func (e *env) profileApply(args []string) error {
	fs := flag.NewFlagSet("profile apply", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "不再确认")
	dry := fs.Bool("dry-run", false, "只显示变更")
	pos, err := e.parse(fs, "qcs profile apply <name> [flags]", args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("profile apply 需要一个 <name>")
	}
	name := pos[0]
	steps, err := core.PreviewProfile(name)
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		fmt.Fprintf(e.out, "profile %s 没有任何 agent。\n", name)
		return nil
	}
	for _, s := range steps {
		fmt.Fprintf(e.out, "== %s -> %s\n", s.Agent, requestText(s.Request))
		switch {
		case s.Error != "":
			fmt.Fprintf(e.out, "无法预览: %s\n", s.Error)
		case s.Preview.Nothing || strings.TrimSpace(s.Preview.Diff) == "":
			fmt.Fprintln(e.out, "没有需要修改的内容。")
		default:
			fmt.Fprintln(e.out, s.Preview.Diff)
		}
	}
	if *dry {
		return nil
	}
	if !*yes && !e.confirm(fmt.Sprintf("应用 profile %s 的这些修改?", name)) {
		fmt.Fprintln(e.out, "已取消。")
		return errSilent
	}
	res, err := core.ApplyProfile(name)
	if err != nil {
		return err
	}
	failed := 0
	for _, s := range res {
		if s.Error != "" {
			failed++
			fmt.Fprintf(e.out, "失败 %s: %s\n", s.Agent, s.Error)
			continue
		}
		fmt.Fprintf(e.out, "已应用 %s -> %s\n", s.Agent, s.Ref)
		if s.Result != nil && s.Result.Notice != "" {
			fmt.Fprintln(e.out, s.Result.Notice)
		}
	}
	if failed > 0 {
		fmt.Fprintf(e.err, "qcs: %d 个 agent 失败\n", failed)
		return errSilent
	}
	return nil
}

func (e *env) profileRm(args []string) error {
	fs := flag.NewFlagSet("profile rm", flag.ContinueOnError)
	pos, err := e.parse(fs, "qcs profile rm <name>", args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("profile rm 需要一个 <name>")
	}
	if err := core.DeleteProfile(pos[0]); err != nil {
		return err
	}
	fmt.Fprintf(e.out, "已删除 profile %s\n", pos[0])
	return nil
}
