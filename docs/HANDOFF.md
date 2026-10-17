# 交接文档

> 截至 2026-10-17。后续工作按阶段验证并提交；阶段状态见 [STAGES.md](STAGES.md)，具体提交见 Git 历史。原交接分支为 `claude/exciting-galileo-cgd4uk`。
> 接手顺序：[AGENTS.md](../AGENTS.md)（开发规范）→ [README.md](../README.md)（用法）→ 本文与 [STAGES.md](STAGES.md)（状态/下一步）→ [ARCHITECTURE.md](ARCHITECTURE.md)（设计细节）。

## 1. 一句话现状

CLI 核心、图形界面、方案和导入已完成；模式 R 现支持 **CLI、GUI/Web 和托盘生命周期管理**。接下来的阶段是可选择的开机自启。切回官方后的 provider 密钥残留已修复。具体阶段与验证见 [STAGES.md](STAGES.md)。真实用户机器上的验收仍未完成，不能把本地假后端测试视为真实 Codex/CPA 验收。

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
| 切回官方时保留 provider 定义、撤销 qcs 写入的密钥；原有值和用户改动保留 | `qcs use codex official` / `qcs restore codex` | clean/dirty、旧记录迁移、重复切换、预览回归 |
| 官方与第三方并存 / 全走第三方的前台路由 | `qcs use codex cpa/gpt-5.5 --relay [--relay-all]` | 本地假后端、SSE、压缩请求、凭据隔离 |
| 路由退出还原、异常重启、手动恢复、端口占用回退 | `qcs relay recover` / 再次运行 `--relay` | 生命周期及进程信号测试 |
| GUI/Web 路由预览、确认、停止、恢复和所有者状态；托盘停止/恢复 | Codex 卡片「本地路由」 / 托盘菜单 | 控制器/API race、Web SIGTERM、真实后端 Chromium、GTK 编译与原生窗口启动 |

## 3. 构建与测试

```sh
make test        # = go test -tags gtk3 ./...（Linux 必须带 gtk3 标签，见 Makefile）
make build       # 带 GUI 的 qcs
make cli         # -tags nogui，纯 CLI + qcs web，可交叉编译
node --test internal/webui/assets/*.test.mjs   # 前端纯函数测试

# 无 GTK/WebKit 的云环境：
source /workspace/.qcs-tools/activate.sh # 此云环境的 Go 1.26；其它环境用已安装的 Go
umask 022                              # readonly fixture 测试要求普通创建权限
go test -tags nogui ./...
go vet -tags nogui ./...
make cli
```

- Go 1.26（go.mod 要求；旧 Go 会自动下载工具链）。
- Linux 编 GUI 需要 `libgtk-3-dev libwebkit2gtk-4.1-dev`；不带 `gtk3`/`nogui` 标签会去找 GTK4 而失败。
- macOS / Windows 的 GUI 需要在本机原生编译（CGO）；只验证过 `nogui` 交叉编译和 Windows 的 GUI 编译。
- 所有测试通过 `internal/testenv` 把 HOME 等指向临时目录，**不会碰真实配置**。新增测试包必须加 `func TestMain(m *testing.M) { testenv.Main(m) }`。
- 界面开发服务器：`go run ./internal/webui/internal/devserver -addr 127.0.0.1:8765 -token dev [-switched]`（用临时 home 和假数据）。
- 当前云环境的完整 Go 依赖已缓存并通过 `go mod verify`。SQLite/libc 首次下载受网络重定向限制，已从官方 GitLab 源码生成与 `go.sum` 完全一致的模块归档；辅助工具及归档在 `/workspace/.qcs-tools`。复建若再次下载受阻，需要环境允许 `storage.googleapis.com`。
- 路由测试只访问本地假后端；真实 ChatGPT 登录与 CPA Fast、原生桌面完整交互未在本次云环境验收。
- 云环境额外准备了 rootless GTK/WebKit 前缀：先激活 Go，再 `source /workspace/.qcs-tools/gtk/activate.sh`，可运行 `make build` 和 `go test -tags gtk3 ./...`。原生启动用 `/workspace/.qcs-tools/gtk/run-native.sh <GUI命令>` 映射 WebKit 的编译时子进程路径；依赖来自经过 APT 签名/校验和验证的 Debian 包。详细用法见该目录 README；普通开发机直接安装 README 中的系统包即可。

阶段 A 的验证记录（后续阶段结果见 STAGES.md）：

