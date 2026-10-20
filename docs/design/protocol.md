# 实时协议（WebSocket mux）

> 本文是会话实时通讯的权威协议定义，前后端都以此为准。
> 设计来源：借鉴 DeepSeek Harness 的 stream-protocol / session follow / StreamChunk / SessionEvent 设计，适配多租户与 MySQL 事件表。

## 1. 连接与鉴权

- 端点：`GET /api/ws?token=<jwt>` → 101 升级（gorilla/websocket）。
- 升级前校验：JWT 有效；Origin 与 Host 匹配（同源部署）或在配置的允许列表内。
- 心跳：服务端每 30s 发 WS ping；连续 2 次未收到 pong 主动断开。
- 一个用户可有多条物理连接（多标签页）；逻辑流互不影响。
- 每条逻辑流 `open` 时独立鉴权（机构成员关系 + 资源归属）；鉴权失败回 `error` 帧并 `end`。

## 2. mux 帧协议

单条物理连接上按 `streamId` 多路复用任意多条逻辑流。帧为 JSON 文本帧。

### 2.1 Client → Server

```jsonc
// 打开逻辑流。streamId 由客户端生成（uuid），endpoint 见 §3
{ "type": "open", "streamId": "s1", "endpoint": "session.follow",
  "payload": { "orgId": "...", "sessionId": "...", "afterSeq": 0 } }

// uplink 消息（逻辑流内的上行数据，见各 endpoint 定义）
{ "type": "item", "streamId": "s1", "value": { "type": "approval-response", ... } }

// 正常关闭 / 中止逻辑流（仅关闭该流，不影响连接与其他流）
{ "type": "end",    "streamId": "s1" }
{ "type": "cancel", "streamId": "s1" }
```

### 2.2 Server → Client

```jsonc
{ "type": "item",  "streamId": "s1", "value": { ... } }   // 逻辑流下行消息（见各 endpoint）
{ "type": "error", "streamId": "s1",
  "error": { "code": "session/not_found", "message": "...", "details": {} } }
{ "type": "end",   "streamId": "s1" }                      // 服务端关闭该流
```

错误码复用 api.md §1.2 的词表。

## 3. 逻辑流端点

| endpoint | 方向 | payload / 用途 |
|---|---|---|
| `session.follow` | 下行 + uplink | 订阅单会话：快照 → 持久事件帧 → 模型增量帧；uplink 承载审批应答 |
| `control` | 下行 | 当前用户在指定机构下全部会话的投影增量（侧栏数据源）。payload `{ "orgId": "..." }` |

## 4. `session.follow`

### 4.1 open payload

```jsonc
{ "orgId": "...", "sessionId": "...", "afterSeq": 0 }
```

- `afterSeq`：客户端本地已持有的最大事件 seq。首开后一般为 0（要完整尾部窗口）；重连后填本地 lastSeq（服务端仍发快照；v1 实现中 records 始终为尾部窗口、不按 afterSeq 裁剪——客户端本就原子替换窗口，裁剪仅为带宽优化，见 §8.2）。

### 4.2 首帧：snapshot（永远第一帧，重连同语义）

```jsonc
{ "type": "snapshot",
  "header": {
    "sessionId": "...", "title": "...", "titlePinned": false,
    "agentId": "...", "agentName": "...", "agentAvatar": "",
    "modelId": "...", "approvalMode": "default",
    "sandboxStatus": "running", "running": true,
    "modelSettingsLocked": true, "lastSeq": 87, "usageTotal": { "input_tokens": 1234, "output_tokens": 567,
      "cache_read_tokens": 0, "cache_write_tokens": 0, "reasoning_tokens": 0 },
    "createdAt": "..." },
  "records": [ /* SessionEvent[]，seq 升序的尾部窗口（默认最近 200 条或 afterSeq 之后全部） */ ],
  "hasMore": true,                 // 更老历史用 REST GET /sessions/{id}/events 向前翻页
  "assistantStream": {             // 仅当存在进行中的流式 attempt 时给出
    "attemptId": "...", "turn": 2, "step": 3, "startedAfterSeq": 84,
    "compactChunks": [ /* 该 attempt 已积累的 StreamChunk[]（可能被压缩合并，保序） */ ],
    "nextIndex": 137 } }
```

