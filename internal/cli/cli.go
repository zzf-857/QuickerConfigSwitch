// Package cli is the qcs command line: argument parsing and plain-text
// output on top of the agent adapters, safekeep and the provider store.
package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	_ "github.com/zzf-857/quickerconfigswitch/internal/agent/claude"
	_ "github.com/zzf-857/quickerconfigswitch/internal/agent/codex"
)

// Version is set at build time with -ldflags.
var Version = "dev"

const usageText = `qcs - 在官方配置与自定义 provider 之间快速切换 Codex / Claude Code

用法:
  qcs                                      打开桌面界面 (托盘 + 窗口), 同 qcs gui
  qcs web [--port N] [--host 127.0.0.1] [--no-open] [--allow-remote]
                                           在浏览器中打开 (默认端口 17320, 带随机 token)
  qcs ls                                   各 Agent 当前状态
  qcs provider ls                          列出 provider
  qcs provider add <id> --base URL [--key K] [--name N] [--template T]
                  [--anthropic URL] [--responses] [--chat] [--header K=V ...]
  qcs provider rm <id>
  qcs provider models <id> [--fetch] [--add m1,m2] [--rm m] [--fast m=true|false]
  qcs provider templates                   可用模板
  qcs use <agent> <provider>/<model> [--fast|--no-fast] [--effort E] [--yes] [--dry-run]
  qcs use codex <provider>/<model> --relay [--relay-all] [--port N] [--yes] [--dry-run]
                                           前台路由，停止后还原配置
  qcs relay recover                        异常退出后还原路由配置
  qcs autostart [status|enable|disable]      查询或设置当前用户的登录自启
  qcs use <agent> official                 切回官方 (同 restore)
  qcs diff <agent> <provider>/<model>|official   只看变更
  qcs restore <agent> [--yes] [--dry-run]  还原
  qcs profile ls                           列出 profile (含内置 official)
  qcs profile save <name>                  把各 Agent 当前选择存为 profile
  qcs profile apply <name> [--yes] [--dry-run]
  qcs profile rm <name>
  qcs import [--add N|all] [--id ID]       发现 CC Switch / Codex / Claude 里已有的 provider 并导入
  qcs doctor [agent]                       检查漂移与常见问题
  qcs version
  qcs help
`

// usageError is a mistake in how the command was called (exit code 2).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return &usageError{fmt.Sprintf(format, a...)} }

// env is what a command runs with.
type env struct {
	in  *bufio.Reader
	out io.Writer
	err io.Writer
}

// Run executes one qcs command line and returns the process exit code.
func Run(args []string, in io.Reader, out, errw io.Writer) int {
	e := &env{in: bufio.NewReader(in), out: out, err: errw}
	err := e.dispatch(args)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errSilent):
		return 1
	}
	var ue *usageError
	if errors.As(err, &ue) {
		fmt.Fprintf(errw, "qcs: %s\n运行 qcs help 查看用法。\n", ue.msg)
		return 2
	}
	fmt.Fprintf(errw, "qcs: %v\n", err)
	return 1
}

// errSilent is a failure already reported on the output.
var errSilent = errors.New("failed")

func (e *env) dispatch(args []string) error {
	if len(args) == 0 {
		return e.cmdGUI(nil)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help", "-help":
		fmt.Fprint(e.out, usageText)
		return nil
	case "version", "--version":
		fmt.Fprintln(e.out, "qcs", Version)
		return nil
	case "gui":
		return e.cmdGUI(rest)
	case "web":
		return e.cmdWeb(rest)
	case "relay":
		return e.cmdRelay(rest)
	case "autostart":
		return e.cmdAutostart(rest)
	case "profile":
		return e.cmdProfile(rest)
	case "import":
		return e.cmdImport(rest)
	case "ls":
		return e.cmdLs(rest)
	case "provider":
		return e.cmdProvider(rest)
	case "use":
		return e.cmdUse(rest, false)
	case "diff":
		return e.cmdUse(rest, true)
	case "restore":
		return e.cmdRestore(rest)
	case "doctor":
		return e.cmdDoctor(rest)
	}
	return usagef("未知命令 %q", cmd)
}

// parse parses args with fs, allowing flags after positionals. It returns
// the positionals. -h prints the command's usage and yields flag.ErrHelp.
func (e *env) parse(fs *flag.FlagSet, synopsis string, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fmt.Fprintf(e.out, "用法: %s\n", synopsis)
				fs.SetOutput(e.out)
				fs.PrintDefaults()
				return nil, flag.ErrHelp
			}
			return nil, usagef("%v", err)
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// confirm asks question on out and reads a y/N answer from in.
func (e *env) confirm(question string) bool {
	fmt.Fprintf(e.out, "%s [y/N] ", question)
	line, _ := e.in.ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

// multi is a repeatable string flag.
type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }
