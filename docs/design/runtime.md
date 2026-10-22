# Agent 运行时详细设计

> 本文是 `internal/runtime`、`internal/sandbox`、`internal/skill`、`internal/provider` 的权威设计。
> 用户云电脑的新资源边界、目录、并行执行和技能更新以 [computers.md](computers.md) 为准。本文 `/workspace`、会话独占实例、统一 `/skills` 的描述仅为迁移前兼容行为；新路径为 `/sessions/<sid>` 与 `/agents/<agent_id>/skills`，改造任务见该文档 §12。
> 依赖事实（调研结论）：
> - eino：`adk.NewChatModelAgent` 即预置 ReAct Agent（model → tools 循环，`MaxIterations` 默认 20）；`adk.Runner{EnableStreaming:true}` 产出 `AsyncIterator[*AgentEvent]`；工具声明用 `components/tool/utils.InferTool`；模型组件在 eino-ext（openai/ark/qwen 各自独立 module）；interrupt + `CheckPointStore` 支持工具审批恢复；`adk.WithCancel` 支持安全点取消。
> - OpenSandbox：lifecycle REST（`POST /v1/sandboxes` 等，header `OPEN-SANDBOX-API-KEY`）；execd 数据面（`/command` SSE 流式、`/files/upload|download|replace|search|info`、`/directories/list`）；官方 Go SDK `github.com/alibaba/OpenSandbox/sdks/sandbox/go`；execd 无原生内容搜索端点（grep 用 exec 跑 `rg`）。
> - **版本校正义务**：实现前以 `go.mod` 锁定版本的源码为准核对本文出现的 eino/OpenSandbox API 签名；不一致处以锁定版为准，本文表达的是设计意图。

## 1. 运行时总链路

```txt
POST /sessions/{id}/prompts
  → session.Service.Prompt：幂等校验(request_id) → 追加 user/message + turn/start + request/header
  → runtime.Orchestrator.StartTurn(session)（按 session_id 内存互斥锁，一会话一 turn）
      1. derive：session_event → []schema.Message（protocol.md §6.4）
      2. build：按会话 agent_snapshot 构建 eino ChatModelAgent（§2）
      3. run：adk.Runner{EnableStreaming:true, CheckPointStore:mysql}.Run(ctx, history, adk.WithCancel())
      4. translate：AgentEvent → StreamChunk / 会话事件（protocol.md §9 映射表）
      5. persist+ broadcast：事件写库（同事务更新投影）→ mux 广播 event 帧；瞬态帧即时广播
  → turn/end（completed|interrupted|failed）→ 释放锁 → usage 异步聚合
```

关键 Go 接口（包 `internal/runtime`）：

```go
type Orchestrator interface {
    StartTurn(ctx context.Context, sessionID string) error      // 幂等：running 中返回 ErrSessionRunning
    Cancel(sessionID string) error                               // 用户停止
    DecideApproval(sessionID, callID string, allow bool, by string) error
    // Follow 由 gateway 调用：返回快照 + 订阅句柄（内部为事件总线）
    Follow(sessionID string, afterSeq int64) (Snapshot, <-chan FollowFrame, func(), error)
}
```

- 事件总线：内存 pub/sub（按 session_id 频道），单实例部署足够；持久层是 session_event。
- `Snapshot` 构造：读 sessions 投影列 + 尾部事件窗口 + 进行中 attempt 的 compactChunks（内存暂存的当前流）。

## 2. Agent 构建（按会话快照）

```go
func buildAgent(ctx context.Context, sess Session, snap AgentSnapshot, deps Deps) (adk.Agent, error) {
    params := merge(snap.ModelParams, sess.ModelParams)                 // 会话级覆盖 Agent 级；factory 内再叠 model.default_params
    if deps.ProviderType(ctx, modelID) == "ark" {
        params["prompt_cache_key"] = sess.ID                            // ark 上下文缓存路由亲和（§3）
    }
    chatModel := deps.ProviderFactory.NewChatModel(ctx, modelID, params) // §3
    tools := buildTools(ctx, snap, deps)                                 // §4
    instruction := renderInstruction(snap, sess)                         // system_prompt + Skills 索引段 + 运行时变量
    return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
        Name: snap.AgentID, Description: snap.Description,
        Instruction: instruction, Model: chatModel,
        ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}},
        MaxIterations: snap.MaxIterations,
    })
}
```

- `renderInstruction`：`agent.system_prompt` + 拼接"## 可用 Skills"索引段（每条 `- {name}: {description}`，提示"用 skill 工具加载完整说明"）+ 末尾注入运行事实（**会话创建日期**（`sess.CreatedAt`，非 `time.Now`——system 是 messages[0]，逐天变化会让其后全部历史的缓存前缀失配，见《缓存命中优先设计原则》§12）、工作目录 `/workspace`、确认模式说明——strict 模式下提示模型"危险操作会被用户审查"）。
- 会话级 `model_id`（用户切换过）覆盖快照中的 model。
- 模型参数三级优先级：**会话 `session.model_params` > Agent 快照 `model_params` > 模型 `default_params`**。会话级承载用户选档（如 `{"reasoning_effort":"high"}`），创建时缺省 = 模型 `capabilities.reasoning_effort_default`；`last_seq > 0` 后锁定（锁档理由：DeepSeek 思考模式带 tools 时要求完整回放历史 `reasoning_content`，中途换档会改变每轮输出形态与回放义务；且同会话内推理深度/时延/成本跳变体验断裂。effort 是请求级参数、不进消息前缀，与前缀缓存无关）。

## 3. 供应商工厂（internal/provider）

```go
type ChatModelFactory interface {
    NewChatModel(ctx context.Context, modelID string, params map[string]any) (model.ToolCallingChatModel, error)
    Check(ctx context.Context, providerID string) error   // 连通性测试
}
```

