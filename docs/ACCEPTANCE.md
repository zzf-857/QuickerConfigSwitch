# 真机验收与回归入口

本文件供后续 Agent 或维护者接手验收使用。自动测试全部使用临时配置和假后端；
不能据此填写真实账号、真实系统托盘或登录自启“通过”。阶段实现见 STAGES.md，
来源与裁剪规则见 UPSTREAM.md。

核心要求与实现证据见 REQUIREMENTS.md，CPA 与本机两种凭据归属见
[CODEX_SUBSCRIPTION.md](CODEX_SUBSCRIPTION.md)。主验收场景是 **CPA 持有订阅 OAuth、
Codex 用 CPA 访问密钥直连（模式 D）**；本机无需 ChatGPT 登录或常驻 QCS 路由。
另需本机官方账号与第三方并存时，再验收可选模式 R。

官方 Codex CLI 0.159.2 已在隔离配置下通过五项实际 app-server 检查：原三项目录
合同，加上无本机登录的新 provider、从 CPA 假后端拉取单一 GPT 列表两项。
两项无登录场景验证原生名称、推理/Fast 档位，并完成真实客户端的 thread/turn 与
loopback 假 SSE 请求；这仍不代表真实订阅 OAuth、远端 CPA 或桌面交互验收。
官方 Claude CLI 2.1.302 已通过临时配置与 loopback SSE 的六项实际请求检查，覆盖
ID、认证、effort 及上限；可用
`python3 scripts/check_claude_contract.py --qcs ./qcs --claude /path/to/claude`
重跑。两个脚本都不访问真实账户或远端模型。
有官方客户端时，可用 Python 3.11+ 运行
`python3 scripts/check_codex_contract.py --qcs ./qcs --codex /path/to/codex`
重做临时配置、目录及假 SSE 请求检查；该脚本不登录账号，不访问真实 CPA 或进行
真实模型推理。

另有保留 ChatGPT 客户端状态与 CPA 分流的联合合同：先用
`go build -tags nogui -o /tmp/qcs-relay-fixture ./scripts/fixtures/codex-relay` 构建测试夹具，
再运行 `python3 scripts/check_codex_relay_contract.py --qcs ./qcs --codex /path/to/codex --relay-fixture /tmp/qcs-relay-fixture`。
该测试仅使用临时合成登录和 loopback；真实 Codex 识别 ChatGPT 状态且原名/Fast
保留、实际请求仅交给假 CPA。Linux 0.159.2 证据不替代真实账号或桌面验收。

## 当前证据

| 项目 | Linux | Windows | macOS |
| --- | --- | --- | --- |
| CLI 编译 | 通过 | 交叉编译通过 | arm64 交叉编译通过 |
| 官方 Codex CLI 配置/目录/请求合同 | 0.159.2 五场景；含无登录、原生 GPT/Fast、上游列表筛选与假 SSE 请求通过 | 未原生运行 | 未原生运行 |
| 官方 Claude CLI 请求合同 | 2.1.302 本地假后端六场景通过 | 未原生运行 | 未原生运行 |
| Claude Desktop 配置 | Linux/macOS/Windows/MSIX 路径夹具、回滚、认证还原、UI 流程通过 | 仅夹具与交叉编译 | 仅夹具，未运行客户端 |
| 桌面编译/启动 | GTK3 编译、Xvfb 原生 WebKit 窗口启动通过 | GUI 交叉编译通过，未原生运行 | 未原生构建/运行 |
| Web 界面与路由生命周期 | 假后端浏览器/HTTP/进程测试通过 | 未原生运行 | 未原生运行 |
| 登录自启 | 临时 HOME + 系统 GLib 参数解析通过，未实际登录 | 模拟注册表通过，未实际登录 | plist 测试通过，未实际登录 |
| 原生托盘/真实 Codex + CPA | 待验收 | 待验收 | 待验收 |

## 对应检查

1. **构建与显示**：使用平台支持的构建产物打开 GUI。确认主窗口显示，关闭窗口后
   只隐藏；托盘点击切换面板，失焦隐藏，再次启动唤起已有主窗口。无托盘宿主的
   Linux 桌面需确认主窗口仍可用。
