# Agent 运行时详细设计

> 本文是 `internal/runtime`、`internal/sandbox`、`internal/skill`、`internal/provider` 的权威设计。
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
func buildAgent(ctx context.Context, snap AgentSnapshot, deps Deps) (adk.Agent, error) {
    chatModel := deps.ProviderFactory.NewChatModel(ctx, snap.ModelID, snap.ModelParams) // §3
    tools := buildTools(ctx, snap, deps)                                               // §4
    instruction := renderInstruction(snap)                                             // system_prompt + Skills 索引段 + 运行时变量
    return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
        Name: snap.AgentID, Description: snap.Description,
        Instruction: instruction, Model: chatModel,
        ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}},
        MaxIterations: snap.MaxIterations,
    })
}
```

- `renderInstruction`：`agent.system_prompt` + 拼接"## 可用 Skills"索引段（每条 `- {name}: {description}`，提示"用 activate_skill 工具加载完整说明"）+ 末尾注入运行事实（当前日期、沙箱工作目录 `/workspace`、确认模式说明——strict 模式下提示模型"危险操作会被用户审查"）。
- 会话级 `model_id`（用户切换过）覆盖快照中的 model。

## 3. 供应商工厂（internal/provider）

```go
type ChatModelFactory interface {
    NewChatModel(ctx context.Context, modelID string, params map[string]any) (model.ToolCallingChatModel, error)
    Check(ctx context.Context, providerID string) error   // 连通性测试
}
```

- 分派：`deepseek|openai_compat` → `eino-ext/components/model/openai`（BaseURL+APIKey）；`qwen` → `eino-ext/components/model/qwen`；`ark` → `eino-ext/components/model/ark`。
- 密钥解密仅在工厂内部（data-model.md §5 加密约定）；ChatModel 实例按 `(model_id, hash(params))` sync.Map 缓存。
- 参数映射：`temperature/max_tokens/top_p` → 各组件 Option；`capabilities.reasoning=true` 且组件支持时开启 reasoning 输出。
- M2 首周 spike：三家供应商真实连通（工具调用 + 流式 + usage 回收），择优固化；若官方组件缺陷，DeepSeek/千问退化到 openai 兼容端点。

## 4. 工具规格（模型可见 schema）

> 全部用 `utils.InferTool` 实现。模型侧只暴露 name/description/parameters。每个工具有：风险等级（审批）、`render` 词汇（UI 卡片）、超时。
> 沙箱工具从 ctx 取 `SandboxHandle`（首次调用惰性创建，§7）；工作目录默认 `/workspace`。

### 4.1 exec（风险 high，render terminal）

```jsonc
// name: exec —— 在沙箱中执行 shell 命令（bash -lc），返回退出码与输出
{ "command": "string（必填）",
  "cwd": "string，默认 /workspace",
  "timeout_ms": "int，默认 30000，上限 600000",
  "env": "object<string,string>，附加环境变量" }
// 返回：{ "exit_code": 0, "stdout": "...", "stderr": "...", "truncated": false }
```

实现：execd `POST /command`（`background:false`，SSE 收集流式输出；流式过程同时以 transient 帧推前端 terminal 卡片实时滚动——作为 `tool/result.meta` 之外的 live 通道，协议上用 assistant-stream 之外的 `tool-output` 瞬态帧，见 §9）。输出 >64KB 截断（保留尾部，`truncated:true`）。

### 4.2 read（风险 low，render read）

```jsonc
// name: read —— 读取沙箱内文本文件（带行号），支持分页
{ "path": "string（必填，沙箱内绝对路径）",
  "offset": "int，起始行（1-based），默认 1",
  "limit": "int，最多返回行数，默认 200，上限 2000" }
// 返回：{ "path", "content": "1\tline...", "total_lines": N, "truncated": bool }
```

实现：execd `GET /files/download` 拉取（>2MB 拒绝并提示用 exec 分段）→ 服务端切片加行号。非 utf8 内容返回错误提示（建议 exec `xxd`）。

### 4.3 write（风险 medium，render diff）

```jsonc
// name: write —— 写入文件（覆盖式），自动创建父目录
{ "path": "string（必填）", "content": "string（必填，完整内容）" }
// 返回：{ "path", "bytes": N }
```

实现：execd `POST /files/upload`；已存在同名文件时先 download 旧内容计算 unified diff 放入 `meta.diff`（供 UI diff 卡片）。

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

实现：execd `POST /command` 执行 `rg --line-number --no-heading [--ignore-case] [-g glob] -- pattern path`（`files_with_matches` 加 `-l`）；沙箱镜像保证预装 `rg`。

### 4.7 activate_skill（风险 low，render generic）

```jsonc
// name: activate_skill —— 加载指定 Skill 的完整使用说明（SKILL.md 全文 + 包内文件清单）
{ "name": "string（必填，Skills 索引段中的 name）" }
// 返回：{ "name", "instructions": "<SKILL.md markdown 全文>",
//         "files": ["scripts/a.py", ...], "sandbox_dir": "/skills/<name>/" }
```

实现：读平台磁盘 `<data_dir>/skills/<org>/<id>/SKILL.md`（不进沙箱）；SKILL.md >32KB 截断并提示用 read 工具读包内具体文件。

### 4.8 task（风险继承子 Agent，render generic，v1=M3）

```jsonc
// name: task —— 把自包含任务委派给指定子智能体。子智能体在独立上下文中执行
//（看不到本会话历史），与你共享同一个沙箱文件系统，完成后返回最终结果（不含中间步骤）。
{ "subagent": "string（必填，可用子智能体名，见工具描述枚举）",
  "prompt": "string（必填，完整自包含的任务描述——子智能体看不到本会话，必须包含全部必要信息）" }
