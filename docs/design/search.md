# 网络搜索

## 1. 范围与决策

- 系统管理增加独立菜单「网络搜索」（`/platform/web-search`），与「模型供应商」分开管理。
- 搜索实例类型首批为 `deepseek`、`tavily`、`bocha`。同类型可创建多个实例，名称、Base URL、密钥、启停状态相互独立。
- 全局默认指向具体实例，可清空。智能体独立控制 `tools_enabled.web_search`，默认为 false；`search_provider_id=null` 表示跟随默认，非空表示明确指定实例。
- **每次搜索只调用所选实例一次，不自动重试、不自动回退。** 空结果是成功；未设置默认、实例不存在或停用、认证/额度/限流/网络/响应错误均明确报错。
- 智能体选择随主/子智能体会话快照固定，修改智能体影响新会话。继承关系保留为 null，在下一次搜索时读取当前默认。密钥和停用状态同样在每次调用读取，快照不含密钥。
- 不新增输入框开关，Agent 根据任务决定调用。第一版不做网页抓取、结果缓存、编号引用或聚合搜索。

## 2. 工具实现

`internal/search` 管理实例、默认及 HTTP 适配；`internal/runtime/tools/web_search.go` 包装为 Eino 工具。服务端直接调用供应商，不依赖云电脑网络，也不向云电脑或浏览器发送密钥。模型支持工具调用即可使用，与对话模型供应商无绑定。

```json
{"query":"Go release notes","count":5}
```

`query` 必填，trim 后 1–512 个 Unicode 字符；DeepSeek 限制 128 字符，超出返回可修正的错误，不截断查询。`count` 缺省/0 为 5，有效值为 1–10。

供应商适配：

| 类型 | Base URL 默认值 | 追加路径 | 主要参数 |
|---|---|---|---|
| deepseek | `https://api.deepseek.com` | `/v1/tools/search` | `count`、`need_content=false` |
| tavily | `https://api.tavily.com` | `/search` | `max_results`、`search_depth=basic`、`include_answer=false`、`include_raw_content=false`、`include_usage=true` |
| bocha | `https://api.bocha.cn` | `/v1/web-search` | `count`、`summary=true`、`freshness=noLimit` |

DeepSeek/博查 Base URL 已以 `/v1` 结尾时避免重复版本段。全部使用 Bearer 密钥。禁止 HTTP 重定向，避免凭据转发；传输/业务错误不回显供应商响应原文或带密钥的请求信息。

结果为 JSON 字符串：

```json
{
  "query":"Go release notes",
  "provider_id":"<instance-id>",
  "provider_name":"DeepSeek 主账号",
  "results":[{
    "title":"Release history",
    "url":"https://example.com/releases",
    "snippet":"展示摘要",
    "content":"提供给模型的相关摘要或片段",
    "published_at":"2026-10-23T00:00:00Z"
  }],
  "duration_ms":420,
  "truncated":false
}
```

发布时间未知时省略；`credits` 仅在供应商返回时提供。不同供应商的相关性分数不做横向归一化。

调用使用 20 秒超时并传播会话取消；HTTP 响应最多读取 2 MiB。只保留不含用户信息的 HTTP(S) 结果 URL（不超过 4096 字节），移除 fragment 后去重。标题/短摘要分别限 300 字符，单结果模型片段限 2000 字符，片段总预算 12000 字符，截断保持 Unicode 完整并置 `truncated=true`。模型工具说明要求将检索内容视为外部资料并引用来源 URL。

工具风险 low，沿用三种审批模式。错误由现有中间件转为 `Tool execution failed: ...`，写入 `tool/result`，让模型报告失败或修正查询；不直接终止整轮。渲染类型为新增的 `web_search`，已有 `search` 继续用于文件检索。历史回放只读取持久化结果，不发起新搜索。

## 3. 配置与 REST

所有路径加 `/api/v1` 前缀。实例写操作、测试和全局默认均仅系统管理员可用。

| 方法 | 路径 | 请求/响应 |
|---|---|---|
| GET | `/platform/web-search/providers` | `{items:[ProviderView]}` |
| POST | `/platform/web-search/providers` | 创建实例，返回 201 ProviderView |
| PUT | `/platform/web-search/providers/{id}` | 完整替换非密钥配置，返回 ProviderView；密钥空/缺省保留，类型不可变 |
| DELETE | `/platform/web-search/providers/{id}` | `{ok:true}`；被全局默认或任何智能体引用时 409 |
| POST | `/platform/web-search/providers/{id}/check` | `{query}`；实际调用一次，返回工具结果结构，最多 3 条 |
| GET | `/platform/web-search/config` | `{default_provider_id:string|null}` |
| PUT | `/platform/web-search/config` | `{default_provider_id:string|null}`；新指定的默认实例必须 active |
| GET | `/orgs/{orgId}/admin/search-providers` | 组织管理员只读：`{items:[{id,name,type,status}],default_provider_id}` |

