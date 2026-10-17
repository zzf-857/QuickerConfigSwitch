# 核心需求核验

核验日期：2026-10-17。范围是从 Magpie 提取配置切换能力：保留原客户端配置，
增加自定义 provider/模型，CPA 可代管官方订阅，客户端保留模型与 Fast，按键修改并可安全还原。
代码和自动回归已覆盖以下要求；真实桌面账号及 CPA 的最终验收仍见 ACCEPTANCE.md。
Codex CLI、ChatGPT/Codex Desktop、Claude CLI 和 Claude Desktop 的逐端支持与遗漏复盘
见 CLIENTS.md；ChatGPT 聊天端和 Claude 的 3p 登录边界不能混为一谈。

## 推荐使用方式

用户的主场景是**官方订阅登录、刷新与账号选择都交给 CPA，QCS 只配置 Codex 直连 CPA**。
Magpie 也将网关上游的 Codex 官方订阅与客户端的 provider 配置分开；完整源码链路见
[CODEX_SUBSCRIPTION.md](CODEX_SUBSCRIPTION.md)。本机无需 ChatGPT 登录，模型和 Fast
也不要求 QCS 本地路由。对应 CLI：

```sh
qcs provider add cpa --template cpa --key sk-xxx
qcs provider models cpa --fetch
qcs use codex cpa/gpt-5.5
qcs use codex cpa/gpt-5.5 --fast # 也可在客户端选 Fast；不传则保留原选择
```

重启 Codex 桌面端及共享 app-server。`cpa/` 是 QCS 内部选择引用，发给 CPA 的仍为
`gpt-5.5`。客户端选择器按 CPA 的明确模型列表生成，标准 GPT 保留官方显示名、完整
指令、推理档位和 Fast。未拉取列表时仍可用已知模型，但选择器可能展示内置的其它模型，
因此建议先拉取 CPA 实际提供的列表。

如果还需要本机活动的官方 provider、官方 provider 下的会话或登录专属功能，再考虑可选
本地路由 `--relay [--relay-all]`，它需常驻且保留本机登录。该需求与 CPA 使用官方订阅、
客户端有模型/Fast 是不同层面，不应把本机登录当作后两者的前提。

## 要求与证据

| 要求 | 当前行为 | 核验证据 |
| --- | --- | --- |
| 保留原始配置 | 适配器只生成必要键的计划；无关设置、注释、BOM/CRLF、链接和权限保留；用户后续改动不被盲目覆盖 | `internal/edit`、`internal/safekeep`、`internal/e2e` 回归 |
| 不替换官方登录 | 不修改或创建 `auth.json`；直连不依赖它，状态检测只读检查。可选路由保留内置 `openai` | 登录文件身份/时间/权限回归；真实客户端无登录请求合同 |
| CPA 管理官方订阅 | 客户端仅用 CPA 凭据，CPA 在上游使用官方账号；本机无需登录 | Magpie 的 `codexAccount`/`codexSign`、实际客户端无登录合同；真实 CPA 账号仍待验收 |
| 模型全部走 CPA | 直连将原模型 ID 和 Responses 请求送 CPA；可选 `--relay-all` 也支持普通 ID/当前 provider 别名及 compact | 五项官方客户端合同；`TestRelayAllPreservesLoginAndFast` |
| Fast 不因切换消失 | 保留原 `service_tier`、`features.fast_mode`；完整目录提供 Fast/priority，实际请求保持档位 | Fast 三态及别名/压缩回归；官方 Codex 0.159.2 无本机登录实际发出 `priority` |
| 选择器反映 CPA 模型 | 明确列表即使全是原生 GPT 也生成目录，不展示 CPA 未提供的内置模型；fetch 的 ID 占位名不覆盖官方显示名 | `TestPlanListedNativeModelsUseProviderCatalog`、`TestNativeDisplayNameAfterProviderFetch`、真实客户端仅列 GPT-5.5 |
| 新增 provider 后直接使用 | 未拉取列表时，已知 GPT/o 系列沿用已有 Fast 默认规则；明确关闭的模型仍关闭 | `TestPlanUnlistedFastDefaults`、路由无列表组合回归 |
| 保留原自定义目录 | 直连有明确模型列表、能力差异或原有目录时，改指 qcs 的 provider 目录；切回恢复原路径，原文件不改 | `TestCodexPreservesOriginalCustomCatalog` |
| 原子修改及安全还原 | 复用 Magpie 的编辑/同步原语；文件原子写，写入失败回滚配置和状态；获取不到进程锁则停止 | safekeep 失败注入、锁失败零写入、会话标记/新旧切换回归 |
| 不重复实现已有基础逻辑 | 复用 Magpie 的 edit、codexcat、文件同步、自启及目录回退相关实现，保留 MIT 来源 | UPSTREAM.md、NOTICE、`third_party/magpie/LICENSE` |