// 返回：{ "output": "子智能体最终答复文本" }
// 失败：error = "<失败原因>\nDiagnostic: ...\nPartial output: ..."（部分输出仍返回给主 Agent）
```

工具描述（运行时按挂载子 Agent 动态生成）：

```txt
Delegate a self-contained task to a subagent. The subagent works in its own context
(it does NOT see this conversation) and shares your sandbox filesystem. It returns
only its final result. Available subagents:
- data-analyst: 数据分析与可视化，擅长 pandas/matplotlib
- web-researcher: 联网检索与资料综述
Use this when a subtask is focused and independent; include everything the subagent needs in `prompt`.
```

## 5. 审批实现（internal/runtime/approval）

### 5.1 拦截点与状态机

eino `ToolsConfig.ToolCallMiddlewares` 注册审批中间件（在工具执行前）：

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
| read / glob / grep / activate_skill / task | 免 | 免 | 免 |

- `grep` 虽走 exec 通道，按工具自身 low 风险归类（实现通道不影响风险语义）。
- 子 Agent 内的工具调用继承同一会话的 approval_mode（嵌套 asked/decided 带 `parent_call_id`）。

## 6. task 委派实现（M3）

```go
func NewTaskTool(snap AgentSnapshot, deps Deps) tool.InvokableTool // snap.SubAgents 为空则不注册
```

执行流程：

1. 校验 `subagent` 在快照的可委派清单（名称大小写不敏感匹配）。
2. 构建子 Agent：子 Agent 快照（创建会话时随 agent_snapshot 固化）+ **共享 ctx 中的 SandboxHandle** + `depth=1`（子 Agent 不注册 task 工具）。
3. 独立上下文运行：`adk.Runner.Run(ctx, []Message{UserMessage(prompt)})`——**不带主会话历史**。
4. 事件嵌套：消费子 AgentEvent 流，翻译为会话事件时全部附加 `parent_call_id=<本次 task 的 call_id>`（含 turn/step 用子序列 `sub_turn`/`sub_step` 字段避免与主冲突：data 内用 `turn:0, sub_turn:N` 表达，前端按 parent_call_id 聚合）。
5. 结果合成：取子 Agent 最终 assistant 消息文本作为 `output`；`stopReason != completed` 时按"错误标题 + Diagnostic + Partial output"合成 error（参考 §4.8 返回约定）。
6. 主 Agent 视角：一次普通 tool/call(task) + tool/result；审批在子 Agent 内部独立触发（嵌套事件）。
7. 并发：v1 只允许主 Agent 同时发起的多个 task 串行执行（eino ToolsNode 并行调用时由 task 工具内互斥锁串行化，避免沙箱文件竞态；v1.1 再评估并行）。

## 7. 沙箱管理器（internal/sandbox）

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
{ "image": {"uri": agent.sandbox_config.image || settings.default_image},
  "timeout": settings.sandbox_ttl_seconds,
  "resourceLimits": agent.sandbox_config.resources || settings.resource_limits,  // {"cpu":"1","memory":"1Gi"}
  "networkPolicy": settings.egress_policy }   // 平台统一黑白名单
```

创建成功后：
1. `GET /sandboxes/{id}/endpoints/44772` 解析 execd 地址 → 构造 ExecdClient。
2. 上传会话激活 Skills：`/skills/<name>/` 整目录（execd upload）。REST 沙箱面板端点（`GET /sessions/{id}/sandbox/file(s)`）在拿到新建/重建沙箱时同样做一次同步（按 sessionID→sandboxID 去重），保证服务重启句柄丢失后技能目录一致。
3. 更新 `session.sandbox` 为 `{"id": <沙箱ID>, "status": "running"}` + append `session/sandbox{action:"created"}`。

### 7.2 续约与回收

- **续约**：会话活动（每次事件写入）刷新内存 lastActive；后台任务对运行中且 `now-lastActive < ttl/2` 的沙箱调 `renew-expiration`。
- **空闲回收**：reaper 每分钟扫描，`now-lastActive > idle_ttl_seconds` → DELETE 沙箱 → `session.sandbox.status="recycled"` + append `session/sandbox{action:"recycled"}`。再次调用沙箱工具 → 重新创建（文件不保留，新事件使轨迹自洽）。
- **兜底**：启动时与每小时对比 lifecycle `GET /sandboxes` 列表与库内记录，孤儿沙箱强制删除。
- **失败**：创建失败置 `session.sandbox.status="failed"` + 事件，工具调用返回"沙箱不可用"错误给模型（可重试一次）。创建路径自带一次退避重试（2s），lifecycle 请求超时 120s（冷启动镜像拉取可能超过 30s）。

### 7.3 沙箱镜像

- 默认镜像：基于 `python:3.12` 预装 `nodejs, ripgrep, jq, curl, git, bash, coreutils` 的自定义镜像（部署文档说明构建方式；Dockerfile 存 `scripts/sandbox-image/`）。
- `platform_setting` 的 `sandbox.images` key 维护可选列表；Agent 编辑器下拉选择。

## 8. 上下文与取消

### 8.1 上下文管理（v1）

- derive 后估算 tokens（粗估：字符数/4 或按供应商 tokenizer 可后补）；超过 `context_size * 0.8` → 从最老非 system 消息起丢弃，**tool 消息与其 assistant tool-call 块成对处理**；仍超 → 返回错误事件建议用户开新会话。
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
- `assistant/message` 提交时写入事件 `data.usage`；异步 worker 按事件日期 `INSERT ... ON DUPLICATE KEY UPDATE` 聚合到 `usage_daily`（子 Agent 用量记主 Agent，事件保留 `sub_agent_id`）。
- 中断/失败 attempt 的 usage 记入 `assistant/attempt` 事件但不聚合（口径：只计成功产出，报表页注明）。