客户端语义：**用快照原子替换本地会话窗口**，再按序应用后续帧。

### 4.3 持久事件帧

```jsonc
{ "type": "event",
  "event": { "seq": 88, "type": "tool/call", "time": 1735000000000,
             "data": { "call_id": "call_x", "name": "exec",
                       "arguments": "{\"command\":\"ls\"}", "turn": 2, "step": 3 } } }
```

- 按 seq 严格递增推送；客户端发现跳号 → 用 REST `events?after_seq=` 补齐（§8.3）。
- 事件词表与 data schema 见 §6。

### 4.4 模型增量帧（assistant-stream，瞬态，不落库）

```jsonc
{ "type": "assistant-stream", "frame":
  { "type": "start", "attemptId": "...", "turn": 2, "step": 3, "startedAfterSeq": 84 } }

{ "type": "assistant-stream", "frame":
  { "type": "chunk", "attemptId": "...", "index": 137, "time": 1735000000000,
    "chunk": { "type": "text-delta", "index": 0, "text": "你好" } } }

{ "type": "assistant-stream", "frame":
  { "type": "end", "attemptId": "...",
    "outcome": { "kind": "committed", "eventType": "assistant/message", "seq": 90 } } }
```

- `index`（chunk 序号）在 attempt 内从 0 严格递增，用于去重与排序；重连快照中的 `nextIndex` 之后的才是新 chunk。
- `outcome.kind`：`committed`（已落库为 seq=N 的持久事件，客户端可丢弃对应瞬态窗口）/ `abandoned`（失败或取消，落库为 `assistant/attempt`，不进模型历史）。

### 4.5 StreamChunk 词表（chunk 字段）

```jsonc
{ "type": "block-start",     "index": 0, "blockType": "text" }        // text | reasoning | tool-call
{ "type": "text-delta",      "index": 0, "text": "..." }
{ "type": "reasoning-delta", "index": 1, "text": "..." }
{ "type": "tool-call-delta", "index": 2, "id": "call_abc",
  "name": "exec",                       // 首个 delta 必带 id+name，后续仅 argumentsDelta
  "argumentsDelta": "{\"command\":\"ls -" }   // 原始 JSON 字符串增量
{ "type": "block-end",       "index": 0, "block": { ... } }           // 组装好的完整块，前端免重组；
                                                          // reasoning 块带 durationMs（思考首帧→末帧耗时）
{ "type": "usage",           "usage": { "inputTokens": 10, "outputTokens": 20,
  "cacheReadTokens": 0, "cacheWriteTokens": 0, "reasoningTokens": 0,
  "cacheReported": true } }   // cacheReported 可缺省：供应商是否上报缓存字段（runtime.md §3.1）
{ "type": "finish",          "reason": "stop" }  // stop | tool-calls | max-tokens | aborted | error
```

块级语义：同一 step 内可交错多个块（`block.index` 关联）；`block-end` 给出该块的完整组装结果——**前端无需自行拼接 delta**（回放与直播渲染一致的关键）。tool-call 块组装后等价于 eino `schema.ToolCall{ID, Name, Arguments}`。

### 4.6 uplink（session.follow 上行消息）

```jsonc
// 审批应答（也可走 REST POST /sessions/{id}/approvals/{callId}，两者等价，服务端去重）
{ "type": "approval-response", "callId": "call_x", "decision": "allow" }  // allow | deny
```

收到非法 uplink 回 error 帧但不断流。

## 5. `control` 流

open payload `{ "orgId": "..." }`。首帧基线 + 增量：