- 分派：缺省 Chat Completions 使用 `eino-ext/components/model/openai`；显式 `api_protocol=responses` 使用 provider 内 Responses 适配器（首期 deepseek/ark/openai_compat）。供应商类型不隐式切换协议，不兼容的协议组合明确拒绝。
- 密钥解密仅在工厂内部（data-model.md §5 加密约定）；模型实例缓存键包含 model_id、合并参数、模型代码、供应商类型、端点、加密凭据和能力配置摘要，参数/凭据/能力改变均重新构建。
- **适配器注册表**（`adapters.go`）：各家差异收敛为按 `provider_type` 分派的 `ProviderAdapter`，`openai_compat` 为兜底适配器（宽容解析、默认「未上报」，新增供应商零适配可跑）。接口三方法：
  - `Capabilities()` → `AdapterCapabilities`：`UsageCacheReadField`/`UsageCacheWriteField`（usage 字段口径）、`SupportsPromptCacheKey`、`MinCacheTokens`（ark 1024、qwen 1024、deepseek 0、compat 0）；
  - `ApplyParams(cfg, params)`：参数映射（白名单，未识别键忽略）；
  - `NormalizeUsage(raw)`：原始 usage 归一化（见下）。
- 参数映射（白名单，未识别键忽略）：
  - `temperature/top_p/max_tokens` → 组件对应字段（各家通用）；
  - `reasoning_effort` 是平台统一档位 `none/minimal/low/medium/high/xhigh/max`，保存值不变，仅调用边界转换，具体见下表；缺省或非法值不额外注入思考参数；
  - `prompt_cache_key`（string）→ `cfg.ExtraFields`，**仅 ark 放行**：ark 上下文缓存的路由亲和参数，上下文连续的请求用同一取值（builder 对 ark 供应商注入 `sessionID`，同 key 流量 ≤15 rpm）；其余各家（含 openai_compat）不放行。
- 模型能力声明 `capabilities.reasoning_efforts`（上述七档的可选子集）+ `reasoning_effort_default`（必须属于已选集合）由平台逐模型录入；空 = 不支持选档。供应商/模型不支持关闭思考或不支持某档位时，配置中不应勾选该档位；不因统一词表而推断所有模型均支持全部七档。

推理等级调用映射（2026-10-16 核对官方文档）：

| 供应商/模型 | none | minimal | low | medium | high | xhigh | max |
|---|---|---|---|---|---|---|---|
| DeepSeek Chat | `thinking.type=disabled`，不发 effort | low | low | high | high | high | max |
| 方舟 ark | 原值 | 原值 | 原值 | 原值 | 原值 | 原值 | 原值 |
| Qwen3.8-* | `enable_thinking=false` | low | low | medium | xhigh | xhigh | xhigh |
| 较早 Qwen（预算模式） | `enable_thinking=false` | 128 | 1024 | 8192 | 16384 | 32768 | 不设预算，使用模型最大值 |
| OpenAI compatible | 原值 | 原值 | 原值 | 原值 | 原值 | 原值 | 原值 |

