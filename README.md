# QuickerConfigSwitch

跨 Windows / macOS / Linux 的本地 Agent 配置快速切换工具。

- 给 Codex CLI/桌面端、Claude Code CLI、Claude Desktop 切换自定义 provider / 模型
- 只改必要的键，注释与格式原样保留；随时还原原配置
- 不碰官方登录文件（Codex 的 `auth.json`、Claude Code 的凭据）：切回即恢复官方订阅
- CPA 可统一管理官方订阅账号；Codex 直连 CPA，无需本机官方登录也保留 GPT 原名和 Fast 能力

各客户端配置、登录边界与 Magpie 复盘见 [docs/CLIENTS.md](docs/CLIENTS.md)。
Magpie 官方订阅链路及 CPA 使用方式见 [docs/CODEX_SUBSCRIPTION.md](docs/CODEX_SUBSCRIPTION.md)。
核心要求、证据和验收边界见 [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md)，
架构设计见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 当前进度

- 已完成：CLI 核心（Codex / Claude Code 的「官方 / 直连」切换、备份还原、漂移检测）、桌面版（托盘 + 窗口）、浏览器版 `qcs web`、方案、从 CC Switch 等导入。
- 已实现：模式 R 本地路由（官方与第三方并存、全走第三方、退出还原及异常退出恢复），支持 CLI、桌面/Web 界面和托盘状态/停止，以及可选择的登录自启。
- 已补齐：Claude CLI 认证/后台模型/effort 细节，以及独立的 Claude Desktop 3p 配置入口。ChatGPT/Codex 桌面端按应用标识识别，与 Codex CLI 共用配置。
- 交接说明见 [docs/HANDOFF.md](docs/HANDOFF.md)。

## 构建

需要 Go 1.26（较旧的 Go 会自动下载对应工具链）。Linux 桌面版需要 `libgtk-3-dev libwebkit2gtk-4.1-dev`。

```sh
make build   # 带桌面界面
make cli     # 只有命令行和 qcs web
make test
```

`qcs` 不带参数打开桌面界面；`qcs web` 在浏览器中打开同一界面。

## 用法

```sh
# 添加 CPA（CLIProxyAPI，默认 http://127.0.0.1:8317）
qcs provider add cpa --template cpa --key sk-xxx
qcs provider models cpa --fetch            # 从 /v1/models 拉取模型
qcs provider models cpa --fast my-alias=true

qcs ls                                     # 各 Agent 当前状态
qcs diff codex cpa/gpt-5.5                 # 只看会改什么
qcs use codex cpa/gpt-5.5                  # 切到 CPA（先显示改动，确认后写入）
qcs use codex cpa/gpt-5.5 --fast           # 同时打开 Fast
qcs use claude cpa/claude-opus-4.6
qcs use codex official                     # 切回官方（= qcs restore codex）
qcs doctor                                 # 检查冲突与漂移
```

如果官方订阅的登录、刷新和账号选择都在 CPA 中管理，以上**直连**就是推荐方式。
客户端使用 CPA key，CPA 再用它持有的官方订阅请求 GPT；本机不需要 `auth.json`，
也不需要运行 QCS 本地路由。`cpa/` 是 QCS 选择引用，实际发给 CPA 的模型仍为 `gpt-5.5`。
拉取后的模型列表决定选择器内容，标准 GPT 条目保留官方显示名、指令、推理档位和 Fast；
`--fast` 可以显式开启，省略时保留原选择。实际优先级由 CPA 与它的账号决定。

切到 Codex 直连模式后：

- 重启 Codex 桌面端及仍在运行的共享 app-server，再打开新 CLI 会话；共享进程可能缓存旧地址和模型目录，仅新开终端不一定生效；
- 桌面会话按 provider 区分；远程控制、云任务等本机 ChatGPT 账号功能仍由客户端决定，不是模型目录或 Fast 的前提；
- `[model_providers.qcs-<id>]` 表在切回官方后会保留，以便加载旧会话；qcs 写入的密钥会撤销，原来已有的密钥还原，被用户另行修改的值保留。再次使用该 provider 时重新切换即可写入当前密钥。
- 原有自定义模型目录文件保持不动；已有 provider 模型列表时始终绑定完整的 provider 目录，还原时恢复原指向。旧版活动 profile 覆盖切换键时会提示冲突；独立 `--profile` 配置层应在客户端启动时确认。

## 保留官方登录的本地路由

要保留本机真实官方登录，同时让对话和上下文压缩全部交给 CPA，使用「本地路由 + 全走上游」。
本机登录用于客户端账号状态与官方功能，CPA 自己持有的登录用于它发出的订阅模型请求；
两份认证分开使用。只有 CPA 存登录的方案仍使用前面的直连方式。

先配置一个支持 Responses 的 provider，然后在终端运行：

```sh
qcs use codex official                      # 如果此前启用了直连，先还原
qcs use codex cpa/gpt-5.5 --relay             # 预览、确认后前台运行
qcs use codex cpa/gpt-5.5 --relay --relay-all # 对话及上下文压缩全部发给 CPA
qcs relay recover                           # 异常退出后只恢复配置
```

