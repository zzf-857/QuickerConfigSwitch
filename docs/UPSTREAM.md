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
| `internal/safekeep` | 上述编辑与还原原语 | 适配 plan/stash/applied 快照及会话条件还原 |
| `internal/codexcat` | `internal/codexcat` | 保留模型条目/Fast 逻辑，适配本项目 provider 类型 |
| `internal/autostart`（阶段 C） | `internal/autostart` 的 self/XDG/LaunchAgent/Run/StartupApproved | 直接裁剪平台实现，替换产品名及 `gui --hidden` 参数；增加显式 GUI 能力与已有记录保护 |
| `internal/cli/autostart.go`（阶段 C） | `internal/cli/autostart_cli.go` | 沿用显式启停后查询系统真实记录的流程，适配本项目输出与错误码 |

Magpie 的配置适配器与网关不是直接整包复制的目标：它们还包含账户、路由组、
后台守护进程和自动重启等本项目暂不需要的功能。只提取当前配置切换所需部分。

## 路由相关参考边界

- `internal/gateway/codex_backend.go`：优先对照模型目录获取、超时与回退、模型命名空间、
  HTTP/SSE 与头部处理。后续提取时同步复制相关回归测试并注明函数/提交。
- `internal/agent/codex.go` 的 `EndCodexFailover`：本项目保留 snapshot + 会话条件还原，
  以恢复用户原来的地址，并保留后来发生的其它切换，不引入其完整 daemon 管理。
- Codex 的共享 app-server 可能跨新 CLI 会话保留 base URL 和模型目录。改配置后需确认
  对应桌面端/共享 app-server 已重启，不能仅凭“新开终端”判断新配置已生效。

## 接手时的操作

参考仓库可克隆到项目之外，例如 `/tmp/qcs-magpie-reference`；记录 `git rev-parse HEAD`
后再复制。不要改动参考仓库、把它混进本项目依赖或覆盖现有用户配置。
移植后运行本项目的临时 HOME 测试，特别检查凭据文件不变、用户改动保留、退出还原、
跨平台路径和失败时的状态回滚。复制可用逻辑之后再按明确的行为差异做最小调整。
