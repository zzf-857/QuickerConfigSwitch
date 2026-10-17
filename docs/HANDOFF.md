# 交接文档

> 截至 2026-10-17。分支 `claude/exciting-galileo-cgd4uk`，所有改动已推送。
> 先读：[README.md](../README.md)（用法）→ [ARCHITECTURE.md](ARCHITECTURE.md)（设计与 Codex 源码分析）→ 本文。

## 1. 一句话现状

第一阶段（CLI 核心）和第二阶段（图形界面、方案、导入）**已完成并通过全部测试**。第三阶段（保留官方订阅的本地路由，即"模式 R"）**尚未开始**。全部功能**还没在真实用户机器上试过**，只在测试和虚拟显示器里验证过。

## 2. 已完成的功能

| 功能 | 入口 | 验证方式 |
| --- | --- | --- |
| 自定义 provider 增删改、拉取 `/v1/models`、每个模型的 Fast 开关 | `qcs provider …`、界面 Providers 页 | 单元测试 + httptest |
| Codex 直连第三方（模式 D），保留 `auth.json`、用户的 `service_tier`/Fast、`[features]`、CC Switch 表 | `qcs use codex cpa/<model>`、界面 Agents 页 | 单元 + 端到端 + 手工冒烟 |
| 为 Codex 生成带 Fast 档位的模型目录（`model_catalog_json`），只在需要时写 | 自动 | 单元 + 端到端 |
| Claude Code 直连第三方（写 `settings.json` 的 `env`），保留 JSONC 注释 | `qcs use claude …` | 单元 + 端到端 |
| 切回官方：文件没被别人改过时逐字节还原，否则只撤销 qcs 写的键 | `qcs use <agent> official` / `qcs restore` | 单元 + 端到端 |
| 先预览差异再确认；预览/界面/CLI 中的 API key 一律遮盖 | 全部入口 | 单元 |
| 漂移检测（被 CC Switch 或 Codex 自己的 `/model` 改过）、冲突检查 | `qcs doctor`、界面「检查」 | 单元 |
| 方案（Profile）：保存当前、一键应用、内置「official」 | `qcs profile …`、界面「方案」页 | 单元 |
| 从 CC Switch（SQLite/旧版 config.json）、Codex config.toml、Claude settings 导入 provider（只读） | `qcs import`、界面 Providers 页底部 | 单元（真实 SQLite 夹具） |
| 桌面版：Wails v3 托盘 + 小面板（`/?mode=panel`）+ 主窗口 | `qcs` 或 `qcs gui [--hidden]` | 在 Xvfb 下真实启动、截图 |
| 浏览器版：本地 HTTP + 随机令牌 + Origin/Host 校验 | `qcs web` | 单元 + Playwright 截图 |

## 3. 构建与测试

```sh
make test        # = go test -tags gtk3 ./...（Linux 必须带 gtk3 标签，见 Makefile）
make build       # 带 GUI 的 qcs
make cli         # -tags nogui，纯 CLI + qcs web，可交叉编译
node --test internal/webui/assets/*.test.mjs   # 前端纯函数测试
```

- Go 1.26（go.mod 要求；旧 Go 会自动下载工具链）。
- Linux 编 GUI 需要 `libgtk-3-dev libwebkit2gtk-4.1-dev`；不带 `gtk3`/`nogui` 标签会去找 GTK4 而失败。
- macOS / Windows 的 GUI 需要在本机原生编译（CGO）；只验证过 `nogui` 交叉编译和 Windows 的 GUI 编译。
- 所有测试通过 `internal/testenv` 把 HOME 等指向临时目录，**不会碰真实配置**。新增测试包必须加 `func TestMain(m *testing.M) { testenv.Main(m) }`。
- 界面开发服务器：`go run ./internal/webui/internal/devserver -addr 127.0.0.1:8765 -token dev [-switched]`（用临时 home 和假数据）。

## 4. 代码地图

