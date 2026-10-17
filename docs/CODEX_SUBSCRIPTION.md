# Codex 官方订阅、CPA 与 Fast

主场景是：**官方 OAuth 和订阅账号由 CPA 管理，QCS 只把 Codex 指向 CPA；
Codex 本机可以没有官方登录，仍能选择 GPT 模型、推理强度和 Fast。**
这条路径使用直连配置，不要求额外运行 QCS 本地路由。

上游使用官方订阅、客户端展示完整模型能力、客户端自身的账号登录状态是三个层次。
模型和 Fast 能力不以本机保存 ChatGPT 登录为前提；账户中心、官方 provider 下的
会话和其它账号功能则需要分别确认，不能用模型请求成功代替验收。

## 使用方式

先在 CPA 中配置实际的官方账号和可用模型，再在 QCS 中添加 CPA：

```sh
qcs provider add cpa --template cpa --key '替换为CPA访问密钥'
qcs provider models cpa --fetch
qcs diff codex cpa/gpt-5.5
qcs use codex cpa/gpt-5.5
```

重启 Codex 桌面端及仍在运行的共享 app-server，再打开新会话。在客户端内选择 Fast；
如需同时把新会话的默认档位设为 Fast，可使用 `qcs use codex cpa/gpt-5.5 --fast`。
未显式指定档位时，QCS 保留用户原来的选择。

`cpa/gpt-5.5` 是 QCS 的 provider/模型引用，发给 CPA 的模型 ID 仍是 `gpt-5.5`。
选择 CPA 实际列出的精确模型名；QCS 不把任意别名擅自转换成官方模型。
还原使用 `qcs use codex official`，恢复切换前的配置。若切换前没有本机登录，
还原后仍没有本机登录；该命令不执行 OAuth 或注销。

## Magpie 的实现依据

以下引用均固定到 Magpie
`7660399a121af67db4efd56ecb6bd4fb04dc53f1`，避免主分支变化后结论失去依据。

1. **官方订阅可作为上游 provider。**
   [`codexAccount`][magpie-account] 建立 `Account{Agent: "codex"}`，请求地址为
   `https://chatgpt.com/backend-api/codex`。
   [`codexSign`][magpie-sign] 用该账号的 OAuth access token、`chatgpt-account-id`
   和 Codex 客户端版本签名。订阅模型由上游账户提供，不由客户端 provider 名称决定。
   QCS 将这些账户、刷新和调度职责留给 CPA，不重复引入账户池。
2. **完整模型目录让 API provider 保留模型能力。**
   [`CodexCatalog`][magpie-catalog] 包含官方订阅模型；
   [`codexcat.Entries`][magpie-entries] 复用官方模型条目，并为支持的 GPT 模型保留
   Fast 的 `service_tiers`。本地客户端通过 `model_catalog_json` 读取目录，
   远端客户端也可读取 [`codexCatalog`][magpie-catalog-http] 输出的同一类目录。
   `GET /v1/models` 的裸模型 ID 列表本身不足以表达这些 Codex 能力。
3. **Fast 是可用档位与请求参数的组合。**
   Magpie 的 [`codexBody`][magpie-body] 保留 `service_tier: "priority"` 和
   `"ultrafast"`。目录中的 Fast 和发出的 priority 都应检查，不能只看配置文件
   里有 `service_tier = "fast"`。
4. **Magpie 还会处理真正的本机登录。**
   [`addLogin`][magpie-login] 在客户端未登录或重新登录当前账号时调用
   `saveCodexLogin`，将真实 OAuth 凭据写入 Codex 的认证存储。因此“只在 Magpie
   中登录过”不等于“客户端没有本机官方登录”。本项目依照配置切换范围，不复制这项
   认证写入行为，也不把 CPA 的访问密钥写成虚假的官方 OAuth 登录。

Magpie 的 [`codexIn`][magpie-wiring] 同时实现两条客户端路径：有本机 ChatGPT 登录时
通过 `openai_base_url` 保留内置 OpenAI provider；无本机登录时使用自己的 provider
和模型目录。后者仍能通过网关消费官方订阅并提供 Fast。QCS 的直连对应后一种配置
方式；CPA 负责官方订阅上游，QCS 负责配置与目录。

## QCS 的目录与配置保证

- 已拉取或手工保存的 provider 模型列表是选择器的范围。即使每个 ID 都是 Codex
  内置 GPT，也生成 provider 目录，避免展示 CPA 没有提供的其它内置模型。
- 已知 GPT 模型复用 Codex 的完整条目，保留显示名、推理档位及其它模型元数据。
  `/v1/models` 只返回 ID、名称仍等于 ID 时，沿用官方显示名；用户明确设置的名称保留。