```jsonc
{ "type": "baseline", "sessions": [
  { "sessionId": "...", "title": "...", "running": true, "agentId": "...",
    "usageTotal": {...}, "lastActiveAt": "...", "status": "active" } ] }

{ "type": "upsert", "session": { /* 同上结构，任一字段变化即推 */ } }
{ "type": "remove", "sessionId": "..." }
```

- 触发源：本连接用户在该机构下任何会话的事件提交（标题变化、running 翻转、用量增长、last_active 更新、归档/删除）。
- 这是侧栏"其他标签页/设备也在跑"状态同步的通道；`session.follow` 负责单会话细节，`control` 负责列表投影。

## 6. 会话事件词表（session_event.type → data schema）

> 所有事件公共字段（表列）：`seq`、`type`、`time_ms`（协议帧中为 `time`）、`data`。
> `turn`/`step` 从 1 开始；`parent_call_id` 仅子 Agent 嵌套事件携带（见 runtime.md §6）。

### 6.1 边界事件

| type | data | 说明 |
|---|---|---|
| `turn/start` | `{turn, request_id}` | 一次 prompt 开始 |
| `turn/end` | `{turn, reason: "completed"\|"interrupted"\|"failed", error?}` | 任何路径保证有收尾 |
| `step/start` | `{turn, step}` | 一次模型请求开始 |
| `step/end` | `{turn, step}` | |

### 6.2 消息事件

| type | data |
|---|---|
| `user/message` | `{turn, content, request_id, attachments?, source?}`。`attachments` 为图片附件引用数组（向后兼容，老事件无此字段）：`[{id: "sha256:<64 hex>", mime, bytes, width, height, name}]`——不透明引用，绝无 base64/路径/URL；字节经 `GET /attachments/{id}` 获取（api.md §3）。`source` 仅合成消息携带（普通用户消息无此字段）：`{kind:"question-reply", call_id}` 表示迟到提问答复（content 为 JSON `{"kind":"answer_to_pending_question","call_id","questions","answers"}`，见 §10） |
| `assistant/message` | `{turn, step, message, usage?, interrupted?}`。`message` 为完整 assistant 消息：`{role:"assistant", content:[ContentBlock...]}`，ContentBlock = `{type:"text",text}` / `{type:"reasoning",text,durationMs?}`（`durationMs` 为思考首帧到末帧的耗时，流式窗口埋点，可缺省）/ `{type:"tool-call",id,name,arguments}`。`usage` 结构同 StreamChunk usage（snake_case），含可选 `cache_reported`（供应商是否上报缓存字段，false/缺省 = 未上报，区别于命中为 0；runtime.md §3.1） |
| `assistant/attempt` | `{turn, step, chunks:[StreamChunk...]（压缩存储）, error?}` —— 失败/取消的尝试留痕，**不进模型历史** |
| `tool/call` | `{turn, step, call_id, name, arguments(原始JSON字符串), parent_call_id?}` |
| `tool/result` | `{turn, step, call_id, name, content(string), error?, meta?, attachments?, parent_call_id?}`。`meta` 含 `{duration_ms, render:"terminal"\|"diff"\|"search"\|"read"\|"generic", truncated?, sandbox_id?, diff?, operation?:"create"\|"update"}`；`diff` 为 unified diff，`operation` 仅用于成功 write 的创建/覆盖标记，探测未知时省略。`attachments` 为工具产出图片的引用数组（read_media；结构与 `user/message.attachments` 相同——不透明引用，绝无 base64，字节经 `GET /attachments/{id}` 获取） |

### 6.3 会话事件