```
cmd/qcs                 入口
internal/cli            命令行（ls/provider/use/diff/restore/doctor/profile/import/web/gui）
internal/core           GUI、web、CLI 共用的门面：Agents/PreviewUse/Use/Doctor/方案/Redact
internal/webui          JSON API + 内嵌页面（assets/ 下原生 JS，无构建步骤）
internal/gui            Wails 桌面壳（!nogui）；nogui.go 为无界面构建
internal/agent          Agent 接口与注册表
  ├ codex               Codex 适配器（规则见 ARCHITECTURE §4.5）
  └ claude              Claude Code 适配器
internal/plan           变更计划类型：适配器只产出计划，不写文件
internal/safekeep       执行计划：快照、滚动备份、stash（原值）、applied（写入值）、还原、漂移、预览
internal/codexcat       Codex 模型目录生成（含内置 models.json 与提示词）
internal/provider       provider 数据与 /models 拉取
internal/importer       从 CC Switch / Codex / Claude 导入
internal/store          防损坏的 JSON 存取（.bak 回退）
internal/edit filememo steady filelock   移植自 magpie（MIT，见 NOTICE）
internal/e2e            端到端测试（官方 → CPA → 官方）
```

关键约定：
- **适配器永远不写文件**，只返回 `plan.Plan`；写入、备份、还原全部由 `safekeep` 负责。
- `safekeep.Apply` 的语义是"本次计划是该 Agent 的**全部**期望状态"：上次写过、这次没写的键会还原。
- `plan.Op.Keep`：还原时保留（Codex 的 `[model_providers.qcs-*]` 表，旧会话重新打开要用）。
- 数据目录：`appdir.Dir()`（可用 `QCS_HOME` 覆盖）下的 `providers.json`、`profiles.json`、`state/stash.json`、`state/applied.json`、`backups/`、`codex/catalog-*.json`。

## 5. 已知问题与待办（按优先级）

**需要尽快做**
1. **在真实机器上验收**：macOS / Windows / Linux 各装一次，用真实 Codex 桌面端 + CPA 走一遍「切换 → /fast 可用 → 切回」。目前只验证了生成的配置符合 Codex 源码，没跑过真实 Codex。
2. **切回官方后 provider 表里留着明文 key**（因为 Keep）。考虑：还原时只删 `experimental_bearer_token`、或提供"彻底清除"选项。
3. **托盘相关行为未实测**：点击切换面板、失焦隐藏、二次启动唤起主窗口、macOS 无 Dock 图标。Xvfb 里没有托盘宿主，只验证了主窗口能显示。

**第三阶段：模式 R（本地路由）** —— 设计见 ARCHITECTURE §2.5、§4.8
- 只写 `openai_base_url = http://127.0.0.1:<port>/backend-api/codex`，ChatGPT 登录、历史会话、桌面端功能都保留。
- 新包 `internal/relay`：`/models` 合并官方列表 + 自定义模型条目（用 `codexcat` 生成）；`/responses` 按模型分流（官方原样透传 chatgpt.com，自定义改 key 转发 CPA，`service_tier` 保留）；WebSocket 回 426；退出时删掉 `openai_base_url`。
- Codex 适配器的 `Plan` 目前对 `agent.Relay` 返回 `ErrRelayUnavailable`，从这里接入。
- magpie 的参考实现：`internal/gateway/codex_backend.go`、`internal/agent/codex.go` 的 `EndCodexFailover`。

**其他待办**
- 界面没有"编辑方案"功能（`PUT /api/profiles/{name}` 已有，前端未调用）；没有"测试连接"按钮。
- 开机自启（`qcs gui --hidden` 已支持，缺各平台注册）。
- 会话迁移：把 `qcs-*` 上开的 Codex 会话改回 `openai`（直连模式下桌面端会话按 provider 隔离）。
- 更多 Agent（Gemini CLI、OpenCode…）：实现 `agent.Agent` 接口并在 `core/core.go` 里空导入注册即可。
- key 存系统钥匙串（目前 `providers.json` 权限 0600 明文）。
- 内置的 Codex `models.json`（`internal/codexcat/bundled_models.json`）会过时，需要定期从 openai/codex 更新；运行时优先读用户的 `~/.codex/models_cache.json`。
- WebUI 令牌 cookie 按主机而非端口隔离（HttpOnly + SameSite=Strict，风险较低）。

## 6. 参考资料位置

上一个会话把参考仓库克隆在临时目录，新会话里需要重新获取：
- magpie：`git clone https://github.com/yetone/magpie`（重点：`internal/agent/codex.go`、`internal/edit`、`docs/subsystems/agent-wiring.md`）
- Codex 源码：`openai/codex` 的 `codex-rs/model-provider-info`、`models-manager`、`tui/src/chatwidget/service_tiers.rs`
- CPA：`router-for-me/CLIProxyAPI` 的 `internal/translator/codex/openai/responses/`（service_tier 透传）

## 7. 提交历史里的 WIP

几处 "WIP …" 提交是子代理工作中途的检查点，后续提交已完成对应内容，不需要处理。
