# QuickerConfigSwitch

跨 Windows / macOS / Linux 的本地 Agent 配置快速切换工具。

- 给 Codex、Claude Code 一键切换到自定义 provider / 模型
- 只改必要的键，注释与格式原样保留；随时还原原配置
- 不碰官方登录文件（Codex 的 `auth.json`、Claude Code 的凭据）：切回即恢复官方订阅
- Codex 切到第三方（如 CPA）后仍保留 Fast 模式

架构设计见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 当前进度

- 已完成：CLI 核心（Codex / Claude Code 的「官方 / 直连」切换、备份还原、漂移检测）、桌面版（托盘 + 窗口）、浏览器版 `qcs web`、方案、从 CC Switch 等导入。
- 已实现：模式 R 的前台本地路由（官方与第三方并存、全走第三方、退出还原及异常退出恢复）。托盘内管理路由和开机自启尚未接入。
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

切到 Codex 直连模式后：

- 重启 Codex 桌面端（或新开 CLI 会话）生效；
- 桌面端只显示在这个 provider 上开的会话，ChatGPT 登录态功能暂不可用；切回官方即恢复；
- `[model_providers.qcs-<id>]` 表在切回官方后会保留，以便加载旧会话；qcs 写入的密钥会撤销，原来已有的密钥还原，被用户另行修改的值保留。再次使用该 provider 时重新切换即可写入当前密钥。

## 保留官方登录的本地路由

先配置一个支持 Responses 的 provider，然后在终端运行：

```sh
qcs use codex official                      # 如果此前启用了直连，先还原
qcs use codex cpa/gpt-5.5 --relay             # 预览、确认后前台运行
qcs use codex cpa/gpt-5.5 --relay --relay-all # 所有模型请求都发给 CPA
qcs relay recover                           # 异常退出后只恢复配置
```

保持终端中的路由进程运行，重启 Codex，再在模型选择器里选择 `cpa/gpt-5.5`。默认并存模式下，原有官方模型仍走官方订阅，带 `cpa/` 前缀的模型走 CPA。`--relay-all` 也将没有前缀的模型发给 CPA，需要上游支持对应模型。

路由只改 `openai_base_url`，保留 `model_provider`、当前模型、Fast 和推理强度；在 Codex 内调整这些选择。已有自定义 `model_catalog_json` 或活动 profile 中覆盖路由配置的字段时，会提示先处理冲突。CLI 路由不接受 `--fast`、`--no-fast`、`--effort`。

Ctrl+C 或 SIGTERM 会停止路由并还原原来的地址。异常退出后，重新执行 `--relay` 会接管旧记录并更换为可用端口，也可以用 `qcs relay recover` 还原；随后重启 Codex。默认端口 17321 被占用时自动选择空闲端口，`--port 0` 可显式选择随机端口。路由只监听本机，不支持 WebSocket，返回 426 以请求客户端使用 HTTP。

当前路由入口在 CLI，运行中的路由不能保存为方案。官方登录、真实上游 Fast 和桌面端仍需要用户机器验收；自动测试使用本地假后端，不需要真实密钥。

## 许可

第三方代码说明见 [NOTICE](NOTICE)。参考项目：[yetone/magpie](https://github.com/yetone/magpie)（MIT）。
