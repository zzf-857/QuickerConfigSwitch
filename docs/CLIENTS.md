# Codex、ChatGPT 与 Claude 配置支持

本轮逐项对照 Magpie `7660399a121af67db4efd56ecb6bd4fb04dc53f1`，
只提取客户端配置和必要的兼容逻辑。来源、裁剪及许可见 UPSTREAM.md。

## 客户端与配置入口

| 客户端 | qcs ID | 配置与模式 | 登录边界 |
| --- | --- | --- | --- |
| Codex CLI | `codex` | `CODEX_HOME/config.toml`，默认 `~/.codex/config.toml`；直连或本地路由 | 直连保留登录文件；路由保留内置官方 provider 和活动登录 |
| ChatGPT/Codex Desktop | `codex` | 与 Codex CLI 共用配置，不重复建立第二份配置 | 同上，改配置后需退出桌面端和仍在运行的共享 app-server |
| Claude Code CLI | `claude` | `CLAUDE_CONFIG_DIR/settings.json`，默认 `~/.claude/settings.json`；单一 provider 直连 | 不改官方登录文件，切回原配置后恢复使用；直连要求明确的第三方认证 |
| Claude Desktop | `claude-desktop` | 原目录与 `Claude-3p` 的部署模式、独立 gateway profile；支持 Code/Cowork 的 3p 配置 | 保留原登录数据，3p 期间使用独立数据目录，不使用官方 Anthropic 登录 |

Magpie 的 `desktopapp.go` 区分两个同名 `ChatGPT.app`：Codex 桌面端标识为
`com.openai.codex`，独立聊天应用标识为 `com.openai.chat`。仅根据名称判断会误识别。
Windows 对应 Codex 的 `OpenAI.Codex` Store 主包。这里的 `codex` 支持前者；
没有给独立聊天应用虚构第三方模型配置接口。

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

- Codex 0.159.2：实际 app-server 严格加载临时配置及目录，检查 Fast 能力；重跑见
  `scripts/check_codex_contract.py`。
- Claude CLI 2.1.302：官方 npm 分发通过 SHA512 校验，在隔离配置与本地假后端检查
  精确模型 ID、第三方认证及官方 OAuth 隔离。未使用真实账号或远端模型。
- 桌面适配以 Magpie/CC Switch 的已实现配置合同及临时文件回归为依据；Windows/macOS
  真实 Desktop、Cowork、登录和上游请求仍按 ACCEPTANCE.md 验收，不标成真机已通过。
- 本轮最终 GTK3/`nogui` 各 20 个测试包、368 个顶层测试通过，19 项前端测试及真实
  后端浏览器流程通过；Linux 桌面和 Windows GUI/macOS CLI 构建通过。条件跳过与分阶段
  证据见 STAGES.md。
- 模型目录中保留 Fast 以及请求保留 `priority`，不证明 CPA 实际提供对应速度或计费。
  官方订阅额度耗尽时的客户端发送限制仍见 REQUIREMENTS.md。