- 新 provider 尚无列表时，已知 GPT/o 系列沿用现有 Fast 默认规则；用户已经保存的
  `Fast=false` 不会被覆盖。未知模型是否支持该档位需由 provider 明确配置。
- 切换只写所需配置，保留原 `service_tier`、`features.fast_mode`、推理强度和无关设置。
  原自定义目录文件不修改，切换期间的目录指向可还原。
- 本机有登录文件时保留它；没有时不创建。CPA 请求使用 CPA 的认证，模型 ID 原样发送。
  Fast 由 Codex 转为 `service_tier: "priority"` 后交给 CPA。

实现入口为 `internal/agent/codex/plan.go`、`internal/codexcat/codexcat.go`；
备份、原子写入和漂移保护仍统一由 `internal/safekeep` 负责。

## CPAMP 1.14.7 / CLIProxyAPI 8.0.32 版本核验

用户自报 CPAMP `1.14.7`、内核 `8.0.32`；已核对原版 tag：
`seakee/CPA-Manager-Plus v1.14.7` 为 `64ce94e6aa1af691555429a3211e67c59c0480ad`，
`router-for-me/CLIProxyAPI v8.0.32` 为 `911f49cbd49f4fe2516979754a7488a3fd14fe64`。
CPAMP 的 Plus 是管理面板名称，其[运行模型][cpamp-runtime]说明客户端直接连接 CPA；
[版本链接映射][cpamp-versions]也指向 CLIProxyAPI，不能据名称推断内核为 CLIProxyAPIPlus。
面板与内核独立升级，CPAMP 版本本身不固定内核版本。

| 核验项 | 8.0.32 原版源码行为 |
| --- | --- |
| 官方订阅鉴权 | 有 [Codex OAuth 交换/刷新实现][cpa-oauth]，executor 使用上游账户 token；客户端连接 CPA 使用 CPA 普通 API key |
| 模型与压缩请求 | 已注册 `POST /v1/responses`、`POST /v1/responses/compact`，以及 `/backend-api/codex` 下对应别名，见[路由注册][cpa-routes] |
| 完整 Codex 目录 | `GET /v1/models?client_version=<客户端版本>` 返回 Codex 目录；不带该参数通常返回四字段 OpenAI 模型列表，见 [`OpenAIModels`][cpa-models] |
| Fast 能力限制 | 目录基于 registry、模型模板和路由生成；非纯 Codex provider 的模型会清空 `service_tiers`，见[能力约束][cpa-model-caps] |
| 官方路径缺口 | `/backend-api/codex` 只有 Responses、compact、search 等明确别名，没有该前缀下的 `/models` 或通配官方转发；不能直接当作 Magpie 的完整前缀代理 |
| 实际 Fast 参数 | 转换器保留 priority/ultrafast、将 fast 转为 priority；管理员 [payload override/filter][cpa-payload-rules] 仍可覆盖或删除该字段 |

这是用户自报版本与原版源码的对应核验，尚未连接用户部署。目录不是对每个官方账号
实时查询的订阅权限清单；模型列出、Fast 可选都不能保证该次路由实际有权限或使用优先档位。
QCS 当前从普通 `/v1/models` 获取 ID，再复用本地完整模型条目，不自动读取上述参数化
目录。本机账号 UI 仍取决于客户端的真实认证状态，CPA key 不生成本机 ChatGPT 登录。

## 官方 Codex CLI 的五项隔离验证

检查程序为 [`scripts/check_codex_contract.py`](../scripts/check_codex_contract.py)，
使用官方 `codex-cli 0.159.2` 的 `app-server --stdio --strict-config`。前面三项检查
原配置和假登录文件的保持，后面两项从完全没有 `auth.json` 的临时目录启动客户端：

| 场景 | 检查内容 |
| --- | --- |
| 新 provider 尚未拉取模型 | 自定义模型可加载，Fast 元数据存在，原配置、目录及假登录文件保持 |
| 已保存的自定义模型 | 非内置模型目录可加载并提供明确配置的 Fast |
| 内置 GPT 覆盖原自定义目录 | 临时目录提供 GPT 能力，还原后恢复原目录指向和原文件 |
| 无本机登录、新 CPA 尚未拉取列表 | `GPT-5.5`、low/medium/high/xhigh 和 Fast 可读取，新建临时 thread/turn 成功 |
| 无本机登录、已拉取 CPA 的 GPT 子集 | 选择器仅列 CPA 提供的 `gpt-5.5`，保留官方显示名与 Fast，新建临时 thread/turn 成功 |

最后两项都由本地假 SSE 服务完成一次响应。每项断言只收到一次
`POST /v1/responses`：`model = "gpt-5.5"`、`service_tier = "priority"`，
认证仅为假 CPA bearer；没有官方账号头、cookie、attestation 或 actor header。
从切换、实际客户端请求到还原，全程没有创建 `auth.json`，并检查原配置和 CPA 密钥清理。

