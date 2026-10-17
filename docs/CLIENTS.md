# Codex、ChatGPT 与 Claude 配置支持

本轮逐项对照 Magpie `7660399a121af67db4efd56ecb6bd4fb04dc53f1`，
只提取客户端配置和必要的兼容逻辑。来源、裁剪及许可见 UPSTREAM.md。

## 客户端与配置入口

| 客户端 | qcs ID | 配置与模式 | 登录边界 |
| --- | --- | --- | --- |
| Codex CLI | `codex` | `CODEX_HOME/config.toml`，默认 `~/.codex/config.toml`；CPA 管理订阅用直连，保留本机登录且模型全 CPA 用 R All | 直连无需本机登录；R All 保持内置官方 provider 和真实本机登录，模型通道使用 CPA key |
| ChatGPT/Codex Desktop | `codex` | 与 Codex CLI 共用配置，不重复建立第二份配置 | 直连的账号界面及历史按 provider 隔离；R All 保留内置官方 provider。改配置后需退出桌面端及共享 app-server |
| Claude Code CLI | `claude` | `CLAUDE_CONFIG_DIR/settings.json`，默认 `~/.claude/settings.json`；单一 provider 直连 | 不改官方登录文件，切回原配置后恢复使用；直连要求明确的第三方认证 |
| Claude Desktop | `claude-desktop` | 原目录与 `Claude-3p` 的部署模式、独立 gateway profile；支持 Code/Cowork 的 3p 配置 | 保留原登录数据，3p 期间使用独立数据目录，不使用官方 Anthropic 登录 |

Magpie 的 `desktopapp.go` 区分两个同名 `ChatGPT.app`：Codex 桌面端标识为
`com.openai.codex`，独立聊天应用标识为 `com.openai.chat`。仅根据名称判断会误识别。
Windows 对应 Codex 的 `OpenAI.Codex` Store 主包。这里的 `codex` 支持前者；
没有给独立聊天应用虚构第三方模型配置接口。

## Codex：CPA 管理订阅，客户端直连

主场景是 **CPA 持有官方订阅 OAuth，Codex 通过 CPA 访问密钥直连**：

```text
Codex CLI / Codex Desktop → CPA Responses 接口 → CPA 中已登录的官方订阅
```

```sh
qcs provider add cpa --template cpa --key '<CPA访问密钥>'
qcs provider models cpa --fetch
qcs use codex cpa/gpt-5.5
# 如需明确开启 Fast：qcs use codex cpa/gpt-5.5 --fast
```

QCS 只配置 CPA 的地址、访问密钥和模型；订阅 OAuth 由 CPA 管理。切换命令退出后，
Codex 直接访问运行中的 CPA，无需 QCS 常驻转发。本机可以从未登录 ChatGPT，
`auth.json` 不存在时仍保持不存在；已有登录文件也不改动。

发给上游的模型 ID 保持原样，例如 `gpt-5.5`。已知 GPT 模型复用 Codex 本身的模型
元数据，保留 `GPT-5.5` 原生显示名、推理档位和 Fast 选项；拉取列表默认返回的
`name == id` 不会覆盖原生显示名。显式提供的自定义显示名仍保留。未知模型使用
兼容目录条目，不能把它当成官方模型能力已经核实。

拉取 CPA 模型列表后，选择器按这份明确列表生成目录：CPA 只提供 `gpt-5.5` 时，
只展示该模型，不额外混入 Codex 的其它内置模型。未拉取列表时没有这份上游可用性
依据，仍可直接选择精确模型 ID，模型实际可用性由 CPA 决定。

Fast 有两项独立证据：客户端目录保留 Fast 档位，以及实际 Responses 请求发送
`service_tier: "priority"`。CPA 及其上游账号是否接受该档位、实际速度或计费如何，
需要对真实 CPA 验收，不能从目录或本地假响应推断。

**本机官方账号体验是独立边界。** 自定义 provider 下，桌面端可能显示 API 模式，
会话列表也按 provider 隔离；这不表示 GPT 原名、推理档位或 Fast 必须依赖本机登录。
切回官方后恢复原配置及其原有登录使用方式；保留的 provider 定义用于打开其历史会话，
qcs 写入的密钥仍按所有权记录撤销。

另需保留**本机真实 ChatGPT 登录**，同时让 Responses/compact 全走 CPA，可选择
R All：保持内置 `openai`，只临时修改 `openai_base_url`，由 QCS 常驻路由转发。
在 Codex 内仍可选择原名 `gpt-5.5`，无需 provider 前缀；CPA 出错时不会自动改走
官方模型通道。官方模型目录加本地别名、搜索等其它官方路径仍走官方；账号/额度
使用独立的 `chatgpt_base_url`。本机账号与 CPA OAuth 可以不同，界面身份及模型
实际消耗分别归属各自账号；本机登录有效性和客户端额度阻断仍需单独确认。

不加 `--relay-all` 的模式 R 则保留按模型前缀分流。两方案命令、精确路径表及
固定版本 Magpie/本地实现依据见 [CODEX_SUBSCRIPTION.md](CODEX_SUBSCRIPTION.md)。

## Claude CLI 的单一配置行为

```sh
qcs use claude cpa/claude-opus-4-6
qcs use claude cpa/claude-opus-4-6 --effort high
qcs use claude official
```

- 直接使用 provider 给出的精确模型 ID，不把 `4.6` 自动改成 `4-6`。Magpie 可以
  在自己的网关中反向转换 ID；直连没有这层转换。原生能力识别可能仍依赖标准模型名。