| type | data |
|---|---|
| `session/title` | `{title, auto: true\|false}`（latest-wins；用户改名 auto=false 且 pinned） |
| `session/sandbox` | `{action: "created"\|"paused"\|"resumed"\|"recycled"\|"failed", sandbox_id, image, error?}`（paused=空闲暂停/重启 adopt，resumed=恢复使用；会话 `sandboxStatus` 对应 `running\|paused\|recycled\|failed`） |
| `session/approval-mode` | `{mode: "strict"\|"default"\|"auto", changed_by}` |
| `session/effort` | `{effort: "<档位>", changed_by}` —— 会话思考强度改档（仅 `last_seq = 0` 时可改，见 api.md §3） |
| `model/selection` | `{model_id, changed_by}` |
| `request/header` | `{turn, provider_id, model_id, model_code, params, tools:[string], image_offload?}` —— 本 turn 请求快照（审计/复现用）；`image_offload` 为图片卸载策略版本（本 turn 携带附件时记录，如 `image-offload-v1`，见 runtime.md §8.3） |
| `approval/asked` | `{turn, step, call_id, name, arguments, risk: "high"\|"medium", parent_call_id?}` |
| `approval/decided` | `{call_id, decision: "allow"\|"deny", decided_by, auto: false}` —— 超时/重启 fail-closed 的 deny 记 `auto:true, decided_by:"system"` |
| `question/asked` | `{turn, step, call_id, questions:[{id, question, header?, options?:[{label, description?}], multi_select?}]}` —— question 工具提问（问题文本显式落库；等待/continued 语义见 runtime.md §5.3） |
| `question/answered` | `{call_id, answers:[{id, selected:[label...], custom?}], answered_by}` —— open 作答由等待中的工具落库（恒在 asked 之后）；continued 迟到答复由 REST 落库 |
| `compaction/summary`（M4 预留） | `{covers: {start_seq, end_seq}, summary}` —— derive 时替换该区间的消息（surface replace 语义） |

### 6.4 deriveMessages()（事件 → 模型历史的确定性规则）

输入：`seq ≤ N` 的全部事件。输出：`[]*schema.Message`。

1. 取最后一个 `turn/start` 之前的全部事件（进行中的 turn 不入历史）。
2. 按 seq 顺序： `user/message` → user 消息（带 `attachments` 时投影附件引用，模型侧组装为图片+文本多模态消息，见 runtime.md §8.3）；`assistant/message` → assistant 消息（content 含 tool-call 块则展开为 `ToolCalls`）；`tool/result` → tool 消息（`ToolCallID=call_id`，name 从配对 `tool/call` 取，error 时 content 为错误描述；带 `attachments` 时同样投影附件引用，模型侧组装为 `[text 信封, image]` 多模态 tool 消息，见 runtime.md §4.10）。
3. 悬空配对保护：`assistant/message` 中 tool-call 块缺少对应 `tool/result`（中断场景）→ 合成 `tool/result{error:"interrupted"}` 保证消息序列合法（模型 API 要求每个 tool_call 有响应）。
4. 子 Agent 嵌套事件（带 `parent_call_id`）不进入主消息流——它们在主 Agent 视角只体现为一次 `task` 工具的 call/result。
5. `compaction/summary`（M4）：命中区间内的消息整体替换为一条 user 角色的摘要消息。

## 7. 审批时序（协议视角）

```txt
Agent 请求 exec → runtime 拦截（runtime.md §5）
  → append approval/asked（持久帧广播）
  → turn 暂停（eino interrupt + checkpoint）
  → follow 流上所有订阅者看到审批卡片（前端同时收到瞬时帧提示）
用户决定 → REST 或 uplink approval-response
  → append approval/decided（广播） → Runner.ResumeWithParams
  → allow：执行工具 → tool/call + tool/result 正常落库
  → deny：不落 tool/call 执行，直接落 tool/result{error:"denied by user"} 回喂模型
超时 10 分钟 / 服务重启 → 系统自动补 approval/decided{decision:"deny", auto:true}
```

## 8. 断线重连与乐观对账

### 8.1 物理重连

客户端指数退避（1s→2s→…→max 30s，抖动 ±20%）重连 `/api/ws`，成功后对每条逻辑流重新 open。