创建/更新实例 body：`{name,type,base_url,api_key?,status?}`。创建必须填写密钥，状态缺省 active。名称 1–128 字符，Base URL 最多 512 字节且不含凭据、查询参数或 fragment。响应包括 `id,name,type,base_url,status,masked_key,created_at,updated_at`，从不返回加密或明文密钥。

智能体创建/更新 body 新增 `search_provider_id:string|null`，并在 `tools_enabled` 增加 `web_search:boolean`。允许保存已停用实例的已有引用，调用时明确失败；组织管理员看不到地址和密钥。删除实例与全局默认更新通过配置单例行锁串行化，智能体外键进一步保护引用。停用允许即时生效，包括当前默认；已有快照指向后来删除的实例时明确报错。

管理接口沿用 `request/invalid`/`request/not_found`/`request/conflict` 类通用响应。搜索错误码：`search/unavailable`（503）、`search/cancelled`（499）、`search/timeout`（504）、`search/network`/`search/upstream`/`search/response`（502）。工具层将错误正文交给已有错误中间件，REST 测试接口返回相应错误信封。

## 4. 数据与用量

迁移 `000009_web_search` 新建 `search_provider`、单例 `search_config`、`search_call`，以及 `agent.search_provider_id` 可空外键。完整增量 DDL 见 [data-model.md §16](data-model.md#16-网络搜索000009)。不自动启用现有智能体，不自动选择第一个实例。

实例密钥复用现有主密钥与 AES-256-GCM 加密方案。创建、更新、删除和默认变更写入审计，审计不含密钥；更新记录 `key_updated` 布尔值。

每次实际向上游发起搜索独立写入 `search_call`，包含实例、组织、用户、智能体、会话、tool call ID、成功状态、结果数、耗时、可选 credits 和错误码。不保存原始查询、密钥、供应商错误原文，不换算未知金额，不混入模型 token 统计；测试搜索使用操作者 user_id，其余业务归属为空。取消后使用独立 2 秒上下文尝试写入用量，失败只记固定错误日志、不影响工具结果。结果及查询保存在原有会话事件中。第一版未提供搜索用量报表 API。

## 5. UI 与验收

- 网络搜索页：默认实例选择器和明确保存操作；供应商卡片展示名称、类型、地址、掩码、启停状态、默认标记；可新建、编辑、停用、删除及测试。
- 编辑弹窗：类型选择填官方地址；同类型可创建多个实例；已有实例不能切换类型；编辑密钥留空保留。
- 测试弹窗：可编辑关键词，提示一次调用可能产生费用，展示来源列表或明确错误。
- 智能体工具区：独立联网搜索开关；开启后选择「跟随全局默认（当前实例）」或具体实例。停用项不可新选，已有选择仍展示；默认缺失或配置加载失败给出提示。
- 对话：保留工具折叠交互；摘要展示查询词，展开显示实际实例、结果数量、耗时、标题链接、域名、日期、短摘要和截断提示。显式区分运行、取消、失败和成功但无结果。使用主题 token，摘要按文本渲染，历史 URL 再校验，不加载外部 favicon。
- Go httptest 覆盖三家请求/响应适配、空结果、错误不重试、凭据重定向保护、超时取消、响应大小、去重及 Unicode 截断。运行时测试覆盖主/子智能体的指定/继承选择、失败不中断整轮与持久化。前端测试覆盖回放、链接验证与错误状态。
- MySQL 集成测试需显式设置 `WISPJAR_SEARCH_TEST_DSN` 指向**空的可丢弃 schema**，测试拒绝非空库且不自动删除库。验证迁移、同类型多实例、默认/显式选择、严格失败、轮换、停用、删除约束和调用记账。真实供应商连通性须在管理员配置密钥后用测试搜索验证。

## 6. 调研依据

接口规格于 2026-10-23 核对：[DeepSeek Search](https://api-docs.deepseek.com/api/search/)、[DeepSeek 指南](https://api-docs.deepseek.com/guides/search/)、[Tavily Search](https://docs.tavily.com/documentation/api-reference/endpoint/search)、[博查官方适配实现](https://github.com/bocha-ai/dsh-web-search-bocha)。