2. **主场景：CPA 订阅直连往返（模式 D）**：先确认 CPA 自己已登录订阅，记录其
   版本、Responses 地址和可用模型；QCS 只配置 CPA 访问密钥。分别使用无本机
   `auth.json` 的隔离 Codex 配置、已有官方登录的配置验收。先记录原配置及已有
   登录文件的哈希/权限，再走原配置 → 直连 → 还原。切换后重启桌面端及共享
   app-server，使用新 CLI 会话；确认以下行为：

   - 拉取 CPA 的 `/v1/models` 后，模型选择器只列上游提供的模型；例如 CPA 只列
     `gpt-5.5`，就不应混入其它内置模型。默认 `name == id` 时保留 `GPT-5.5`
     原生名称、原生推理档位及 Fast；明确设置的自定义名称仍尊重用户选择。
   - 开启 Fast 后，实际模型请求使用原始 `gpt-5.5` 和 `service_tier: priority`，
     只携带 CPA 的认证，CPA 再使用其管理的订阅。记录 CPA 是否接受档位、响应/
     计费信息和实际延迟；目录出现 Fast 或请求有 priority 不能替代这些证据。
   - QCS 切换命令结束后，Codex 仍可直接访问运行中的 CPA。无本机登录的用例全程
     不创建 `auth.json`；已有登录文件内容、身份、时间和权限保持。切回后原设置
     恢复，qcs 写入的 token/认证头撤销，provider 定义仍保留以支持历史会话。
   - 用户期间改过的设置应保留并在还原报告显示。另行记录桌面端的本机账号界面、
     API 模式及 provider 会话列表隔离；这些不等同于 GPT 模型元数据或 Fast 丢失。
     真实订阅认证失败、额度或档位限制由 CPA 与其上游的证据定位。

3. **可选：本机官方账号与 CPA 并存（模式 R）**：使用本机已有官方登录的配置。
   预览后取消，确认没有配置变化；再确认启动，重启对应桌面端和
   仍在运行的共享 app-server。并存模式下官方模型仍用官方订阅，provider 前缀
   模型走第三方；全走上游时普通模型名也走第三方。确认 Fast、SSE、压缩请求和
   WebSocket 426 后的 HTTP 回退。停止路由后原地址恢复，再重启 Codex。
   分别记录 Fast 可选、实际请求档位、CPA 的响应档位/计费信息及实际延迟，不能以
   “请求有 priority”替代真实上游接受档位的证据。若订阅额度耗尽导致桌面端不发送，
   单独记录；本工具没有实现 Magpie 完整额度检测/API 状态回退。这是保留本机
   官方 provider 的路由场景边界，不应作为无本机登录的模式 D 的前置要求。
   对 R All 另行记录 `account/read`（关闭 refreshToken）的账号类型仍为 `chatgpt`、
   内置 provider 不变，且原名 GPT 的一次实际 Responses 请求仅由 CPA 承接、带 priority。
   本机账号与 CPA 实际所用账号可不同，应分别记录身份/额度归属。CPA 失败时应直接
   报错，不自动消耗本机订阅；目录、搜索与其它官方请求仍可能访问官方，不能混称全网络 CPA。
4. **多窗口与进程**：主窗口和托盘面板显示相同状态；外部 CLI 持有的路由不能被
   当前窗口错误停止。退出持有路由的应用/服务会还原，关 Web 标签不会停止已运行
   的路由。强制结束后再次启动接管，或显式恢复残留配置。
5. **登录启动**：在设置中明确开启后检查系统登记指向当前程序，注销/登录后只启动
   隐藏 GUI，路由仍关闭。停止应用后下次登录可再次启动。关闭登记、移动程序后更新
   路径、在系统设置中禁用等行为需分别验收。Windows 检查 StartupApproved；macOS
   检查 Login Items 名称；Linux 检查 XDG 目录与实际桌面兼容性。
6. **Claude CLI**：使用真实 provider 验证精确 ID、主/后台/子代理模型、请求头及 effort。
   原有 maxEffortLevel 不应被放宽；不支持的档位明确拒绝。先在专用账户确认空认证被
   拒绝，不能为验证方便把官方令牌配到第三方。往返后比较配置和登录文件身份/时间/权限。
7. **Claude Desktop**：完整退出后切换 3p 并重开，验证 Code/Cowork 的实际模型请求、
   原目录与 Claude-3p 数据隔离、MCP/会话的保留、切回原登录模式及 Windows MSIX
   新旧版本目录。验证真实账户/客户端计划及网络策略；不默认开启任意 Cowork 出站。
   同时检查新 profile 的 Unix 权限或 Windows 用户目录 ACL，以及用户期间增改 profile
   后的还原报告。复制路径测试不能代表三个系统上原生应用都已支持。

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