重跑需要 Python 3.11+ 及已构建的 QCS、已安装的官方 Codex：

```sh
python3 scripts/check_codex_contract.py --qcs ./qcs --codex /path/to/codex
```

这证明真实 CLI 可以在无本机官方登录时加载目录并发送 Fast 请求。服务返回预置 SSE，
未使用真实订阅、未执行真实模型推理，也不证明用户的 CPA 部署实际保留该档位。
真实 Codex Desktop 模型选择器、CPA OAuth 账户可用性、Fast 的速度和计费仍按
[ACCEPTANCE.md](ACCEPTANCE.md) 验收。CLI 与 Desktop 共用配置，不代表云环境已经验过
完整桌面 UI。

## 其它官方能力的边界

如需继续使用本机官方账号功能，可选择现有「本地路由」模式；它保留内置 OpenAI
provider 和客户端登录，模型请求可全走 CPA。此模式解决另一类需求，不是上述
CPA 统一管理订阅、客户端无登录也能使用 GPT/Fast 的前提。

Magpie 的 [`putProvider`][magpie-actor] 还写入 `x-openai-actor-authorization`，让
Codex 提供 `image_gen` 等 OpenAI actor 工具。这与模型目录、Fast 和本机 OAuth 登录
独立；本项目本轮没有自动添加该头。CPA 的对应工具处理、实际实例配置和客户端工具
调用需单独验收，不能把这个头作为 Fast 的必要条件或给所有第三方默认开启。

CPA 源码 `911f49cbd49f4fe2516979754a7488a3fd14fe64` 的
[`applyCodexHeadersFromSources`][cpa-headers] 从新请求构造上游认证，默认不复制 actor
header；[`isImageGenerationFunctionTool`][cpa-image] 认识 Codex 的 namespace/平铺
`image_gen.imagegen`。这只是能力参考，运营方自定义 header、模型、计划和图片开关仍
可能改变实际行为，不能作为完整官方工具体验已经验收的依据。

[magpie-account]: https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/provider/account.go#L1012
[magpie-sign]: https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/provider/codex_request.go#L31
[magpie-catalog]: https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/provider/codex_models.go#L404
[magpie-entries]: https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/codexcat/codexcat.go#L190
[magpie-catalog-http]: https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/gateway/codex_catalog.go#L33
[magpie-body]: https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/provider/codex_request.go#L252
[magpie-login]: https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/provider/signin.go#L983
[magpie-wiring]: https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/agent/codex.go#L230
[magpie-actor]: https://github.com/yetone/magpie/blob/7660399a121af67db4efd56ecb6bd4fb04dc53f1/internal/agent/codex.go#L395
[cpa-headers]: https://github.com/router-for-me/CLIProxyAPI/blob/911f49cbd49f4fe2516979754a7488a3fd14fe64/internal/runtime/executor/codex_executor_request.go#L242
[cpa-image]: https://github.com/router-for-me/CLIProxyAPI/blob/911f49cbd49f4fe2516979754a7488a3fd14fe64/internal/runtime/executor/codex_executor_request.go#L418
[cpamp-runtime]: https://github.com/seakee/CPA-Manager-Plus/blob/64ce94e6aa1af691555429a3211e67c59c0480ad/apps/docs/guide/runtime-model.md#L3
[cpamp-versions]: https://github.com/seakee/CPA-Manager-Plus/blob/64ce94e6aa1af691555429a3211e67c59c0480ad/apps/web/src/features/dashboard/versionReleaseLinks.ts#L3
[cpa-oauth]: https://github.com/router-for-me/CLIProxyAPI/blob/911f49cbd49f4fe2516979754a7488a3fd14fe64/internal/auth/codex/openai_auth.go#L96
[cpa-routes]: https://github.com/router-for-me/CLIProxyAPI/blob/911f49cbd49f4fe2516979754a7488a3fd14fe64/internal/api/server_routes.go#L63
[cpa-models]: https://github.com/router-for-me/CLIProxyAPI/blob/911f49cbd49f4fe2516979754a7488a3fd14fe64/sdk/api/handlers/openai/openai_handlers.go#L64
[cpa-model-caps]: https://github.com/router-for-me/CLIProxyAPI/blob/911f49cbd49f4fe2516979754a7488a3fd14fe64/internal/client/codex/models/models.go#L561
[cpa-payload-rules]: https://github.com/router-for-me/CLIProxyAPI/blob/911f49cbd49f4fe2516979754a7488a3fd14fe64/internal/runtime/executor/helps/payload_helpers.go#L155