还原不会删除保留旧线程所需的 `[model_providers.qcs-<id>]` 定义，但会撤销 qcs 写入的
密钥/认证头，恢复已有值；用户自己改过的内容保留并报告。所以“保留原配置”不表示
直连往返后整个文件必然与原文件哈希相同。

## 实际客户端和上游合同核对

- Magpie 参考：`7660399a121af67db4efd56ecb6bd4fb04dc53f1`，复用范围见 UPSTREAM.md。
- 实际运行官方 `codex-cli 0.159.2` 的 `app-server --stdio --strict-config`：原三项目录
  案例检查别名 Fast、原目录和假登录文件保持；新增两个全程没有 `auth.json` 的案例，
  覆盖未拉取列表与 CPA 只提供 GPT-5.5。原名、原生推理档位及 Fast 均被实际客户端加载。
- 可重跑实际客户端检查（Python 3.11+，显式传入已构建的两个程序）：

  ```sh
  python3 scripts/check_codex_contract.py --qcs ./qcs --codex /path/to/codex
  ```

  共五个案例。两个无登录案例实际创建临时 thread/turn，由 loopback 假 SSE 服务完成，
  各捕获恰好一次 `/v1/responses`：`gpt-5.5`、`service_tier: priority`、仅 CPA bearer，
  无官方账号鉴权头；还原后原设置恢复、CPA key 撤销。没有真实推理、订阅或远端 CPA 请求。
- 对应 Codex 源码版本 `ff6aec96948b70d94983af2641a6b67c94faeff5`：核对
  内置 OpenAI 认证、模型档位及 ChatGPT 请求压缩/HTTP 回退合同。
- CPA 源码 `911f49cbd49f4fe2516979754a7488a3fd14fe64`：其 Codex Responses
  转换器将 `fast` 规范为 `priority`，保留 `priority`/`ultrafast`；其它值可能被移除。

## 仍需真实环境确认的边界

- Fast 可选、请求带 `priority`，证明客户端能力和转发链路保留；实际档位、计费和速度
  取决于 CPA 版本、模型及它的上游账号，不能从本地测试保证实际加速。
- CPA 直连的模型目录由 QCS 根据 CPA 列表和原生元数据生成，不依赖本机登录拉取官方目录。
  只有可选本地路由的“全走上游”仍将目录及其它官方后端路径留给官方。
  两者均不会自动把不支持的模型 ID 映射成目标模型。
- 可选本地路由使用本机官方登录，认证过期/401/403 会正常显示。部分桌面版本在订阅额度耗尽时可能先阻止
  发送请求；未移植 Magpie 完整的额度耗尽检测及 API 状态回退，不能承诺绕过这种限制。
  相关上游依据为 Magpie `docs/subsystems/agent-wiring.md` 的 ChatGPT/API 状态说明。
- 旧版配置内的活动 profile 覆盖切换键时会拒绝冲突；新版客户端独立的 `--profile`
  配置层、命令行或环境变量也可能覆盖默认配置，应以启动时生效的配置为准。
- 完整桌面登录态功能、历史会话交互、真实 CPA/Fast、三平台托盘与登录自启需按
  ACCEPTANCE.md 真机验收；云环境的原生窗口启动和交叉编译不代替这些结果。
