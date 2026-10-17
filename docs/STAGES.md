# 分阶段实施与验证记录

本文件记录可独立审阅的阶段。每阶段先完成实现和对应验证，再更新本文件与
`HANDOFF.md` 并提交。提交历史用 `git log --oneline` 查看；未推送不代表未提交。
测试计数只描述对应阶段的检查，不当作后续阶段的固定基线。

已提交的阶段入口：A `96f94d4`，B `3865bec`，C `1ca7516`，D `f67c90c`。
阶段 E 为 `6893f29`，F 为 `9a5a6ff`，G 为 `3d3c17b`。阶段 H 随本记录提交，
最终 SHA 用 `git log --oneline` 查看。
远端交接目标为 `origin/claude/exciting-galileo-cgd4uk`，
提交与远端状态分别以 Git 历史、远端引用为准。

## 阶段 A：安全还原与 CLI 路由（已完成）

- 切回官方或更换 provider 时撤销 qcs 写入的 token，恢复原有值并保留用户改动。
  兼容仍保有 ownership 记录的旧状态；无记录的历史值不按名字猜测删除。
- 模式 R 前台路由：模型目录合并、官方/第三方分流、SSE、zstd/gzip 请求、
  凭据隔离、正常停止/异常重启恢复、端口回退。
- 配置与状态文件任一写入失败时一起回滚；旧会话清理不能撤销新选择。
- 验证：完整 `nogui` race 测试、vet、模块校验、前端 9 项测试、Linux CLI 构建、
  Windows amd64/macOS arm64 CLI 交叉编译、Web HTTP 冒烟与 Unix 进程信号测试。
- 边界：GUI/托盘还没有路由管理；真实 Codex/CPA、原生托盘行为待真机验收。

## 阶段 B：GUI/Web 与托盘路由管理（已完成）

- 在界面预览、启动、停止和恢复路由，显示本进程拥有/外部进程/异常残留状态。
- 主窗口与托盘面板共用一个所有者；取消预览、关闭进程、切换模式时正确释放。
- 普通切换和方案与路由生命周期一致，避免遗留监听器或还原新配置。
- 验证目标：控制器并发与生命周期、鉴权 API、前端操作、关闭清理、GUI 构建。
- 已实现：两分钟预览 ID 与地址预留、共享进程控制器、读锁区分外部/残留状态、
  GUI 同步退出清理、Web SIGTERM 清理、托盘停止和恢复、保持编辑内容的状态轮询。
- 方案使用已检查的不可变请求快照，防止并发替换方案后绕过 Codex 路由守护。
- 验证：控制器/API/进程级 race 测试，真实后端 Chromium 预览取消/启动/停止/网络
  失败重试，前端 15 项测试；GTK3 GUI 编译和 Xvfb 下原生 WebKit 窗口启动。
- 边界：裸 Xvfb 没有系统托盘宿主，不作为真实托盘点击、失焦、登录验收。

## 阶段 C：可选择的开机自启（已完成）

- Linux XDG autostart、macOS LaunchAgent、Windows 当前用户启动配置。
- 提供状态、开启与关闭；使用当前可执行文件的绝对路径及正确的平台引用规则。
- 只在用户明确选择后写入；测试仅在临时目录/隔离接口，不注册当前云机器。
- 验证目标：路径/引用/所有权保护、重复操作、平台构建；真实登录启动待真机验收。
- 来源：直接裁剪 Magpie `internal/autostart` 及 CLI 入口，来源提交、许可与差异
  记录在 [UPSTREAM.md](UPSTREAM.md)。补充本项目 `gui --hidden` 与 `nogui` 能力限制。
- 设置页以系统实际记录为准；开启/关闭只响应明确操作，不在初始化、查询或登录时启用路由。
- 测试只使用临时 HOME 或模拟注册表，未给当前云机器登记登录启动项。
- 验证：GTK3 全量 race/vet/桌面构建、18 项前端测试、真实 API 的 Chromium 启停与
  冲突流程、系统 GLib 对路径/参数的无启动解析；Windows GUI 和 macOS CLI 交叉编译。
