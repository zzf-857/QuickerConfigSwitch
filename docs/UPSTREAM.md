# Magpie 复用与来源

本项目从 [yetone/magpie](https://github.com/yetone/magpie) 剥离配置切换功能。
开发前优先查找其已经实现的逻辑与测试，能直接复用就复制/裁剪，不另造同类基础设施。
来源为 MIT，许可原文保存在 `third_party/magpie/LICENSE`，复制后的文件保留版权说明，
覆盖范围写入 `NOTICE`。产品本身的范围仍以 README 和交接文档为准。

## 当前来源映射

本轮新增复用核对基于 Magpie 提交
`7660399a121af67db4efd56ecb6bd4fb04dc53f1`。早期移植未记录具体提交，
不要把它们错误地标成全部来自该版本。

| 本项目 | Magpie 来源 | 本地适配 |
| --- | --- | --- |
| `internal/edit` | `internal/edit` | 保格式编辑、原子写与原始值恢复；增加本项目所需的回归覆盖 |
| `internal/filememo`、`internal/steady`、`internal/filelock` | 同名包 | 保留原有文件/进程同步实现，替换模块导入 |
| `internal/safekeep` | 上述编辑与还原原语 | 适配 plan/stash/applied 快照及会话条件还原；调用方获取不到进程锁时停止，不改变上游 filelock 的通用语义 |
| `internal/codexcat` | `internal/codexcat` | 保留模型条目/Fast 逻辑，适配本项目 provider 类型 |
| Codex 官方订阅模型目录复盘 | `internal/provider/codex_models.go` 的 `CodexCatalog`/`CodexTiers`、`internal/codexcat/codexcat.go` 的 `Entries`、`internal/agent/codex.go` 的双路径配置 | 沿用完整原生条目与 Fast 规则；直连使用精确上游 ID，显式模型列表始终生成目录；名称等于 ID 时保留官方显示名；账户与 OAuth 由 CPA 管理 |
| `internal/autostart`（阶段 C） | `internal/autostart` 的 self/XDG/LaunchAgent/Run/StartupApproved | 直接裁剪平台实现，替换产品名及 `gui --hidden` 参数；增加显式 GUI 能力与已有记录保护 |
| `internal/cli/autostart.go`（阶段 C） | `internal/cli/autostart_cli.go` | 沿用显式启停后查询系统真实记录的流程，适配本项目输出与错误码 |
| `internal/relay/models.go`、命名空间检查（阶段 D） | `internal/gateway/codex_backend.go` 的 `codexModelsWait`、`codexModels`、`codexBackend`；`internal/codexcat/codexcat.go` 的 `CacheEntries` | 精简提取三秒预算、目录缓存回退与未知前缀拒绝，保留原始 JSON、只读/有界缓存、401/403 和会话权限边界 |
| `internal/desktopapp`、Codex Detect（阶段 F） | `internal/agent/desktopapp.go`、`desktopapp_windows.go` 与身份测试 | 只提取 Codex bundle/Store 身份及有限安装目录；测试隔离真实应用目录和注册表 |
| `internal/agent/codex/proxy.go`（阶段 F） | `internal/agent/codex_proxy.go`、`codex_proxy_test.go` | 复制 reqwest 环境变量/NO_PROXY 匹配及回归；不引入 login shell、系统代理或修改代理配置 |
| 显式请求头子表支持（阶段 F） | `internal/agent/codex_headers_table_test.go`、`internal/edit/toml.go` 的 Inline→子表合并 | safekeep 拆分子表所有权、迁移旧 inline 记录；新增小型 quoted-key 编辑入口复用原语；新文件 0600 在调用层处理 |
| `internal/agent/claude`（阶段 G） | `internal/agent/claude.go` 的 cloudSwitches、writeTiers/dropAbout、claudeName/claudeEffortsFor/effort 字段；`internal/provider/claudedash.go` | 提取云/别名/能力清理和 effort 规则；直连没有网关反向映射，保留精确 ID；加认证隔离、原有上限及管理员冲突检查 |
| `internal/desktopdir`、`internal/agent/claudedesktop`（阶段 H） | `internal/desktopdir/desktopdir.go` 及测试；`internal/agent/claudedesktop.go` 的路径、profile/meta/deploymentMode 写入；`internal/gateway/desktop.go` 的模型过滤 | 复制多平台/MSIX 路径与 recency 逻辑，写入改为 plan/safekeep；只支持单一 Anthropic endpoint/Claude 兼容模型，保留原登录/用户数据，不复制账户池或别名路由网关 |

Magpie 的配置适配器与网关不是直接整包复制的目标：它们还包含账户、路由组、
后台守护进程和自动重启等本项目暂不需要的功能。只提取当前配置切换所需部分。

## Codex 官方订阅与 CPA

主场景为 CPA 持有官方 OAuth/订阅，QCS 配置直连和模型目录。客户端本机没有官方
登录，也能选择 GPT 模型与 Fast；不要把自定义 provider 状态等同于上游不再使用
官方订阅。实际官方 Codex 0.159.2 的无登录目录、临时 thread/turn、priority 请求
及还原证据见 [CODEX_SUBSCRIPTION.md](CODEX_SUBSCRIPTION.md)。本地假 SSE 不替代
真实 Desktop/CPA/订阅验收。

- Magpie 的 [`codexAccount`](https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/provider/account.go#L1012)
  与 [`codexSign`](https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/provider/codex_request.go#L31)
  将官方订阅作为 provider，用 OAuth 签名请求 ChatGPT backend。本项目不复制账户池、
  令牌刷新或订阅网关，将上游职责留在 CPA。
- Magpie 的 `CodexCatalog`/`Entries` 为自定义 provider 提供完整官方模型条目与
  `service_tiers`。本项目保留该设计；修复纯原生 GPT 列表未绑定目录时显示过多模型、
  拉取结果的默认名称覆盖官方显示名的问题。
- Magpie 的 [`addLogin`](https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/provider/signin.go#L983)
  在客户端尚未登录时还会写入真实 Codex 认证存储。它与本项目只改配置、不写登录文件
  的范围不同；不能以“只在 Magpie 登录”推断本机没有官方登录，也不为兼容 API provider
  构造虚假官方认证。
- Magpie 的 `putProvider` 所写 actor header 用于 OpenAI 工具能力，独立于模型列表和
  Fast；本轮不自动移植。CPA 的工具协议、实例开关及实际调用需独立验证，不给所有
  provider 增加共享默认头。来源与 CPA 固定版本证据见 CODEX_SUBSCRIPTION.md。

## 路由相关参考边界

- `internal/gateway/codex_backend.go`：阶段 D 已提取目录限时/回退和命名空间检查；
  HTTP/SSE 仍使用本项目的小型 ReverseProxy，不导入账户池、路由组或协议转换。
- `internal/agent/codex.go` 的 `EndCodexFailover`：本项目保留 snapshot + 会话条件还原，
  以恢复用户原来的地址，并保留后来发生的其它切换，不引入其完整 daemon 管理。
- Codex 的共享 app-server 可能跨新 CLI 会话保留 base URL 和模型目录。改配置后需确认
  对应桌面端/共享 app-server 已重启，不能仅凭“新开终端”判断新配置已生效。
- 沿用其 GPT/o 系列 Fast 默认能力规则，补齐 provider 尚无模型列表时的隐式模型；
  已明确保存的 Fast=false 不覆盖。旧配置内 profile 冲突保护参考 `internal/agent/codex.go`
  的 Check；现代独立 profile 配置层仍由客户端启动方式决定。
- 不移植完整账户额度耗尽检测或 API 状态回退；保留官方登录模式仍受客户端本身的
  认证/额度行为影响。实际合同与核验边界见 REQUIREMENTS.md。
- Claude CLI 的新行为还用官方 npm 分发的 2.1.302 实际请求核对，分发归档经过其
  SHA512 integrity 校验。空 token 会回退官方 OAuth、X-Api-Key-only 不阻止附带 OAuth，
  直连需第三方 token 或明确 Authorization；这与 Magpie 自有网关负责隔离的边界不同。
- Desktop 的 `inferenceModels` 与直接 gateway profile 格式还核对了 CC Switch
  `7344cc37865da59308bca1a90c95ab2ee05c957c` 的 `claude_desktop_config.rs`；
  只作格式证据，未复制它的实现。未知 URL 子路径和需要网关转换的模型直接拒绝，
  不把配置写入成功当成客户端实际支持。不同客户端边界见 CLIENTS.md。

## 接手时的操作

参考仓库可克隆到项目之外，例如 `/tmp/qcs-magpie-reference`；记录 `git rev-parse HEAD`
后再复制。不要改动参考仓库、把它混进本项目依赖或覆盖现有用户配置。
移植后运行本项目的临时 HOME 测试，特别检查凭据文件不变、用户改动保留、退出还原、
跨平台路径和失败时的状态回滚。复制可用逻辑之后再按明确的行为差异做最小调整。