保持终端中的路由进程运行，重启 Codex。全走上游时可以直接选原名 `gpt-5.5`，无需 `cpa/` 前缀；上游需实际支持所选模型。默认并存模式则选择 `cpa/gpt-5.5` 才走 CPA，普通官方模型仍走官方订阅。

全走上游仅指 Responses/compact 模型请求，失败会报 CPA 错误，不自动改用官方推理。
模型目录、搜索和其它官方路径仍走官方；账号功能也仍由客户端使用本机真实登录访问。
因此保留登录不意味着对话绕过 CPA，也不意味着所有网络都交给 CPA。路由保留 Fast
档位和请求参数，实际提速由 CPA 与上游账号决定。界面显示的本机账号额度与 CPA
实际使用账号的额度不是同一份；本机登录失效及客户端额度阻断仍需单独处理。

模型目录沿用 Magpie 的三秒官方请求预算：网络不可用时回退到 Codex 现有缓存并追加当前 provider 别名，无缓存时仍返回别名；官方 401/403 会正常显示。并存模式的未知 provider 前缀会在本地拒绝。

路由只改 `openai_base_url`，保留 `model_provider`、当前模型、Fast 和推理强度；在 Codex 内调整这些选择。已有自定义 `model_catalog_json` 或活动 profile 中覆盖路由配置的字段时，会提示先处理冲突。CLI 路由不接受 `--fast`、`--no-fast`、`--effort`。

Ctrl+C 或 SIGTERM 会停止路由并还原原来的地址。异常退出后，重新执行 `--relay` 会接管旧记录并更换为可用端口，也可以用 `qcs relay recover` 还原；随后重启 Codex。默认端口 17321 被占用时自动选择空闲端口，`--port 0` 可显式选择随机端口。路由只监听本机，不支持 WebSocket，返回 426 以请求客户端使用 HTTP。

桌面或 `qcs web` 中，在 Codex 卡片选择「本地路由」，选择 provider/模型后预览并确认。预览关闭或两分钟过期会释放预留，不写配置；运行状态可直接停止并还原。托盘菜单显示路由状态，并提供停止和异常恢复。主窗口与托盘面板共享同一个路由，关闭窗口隐藏到托盘，退出应用才会停止路由。

`qcs web` 由服务进程持有路由，关闭浏览器标签不停止已启动的路由；停止服务会还原。另一个 CLI/桌面进程持有的路由会显示为外部进程，本窗口不能代替它停止。运行中的路由不能保存为方案；先停止路由再进行 Codex 直连切换或应用包含 Codex 的方案。路由使用启动时的 provider 配置，编辑 provider 后需停止并重启才生效。

官方 Codex CLI 0.159.2 已验证无本机登录的模型目录、原生推理档位和实际 Fast 请求：本地假 CPA 收到原模型 ID、CPA key 及 `service_tier: priority`。真实订阅、CPA 的实际提速、原生托盘和完整桌面交互仍需要用户机器验收。可选路由中的本机官方认证失效或客户端额度限制仍由官方处理。

## Claude CLI / Desktop

```sh
qcs use claude cpa/claude-opus-4-6 --effort high
qcs use claude-desktop cpa/claude-opus-4-6
qcs use claude-desktop official
```

选择上游实际支持的精确模型 ID。Claude CLI 支持 provider 请求头，阻止空认证回退到官方 OAuth；保留原登录文件和 effort 上限。Claude Desktop 使用独立的 3p 部署模式，当前支持 Code/Cowork 的标准 Claude 兼容模型、Anthropic 根地址和 API key；不提供任意模型转换或自定义请求头。切回恢复原配置，改动后完整退出并重开客户端。

Claude Desktop 的官方登录、MCP 和会话数据保留在原目录，第三方模式使用单独的 `Claude-3p` 目录。详细支持范围及未验收项目见 [docs/CLIENTS.md](docs/CLIENTS.md)。

## 登录自启

桌面应用的「设置」页可以查询、开启或关闭登录自启，也可以用：

```sh
qcs autostart status
qcs autostart enable
qcs autostart disable
```

直接裁剪复用 Magpie 的平台实现：Linux XDG `.desktop`、macOS LaunchAgent、Windows 当前用户 Run/StartupApproved。开启后下次登录运行当前程序的绝对路径及 `gui --hidden`，只打开托盘，不自动开启路由，也不会立即启动另一个进程。关闭自启不退出正在运行的应用。

CLI/Web 专用的 `nogui` 构建可查询和撤销已有登记，不能开启桌面自启。移动程序后，在设置中显式更新登记路径；未知、被其它工具修改或指向符号链接的启动项会保留并显示原因。macOS 下载隔离目录中的应用需先移到 Applications。操作只影响当前用户，不需要系统级安装；系统策略仍可能禁止登录启动。

Linux 的 GLib 无法加载可执行文件路径含 `%` 的启动项，即使按规范转义也会拒绝。因此该路径下会拒绝开启并提示先移动程序，不会写入一个看似开启但无法使用的登记。

移植来源及本地适配见 [docs/UPSTREAM.md](docs/UPSTREAM.md)。

## 许可

第三方代码说明见 [NOTICE](NOTICE)。参考项目：[yetone/magpie](https://github.com/yetone/magpie)（MIT）。
