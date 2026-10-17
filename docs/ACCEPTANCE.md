# 真机验收与回归入口

本文件供后续 Agent 或维护者接手验收使用。自动测试全部使用临时配置和假后端；
不能据此填写真实账号、真实系统托盘或登录自启“通过”。阶段实现见 STAGES.md，
来源与裁剪规则见 UPSTREAM.md。

核心要求与实现证据见 REQUIREMENTS.md。官方 Codex CLI 0.159.2 已在隔离配置下通过
app-server 严格加载及模型列表/Fast 检查；这仍不代表真实桌面账号或远端 CPA 验收。
官方 Claude CLI 2.1.302 已通过临时配置与 loopback SSE 的六项实际请求检查，覆盖
ID、认证、effort 及上限；可用
`python3 scripts/check_claude_contract.py --qcs ./qcs --claude /path/to/claude`
重跑。两个脚本都不访问真实账户或远端模型。
有官方客户端时，可用 Python 3.11+ 运行
`python3 scripts/check_codex_contract.py --qcs ./qcs --codex /path/to/codex`
重做临时配置检查；该脚本不进行登录或推理。

## 当前证据

| 项目 | Linux | Windows | macOS |
| --- | --- | --- | --- |
| CLI 编译 | 通过 | 交叉编译通过 | arm64 交叉编译通过 |
| 官方 Codex CLI 配置/目录合同 | 0.159.2 隔离配置加载与 Fast 档位通过 | 未原生运行 | 未原生运行 |
| 桌面编译/启动 | GTK3 编译、Xvfb 原生 WebKit 窗口启动通过 | GUI 交叉编译通过，未原生运行 | 未原生构建/运行 |
| Web 界面与路由生命周期 | 假后端浏览器/HTTP/进程测试通过 | 未原生运行 | 未原生运行 |
| 登录自启 | 临时 HOME + 系统 GLib 参数解析通过，未实际登录 | 模拟注册表通过，未实际登录 | plist 测试通过，未实际登录 |
| 原生托盘/真实 Codex + CPA | 待验收 | 待验收 | 待验收 |

## 对应检查

1. **构建与显示**：使用平台支持的构建产物打开 GUI。确认主窗口显示，关闭窗口后
   只隐藏；托盘点击切换面板，失焦隐藏，再次启动唤起已有主窗口。无托盘宿主的
   Linux 桌面需确认主窗口仍可用。
2. **直连往返**：记录已有配置及登录文件的哈希/权限，用测试 provider 走官方 →
   直连 → 官方。确认 `/fast` 可选且上游实际收到对应档位，用户设置和登录文件
   保留，qcs 写入的 token/认证头撤销，provider 定义仍存在。用户期间改过的值
   应保留并在还原报告中显示。
3. **路由往返**：预览后取消，确认没有配置变化；再确认启动，重启对应桌面端和
   仍在运行的共享 app-server。并存模式下官方模型仍用官方订阅，provider 前缀
   模型走第三方；全走上游时普通模型名也走第三方。确认 Fast、SSE、压缩请求和
   WebSocket 426 后的 HTTP 回退。停止路由后原地址恢复，再重启 Codex。
   分别记录 Fast 可选、实际请求档位、CPA 的响应档位/计费信息及实际延迟，不能以
   “请求有 priority”替代真实上游接受档位的证据。若订阅额度耗尽导致桌面端不发送，
   单独记录；本工具没有实现 Magpie 完整额度检测/API 状态回退。
4. **多窗口与进程**：主窗口和托盘面板显示相同状态；外部 CLI 持有的路由不能被
   当前窗口错误停止。退出持有路由的应用/服务会还原，关 Web 标签不会停止已运行
   的路由。强制结束后再次启动接管，或显式恢复残留配置。
5. **登录启动**：在设置中明确开启后检查系统登记指向当前程序，注销/登录后只启动
   隐藏 GUI，路由仍关闭。停止应用后下次登录可再次启动。关闭登记、移动程序后更新
   路径、在系统设置中禁用等行为需分别验收。Windows 检查 StartupApproved；macOS
   检查 Login Items 名称；Linux 检查 XDG 目录与实际桌面兼容性。

每次验收记录系统版本、程序提交、Codex/CPA 版本、检查步骤与实际结果。失败时保留
经过遮盖的错误和复现条件，不把 API key 或登录文件内容写入报告。需要代码修复时
继续走独立阶段的测试、文档和提交。

## 本地自动回归

遵循 AGENTS.md 的 Go 版本与临时 HOME 规范。常规命令：

```sh
umask 022
go test -race -p 4 -tags nogui ./...
go vet -tags nogui ./...
node --test internal/webui/assets/*.test.mjs
make cli
```

准备好 GTK/WebKit 的 Linux 环境还运行 `go test -race -tags gtk3 ./...`、
`go vet -tags gtk3 ./...` 和 `make build`。测试跳过项及原因应按当前运行结果记录。
