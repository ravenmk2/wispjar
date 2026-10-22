# REST API 规格

> 用户云电脑资源 API 见本文 §8；文件与会话接入按 [computers.md §8](computers.md#8-rest-契约) 的 T4 实施。创建会话将增加 computer_id，旧 sandbox/file(s) 保留兼容代理；下文旧会话独占沙箱行为不适用于已绑定电脑的会话。

> 全部端点以 `/api/v1` 为前缀（WS 实时协议 `/api/ws` 见 protocol.md）。
> 认证：除 `/api/v1/auth/login` 外全部需要 `Authorization: Bearer <jwt>`。
> 机构上下文：机构域端点路径内带 `orgId`；中间件校验当前用户是该机构成员并注入 `(uid, org_id, role)`。

## 1. 通用约定

### 1.1 响应信封

- 成功：HTTP 200/201，body 为资源 JSON 或列表 `{ "items": [...], "next_cursor": "..." }`（`next_cursor` 为空串表示没有更多）。
- 失败：统一信封，HTTP 状态语义化：

```json
{ "error": { "code": "session/not_found", "message": "会话不存在或无权访问", "details": {} } }
```

### 1.2 错误码（命名空间/具体码）

| HTTP | code | 场景 |
|---|---|---|
| 400 | `request/invalid` | 参数校验失败（details 含字段级错误） |
| 401 | `auth/unauthorized` | 未登录或 token 失效 |
| 401 | `auth/credentials` | 用户名或密码错误 |
| 401 | `auth/password_change_required` | 首次登录需先改密 |
| 403 | `auth/forbidden` | 已登录但角色不足（details.required 说明需要的角色） |
| 403 | `tenant/not_member` | 不是该机构成员 |
| 404 | `*/not_found` | 资源不存在或无权访问（不区分，防枚举） |
| 409 | `*/conflict` | 唯一性冲突（username、skill name 等） |
| 409 | `session/running` | 会话有进行中的 turn，冲突操作（如重复 prompt 未走队列） |
| 409 | `session/effort_locked` | 首条用户消息已被接受，推理等级不可再改（details 含当前值） |
| 409 | `approval/not_pending` | 审批已决定或已过期 |
| 410 | `approval/expired` | 审批超时 |
| 404 | `question/not_found` | 提问不存在（无对应 `question/asked`） |
| 409 | `question/not_pending` | 提问已作答 |
| 400 | `question/bad_answer` | 提问答案校验失败（每题恰好一条；details.reason 含原因） |
| 409 | `session/model_locked` | 首条用户消息已被接受，不能修改模型 |
| 409 | `session/config_changed` | 首条消息提交期间配置发生变化，刷新配置后重新发送 |
| 409 | `session/vision_locked` | 会话历史包含图片，不能切换到非视觉模型（PATCH model_id） |
| 422 | `model/not_vision` | 带图 prompt 命中不支持视觉的模型，需换模型 |
| 404 | `attachment/not_found` | 附件不存在或无权访问（不区分，防枚举） |
| 422 | `attachment/invalid` | 图片校验失败（格式/大小/像素，details 含原因） |
| 422 | `skill/invalid` | skill zip 校验失败（details 含原因） |
| 429 | `auth/rate_limited` | 登录尝试过多 |
| 502 | `provider/upstream` | 调用模型供应商失败 |
| 502 | `sandbox/upstream` | 调用 OpenSandbox 失败 |
| 500 | `internal` | 未分类异常（details.request_id 供排查） |

### 1.3 分页与排序

- 游标分页：`?cursor=<上一页最后一条的 id>&limit=<1..100，默认 20>`；按 id（UUIDv7 时序）倒序，即最新在前。会话列表按 `last_active_at` 倒序时用 `(last_active_at, id)` 复合游标（`cursor=<last_active_ms>_<id>`）。
- 校验失败返回 `request/invalid`。

### 1.4 权限记号

- `member+`：该机构任意成员；`admin`：该机构 admin；`platform`：平台管理机构 admin（超管）；`owner`：资源属主本人。

---

## 2. 认证

### POST /api/v1/auth/login

请求：`{ "username": "...", "password": "..." }`
响应：

```json
{ "token": "<jwt>", "user": { "id", "username", "display_name", "must_change_password": false },
  "orgs": [ { "id", "name", "role": "admin|member", "is_platform": false } ] }
```

- JWT：HS256，claims `{sub: user_id, exp, iat}`，有效期 24h（配置 `auth.token_ttl`）。
- 限流：同一 IP+username 5 次失败锁 5 分钟（内存计数器）。
- `must_change_password=true` 时仅放行 `POST /auth/change-password`。

### POST /api/v1/auth/change-password

请求：`{ "old_password", "new_password" }`（新密码 ≥8 位，含字母+数字）→ `{ "ok": true }`，同时使旧 token 失效（签发新 token 返回）。

### GET /api/v1/auth/me

响应同 login 的 `user + orgs`（每次机构变更后前端刷新）。

---

## 3. 会话域（`/api/v1/orgs/{orgId}/sessions`）

> 全部 `member+`；会话级操作再校验 `owner`（会话仅本人可见，决策 #9）。

### POST /api/v1/orgs/{orgId}/sessions

创建会话。请求：

```json
{ "agent_id": "...", "model_id": "可选，缺省使用智能体模型", "title": "可选，默认空（首条 prompt 后自动生成）", "reasoning_effort": "可选，思考强度档位" }
```

- 校验 agent 属于该机构且 `status=published`。
- `model_id` 可选，必须为已上架（active）模型；选择与推理档位在创建时一并保存。
- `reasoning_effort` 必须在目标模型 `capabilities.reasoning_efforts` 内，否则 400；缺省 = 模型的 `reasoning_effort_default`，模型未配默认则不注入。生效值写入 `session.model_params`（如 `{"reasoning_effort":"high"}`）。
- 服务端：写 `session`（`agent_snapshot` 为创建时刻 Agent 全量配置 + 解析后的 skills/subagents 清单；`model_id` 使用所选模型，缺省 `agent.model_id`）→ 201 返回会话对象。

### GET /api/v1/orgs/{orgId}/sessions

列表（本人）。query：`cursor/limit`、`q`（标题模糊）、`status=active|archived`（默认 active）、`agent_id`（可选过滤）。
响应 `{items:[{id, agent_id, agent_name, title, running, approval_mode, model_id, usage_total, last_active_at, created_at}], next_cursor}`。

### GET /api/v1/sessions/{id}

会话详情（owner）：会话对象全字段（含 `model_params`）+ `agent`（快照中的名称/描述/头像）+ 派生 `model_settings_locked`（完整历史存在 `user/message` 时为 true，模型及推理等级已锁定），`effort_locked` 保留同义兼容。

### PATCH /api/v1/sessions/{id}

请求（均可选）：`{ "title": "...(改名并 title_pinned=1)", "approval_mode": "strict|default|auto", "status": "active|archived", "model_id": "...(会话级切换模型)", "reasoning_effort": "...(思考强度档位)" }`
- 改 `approval_mode` 追加 `session/approval-mode` 事件；改 `model_id` 追加 `model/selection` 事件；改 `reasoning_effort` 追加 `session/effort` 事件（事件词表见 protocol.md §6）。
- `model_id` 与 `reasoning_effort` 仅首条 `user/message` 被服务端接受前可设置；锁定判断与配置更新使用同一会话行锁事务，与消息追加互斥。开始后改模型返回 409 `session/model_locked`；改档位（含重置默认）返回 409 `session/effort_locked`，details 带当前值。标题、审批及其它配置事件不触发锁定。历史及包含用户消息的分叉会话保持锁定；详情返回 `model_settings_locked`，`effort_locked` 保留同义兼容。首条消息提交时若配置读取后事件版本变化，返回 409 `session/config_changed`，不追加消息。
- 改 `model_id`：同时重置为新模型默认推理档位，显式传 `reasoning_effort` 则以显式值为准；若目标模型 `capabilities.vision=false` 且会话历史已含图片附件（任一 `user/message` 带 `attachments`），返回 409 `session/vision_locked`（否则历史投影要从图片变占位文本，前缀从此处失效）。
- `reasoning_effort` 值必须在目标模型 `capabilities.reasoning_efforts` 内，否则 400（同请求改 `model_id` 时按新模型校验）。
- 显式传 `reasoning_effort: ""` 表示恢复默认：目标模型配置了默认档则恢复该档，否则移除会话级 `reasoning_effort` 覆盖，按既有 Agent/模型参数及提供方默认规则执行；保留其它 `model_params`，空对象写为 NULL。重置同样受锁档规则约束，省略字段表示不修改。

### DELETE /api/v1/sessions/{id}

软删（`status=deleted`）；若沙箱存在则异步销毁；进行中的 turn 先 cancel。**附件 blob 与 attachment 行不删**（附件永不自动删除，GC 后续另做，见 data-model.md §10）。

### POST /api/v1/sessions/{id}/prompts

投递用户消息（启动一个 turn）。请求：

```json
{ "content": "...", "request_id": "<客户端 uuid>",
  "attachments": ["sha256:<64 hex>", "...（可选，先经 attachments 端点上传）"] }
```

- `request_id` 幂等：相同 `(session_id, request_id)` 重复投递直接返回 200 + 已存在的 `user/message` seq（乐观对账，见 protocol.md §8）。
- `content` 允许为空字符串当且仅当携带 `attachments`（纯图消息）；两者皆空 → 400。纯图消息组装时不带空文本 part，首条为纯图时会话标题静态兜底"图片会话"。
- `attachments`（可选）：附件 id 数组，≤9 个；每个 id 必须已存在并授权给会话机构（attachment_access；迁移兼容首发机构），任一不满足 → 400 `request/invalid`（整条消息拒绝，不产生任何事件）。
- vision 门控：携带附件而当前模型 `capabilities.vision=false` → 422 `model/not_vision`（提示换模型；不静默降级，避免"模型假装看不见"）。
- 会话 `running=1` 时返回 409 `session/running`（v1 不支持排队；steer/queue 在 M4）。
- 成功 202：`{ "accepted": true, "seq": <user/message 事件 seq> }`。流式输出经 `session.follow` 推送（protocol.md）。

### POST /api/v1/sessions/{id}/attachments

上传图片附件（`multipart/form-data`，字段名 `file`）。校验：

- mime 白名单 png/jpeg/webp/gif，**按魔数嗅探**（不信客户端声明）；
- 单图 ≤8MB（原图字节）；
- 解码读取宽高，总像素 ≤64M（超出拒绝）；
- 全部失败 → 422 `attachment/invalid`；缺少 `file` 字段 → 400。

服务端写内容寻址存储（同字节去重，重复上传幂等返回既有行）+ 插 attachment 表，201 返回：

```json
{ "id": "sha256:<64 hex>", "mime": "image/png", "bytes": 183420,
  "width": 1600, "height": 900, "name": "截图.png" }
```

### GET /api/v1/attachments/{id}

下载附件字节。登录 + 授权机构成员校验（attachment_access 中存在用户所属机构的授权关系；非成员按 404 `attachment/not_found` 处理，防枚举）。物理内容全局去重不等于跨机构授权。
按表记录 mime 回字节（读时按 id 校验 sha256），响应头 `Cache-Control: private, immutable` + `ETag: "<sha256 hex>"`（支持 `If-None-Match` → 304）。
前端 `<img>` 无法带 Authorization 头：fetch 拉 blob 转 objectURL，会话级缓存 attachmentId→objectURL。

### POST /api/v1/sessions/{id}/cancel

中断当前 turn → `{ "ok": true }`。无进行中 turn 时 409 `session/running`。

### POST /api/v1/sessions/{id}/fork （M4）

请求 `{ "at_seq": 42 }` → 从该事件点创建聊天副本（复制 ≤at_seq 的事件 + 快照 + `model_params`，标题追加"（副本）"），201 返回 `{ "id": "<新会话 id>" }`；`at_seq` 不在 `1..last_seq` 时 400。副本会话 `last_seq ≥ 1`，思考强度天然处于锁定态。

### GET /api/v1/sessions/{id}/events

历史分页与间隙修复。query：`after_seq`（默认 0）、`limit`（默认 200，最大 500）。
响应 `{ "items": [ { "seq", "type", "time", "data" } ], "last_seq": <当前最新> }` 按 seq 升序。

### POST /api/v1/sessions/{id}/approvals/{callId}

审批决定（owner）。请求 `{ "decision": "allow|deny" }` → `{ "ok": true }`。
- 状态非 pending → 409 `approval/not_pending`；超时 → 410 `approval/expired`（服务端已自动补 deny）。

### POST /api/v1/sessions/{id}/questions/{callId}/answer

提问作答（owner；question 工具，runtime.md §5.3）。请求：

```json
{ "answers": [ { "id": "q1", "selected": ["label"...], "custom": "（可选自由文本）" } ] }
```

- 校验：`callId` 无对应 `question/asked` → 404 `question/not_found`；已作答 → 409 `question/not_pending`；
  每题必须恰好一条答案（按 id 对应，不允许未知/重复 id；有 options 的题 `selected` 必须是选项 label，
  单选至多一个；自由输入题不允许 `selected`；显式跳过 = 空 `selected` 且无 `custom`）→ 400 `question/bad_answer`。
- open（工具仍在前台等待）→ 投递内存等待，答案作为该工具结果返回，200 `{ "ok": true, "delivered": "open" }`。
- continued（超时/中断/重启后）→ 落 `question/answered` + 合成 `user/message`（`source:{kind:"question-reply"}`）
  开启新 turn，202 `{ "accepted": true, "delivered": "continued", "seq": <user/message seq> }`；
  会话忙 → 409 `session/running`（v1 不支持排队）。

### GET /api/v1/sessions/{id}/sandbox/files

沙箱文件浏览（owner，代理 execd）。query：`path`（默认 `/workspace`）→ `{ "items": [{ "name", "path", "type": "file|dir", "size", "modified_at" }] }`。

### GET /api/v1/sessions/{id}/sandbox/file

文件预览/下载。query：`path`、`max_bytes`（默认 256KB，超出截断并标记 `truncated:true`）→ `{ "path", "content"(utf8 文本), "truncated" }`；二进制文件返回 `application/octet-stream` 原始流。

---

## 4. 工作区数据

### GET /api/v1/orgs/{orgId}/agents （member+）

本机构 `published` 智能体列表（选择对话对象）：`{items:[{id, name, description, avatar, model_id, model_display_name}]}`。

### GET /api/v1/orgs/{orgId}/models （member+）

平台上架且 `active` 的模型列表（会话切模型用，不含供应商密钥信息）：`{items:[{id, display_name, model_code, provider_name, capabilities, context_size}]}`。

---

## 5. 机构管理（`/api/v1/orgs/{orgId}/admin/*`，全部 `admin`）

### 智能体管理

| 方法/路径 | 说明 |
|---|---|
| `GET /admin/agents` | 本机构全部（含 draft/archived），游标分页；列表项含 `model_display_name` / `skill_count` / `sub_agent_count` |
| `POST /admin/agents` | 创建（默认 draft）。body 见下 |
| `GET /admin/agents/{id}` | 详情（含 skills/subagents 解析） |
| `PUT /admin/agents/{id}` | 全量更新（仅 draft/archived 可直接改；published 修改自动转 draft 需重新发布——避免线上配置被悄悄改变） |
| `POST /admin/agents/{id}/publish` | 发布：校验完整性（model 有效、prompt 非空、subagents DAG 合法且均 published） |
| `POST /admin/agents/{id}/archive` | 下线（进行中的会话不受影响——会话持有快照；新会话不可选） |
| `PATCH /admin/agents/{id}/env` | `{set:{KEY:value},unset:[KEY]}`；机构管理员独立修改变量，不改变发布状态；已有会话下一轮任务生效 |
| `DELETE /admin/agents/{id}` | 删除（仅 draft/archived；有会话引用的置 archived 并标记） |

创建/更新 body：

```json
{ "name": "...", "description": "...", "avatar": "",
  "system_prompt": "...", "model_id": "...",
  "model_params": { "temperature": 0.7 }, "max_iterations": 20,
  "tools_enabled": { "exec": true, "read": true, "write": true, "edit": true, "glob": true, "grep": true, "read_media": true },
  "skill_ids": ["..."], "sub_agent_ids": ["..."],
  "sandbox_config": { "image": "", "resources": null, "env": { "KEY": "value" } } }
```

### Skills 管理

| 方法/路径 | 说明 |
|---|---|
| `POST /admin/skills` | 上传 zip（`multipart/form-data`，字段名 `file`）。限制：≤20MB、解压后 ≤50MB、文件数 ≤200、zip slip 防护。服务端解压校验 SKILL.md frontmatter（agentskills.io 规范：name 合法且与目录一致、description 1..1024）→ 201 返回 skill 对象；失败 422 `skill/invalid`（details 说明） |
| `PUT /admin/skills/{id}/package` | 替换当前包，multipart 字段 `file` + `expected_revision`（十进制整数 >0）。ID/name 保持不变；成功 200 返回 skill 对象（`revision` 加一、`content_hash` 更新）；修订不一致返回 409，`details.revision` 为当前值；包名变化或非法包返回 422 |
| `GET /admin/skills` | 列表（`size_bytes` / `uploader_name` / 被哪些智能体引用 `referenced_by`；不返回服务端 `storage_path`） |
| `GET /admin/skills/{id}` | 详情（meta 全文 + 顶层 `files` 文件清单 + `size_bytes` / `uploader_name` / `referenced_by`） |
| `GET /admin/skills/{id}/file?path=SKILL.md` | 查看包内文本文件（≤256KB）→ `{ "path", "content", "truncated" }` |
| `DELETE /admin/skills/{id}` | 删除；被智能体引用、被运行使用、安装远端状态不明确或仍有安装副本时返回 409。解除关联后须在安全任务边界完成副本移除，再删除源包 |

上传后 `revision=1`，`content_hash` 为排序文件路径与文件 SHA256 清单的摘要。`_source.zip` 为平台保留名称，上传包不能包含该文件。API 只公开当前源包；内部隐藏候选目录用于提交恢复，不提供多版本选择。源码读取、替换和回收使用同一跨进程锁，避免替换时在线预览读到已回收目录。

替换源包只建立用户安装更新屏障，不覆盖正在运行的技能目录。`skill/update_pending` 表示等待已有运行（包括审批等待）释放；`skill/sync_failed` 表示同步失败；`skill/sync_blocked` 表示远端结果未知，必须确认同步命令停止后显式恢复。恢复能力由领域 `Coordinator.Recover` 提供并记录审计，不能将租约超时视为停止证明。

### 沙箱设置（只读）

| 方法/路径 | 说明 |
|---|---|
| `GET /admin/sandbox-settings` | 平台 `sandbox.*` 设置子集（镜像列表/默认资源/TTL/ egress），机构 admin 只读（编辑器沙箱分区用；写仍走 `/platform/settings`） |

### 成员管理

| 方法/路径 | 说明 |
|---|---|
| `GET /admin/members` | 成员列表 `{items:[{user_id, username, display_name, status, role, created_at}]}` |
| `POST /admin/members` | 添加已有用户：`{ "username": "...", "role": "admin|member" }`（按 username 查找；不存在 404） |
| `PATCH /admin/members/{userId}` | 改角色 `{ "role": "..." }`（不得移除本机构最后一名 admin） |
| `DELETE /admin/members/{userId}` | 移出机构（同上约束） |

### 机构用量

`GET /admin/usage?from=YYYY-MM-DD&to=YYYY-MM-DD&group_by=user|agent|model|day` →
`{ "items": [ { "key": "...", "requests", "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "total_tokens" } ], "totals": {...}, "names": { "<key>": "<显示名>" } }`（基于 `usage_daily`；`names` 把 user/agent/model/org 维度的 key 映射为显示名，`day` 维度为空）。机构端 `group_by=org` 返回 400。

---

## 6. 平台管理（`/api/v1/platform/*`，全部 `platform`）

### 机构

| 方法/路径 | 说明 |
|---|---|
| `GET /platform/orgs` | 全机构列表（含成员数/智能体数统计） |
| `POST /platform/orgs` | 创建机构 `{ "name": "..." }` |
| `PATCH /platform/orgs/{id}` | 改名 / 停用启用 `{ "name"?, "status"? }`（平台管理机构不可停用） |
| `GET /platform/orgs/{id}` | 详情（成员列表 + 统计） |

### 用户

| 方法/路径 | 说明 |
|---|---|
| `GET /platform/users?q=&cursor=&limit=` | 用户列表；每项含 `orgs: [{org_id, org_name, role}]` 机构归属 |
| `POST /platform/users` | 创建用户 `{ "username", "display_name", "password"（可空→随机生成）, "org_ids": ["..."], "role": "member" }`；201 返回 `{ "user": {...}, "password": "<初始密码，仅此一次展示>" }`，`must_change_password=1`；审计 `tenant.user_create`，且每个初始机构各记一条 `member.add` |
| `PATCH /platform/users/{id}` | 改名/停用启用/重置密码 `{ "display_name"?, "status"?, "reset_password"? }`；返回 `{ "user": {...} }`，重置密码时顶层附带 `"password"`（同样强制改密） |
| `POST /platform/users/{id}/orgs` | 把用户加入机构 `{ "org_id", "role" }`（已是成员返回 409）；201 返回成员条目 `{user_id, username, display_name, status, role, created_at}` |
| `PATCH /platform/users/{id}/orgs/{orgId}` | 修改用户在该机构的角色 `{ "role": "admin\|member" }`；不得移除本机构最后一名 admin（409），审计 `member.update_role` |
| `DELETE /platform/users/{id}/orgs/{orgId}` | 移出机构（同上最后一名 admin 约束） |

### 供应商与模型

| 方法/路径 | 说明 |
|---|---|
| `GET /platform/providers` | 列表；密钥仅返回 `masked_key` 掩码（如 `sk-****1234`），扩展信息在 `extra`（`{remark?, last_check?: {ok, at, latency_ms, error?}}`，无则为 null） |
| `POST /platform/providers` | 创建 `{ "name", "type": "deepseek|ark|qwen|openai_compat", "base_url", "api_key", "extra"?, "remark"? }`；创建后服务端异步做连通性测试（GET base_url/models），结果存 `extra.last_check`，失败仍允许保存但标记 |
| `GET /platform/providers/{id}` | 详情（结构同列表项，密钥掩码） |
| `PUT /platform/providers/{id}` | 更新；`api_key` 字段留空表示不更换 |
| `DELETE /platform/providers/{id}` | 删除；存在 `active` 模型时 409 |
| `POST /platform/providers/{id}/check` | 重新连通性测试 → `{ "ok": true, "latency_ms": 0 }` |
| `GET /platform/models` | 模型列表（可按 provider 过滤） |
| `POST /platform/models` | 上架 `{ "provider_id", "model_code", "display_name", "capabilities", "context_size", "default_params"? }` |
| `PUT /platform/models/{id}` | 更新/上下架（`status`） |
| `DELETE /platform/models/{id}` | 删除；被智能体引用时 409 并列出 |

`capabilities.reasoning_efforts` 可从 `none/minimal/low/medium/high/xhigh/max` 多选，非法值或重复值返回 400；可选 `reasoning_effort_default` 必须属于已选集合。此处与会话保存的都是平台档位，供应商实际请求参数按 runtime.md §3 转换，不回写为供应商枚举或 token 预算；字段缺省/空列表表示不提供选档。

`capabilities.api_protocol` 可选 `chat_completions`（缺省）或 `responses`。`tool_images` 默认为 false，启用需同时满足 responses、vision、tools。仅支持已实现 Responses 适配的供应商。含工具图片历史切换到不支持工具图片的模型返回 409 `session/media_locked`。图片与租户授权、供应商文件有效期规则见 [media.md](media.md)。

### 平台设置与报表

| 方法/路径 | 说明 |
|---|---|
| `GET /platform/settings` | 全部 platform_setting KV |
| `PUT /platform/settings/{key}` | 更新单 key（body 即 value JSON；仅允许 data-model.md §7 列出的 key） |
| `GET /platform/usage?from=&to=&group_by=org|user|agent|model|day` | 全平台用量（结构同机构用量） |
| `GET /platform/audit?org_id=&cursor=&limit=` | 审计日志（id 倒序即时间倒序），`{items:[{id, org_id, actor_id, actor_name, action, target, detail, created_at}], next_cursor}`；`actor_name` 为操作者显示名（回退 username） |

---

## 7. 静态资源与 SPA 回退

- `/api/*` 之外的 GET 路径：命中 `web/dist` 内静态文件则返回，否则回退 `index.html`（前端路由）。
- 静态资源带强缓存头（Vite 产物指纹）；`index.html` `no-cache`。

## 8. 用户云电脑、绑定、卷与操作

以下路径均以 `/api/v1` 为前缀。所有操作要求登录、当前机构成员关系及本人归属；资源 ID 不可见时返回 404，机构管理员不能通过此接口访问他人的私人电脑。列表返回 `{items:[]}`。查询不启动电脑、不创建卷。

| 方法/路径 | 输入与行为 |
|---|---|
| `GET /orgs/{orgId}/computers` | 当前用户在机构内的未删除电脑 |
| `POST /orgs/{orgId}/computers` | `{name, agent_id?, config?}`；保存逻辑电脑和创建意图，再异步准备三卷及实例 |
| `GET /computers/{id}` | 安全电脑视图 |
| `PATCH /computers/{id}` | `{name?, env?:{set:{KEY:value},unset:[KEY]}}`，直接保存名称和变量；至少一项，变量下一轮任务生效 |
| `GET /computers/{id}/env` | 仅本人可读，返回 `{keys:[KEY]}`，不返回变量值 |
| `POST /computers/{id}/start` | 无需 body；running 幂等完成，paused 恢复；确认实例丢失且无活动运行时以新 generation 重建 |
| `POST /computers/{id}/pause` | 无需 body；活动任务、后台任务或维护占用返回 409 `computer/busy` |
| `POST /computers/{id}/rebuild` | 可空 body 或 `{config?,env?:{set:{KEY:value},unset:[KEY]}}`；空闲时替换实例、增加 generation，保留三卷 |
| `POST /computers/{id}/recover` | 必需 `{confirm_stop_all:true}`；显式强制终止该电脑所有任务与维护并停止实例，保留三卷及会话／默认绑定，202 异步操作 |
| `DELETE /computers/{id}` | 有活动任务返回 `computer/busy`；存在未删除会话或 Agent 默认绑定返回 `computer/in_use`；成功仅删除电脑 |
| `GET /computers/{id}/runs` | 非终态任务及维护占用，租约过期不隐藏记录 |
| `GET /orgs/{orgId}/agent-bindings` | 本人的 `{org_id,user_id,agent_id,computer_id,created_at,updated_at}` 列表 |
| `PUT /orgs/{orgId}/agent-bindings/{agentId}` | `{computer_id}`；Agent 必须属于机构且已发布，电脑必须属于本人；只影响后续解析 |
| `DELETE /orgs/{orgId}/agent-bindings/{agentId}` | 幂等清除本人默认绑定，返回 `{ok:true}`；保留已有会话绑定，已归档的 Agent 偏好也可清理 |
| `GET /orgs/{orgId}/volumes` | 三类已登记卷摘要，首次创建电脑前可以为空 |
| `GET /operations/{id}` | 本人电脑操作状态；不触发任何后端操作 |

创建及重建的 `config` 支持：

```json
{
  "image": "python:3.12",
  "resources": {"cpu": "1", "memory": "1Gi"},
  "env": {"APP_MODE": "development"},
  "ttl_seconds": 3600
}
```

- 创建镜像与资源优先级：显式配置 → 可选 Agent 模板 → 平台默认。电脑 env 只合并平台默认与显式 config.env，不复制 Agent env；resources 显式提供时整体替换。
- 镜像必须是平台默认或 `sandbox.images` 中启用的镜像。resources 仅支持 cpu、memory 的正数资源量；env 沿用下述任务变量校验。TTL 为 60–604800 秒，表示底层续租时长，不是数据保留期。
- `network_policy` 由平台 egress 设置决定，普通用户请求中出现该字段会被拒绝；不得通过电脑配置绕过统一出口策略。
- rebuild 的 config 在已有配置上合并，并在新实例创建时生效；变量通过顶层 env 补丁修改，不接受 config.env。补丁与新配置在同一意图事务保存，保留并发编辑过的最新变量。单独 PATCH env 无需重建，运行中的任务保留本轮值；生命周期转换期间修改变量返回 computer/busy。
- env 保存在电脑配置中，但仅注入非 root 任务进程，不传入容器级 root execd/维护环境。WISPJAR、HOME、EXECD_* 为保留变量，值拒绝 NUL。每层及合并后的用户变量最多 64 项，每值最多 4096 字节。exec 按电脑 → 当前 Agent → 单次 env 覆盖，最后注入 WISPJAR=1；HOME 固定 /tmp。电脑强制启用 execd 认证。
- 响应 config 仅包含 image、resources、ttl_seconds、has_env，不回传 env 值、网络策略、后端地址或卷物理名。

恢复操作用于服务重启、连接中断后遗留占用，也允许用户明确终止健康任务。进入 `recovering` 后拒绝新运行、文件维护、启动和普通生命周期操作。只有按 installation/电脑/generation 校验归属并删除旧实例、再次查询确认不存在后，才终结相关运行并释放技能使用权；随后恢复匹配的技能同步与会话运行状态，最后推进 generation、进入 `stopped`。不会重放中断命令，也不会新建或删除持久卷。删除响应丢失、查询失败或归属不符时维持 `recovering` 与 operation `running`，返回安全的 `error` 说明并由后台重试；未取得停止证明前不释放运行与技能占用。相同幂等键返回原操作，完成后重复请求不再次终止电脑。

电脑视图示例：

```json
{
  "id": "<computer_id>",
  "org_id": "<org_id>",
  "name": "开发电脑",
  "status": "running",
  "generation": 1,
  "config": {
    "image": "python:3.12",
    "resources": {"cpu": "1", "memory": "1Gi"},
    "ttl_seconds": 3600,
    "has_env": true
  },
  "last_error": "",
  "last_active_at": "2026-10-20T10:00:00+08:00",
  "created_at": "2026-10-20T09:00:00+08:00"
}
```

所有生命周期写操作返回 `202 Accepted`：

```json
{"operation_id":"<operation_id>","status":"pending","resource_id":"<computer_id>"}
```

已经满足目标的 start 可以返回 `status: "completed"`。创建和重建推荐传 `Idempotency-Key`（最多 128 字符），其他生命周期操作也支持。同 `(org,user,action,key)` 且规范化请求摘要相同返回原操作；不同摘要返回 `409 operation/conflict`。摘要包含目标 computer_id，不能把一个键复用于另一台电脑。修改默认配置不重算已落库的电脑配置或卷名。

操作查询返回 `{id,resource_id,resource_type,action,status,error,error_detail,created_at,updated_at}`，status 为 `pending/running/completed/failed`。电脑视图失败时也提供可选 `error_detail`，`last_error` 和操作 `error` 保持为面向用户的原因摘要。`error_detail` 包含 `{code,message,suggestion,operation_id?}`，分别用于失败原因、处理建议与日志定位；成功时省略或为 null。稳定错误码区分 `computer/backend_dns`、`computer/network_not_ready`、`computer/image_unavailable`、`computer/backend_auth`、`computer/backend_timeout`、`computer/instance_missing`、`computer/identity_mismatch`、`computer/rebuild_required`、`volume/missing`、`volume/conflict`；未分类故障为 `computer/backend_failed`。旧记录未保存具体原因时返回 `computer/legacy_failure` 与安全的通用说明，不推断历史原因。管理页和聊天页显示原因与建议，错误码和操作编号收纳在“排查信息”中。

内部进度中的实例身份、generation 清理计划和连接信息不对客户端暴露。公开说明使用预定义分类，不回显原始后端地址、请求头、凭据或调用堆栈；详细后端诊断仅记录服务日志。底层故障分类保留在恢复操作中，`recovering` 仍维持恢复门禁与后台重试，不因显示错误而释放占用。

卷摘要字段为 `id,org_id,user_id,kind,status,policy,usage,last_error,created_at,updated_at`。不含 Docker socket、backend_name 或宿主路径。此端点不提供卷删除能力。

运行摘要字段为 `id,session_id,agent_id,parent_run_id,status,lease_until`。维护占用的 session_id/agent_id 为空；未知或失效租约仍阻止暂停、重建和删除，直到执行方确认命令已终止。

错误沿用统一信封：`computer/not_found` 404；`computer/invalid` 400；`computer/required`、`computer/busy`、`computer/in_use`、`operation/conflict`、`volume/conflict`、`volume/missing` 为 409；后端未配置或不可用为 `computer/unavailable` 503。异步执行中发生的后端故障记录到 operation，不把“已接收”当作“已启动”。

部署兼容：`opensandbox.legacy_sessions_enabled` 默认 true，保留旧会话管理器。共用 OpenSandbox 的新独立部署必须配置为 false，防止无 installation 标签的旧 `wispjar=1` 实例被扫描。电脑实例始终使用独立的 `wispjar=computer-<installation>` 标签，按机构、用户、电脑、存储域和 generation 完整校验后接管。