- 实测 GLib 不能加载程序路径含 `%` 的启动项，因此明确拒绝该路径的新登记，仍能
  撤销旧登记；不使用额外 shell/env 包装来隐藏失败。真实三平台登录启动仍待验收。

## 阶段 D：上游路由逻辑复用与边界修补（已完成）

- 精简移植 Magpie 的模型目录限时请求/本地缓存回退，保持鉴权失败可见。
- 精简移植未知 provider 命名空间的本地拒绝；保留全走上游时的模型命名能力。
- 扩展已有键级还原机制以撤销 qcs 写入的 `http_headers`，覆盖认证头和旧记录迁移。
- 单独验证并提交，记录直接来源及必须保留的本地条件还原差异。
- 已移植 Magpie 目录三秒预算和只读缓存回退；成功保留 JSON 元数据、401/403
  保持可见，超时与客户端取消分别处理。无缓存只列当前别名，过滤旧命名空间。
- 已覆盖 `http_headers` 整体键的原值恢复和当前/旧记录迁移；被用户改动的值保留
  并报告。不会按表名前缀清理已经丢失所有权记录的历史值。
- 最终检查：GTK3 和 `nogui` 全量 race、两种 vet、依赖校验、18 项前端测试、
  Linux 桌面构建、Windows GUI amd64/macOS CLI arm64 交叉编译全部通过。
  GTK3 运行 17 个测试包、318 个顶层测试通过；2 项按条件跳过（无第二个用户组、
  文件锁仅供子进程调用的 helper）。独立审查未发现新的明确缺陷。

## 阶段 E：核心需求全面核验与修复（已完成）

- 以用户要求的「保留原配置 + 官方登录 + CPA 模型 + Fast」组合审查现有代码。
- 修复 provider 尚无模型列表时遗漏 Fast 能力；保留明确的 Fast=false 和用户原档位。
- 直连已有自定义目录时切换到 provider 目录，保留原文件并在还原时恢复原路径。
- 旧版活动 profile 覆盖切换计划时拒绝；兼容一致值，不改动原 profile。
- safekeep 获取不到进程锁时 Apply/Restore/条件还原全部停止，覆盖失败零写入及重试。
- 新增组合回归：官方模型目录/认证保持、普通及别名模型的 Responses/compact 全走
  CPA、priority 档位透传、官方凭据隔离、原配置和登录文件身份/时间/权限保持。
- 用官方 Codex CLI 0.159.2 的严格配置 app-server 实际验证自定义模型和 Fast 档位；
  配置/登录均为临时夹具，未使用真实账号或请求远端 CPA。
- 具体需求、证据、推荐模式及实际提速/账号额度等边界见 REQUIREMENTS.md。
- 最终回归：GTK3/`nogui` 各 17 个测试包通过，324 个顶层测试通过、2 项条件跳过
  （无第二用户组、子进程文件锁 helper）；race/vet、18 项前端测试及模块校验通过。
  Linux GTK3 桌面构建、Windows amd64 GUI/macOS arm64 CLI 交叉编译通过。
  `scripts/check_codex_contract.py` 可用已安装的官方客户端重复目录合同检查。

## 阶段 F：Codex 桌面识别、认证与请求头完整性（已完成）

- 对照 Magpie 的显式请求头子表回归，修复已有 `http_headers` 子表无法切换的问题。
  独立记录每个受管请求头，保留原注释、带点键和用户后续增加/修改的头。
- 兼容仍有 ownership 的 inline→子表结构变更，不把 qcs 的旧密钥误记成原始值；
  普通 inline `http_headers` 仍按整体键管理。
- 阻止既有 qcs provider 的官方认证、env_key、认证命令或冲突动态头覆盖第三方认证；
  保留无冲突的重试/跟踪等用户设置，报错不打印敏感值。
- 复用 Magpie 的桌面身份检测，区分 Codex 版 `ChatGPT.app` 与独立聊天应用；
  增加 Windows Store 主包识别及仅进程环境范围内的 loopback 代理诊断。
- 新创建配置先设为 0600 再写密钥，含还原时重新创建保留表的路径；原文件权限不变。
- 验证：Codex/desktopapp/edit/safekeep/e2e race、相关 vet 通过；覆盖子表还原/漂移/
  旧记录迁移、点号键、写密钥前权限、失败占位文件清理。Windows 检测测试交叉编译通过。