- 主模型及 Opus/Sonnet/Haiku、旧版 small-fast 别名统一指向所选模型；撤销旧的
  子代理/Fable 覆盖及过期的名称、说明、能力标签。切回恢复原值，用户期间改过的值保留。
  本项目当前不实现 Magpie 的多模型分组或自动挑选便宜 Haiku。
- 同时处理 Bedrock、Vertex、Foundry、Anthropic AWS、Anthropic Google Cloud、Mantle
  开关；下层环境或全局配置中的开关会用临时关闭值遮蔽，不编辑 shell 或管理员配置。
- provider 自定义请求头写入 Claude 支持的 `ANTHROPIC_CUSTOM_HEADERS`。切换期间
  不沿用原认证头，切回恢复；预览遮盖整段请求头字符串。
- 空 token 不代表匿名：Claude CLI 可能回退到已保存的 OAuth。无 API key 时只允许
  明确的 `Authorization` 头；仅有 `X-Api-Key` 仍可能附带官方 OAuth，因此拒绝。
- 显式推理强度按客户端/模型支持范围校验并写入，旧版或不支持的档位明确报错。
  dotted Claude ID 虽能原样发送，但 CLI 不可靠地识别它的推理能力，因此不接受这类
  ID 的显式 effort；需要上游真实支持的标准 dashed 模型 ID，不能仅在本地改名。
  `--fast` 不作为 Claude 直连配置参数；客户端自己的 Fast 行为仍由它管理。
- 管理员配置覆盖目标字段时拒绝切换；项目设置、启动参数和环境也可能覆盖用户设置，
  使用 `qcs doctor` 检查，最终以客户端实际请求为准。

## Claude Desktop 的配置范围

```sh
qcs use claude-desktop cpa/claude-opus-4-6
qcs use claude-desktop official
```

使用支持 Anthropic Messages 的 gateway **根地址**与 API key，选择标准 Claude 兼容 ID。
为避免配置成功却被客户端拒绝，当前不接受带 `/v1` 或其它路径的 gateway 地址、
自定义请求头、GPT 等需要模型别名转换的 ID，以及直接传入的 Fast/effort 覆盖。
界面只列出该适配器能配置的 provider/模型，不把 Responses 支持等同于 Desktop 支持。

原客户端配置中的 MCP、登录与会话数据保留；3p 使用独立的 `Claude-3p` 用户数据，
不会自动复制这些数据，也不会宣称原官方会话和登录在第三方模式中同时活跃。
不默认写入 Magpie 的 `coworkEgressAllowedHosts=["*"]` 或禁用模式选择器等额外策略。
完整退出并重启客户端后生效；Chat 标签和客户端计划/权限仍由其自身决定。

路径沿用 Magpie 的 `desktopdir`：macOS 为 `~/Library/Application Support`，Linux 为
`XDG_CONFIG_HOME`，Windows 区分 Roaming/Local，并对 MSIX 的包目录与真实目录分别配置，
兼容不同 Desktop 版本读取位置。退出还原由 qcs 的 stash/applied 记录管理，不调用官方注销。

元数据的 `entries` 当前按整个数组记录所有权；如果用户期间增改其它 profile，
还原会保留该数组并报告，可能留下非活动的 qcs 列表项，不会覆盖用户新条目。
原 `appliedId` 若仍由 qcs 控制则恢复；用户自己切换的活动 profile 保留。
还原后空目录可能保留。新建含密钥的配置在 Unix 写入前设为 0600，已有文件权限保持；
Windows 仍需验收用户目录继承的 ACL，不能用 Unix 权限测试替代。

## 验证与边界

- Codex 0.159.2：官方 app-server 在临时配置中完成五项检查。原三项目录合同覆盖
  新 provider、显式自定义模型、原自定义目录；新增两项覆盖**无本机登录**的新 provider
  与从 CPA 假后端拉取的单一 GPT 列表。实际 `model/list` 保留原名、推理及 Fast，
  明确列表只有上游提供的模型。两项无登录场景还完成 `thread/start`、`turn/start`
  及 loopback 假 SSE：每项恰好一条 `/v1/responses`，原始 `gpt-5.5`、`priority`、
  仅 CPA bearer，无官方账号认证，始终没有 `auth.json`，切回恢复原配置。
  重跑见 `scripts/check_codex_contract.py`；这验证真实客户端的配置与请求行为，
  未验证真实 CPA 的 OAuth、远端推理或原生桌面交互。
- Claude CLI 2.1.302：官方 npm 分发通过 SHA512 校验，在隔离配置与本地假后端检查
  精确模型 ID、第三方认证及官方 OAuth 隔离。未使用真实账号或远端模型。
- 桌面适配以 Magpie/CC Switch 的已实现配置合同及临时文件回归为依据；Windows/macOS
  真实 Desktop、Cowork、登录和上游请求仍按 ACCEPTANCE.md 验收，不标成真机已通过。
- 全量回归、构建、浏览器流程及条件跳过的分阶段证据见 [STAGES.md](STAGES.md)。
  历史阶段的计数不代替本轮最终运行结果。
- 模型目录中保留 Fast 以及请求保留 `priority`，不证明 CPA 实际提供对应速度或计费。
  本机官方登录在模式 R 下的额度发送限制见 REQUIREMENTS.md；模式 D 的上游订阅
  认证和额度由 CPA 管理，仍需真实部署确认。