### 8.2 follow 重开

`afterSeq` 填本地 lastSeq → 服务端发 snapshot（records 为最新尾部窗口，不裁剪）→ 客户端**原子替换**本地窗口 → 若有进行中 attempt，从 `compactChunks + nextIndex` 继续追加 chunk。渲染结果与从未断线一致。

### 8.3 间隙修复

`event` 帧 seq ≠ 本地 lastSeq+1 → 丢弃该帧，调 REST `GET /sessions/{id}/events?after_seq=<local>` 批量补齐后恢复正常消费。

### 8.4 乐观对账

1. 用户发送 prompt → 前端立即渲染乐观气泡（状态"发送中"），携带客户端生成的 `request_id`。
2. 服务端把 `request_id` 写入 `user/message` 事件；follow 事件帧到达后前端按 `request_id` 把乐观气泡替换为持久消息。
3. 重复投递（双击/重试）：服务端发现同 `(session_id, request_id)` 已存在 → 202 返回已存在 seq，不产生新事件。

## 9. 与 eino 事件流的映射（后端实现指引）

| eino adk AgentEvent / 状态 | 协议产物 |
|---|---|
| `Runner.Run` 开始一个 turn | `turn/start` + `request/header` |
| `MessageOutput{MessageStream, Role:assistant}` 每个 chunk | `assistant-stream.chunk`（text/reasoning/tool-call delta、block-start/end；含 usage 时发 usage chunk） |
| `MessageOutput{Message, Role:assistant}` 完整消息（无 tool calls） | `assistant/message` + `turn/end{completed}` |
| assistant 消息含 `ToolCalls` | `assistant/message`（content 含 tool-call 块）+ 每个 call 落 `tool/call` |
| `MessageOutput{Role:tool, ToolName}` | `tool/result`（meta.render 按工具映射） |
| `Action.Interrupted`（审批） | `approval/asked` + checkpoint；Resume 后 `approval/decided` |
| 一个 step（模型请求）开始/结束 | `step/start` / `step/end` |
| `Err` | `assistant/attempt` + `turn/end{failed}` |
| ctx cancel（用户停止） | 已产出前缀 finalize 为 `assistant/message{interrupted:true}` + `turn/end{interrupted}` |

事件顺序约束：先广播瞬态帧再提交持久事件；持久事件提交（DB 事务成功）后才广播 `event` 帧——保证"模型可见 ⟺ 已落库"不变量（system-design.md §4.3）。

## 10. 提问时序（协议视角）

```txt
Agent 调用 question → append question/asked（持久帧广播，问题文本显式落库）
  → 工具前台等待（内存 channel，默认 10 分钟；不经过审批 interrupt）
窗口内用户作答 → REST POST /sessions/{id}/questions/{callId}/answer
  → 投递内存等待 → 工具 append question/answered → 答案作为 tool/result 正常落库，turn 继续
超时 → tool/result 内容为 {"pending":true,"call_id","message"}，turn 正常继续；
  问题无 question/answered，投影上即 continued（open/continued/settled 全集由
  question/asked、question/answered、tool/result{pending} 折叠推导，重启零成本恢复）
迟到答复（continued）→ 同一 REST 端点
  → append question/answered + 合成 user/message{source:{kind:"question-reply",call_id}}
    （正文 JSON {"kind":"answer_to_pending_question","call_id","questions","answers"}）
  → 开启新 turn（复用 Prompt 管线；合成 user/message 照常进模型历史）
```

迟到答复不新增事件类型：settled 判定即"asked 存在且 answered 存在"。turn 取消/中断/重启后未答的提问保持可补答（runtime.md §5.3）。

`session.follow` 快照头 `modelSettingsLocked` 按完整历史是否存在 `user/message` 计算；首条消息被接受后为 true，历史分页、重连及分叉不解除。客户端收到 `user/message` 同步置 true，配置事件不触发锁定。