- DeepSeek 的非 none 档位同时发送 `thinking.type=enabled`，表中值发到顶层 `reasoning_effort`。依据：[DeepSeek Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode/)。
- 方舟支持选档的模型接受全部七档并按模型版本执行官方兼容映射，例如豆包 `none→minimal`、`xhigh/max→high`；DeepSeek/GLM 的规则不同，`ep-*` 接入点又不暴露真实模型，因此保留原值交由方舟转换，不能全局压到 high。`minimal` 在部分方舟模型上也会关闭思考。依据：[方舟深度思考](https://docs.volcengine.com/docs/ark/deep-thinking)。
- Qwen 非 none 档位发送 `enable_thinking=true`。Qwen3.8-* 用顶层 `reasoning_effort`，不发 `thinking_budget`，二者互斥；较早 Qwen 表中的数字是平台约定的思考 token 预算，通过顶层 `thinking_budget` 传入（`ExtraFields`），并非阿里云官方枚举或官方档位等价表；max 省略预算，按官方默认使用该模型最大思维链长度。依据：[百炼 Chat Completions 参数](https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions/)、[思考预算说明](https://www.alibabacloud.com/help/en/model-studio/deep-thinking)。
- OpenAI compatible 是开放集合，按模型实际支持情况配置可选子集，原值传入顶层 `reasoning_effort`，不猜测第三方兼容服务的私有映射。依据：[OpenAI reasoning effort](https://developers.openai.com/api/docs/guides/reasoning)。

### 3.1 usage 按供应商归一化（原始 usage 旁路抓取）

本节为 Chat Completions 路径。显式选择 Responses 的模型使用独立适配器：读取 `input_tokens`、`output_tokens`、`total_tokens`，将 `input_tokens_details.cached_tokens` 写入同一 UsageSink；没有该字段时保留未上报语义。Responses 使用 `reasoning.effort`，DeepSeek 的 none 直接传 none，其余档位归并为 low/high/max；不发送 Chat 专属 thinking 字段。OpenAI/方舟保留所配置的 effort 原值。详见 [media.md](media.md)。

eino acl（`libs/acl/openai` 的 `toEinoTokenUsage`）只映射 OpenAI 标准字段，供应商特有的缓存字段在转换时被丢弃（fork 层 `Usage.ExtraFields` 捕获了顶层未知键但 acl 不透传；`prompt_tokens_details` 内的未知键在 fork 层即丢），`schema.TokenUsage` 无扩展口子。因此工厂在注入组件的 `HTTPClient` 上包一层 `usageCaptureTransport`（`usage_capture.go`）旁路抓取原始 usage：

- **注入点**：`components/model/openai` 的 `ChatModelConfig.HTTPClient`（acl `Config.HTTPClient` 同理）；go-openai 经 `http.NewRequestWithContext` 发请求，调用方 ctx 透传到 `http.Request`——orchestrator 在 StartTurn/审批 resume 前把 turn 级 `UsageSink` 挂到 ctx value，transport 从 `req.Context()` 取 sink 写入，按请求路由到正确的 turn（模型实例跨会话/并发共享也安全）。
- **抓取**：非流式 tee body（上限 8MB）解析 JSON 取 `usage`；流式 SSE 逐行扫描 `data:` 帧找含 `"usage"` 的 chunk（acl 已设 `stream_options.include_usage`，usage 在末帧），不全量缓冲；body 与 eino 顺序共享（无并发 tee goroutine）。抓取失败一律静默降级，绝不影响主请求。
- **归一化顺序**（`NormalizeUsage(raw)` → cacheRead/cacheWrite/reported）：

  | provider_type | cache_read | cache_write |
  |---|---|---|
  | deepseek | 顶层 `prompt_cache_hit_tokens` → fallback `prompt_tokens_details.cached_tokens` | 无 |
  | ark | `prompt_tokens_details.cached_tokens` | 无 |
  | qwen | `prompt_tokens_details.cached_tokens` | `prompt_tokens_details.cache_creation_input_tokens`（显式缓存） |
  | openai_compat | `prompt_tokens_details.cached_tokens`（宽容解析，容忍畸形） | 无 |

  raw 中没有该家任何缓存字段 → `reported=false`（区分「未命中」与「供应商未上报」）。
- **合并**：orchestrator 每个 step 提交时从 sink 取最新一条（主流程与 task 子运行均串行，一一对应）；`reported=true` 时覆盖 assistant/message 事件 usage 的 cache_read/cache_write 并置 `cache_reported=true`，否则回退 eino 侧 `CachedTokens`（抓取失败维持原行为）。`cache_reported` 语义：本次调用供应商上报了缓存字段（命中可为 0）；前端据此区分「命中率 0%」与「未上报」（ContextRing 悬停统计显示「缓存命中 —」）。usage_daily 聚合口径不变。

## 4. 工具规格（模型可见 schema）

> 纯文本工具用 `utils.InferTool` 实现；read_media 用 `utils.InferEnhancedTool`（返回 `schema.ToolResult`，tool 消息可携带图片 part）。模型侧只暴露 name/description/parameters。每个工具有：风险等级（审批）、`render` 词汇（UI 卡片）、超时。
> 沙箱工具从 ctx 取 `SandboxHandle`（首次调用惰性创建，§7）；工作目录默认 `/workspace`。

### 4.1 exec（风险 high，render terminal）

```jsonc
// name: exec —— 执行 shell 命令（bash -lc），返回退出码与输出
{ "command": "string（必填）",
  "cwd": "string，默认 /workspace",
  "timeout_ms": "int，默认 30000，上限 600000",
  "env": "object<string,string>，附加环境变量" }
// 返回：{ "exit_code": 0, "stdout": "...", "stderr": "...", "truncated": false }
// infra 错误（超时/沙箱不可达/启动失败）：{ "error": "...", "stderr": "" }（不中断 run，§4.11）
```

实现：execd `POST /command`（`background:false`，SSE 收集流式输出；流式过程同时以 transient 帧推前端 terminal 卡片实时滚动——作为 `tool/result.meta` 之外的 live 通道，协议上用 assistant-stream 之外的 `tool-output` 瞬态帧，见 §9）。输出 >64KB 截断（保留尾部，`truncated:true`）。

### 4.2 read（风险 low，render read）

```jsonc
// name: read —— 读取文本文件（带行号），支持分页
{ "path": "string（必填，绝对路径）",
  "offset": "int，起始行（1-based），默认 1",
  "limit": "int，最多返回行数，默认 200，上限 2000" }
// 返回：{ "path", "content": "1\tline...", "total_lines": N, "truncated": bool }
```

实现：execd `GET /files/download` 拉取（>2MB 拒绝并提示用 exec 分段）→ 服务端切片加行号。非 utf8 内容返回错误提示（建议 exec `xxd`）。

### 4.3 write（风险 medium，render diff）

```jsonc
// name: write —— 写入文件（覆盖式），自动创建父目录
{ "path": "string（必填）", "content": "string（必填，完整内容）" }
// 返回：{ "path", "bytes": N, "operation"?: "create" | "update" }
```

实现：execd `POST /files/upload`；写入前通过 file info 区分不存在（create）或已存在文件（update），仅写入成功后将 operation 放入返回结果及 `meta.operation`，探测失败时省略标记，不阻断写入。已有文本可读取时计算 unified diff 放入 `meta.diff`（供 UI diff 卡片），空文件同样参与比较；旧文件读取失败、非 UTF-8、任一侧超过 2MB 或行数乘积超过 4,000,000 时省略 diff，避免可选展示影响写入。前端从持久工具调用参数 `content` 展示及复制该次写入全文，不依赖当前沙箱文件；无 diff 也可查看全文。

### 4.4 edit（风险 medium，render diff）

```jsonc
// name: edit —— 对文件做精确字符串替换
{ "path": "string（必填）",
  "old_string": "string（必填，须唯一匹配，除非 replace_all）",
  "new_string": "string（必填）",
  "replace_all": "bool，默认 false" }
// 返回：{ "path", "replacements": 1 }
```

实现：execd `POST /files/replace`；`replacements=0` 或（非 replace_all 且 >1）视为失败并把原因返回给模型；`meta.diff` 同样生成。

### 4.5 glob（风险 low，render search）

```jsonc
// name: glob —— 按 glob 模式搜索文件名
{ "pattern": "string（必填，如 **/*.go）", "path": "string，默认 /workspace" }
// 返回：{ "files": [ {"path","size","modified_at"} ], "truncated": bool }（按修改时间倒序，上限 100）
```

实现：execd `GET /files/search?path=&pattern=`。

### 4.6 grep（风险 low，render search）

```jsonc
// name: grep —— 搜索文件内容（ripgrep 语义）
{ "pattern": "string（必填，正则）",
  "path": "string，默认 /workspace",
  "glob": "string，文件名过滤，如 *.go",
  "ignore_case": "bool，默认 false",
  "output_mode": "content|files_with_matches，默认 content" }
// 返回 content 模式：{ "matches": ["path:line:text", ...], "truncated": bool }（上限 250 行）
```

实现：execd `POST /command` 执行 `rg --line-number --no-heading [--ignore-case] [-g glob] -- pattern path`（`files_with_matches` 加 `-l`）；沙箱镜像保证预装 `rg`。沙箱缺 `rg`（exit 127 / "command not found"）时自动降级 `grep -rInE --color=never`（参数映射见 §4.11），输出同为 `path:line:text`。

### 4.7 skill（风险 low，render generic）

```jsonc
// name: skill —— 加载指定 Skill 的完整使用说明（SKILL.md 全文 + 包内文件清单）
{ "name": "string（必填，Skills 索引段中的 name）" }
// 返回：{ "name", "instructions": "<SKILL.md markdown 全文>",
//         "files": ["scripts/a.py", ...], "dir": "/skills/<name>/" }
```

新云电脑路径：根据主运行取得的 `skill.Manifest`，读取当前执行 Agent 的 `/agents/<agent_id>/skills/<name>/SKILL.md`。不能读取已更新的平台源包来搭配尚未同步的安装脚本。SKILL.md >32KB 截断并提示用 read 工具读包内具体文件。旧会话兼容路径仍读取平台源包，待迁移后切换。

`skill.Coordinator.Acquire(ctx, AcquireRequest{OrgID,UserID,RootRunID,AgentIDs}, installer)` 要求先登记主 `computer_run`；AgentIDs 包含主 Agent 和所有可达 subagent。它在新主任务边界解析当前关联，整组取权后把 `skill_manifest` 合并写入运行 manifest。同 rootRunID 的审批恢复返回原 manifest，不重新解析新版本。所有主/子任务和后台命令均确认结束后才能 `Release(..., commandsStopped=true)`；租约失效不能代替确认。

平台通过 `skill.NewSandboxInstaller(upload, run)` 使用原始 root 执行能力，同步校验后将技能文件设为只读。`/agents`、Agent 根和 `skills` 根归 root，任务只能写自己的 `files` 与会话目录；`/agents/.wispjar` 是 root-only 控制区。实际任务采用非 root 身份。安装失败已确认退出可重试；进程状态未知则保留屏障，须经带审计的 `Recover` 确认停止后推进 fence。

电脑显式恢复在删除旧实例且 Inspect 证实消失、旧代次运行已终结后，调用 `Coordinator.RecoverComputer` 收敛该电脑代次的技能状态。安装计划归属来自持久运行记录；旧无归属计划继续阻塞。安装结果与整组租约提交再次锁运行行并校验终态、释放标记和代次，拒绝恢复期间晚到的旧安装结果；锁顺序与生命周期 hook 契约见 computers.md §7。

### 4.8 task（风险继承子 Agent，render generic，v1=M3）

```jsonc
// name: task —— 把自包含任务委派给指定子智能体。子智能体在独立上下文中执行
//（看不到本会话历史），与你共享同一个文件系统，完成后返回最终结果（不含中间步骤）。
{ "subagent": "string（必填，可用子智能体名，见工具描述枚举）",
  "prompt": "string（必填，完整自包含的任务描述——子智能体看不到本会话，必须包含全部必要信息）" }
// 返回：{ "output": "子智能体最终答复文本" }
// 失败：error = "<失败原因>\nDiagnostic: ...\nPartial output: ..."（部分输出仍返回给主 Agent）
```

工具描述（运行时按挂载子 Agent 动态生成）：

```txt
Delegate a self-contained task to a subagent. The subagent works in its own context
(it does NOT see this conversation) and shares your filesystem. It returns
only its final result. Available subagents:
- data-analyst: 数据分析与可视化，擅长 pandas/matplotlib
- web-researcher: 联网检索与资料综述
Use this when a subtask is focused and independent; include everything the subagent needs in `prompt`.
```

### 4.9 question（风险 low，render generic）

```jsonc
// name: question —— 向用户提问并等待回答（一卡多题，前台最多等待 10 分钟）。
// 仅在 genuinely 需要用户决策或缺失信息时使用，不做礼节性确认。
{ "questions": [ { "id": "string（必填，本次调用内唯一，答案中回显）",
                   "question": "string（必填，问题文本）",
                   "header": "string，短标题，如 Confirm / Choose Mode",
                   "options": [ {"label": "string（必填）", "description": "string"} ],
                   "multi_select": "bool，默认 false" } ] }
// 题型由 options 有无 + multi_select 推导：无 options=自由输入；有 options 单选/多选。
// 窗口内作答返回：{ "answers": [ {"id", "selected": ["label"...], "custom"?} ] }
//   —— 每题恰好一条；selected 回传选项 label（不是下标，模型最不易错）；
//      空 selected 且无 custom = 用户显式跳过（仍算已作答）
// 超时返回：{ "pending": true, "call_id": "...", "message": "..." }
//   —— 是 pending 不是跳过：用户答复稍后会作为一条新 user message 到来，绝不能当作许可
```

实现：internal/runtime/question.go（等待语义与子 Agent 规则见 §5.3）。任何 approval_mode 下不被审批中间件拦截（交互工具，豁免方式同 skill）。仅 depth=0 根 Agent 可用；depth=1 注册同 schema 拒绝桩（§5.3）。

### 4.10 read_media（风险 low，render read）

协议、持久化和恢复完整规格见 [media.md](media.md)。请求边界统一处理轮内和历史图片预算，工具结果与 checkpoint 使用本地附件引用。

```jsonc
// name: read_media —— 读取 PNG/JPEG/WebP/GIF 图片并把图片本身返回给模型查看
{ "path": "string（与 attachment_id 互斥，绝对或工作区相对路径）", "attachment_id": "sha256:<64 hex>（重读已保存图片）" }
// 返回（eino ToolResult，tool 消息直接携带多模态 parts）：
//   parts[0] text:  { "path", "mime", "width", "height", "bytes",
//                     "sha256": "<前8位>", "attachment_id": "sha256:<64 hex>" }（确定性信封）
//   parts[1] image: wispjar-attachment:<attachment_id> + MIMEType（仅平台内部引用）
// 域拒绝均为文本工具结果（非 Go error，不中断 run）：
//   当前路由不支持原生工具图片 → "read_media requires vision, tools, tool_images…"（先于任何图片 I/O）
//   非图片（魔数嗅探 png/jpeg/webp/gif）→ "is not a supported image…"
//   原图 >8MB / >64M 像素 → 拒绝并建议用 exec 缩放后再读
```

实现（internal/runtime/tools/read_media.go）：沙箱读文件（FileInfo 预检大小 → download）→ `attachment.ValidateImage`（8MB/64M 像素闸值）→ 字节原样写入内容寻址存储 → 附件引用登记进 tool/result 的 `attachments`。`attachment_id` 路径先校验租户授权再返回既有引用，不访问沙箱。首期不做 EXIF 转正/重编码/压缩。门控按当前主/子 Agent 模型分别检查 vision、tools、tool_images、Responses 协议；不支持则返回文本错误。审批分级与 read 同级（low，strict 也不拦）。

回放与卸载：derive 把 tool/result 的 attachments 投影到 tool 消息；history 组装 `[text 信封, 本地 image 引用]`，字节只在 provider 请求边界读取。user 与 tool 共用预算，轮内后续调用同样执行预算。占位包含完整 attachment_id，可用 read_media 重读，不依赖旧路径仍存在。

**供应商兼容**：Responses 适配器将图片放入原生 `function_call_output.output[].input_image.file_id`。Chat Completions 的工具图片直接拒绝，不降级成信封或额外 user 消息。用户上传图片在 Chat Completions 请求边界保留原有 base64 路径。Responses 原生输出以 assistant 的 provider_replay 保存，保留工具调用 ID 与推理状态；跨连接/模型不复用。

### 4.11 错误语义（工具失败不中断 run）

eino ToolsNode 会把工具返回的 Go error 升级为整个 run 失败（turn interrupted）。因此运行时统一约定：**工具执行失败一律作为错误文本的 tool result 回喂模型继续运行，Go error 不外溢**。三类语义：

- **域拒绝**（模型可修正的输入问题）：工具内直接返回文本结果（read_media 的 vision 门控/非图/超限；edit 的 old_string 未命中；read 的文件不存在/非 UTF-8 等——后者经归一化层统一转换）。
- **执行失败**（infra/环境错误：`errorResultMiddleware`（注册在 `ToolCallMiddlewares` 链尾、贴近执行处，Invokable 与 EnhancedInvokable 双链覆盖）把工具返回的 Go error 统一转为文本结果 `Tool execution failed: <err>`；exec 例外地返回结构化结果 `{"error": "...", "stderr": "..."}`（render 仍为 terminal）。失败路径 meta/render/duration 照常落 tool/result 事件。
- **中断**（不外溢也不归一化，原样上抛）：仅来自 ① 用户停止/服务中断（外层 ctx 取消/超时，工具自身超时不在此列——外层 ctx 存活时按执行失败转文本）；② 审批等 eino interrupt 机制（interrupt 信号原样传递，审批 deny 本就是文本结果 `"denied by user"`）。

链序：`approvalMiddleware`（外层，interrupt/deny 不经归一化层）→ `errorResultMiddleware`（内层）。grep 在缺 `rg` 的沙箱（exit 127 / "command not found"）自动降级 `grep -rInE --color=never`（`--ignore-case→-i`、`-g→--include`、`-l` 保持），重试仍失败按执行失败归一化。

## 5. 审批实现（internal/runtime/approval）

### 5.1 拦截点与状态机

eino `ToolsConfig.ToolCallMiddlewares` 注册审批中间件（在工具执行前；链序 `approvalMiddleware → errorResultMiddleware`，审批 interrupt/deny 不经归一化层，§4.11）：

```txt
查会话 approval_mode + 工具风险等级 → 需确认？
  否 → 放行
  是 → append approval/asked + 广播 → 触发 eino interrupt（runner checkpoint 落 agent_checkpoint）
       → 等待 DecideApproval / 超时(10min) / 服务重启(fail-closed=deny)
       → append approval/decided → Runner.ResumeWithParams(checkpointID, {allow})
```

pending 审批登记内存表 `map[callID]pendingApproval{sessionID, checkpointID, askedAt, timer}`；重启后为空 → 恢复时扫描无 decided 的 asked → 补 deny（`auto:true`）。

### 5.2 三模式判定

| 工具 \ 模式 | strict | default | auto |
|---|---|---|---|
| exec | 确认 | 确认 | 免 |
| write / edit | 确认 | 免 | 免 |
| read / read_media / glob / grep / skill / task / question | 免 | 免 | 免 |

- `grep` 虽走 exec 通道，按工具自身 low 风险归类（实现通道不影响风险语义）。
- 子 Agent 内的工具调用继承同一会话的 approval_mode（嵌套 asked/decided 带 `parent_call_id`）。

### 5.3 提问（question）实现（internal/runtime/question）

与审批**刻意不共用管线**（对齐 DSH：两者各自拥有独立的 pending-settlement 生命周期）——审批 resume 依赖 eino checkpoint 且 fail-closed 合理；提问答案是结构化的、等待时间可能远超 10 分钟，且超时不应让 turn 失败。挂起原语同构：工具 execute 内按 callID 登记内存等待（channel，缓冲 1）后 `select` 等待。

状态机（timed + continued 简化版）：

```txt
execute：登记内存等待 → append question/asked（问题文本显式落库）→ select 等待
  ├─ 窗口内 REST 投递 → 工具 append question/answered（保证 asked 先于 answered）
  │    → {"answers":[...]} 作为普通工具结果返回，turn 继续
  ├─ 超时（默认 10 分钟，Deps.QuestionWaitTimeout 可注入）
  │    → 工具结果 {"pending":true,"call_id","message"}，turn 正常继续（不是失败）；
  │      问题无 question/answered，投影上即 continued，保持可答
  └─ ctx.Done（turn 取消/中断）→ 等待随 ctx 结束，按现有中断语义落库（derive 悬空配对
       合成 interrupted tool/result）；已投递未消费的答案仍补记 question/answered
       （不丢用户作答）；未答的问题仍属 continued 可补答
continued（迟到答复）：REST 校验 → append question/answered + 合成 user/message
  （source:{kind:"question-reply",call_id}，正文 JSON
  {"kind":"answer_to_pending_question","call_id","questions","answers"}）开启新 turn
  ——迟到答复 = 一个新 turn，而不是恢复旧工具调用；会话忙 → 409 session/running
```

重启语义（零成本恢复）：重启后内存等待清空，未答的 `question/asked` 天然视为 continued（投影规则推导，**无需启动时扫表补偿**）。提问所在 turn 若仍 running，由 `RecoverPendingApprovals` 按现有中断收尾（补 `turn/end{failed}`）；挂起的工具调用按 derive 悬空配对规则合成 interrupted 结果，问题保持可补答。

子 Agent 规则：仅 depth=0 根 Agent 可真正提问。depth=1（task 子 Agent）注册同 schema 的拒绝桩，调用即返回错误文本，提示"把未决问题写进 task 最终结果由父智能体上浮"（对齐 DSH DELEGATED_CALLER）；桩以工具结果文本回喂而非 Go error（工具错误会被 eino 升级为整个子运行失败，见 §4.8）。不做嵌套 pending（审批的 parent_call_id 复杂度不在提问上重演）。

答案校验（REST，400 `question/bad_answer`）：每题恰好一条答案（按 id 对应，不允许未知/重复 id）；有 options 的题 selected 必须是选项 label，单选至多一个；自由输入题不允许 selected。

## 6. task 委派实现（M3）

```go
func NewTaskTool(snap AgentSnapshot, deps Deps) tool.InvokableTool // snap.SubAgents 为空则不注册
```

执行流程：

1. 校验 `subagent` 在快照的可委派清单（名称大小写不敏感匹配）。
2. 构建子 Agent：子 Agent 快照（创建会话时随 agent_snapshot 固化）+ **共享 ctx 中的 SandboxHandle** + `depth=1`（子 Agent 不注册 task 工具）。模型参数：子 Agent `model_params` 显式指定 `reasoning_effort` 时用子 Agent 的，否则继承父会话锁定的档位（同会话内输出形态/成本一致）。
3. 独立上下文运行：`adk.Runner.Run(ctx, []Message{UserMessage(prompt)})`——**不带主会话历史**。
4. 事件嵌套：消费子 AgentEvent 流，翻译为会话事件时全部附加 `parent_call_id=<本次 task 的 call_id>`（含 turn/step 用子序列 `sub_turn`/`sub_step` 字段避免与主冲突：data 内用 `turn:0, sub_turn:N` 表达，前端按 parent_call_id 聚合）。
5. 结果合成：取子 Agent 最终 assistant 消息文本作为 `output`；`stopReason != completed` 时按"错误标题 + Diagnostic + Partial output"合成 error（参考 §4.8 返回约定）。
6. 主 Agent 视角：一次普通 tool/call(task) + tool/result；审批在子 Agent 内部独立触发（嵌套事件）。
7. 并发：原 v1 使用 task 互斥锁串行执行；云电脑改造支持有界并行，必须先隔离工具 Context、UsageSink、审批/取消归属，再移除串行锁。电脑及会话工作区继承主任务，技能按实际执行的子 Agent 解析（computers.md §7）。

## 7. 沙箱管理器（internal/sandbox）

本节为旧会话实例的迁移兼容说明。新模式由 computer 持有实例生命周期，volume 解析三卷挂载，sandbox 只负责 SDK 适配；删除会话不再回收共享电脑，见 computers.md §3/§6。

新模式的工作区初始化区分“已退出失败”和“完成状态未知”：同步命令返回明确非零退出码时，包装为 `StoppedControlError`，主任务与文件维护都以 failed 释放对应运行占用；例如 inputs/outputs 是符号链接而被 `O_NOFOLLOW` 拒绝，不能因此永久占用电脑。只有传输断链、没有退出状态等无法确认完成的情况才保留占用，等待核查恢复。初始化尚未取得技能使用权，不得误当作仍有技能命令在执行。

资源收尾先关闭命令准入，再检查所有命令已停。准备过程保留独立预约，取消不能在准备尚未完成时释放资源；子任务在父运行锁内预占计数后才登记数据库运行，登记失败归还预约。父任务开始结束后拒绝新增子任务，避免数据库登记期间提前释放根技能租约。运行环境准备错误使用稳定错误码与固定文案写入 `turn/end` 和 API；后端地址、路径及原始原因仅记录服务日志。

显式电脑恢复在旧实例确认消失并将旧运行置终态后调用 `runtime.RecoverComputer`。该 hook 取消旧内存运行、阻断晚到的事件和审批恢复，并对重启残留的 running 会话补 deny 与 `turn/end{failed,error_code:"computer/recovered"}`；完成前电脑保持 recovering，禁止新 `turn/start`。运行事件落库还在会话行锁内校验 turn 代次，旧回调不能清除新任务的 running 状态。创建、Fork 和切换绑定同样在目标电脑行锁内拒绝 deleting/deleted。

恢复意图先持久化 recovering，再删除旧实例。若工具因此断链，而模型在恢复 hook 前已经自行收尾，事件落库边界仍按电脑 recovering 状态将 `turn/end` 统一记为 `failed` / `computer/recovered`；不得将恢复中断误报为 completed。该判定不改变命令确认与技能租约释放次序。

```go
type Manager interface {
    Get(ctx context.Context, sessionID string) (*Handle, error)  // 惰性创建 + 内存缓存
    Recycle(sessionID string, reason string) error               // 主动销毁
}
type Handle struct {
    SandboxID, ExecdURL string
    Execd *opensandbox.ExecdClient  // 命令/文件操作入口
}
```

### 7.1 创建参数（lifecycle `POST /v1/sandboxes`）

```jsonc
{ "image": {"uri": computer.config.image},
  "timeout": settings.sandbox_ttl_seconds,
  "resourceLimits": computer.config.resources,  // {"cpu":"1","memory":"1Gi"}
  "networkPolicy": settings.egress_policy,   // 平台统一黑白名单
  "env": {"WISPJAR": "1", "EXECD_ACCESS_TOKEN": "<platform-managed>"} }
```

电脑 env 创建时合并平台 `sandbox.env` 与显式配置，不从 Agent 模板复制。每轮开始读取电脑变量和主/子 Agent 最新 `sandbox_config.env`，本轮固定；exec 按电脑 → 当前 Agent → 命令 env 逐键覆盖，再注入 `WISPJAR=1`。变量只传入非 root 任务进程，不传给容器级 execd 或维护进程。子 Agent 不继承父 Agent env。电脑变量可通过 PATCH 编辑，Agent 变量可独立保存且不改变发布状态，已有会话下一轮任务生效，无需重建或改写历史快照。读取失败终止准备，不静默使用旧值。

键名须匹配 `[A-Za-z_][A-Za-z0-9_]*`，每层及合并后最多 64 项、单值最多 4096 字节，值不可包含 NUL。WISPJAR、HOME、EXECD_* 为保留变量；HOME 固定 /tmp。

创建成功后：
1. `GET /sandboxes/{id}/endpoints/44772` 解析 execd 地址 → 构造 ExecdClient。
2. 上传会话激活 Skills：`/skills/<name>/` 整目录（execd upload）。REST 沙箱面板端点（`GET /sessions/{id}/sandbox/file(s)`）在拿到新建/重建沙箱时同样做一次同步（按 sessionID→sandboxID 去重），保证服务重启句柄丢失后技能目录一致。
3. 更新 `session.sandbox` 为 `{"id": <沙箱ID>, "status": "running"}` + append `session/sandbox{action:"created"}`。

### 7.2 续约、暂停与回收

- **续约**：会话活动（每次事件写入）刷新内存 lastActive；后台任务每 30s 对距离过期不足 TTL/2 的沙箱调 `renew-expiration`。running 句柄仅在仍有活动（idle < idle_ttl）时续约；paused 句柄不受 idle 限制、持续续约以防被服务端过期清理。续约返回 404（沙箱已消失）→ 直接回收句柄。
- **空闲暂停（idle → pause）**：reaper 每分钟扫描，`now-lastActive > idle_ttl_seconds`（默认 28800=8h）→ `POST /sandboxes/{id}/pause` 保留文件系统 → `session.sandbox.status="paused"` + append `session/sandbox{action:"paused"}`。pause 404 → 按沙箱已消失回收；其他错误保留下一轮重试。
- **恢复（resume）**：沙箱获取路径（turn 内 ensure / REST 沙箱面板）遇到 paused 句柄先 `POST /sandboxes/{id}/resume`（同会话并发由句柄级互斥锁串行，只 resume 一次）→ `status="running"` + `action:"resumed"`，文件系统与 /skills 保留；resume 失败（沙箱已消失）→ 落入按需新建（Skills 自动重传逻辑不变）。
- **硬 TTL 回收**：`expiresAt` 到期（续约也无法延续）的句柄无论 running/paused 都 DELETE 回收（reason `ttl_expired`）→ `status="recycled"` + `action:"recycled"`。删除会话（DeleteSession）的回收不变：直接 recycle，不 pause。
- **重启 adopt**：启动时与每小时对比 lifecycle `GET /sandboxes` 列表与本进程句柄。wispjar 标签的孤儿沙箱：metadata 会话在本库存在且未软删且沙箱处于可恢复状态 → 重连并暂停，adopt 为 paused 句柄（不 touch 文件系统，之后按需 resume，事件 `action:"paused", reason:"adopted"`）；会话不存在/已删除或沙箱终态 → 强制删除（`action:"recycled", reason:"orphan"`）。
- **失败**：创建失败置 `session.sandbox.status="failed"` + 事件，工具调用返回"execution environment unavailable"错误给模型（可重试一次）。创建路径自带一次退避重试（2s），lifecycle 请求超时 120s（冷启动镜像拉取可能超过 30s）。
- **配置优先级**：`sandbox.idle_ttl_seconds` 生效顺序为 platform_setting 表（热加载，30s 缓存）→ config 文件 `opensandbox.idle_ttl_seconds` → 代码默认 28800；`sandbox.ttl_seconds`（硬 TTL 步长）经 platform_setting 热加载生效于新建/续约。

### 7.3 沙箱镜像

- 默认镜像：基于 `python:3.12` 预装 `nodejs, ripgrep, jq, curl, git, bash, coreutils` 的自定义镜像（部署文档说明构建方式；Dockerfile 存 `scripts/sandbox-image/`）。
- `platform_setting` 的 `sandbox.images` key 维护可选列表；Agent 编辑器下拉选择。

## 8. 上下文与取消

### 8.1 上下文管理（v1）

- derive 后估算 tokens（粗估：字符数/4 或按供应商 tokenizer 可后补；多模态消息的图片按视觉 token 折算——DeepSeek ≤512、ark high 1280 tok/图，取保守上沿 1280 计入，与 base64 体积无关；卸载后的占位文本按文本计）；超过 `context_size * 0.8` → **按 turn 整块丢弃**：从最早的用户轮次开始整轮（user 消息 + 后续 assistant/tool 消息）丢弃直到估算量达标；system 恒保留；单轮即超窗 → 返回错误事件建议用户开新会话。裁剪边界恒落在轮次边界，不随每轮新增内容逐条漂移（《缓存命中优先设计原则》§12 第 7 条）。
- M4：接 `adk/middlewares/summarization` 自动摘要（事件侧 `compaction/summary`）。

### 8.2 取消

```go
opt, cancel := adk.WithCancel()
iter := runner.Run(ctx, history, opt)
// Cancel(sessionID)：先 CancelAfterToolCalls（5s 超时）→ 降级 CancelImmediate
```

- 已产出的 assistant 前缀 finalize：`assistant/message{interrupted:true}`（含 usage 若有）。
- 悬空 tool_calls：derive 时合成 interrupted tool/result（protocol.md §6.4.3），不落库。
- 任何退出路径补 `turn/end`。

### 8.3 图片输入（多模态，internal/attachment + orchestrator 图片组装）

链路：`POST /sessions/{id}/attachments` 上传（魔数嗅探/8MB/64M 像素校验 → 内容寻址存储 + attachment 表）→ prompt 携带附件 id 引用（校验后写入 `user/message.attachments`，base64 不进事件）→ 每轮 history 组装时把引用投影为图片 part。

- **组装**（eino `UserInputMultiContent`，user 与 tool 角色可用；system 角色不支持多模态）：带附件的 user 消息构造 `schema.Message{Role: User, UserInputMultiContent: [...image parts, text part]}`——**图片在前、文本在后**（ark 官方建议多图时文字在后；固定顺序保证逐字节确定性）。image part 用 `Base64Data`+`MIMEType` 内联（DeepSeek/ark/qwen 三家唯一共同路径），字节从内容寻址存储读取并校验 sha256。
- **agent 主动读图（read_media）**：工具返回 `[text 信封, 本地 image 引用]`，事件与 checkpoint 不含图片字节；provider 请求边界上传并使用 file_id（§4.10）。
- **能力门控**：prompt 图片保留 vision 检查；PATCH 换到非视觉模型且历史含用户图片 → 409 `session/vision_locked`。存在工具图片且目标不满足原生工具图片要求 → 409 `session/media_locked`。read_media 的能力拒绝先于任何图片 I/O，主/子 Agent 独立判定。
- **卸载（offload），规则版本 `file-reference-v1`**：预算仍为 8 张、16MiB 等价 base64 字节，user/tool 共用；最旧优先，卸载张数向上取 2 的倍数，字节目标向上取 4MiB 的倍数。历史初始投影和每次 provider 请求均执行预算，覆盖轮内工具新增图片。原始事件不改写；占位给出 attachment_id 和重读提示。本地 `request/header.image_offload` 事件字段记录版本，供应商文件映射缓存的作用域也保留该版本，不向供应商发送自定义媒体策略 HTTP 请求头；版本变更可能影响前缀缓存，file_id 失效重建不承诺继续命中。
- **trimContext 计入图片**：卸载决策在裁剪之前；估算时保留图片按视觉 token 折算（1280 tok/图保守上沿，不按 base64 体积），卸载后的占位文本按文本计（§8.1）。
- **删除与 GC**：删除会话不删附件 blob 与表行——附件永不自动删除（fork 共享同一不可变对象），引用感知 GC 后续另做（data-model.md §10）。

## 9. 瞬态通道补充（tool-output live 帧）

exec 的流式 stdout 为增强体验，在 `tool/result` 落库前经瞬态帧推送（不落库）：

```jsonc
{ "type": "tool-output", "callId": "call_x", "stream": "stdout", "delta": "..." }
```

跟随在 `session.follow` 的下行消息序列中（与 event / assistant-stream 并列）。前端 terminal 卡片实时滚动；重连后该瞬态丢失由持久 `tool/result` 兜底。

## 10. 标题生成

- 触发：首个 `user/message` 提交后异步执行（独立小调用：用会话模型，prompt 模板"为以下对话生成不超过 20 字的中文标题"）。
- 成功且 `title_pinned=0` → append `session/title{auto:true}`；用户改名后 pinned，停止自动生成。
- 失败静默（保留空标题，前端显示"新会话"兜底）。

## 11. 用量回收

- 挂 eino 全局 callbacks handler：`OnEndWithStreamOutputFn` 读 `ResponseMeta.Usage`；每个 step 结束把 usage 暂存 turn 上下文。
- 缓存读/写字段以原始 usage 旁路抓取 + 按供应商归一化为准（§3.1），eino 侧 `CachedTokens` 作 fallback；`cache_reported` 标记供应商是否上报了缓存字段。
- `assistant/message` 提交时写入事件 `data.usage`；异步 worker 按事件日期 `INSERT ... ON DUPLICATE KEY UPDATE` 聚合到 `usage_daily`（子 Agent 用量记主 Agent，事件保留 `sub_agent_id`）。
- 中断/失败 attempt 的 usage 记入 `assistant/attempt` 事件但不聚合（口径：只计成功产出，报表页注明）。

## 12. 缓存命中优先设计原则

> 背景：DeepSeek / ark / qwen 的上下文缓存均按**输入 token 前缀**命中（system 在 messages[0]，其后任何一个字节变化都会让其后的全部历史前缀失配；tools 定义在 token 化时位于消息之前，同理）。下列原则是运行时与协议变更的评审基线；前缀命中率经缓存读 tokens 观测（各家 usage 字段口径见 §3.1）。

1. **前缀稳定第一**：进入 messages[0]（system）或历史消息前缀的任何内容，必须在会话生命周期内逐字节不变。
2. **system 是冻结区**：system 中禁止出现 `time.Now`、请求 ID、随机数、在线计数等逐轮/逐天变化值；必需的时间上下文用会话创建时刻（如"当前日期"取 `sess.CreatedAt`），或放首条 user 消息。
3. **会话快照即缓存契约**：会话创建时的 `agent_snapshot` 固定 system、tools、skills 索引；运行期配置变更只作用于新会话，此契约不可回退。
4. **工具定义与顺序恒定**：tools 列表用显式有序 slice 注册，禁止 map 迭代；工具 schema/description 变更视为前缀变更，需评审对存量会话的影响。
5. **动态内容一律后置**：时间戳、检索结果、易变状态只出现在消息尾部或工具结果中，永不进入 system 与历史前缀。
6. **历史只追加不改写**：历史由事件确定性重放（derive），已落库内容永不编辑/重排；中断产生的部分消息一旦落库即冻结。
7. **裁剪对齐边界**：上下文裁剪必须确定性、阶梯式——按 turn 整块丢弃（§8.1），禁止窗口起点随输入量逐条漂移；中长期以 compaction 摘要替换头部而非滑动窗口。
8. **子智能体前缀自治**：子智能体独立上下文，不追求与父会话共享前缀；其 system 与工具定义须稳定，以命中平台级公共前缀缓存。
9. **采样参数与缓存解耦**：`temperature/top_p`、`reasoning_effort` 等是请求级参数，不进消息前缀、不影响命中，只影响输出，纳入会话级配置稳定治理；接入 ark 显式缓存时保持 thinking/caching 字段逐轮一致。
10. **命中可观测**：所有模型调用记录 cache_read/cache_write tokens（字段口径按供应商归一化，§3.1），按会话/模型/供应商聚合；命中率突降视为回归。
11. **供应商能力路由**：ark 会话设置稳定 `prompt_cache_key`（取 sessionID，同 key ≤15 rpm，§3）；认知各供应商最小命中粒度（ark/qwen 默认 1024 tokens），前缀过短场景不预期命中；usage 按供应商归一化——各家缓存字段口径不同（§3.1），观测命中率须经适配器归一化并以 `cache_reported` 区分「未命中」与「未上报」，禁止把 `cached_tokens==0` 一律当未命中。
12. **合理失效显式化**：新建会话（新快照）、skills/子智能体/模型配置变更后的新会话、超窗裁剪或 compaction、跨供应商切换属合理失效，在此列举评审；此外的任何前缀变化视为缺陷。
13. **图片前缀自治**：图片字节来自不可变内容寻址对象，base64 天然逐轮一致；历史中的图片位置一旦提交永不移动/改写；多轮增长控制只走版本化的最旧优先 + 量子化卸载（§8.3），禁止滑动窗口式"仅最近 N 轮带图"（窗口每轮滑动 = 历史每轮都变 = 缓存全毁）。