- 边界：代理诊断不读取桌面登录 shell 或平台系统代理；实际应用/Store 和代理仍待真机验收。

## 阶段 G：Claude CLI 单一配置与认证/模型细节（已完成）

- 对照 Magpie 的云开关、writeTiers/dropAbout、原生模型努力档位，补齐六种云开关、
  small-fast、普通/workflow 子代理和过期模型说明清理；按原值/漂移安全还原。
- 直连保留 provider 的精确模型 ID，不照搬依赖网关反向转换的 dashed 写法。
- 支持明确配置的自定义头，隔离原认证头；拒绝可能回退到官方 OAuth 的空认证，
  原登录文件不读不写。预览整段遮盖 `ANTHROPIC_CUSTOM_HEADERS`，含旧凭据及转义值。
- 显式 effort 不再静默忽略：验证客户端/模型能力及原有 maxEffortLevel，保留所有
  上限。dotted Claude ID 的显式 effort 不能可靠生效时拒绝，不擅自改 ID。
- 管理员字段冲突时报错；下层 global/shell 的云/模型/认证覆盖使用临时值遮蔽并还原。
- 验证：Claude/core/webui/e2e/CLI race 及相关 vet，通过官方 Claude CLI 2.1.302
  的六项实际本地 SSE 请求检查：精确 ID、原生/自定义/max effort、header-only
  Authorization 及原有 effort 上限；配置逐字节恢复，登录文件内容/身份/时间/权限保持。
- 可重跑 `scripts/check_claude_contract.py --qcs ./qcs --claude /path/to/claude`，
  需要 Python 3.11+；只用临时配置、假令牌及 loopback 后端，不访问真实账户或远端模型。

## 阶段 H：Claude Desktop 单一配置与四端支持矩阵（已完成）

- 精简复制 Magpie 的 Desktop 路径/配置合同，独立 `claude-desktop` ID；通过已有
  plan/safekeep 写入 profile、meta 和两个部署模式，原登录/CLI/MCP/会话数据保留。
- 复制 macOS/Linux/Windows/MSIX 路径和最近运行目录判断，多个配置副本失败一起回滚。
- 只接受可直接配置的 Claude 模型、Anthropic gateway 根地址与有效 bearer key；
  不静默忽略自定义头/Fast/effort，不引入任意模型转换或 Cowork 通配出站策略。
- 服务端给出 ModelRefs，界面只列兼容选择，登录状态显示未知，不误称官方登录仍活跃。
- 新增往返/漂移/异常配置/私有权限、API/Core 集成及前端过滤回归；真实后端 Chromium
  验证卡片、模型筛选、取消零写入、确认写入 0600 profile、切回删除生成文件及无脚本错误。
- 最终全量：GTK3/`nogui` 各 20 个测试包、368 个顶层测试通过；2 个条件跳过
  （无第二用户组、文件锁子进程 helper）。两种 race/vet、19 项前端测试和依赖校验通过；
  Linux GTK3、Windows amd64 GUI、macOS arm64 CLI 构建通过。
- 最终构建再次通过官方 Codex 0.159.2 三项目录合同及 Claude CLI 2.1.302 六项本地请求合同。
- 客户端支持/登录边界、真实桌面待验收与已知元数据数组/空目录边界见 CLIENTS.md、
  ACCEPTANCE.md；实现与来源见 UPSTREAM.md。真机行为不以夹具或交叉编译替代。

## 后续验收与优化

- 三平台真实 Codex/CPA 切换、Fast、HTTP 回退、托盘面板和登录自启。
- GUI 方案编辑和 provider 连接测试；之后再评估系统钥匙串、会话迁移和新 Agent。
- 真机或凭据不可用时记录具体未验证项，继续独立工作，不把它们标成通过。
- 具体验收步骤与当前平台证据见 [ACCEPTANCE.md](ACCEPTANCE.md)。后续优先查阅
  Magpie 已有实现及其测试，再裁剪需要的部分，避免重复建设。
