# QuickerConfigSwitch

跨 Windows / macOS / Linux 的本地 Agent 配置快速切换工具。

- 给 Codex、Claude Code 一键切换到自定义 provider / 模型
- 只改必要的键，注释与格式原样保留；随时还原原配置
- 不碰官方登录文件（Codex 的 `auth.json`、Claude Code 的凭据）：切回即恢复官方订阅
- Codex 切到第三方（如 CPA）后仍保留 Fast 模式

架构设计见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 当前进度

第一阶段（命令行版核心）已完成：Codex 与 Claude Code 的「官方 / 直连」两种模式，备份、原值记录、还原与漂移检测。图形界面（第二阶段）和保留订阅的本地路由（第三阶段）尚未开始。

## 构建

需要 Go 1.26（较旧的 Go 会自动下载对应工具链）。

```sh
go build -o qcs ./cmd/qcs
go test ./...
```

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
- `[model_providers.qcs-<id>]` 表在切回官方后会保留，以便重新打开在它上面开过的会话。

## 许可

第三方代码说明见 [NOTICE](NOTICE)。参考项目：[yetone/magpie](https://github.com/yetone/magpie)（MIT）。