- `go test -race -p 4 -tags nogui ./...`：16 个有测试的包通过，269 个顶层测试通过；2 个按条件跳过（当前用户没有第二个组，及只供子进程调用的文件锁 helper）。含子用例共 803 条通过记录。
- `go vet -tags nogui ./...`、`go mod verify`、前端 9 项测试、Linux `make cli` 均通过。
- Windows amd64、macOS arm64 的 `nogui` 交叉编译通过；这不代表原生 GUI 或真机运行验收。
- Web fixture HTTP 冒烟通过：页面、授权状态查询、未授权 401、切换预览。Unix 子进程测试验证 SIGTERM、SIGKILL、重启恢复和登录文件不变。
- 阶段 B：新增控制器/鉴权 API/进程级检查，前端现在 15 项测试；真实后端 Chromium 验证全部预览关闭路径、单次确认、网络失败释放、停止还原、方案守护和编辑保护。GTK3 原生窗口在 Xvfb 下启动；实际托盘宿主与真实账号仍待验收。

## 4. 代码地图

```
cmd/qcs                 入口
internal/cli            命令行（ls/provider/use/diff/restore/doctor/profile/import/web/gui）
internal/core           GUI、web、CLI 共用的门面：Agents/PreviewUse/Use/Doctor/方案/Redact
                       relay.go 管理前台路由的预览、进程锁、启动、退出和恢复
                       relay_control.go 管理 GUI/Web 共享所有权、预览过期与关闭
internal/relay          Codex 官方 / 第三方 HTTP 路由、模型目录合并、SSE、压缩请求
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
- `plan.Op.RestoreKeys`：Keep 表中仍须撤销的键，当前用于 `experimental_bearer_token`；按键记录原值与最后写入值，用户改过的值不动。旧状态迁移只处理仍有明确写入记录的 qcs 表。
- `safekeep.RestoreIfState`：锁内校验会话标记后还原；路由退出不能撤销另一个进程后来的切换。Apply/Restore 在配置文件和状态记录任一写入失败时一起回滚。
- GUI/Web 的 `core.RelayController` 由宿主进程创建并负责 Close；Handler 不隐式创建生命周期。Wails `OnShutdown` 必须同步清理，单用 defer 在部分平台无效。路由预览返回一次性 ID，确认只用该 ID；关闭/超时不能保留进程锁。
- 数据目录：`appdir.Dir()`（可用 `QCS_HOME` 覆盖）下的 `providers.json`、`profiles.json`、`state/stash.json`、`state/applied.json`、`backups/`、`codex/catalog-*.json`。

## 5. 已知问题与待办（按优先级）

**需要尽快做**
1. **在真实机器上验收**：macOS / Windows / Linux 各装一次，用真实 Codex 桌面端 + CPA 走一遍「切换 → /fast 可用 → 切回」。目前只验证了生成的配置符合 Codex 源码，没跑过真实 Codex。
2. **旧版本已还原且丢失管理记录的残留 key**：新的撤销逻辑和旧状态迁移已经实现，但不能仅凭 `qcs-*` 表名前缀删除无记录的值。若要提供彻底清理，需另做可预览、用户明确选择的流程。provider 数据和历史备份仍按原设计保存；本次修复不是整盘密钥擦除。
3. **托盘相关行为未实测**：点击切换面板、失焦隐藏、二次启动唤起主窗口、macOS 无 Dock 图标。Xvfb 里没有托盘宿主，只验证了主窗口能显示。

**第三阶段：模式 R（已完成的 CLI 范围与后续）** —— 设计见 ARCHITECTURE §2.5、§4.8
- 已完成：只写 `openai_base_url`；保留官方 provider、登录文件、模型/Fast/推理选择。已有直连、显式模型目录、活动 profile 覆盖时拒绝并说明冲突。
- 已完成：`/models` 合并官方列表和 `<provider>/<model>` 别名；`/responses`、`/responses/compact` 分流，保持 `service_tier` 和流式响应；WebSocket 回 426。
- 已完成：官方请求的凭据和请求体透传；第三方只使用明确配置的头部与 provider key，移除官方查询参数，不允许第三方重定向绕过路由。支持 Codex 的 zstd 压缩请求并限制解压后的大小。
- 已完成：`core.PrepareRelay` 预览并持有进程锁，`Start` 重新校验后写入，退出按会话标记还原；默认端口占用时回退；异常退出后重启接管或 `qcs relay recover`。运行中的路由暂不能保存为普通方案。
- 已完成：GUI/Web/托盘的共享路由控制器和生命周期；主窗口关闭只隐藏，退出应用会还原；Web 服务关闭会还原，关浏览器标签不会停止已经运行的路由。预览两分钟后自动释放。
- 后续：跨平台开机自启、真实 Codex/CPA 验收（含压缩请求和 426 后 HTTP 回退）。不要将 CLI 进程直接启动后立即退出，否则配置会随清理还原。

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
