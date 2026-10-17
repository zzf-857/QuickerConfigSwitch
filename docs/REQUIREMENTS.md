# 核心需求核验

核验日期：2026-10-17。范围是从 Magpie 提取配置切换能力：保留原客户端配置，
增加自定义 provider/模型，保留官方登录及 Fast，按键修改并可安全还原。
代码和自动回归已覆盖以下要求；真实桌面账号及 CPA 的最终验收仍见 ACCEPTANCE.md。
Codex CLI、ChatGPT/Codex Desktop、Claude CLI 和 Claude Desktop 的逐端支持与遗漏复盘
见 CLIENTS.md；ChatGPT 聊天端和 Claude 的 3p 登录边界不能混为一谈。

## 推荐使用方式

要在使用 CPA 时继续保留活动的官方登录和官方 provider 下的会话，使用**本地路由**。
桌面/Web 的 Codex 卡片选择「本地路由」，需要所有模型请求走 CPA 时开启「全走上游」。
对应 CLI：

```sh
qcs use codex official # 已启用直连时先还原
qcs use codex cpa/gpt-5.5 --relay --relay-all
```

让持有路由的进程保持运行，重启 Codex 桌面端及其共享 app-server。在 Codex 内选择模型
和 Fast。默认并存模式不加 `--relay-all`：普通官方模型走官方，`cpa/` 别名走 CPA。

直连 `qcs use codex cpa/gpt-5.5` 只保留登录文件；Codex 此时使用第三方 provider，
ChatGPT 登录态功能及官方 provider 下的会话需要切回官方后恢复。

## 要求与证据

| 要求 | 当前行为 | 核验证据 |
| --- | --- | --- |
| 保留原始配置 | 适配器只生成必要键的计划；无关设置、注释、BOM/CRLF、链接和权限保留；用户后续改动不被盲目覆盖 | `internal/edit`、`internal/safekeep`、`internal/e2e` 回归 |
| 不替换官方登录 | 不写 `auth.json`；路由保留内置 `openai`，仅改 `openai_base_url` | 路由生命周期/进程测试检查登录文件字节、身份、时间与权限；真实账号待验收 |
| 保留官方订阅并使用 CPA | 并存模式按模型前缀分流；原官方请求的认证和模型元数据保持，第三方请求只用 provider 凭据 | `internal/relay` 的官方/第三方分流及隔离回归 |
| 模型全部走 CPA | `--relay-all` 将 `/responses` 和 `/responses/compact` 的普通 ID、当前 provider 别名都送 CPA | `TestRelayAllPreservesLoginAndFast`；不把官方登录凭据、账户头、cookie 或查询参数送 CPA |
| Fast 不因切换消失 | 保留原 `service_tier`、`features.fast_mode`；目录提供 Fast/priority，转发保持该档位 | Fast 三态、别名、压缩请求及上述组合回归；官方 Codex 0.159.2 实际加载目录并返回 Fast 档位 |
| 新增 provider 后直接使用 | 未拉取列表时，已知 GPT/o 系列沿用已有 Fast 默认规则；明确关闭的模型仍关闭 | `TestPlanUnlistedFastDefaults`、路由无列表组合回归 |
| 保留原自定义目录 | 直连按需改为 qcs 的 provider 目录，切回恢复原路径；原目录文件不改 | `TestCodexPreservesOriginalCustomCatalog` |
| 原子修改及安全还原 | 复用 Magpie 的编辑/同步原语；文件原子写，写入失败回滚配置和状态；获取不到进程锁则停止 | safekeep 失败注入、锁失败零写入、会话标记/新旧切换回归 |
| 不重复实现已有基础逻辑 | 复用 Magpie 的 edit、codexcat、文件同步、自启及目录回退相关实现，保留 MIT 来源 | UPSTREAM.md、NOTICE、`third_party/magpie/LICENSE` |

还原不会删除保留旧线程所需的 `[model_providers.qcs-<id>]` 定义，但会撤销 qcs 写入的
密钥/认证头，恢复已有值；用户自己改过的内容保留并报告。所以“保留原配置”不表示
直连往返后整个文件必然与原文件哈希相同。

## 实际客户端和上游合同核对

- Magpie 参考：`7660399a121af67db4efd56ecb6bd4fb04dc53f1`，复用范围见 UPSTREAM.md。
- 实际运行官方 `codex-cli 0.159.2` 的 `app-server --stdio --strict-config`：临时配置、
  假登录文件、自定义 provider 和本地模型目录；初始化及 `model/list` 成功，返回
  自定义模型及 `priority/Fast` 档位。未进行登录、推理或远端 CPA 请求。
- 可重跑实际客户端检查（Python 3.11+，显式传入已构建的两个程序）：

  ```sh
  python3 scripts/check_codex_contract.py --qcs ./qcs --codex /path/to/codex
  ```

  脚本只对临时自定义 provider 读取配置/本地目录，检查未拉取模型、别名 Fast、
  原目录还原及登录文件保持；不创建线程、不执行推理，也不证明真实订阅状态。
- 对应 Codex 源码版本 `ff6aec96948b70d94983af2641a6b67c94faeff5`：核对
  内置 OpenAI 认证、模型档位及 ChatGPT 请求压缩/HTTP 回退合同。
- CPA 源码 `911f49cbd49f4fe2516979754a7488a3fd14fe64`：其 Codex Responses
  转换器将 `fast` 规范为 `priority`，保留 `priority`/`ultrafast`；其它值可能被移除。

## 仍需真实环境确认的边界

- Fast 可选、请求带 `priority`，证明客户端能力和转发链路保留；实际档位、计费和速度
  取决于 CPA 版本、模型及它的上游账号，不能从本地测试保证实际加速。
- “全走 CPA”指上述模型请求。模型目录和其它官方后端路径仍走官方，保留订阅登录
  不等于把所有网络请求改发 CPA，也不会自动把不支持的模型 ID 映射成目标模型。
- 原官方认证过期/401/403 会正常显示。部分桌面版本在官方订阅额度耗尽时可能先阻止
  发送请求；未移植 Magpie 完整的额度耗尽检测及 API 状态回退，不能承诺绕过这种限制。
  相关上游依据为 Magpie `docs/subsystems/agent-wiring.md` 的 ChatGPT/API 状态说明。
- 旧版配置内的活动 profile 覆盖切换键时会拒绝冲突；新版客户端独立的 `--profile`
  配置层、命令行或环境变量也可能覆盖默认配置，应以启动时生效的配置为准。
- 完整桌面登录态功能、历史会话交互、真实 CPA/Fast、三平台托盘与登录自启需按
  ACCEPTANCE.md 真机验收；云环境的原生窗口启动和交叉编译不代替这些结果。
