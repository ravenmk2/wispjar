# REST API 规格

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
| 409 | `approval/not_pending` | 审批已决定或已过期 |
| 410 | `approval/expired` | 审批超时 |
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
{ "agent_id": "...", "title": "可选，默认空（首条 prompt 后自动生成）" }
```

- 校验 agent 属于该机构且 `status=published`。
- 服务端：写 `session`（`agent_snapshot` 为创建时刻 Agent 全量配置 + 解析后的 skills/subagents 清单；`model_id=agent.model_id`）→ 201 返回会话对象。

### GET /api/v1/orgs/{orgId}/sessions

列表（本人）。query：`cursor/limit`、`q`（标题模糊）、`status=active|archived`（默认 active）、`agent_id`（可选过滤）。
响应 `{items:[{id, agent_id, agent_name, title, running, approval_mode, model_id, usage_total, last_active_at, created_at}], next_cursor}`。

### GET /api/v1/sessions/{id}

会话详情（owner）：会话对象全字段 + `agent`（快照中的名称/描述/头像）。

### PATCH /api/v1/sessions/{id}

请求（均可选）：`{ "title": "...(改名并 title_pinned=1)", "approval_mode": "strict|default|auto", "status": "active|archived", "model_id": "...(会话级切换模型)" }`
- 改 `approval_mode` 追加 `session/approval-mode` 事件；改 `model_id` 追加 `model/selection` 事件（事件词表见 protocol.md §6）。

### DELETE /api/v1/sessions/{id}

软删（`status=deleted`）；若沙箱存在则异步销毁；进行中的 turn 先 cancel。

### POST /api/v1/sessions/{id}/prompts

投递用户消息（启动一个 turn）。请求：

```json
{ "content": "...", "request_id": "<客户端 uuid>" }
```

- `request_id` 幂等：相同 `(session_id, request_id)` 重复投递直接返回 200 + 已存在的 `user/message` seq（乐观对账，见 protocol.md §8）。
- 会话 `running=1` 时返回 409 `session/running`（v1 不支持排队；steer/queue 在 M4）。
- 成功 202：`{ "accepted": true, "seq": <user/message 事件 seq> }`。流式输出经 `session.follow` 推送（protocol.md）。

### POST /api/v1/sessions/{id}/cancel

中断当前 turn → `{ "ok": true }`。无进行中 turn 时 409 `session/running`。

### POST /api/v1/sessions/{id}/fork （M4）

请求 `{ "at_seq": 42 }` → 从该事件点分叉出新会话（复制 ≤at_seq 的事件 + 快照），201 返回新会话 id。

### GET /api/v1/sessions/{id}/events

历史分页与间隙修复。query：`after_seq`（默认 0）、`limit`（默认 200，最大 500）。
响应 `{ "items": [ { "seq", "type", "time", "data" } ], "last_seq": <当前最新> }` 按 seq 升序。

### POST /api/v1/sessions/{id}/approvals/{callId}

审批决定（owner）。请求 `{ "decision": "allow|deny" }` → `{ "ok": true }`。
- 状态非 pending → 409 `approval/not_pending`；超时 → 410 `approval/expired`（服务端已自动补 deny）。

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
| `DELETE /admin/agents/{id}` | 删除（仅 draft/archived；有会话引用的置 archived 并标记） |

创建/更新 body：

```json
{ "name": "...", "description": "...", "avatar": "",
  "system_prompt": "...", "model_id": "...",
  "model_params": { "temperature": 0.7 }, "max_iterations": 20,
  "tools_enabled": { "exec": true, "read": true, "write": true, "edit": true, "glob": true, "grep": true },
  "skill_ids": ["..."], "sub_agent_ids": ["..."],
  "sandbox_config": { "image": "", "resources": null } }
```

### Skills 管理

| 方法/路径 | 说明 |
|---|---|
| `POST /admin/skills` | 上传 zip（`multipart/form-data`，字段名 `file`）。限制：≤20MB、解压后 ≤50MB、文件数 ≤200、zip slip 防护。服务端解压校验 SKILL.md frontmatter（agentskills.io 规范：name 合法且与目录一致、description 1..1024）→ 201 返回 skill 对象；失败 422 `skill/invalid`（details 说明） |
| `GET /admin/skills` | 列表（`size_bytes` / `uploader_name` / 被哪些智能体引用 `referenced_by`；不返回服务端 `storage_path`） |
| `GET /admin/skills/{id}` | 详情（meta 全文 + 顶层 `files` 文件清单 + `size_bytes` / `uploader_name` / `referenced_by`） |
| `GET /admin/skills/{id}/file?path=SKILL.md` | 查看包内文本文件（≤256KB）→ `{ "path", "content", "truncated" }` |
| `DELETE /admin/skills/{id}` | 删除；被引用时 409（`details.referenced_by` 列出引用者） |

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
`{ "items": [ { "key": "...", "requests", "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "total_tokens" } ], "totals": {...} }`（基于 `usage_daily`）。

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
| `POST /platform/users` | 创建用户 `{ "username", "display_name", "password"（可空→随机生成）, "org_ids": ["..."], "role": "member" }`；201 返回 `{ "user": {...}, "password": "<初始密码，仅此一次展示>" }`，`must_change_password=1` |
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

### 平台设置与报表

| 方法/路径 | 说明 |
|---|---|
| `GET /platform/settings` | 全部 platform_setting KV |
| `PUT /platform/settings/{key}` | 更新单 key（body 即 value JSON；仅允许 data-model.md §7 列出的 key） |
| `GET /platform/usage?from=&to=&group_by=org|user|agent|model|day` | 全平台用量（结构同机构用量） |
| `GET /platform/audit?org_id=&cursor=&limit=` | 审计日志 |

---

## 7. 静态资源与 SPA 回退

- `/api/*` 之外的 GET 路径：命中 `web/dist` 内静态文件则返回，否则回退 `index.html`（前端路由）。
- 静态资源带强缓存头（Vite 产物指纹）；`index.html` `no-cache`。
