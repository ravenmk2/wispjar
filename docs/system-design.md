# Wispjar 系统设计（总览）

> 版本：v1.1（评审修订基线）
> 读者约定：本文档是系统设计的唯一入口；**未参与需求讨论的开发者/Agent 应能从 docs/ 全部文档自主完成开发**。细节分散在 `docs/design/` 子文档，本文只保留全局决策与架构。
> 文档集：
> - `docs/system-design.md`（本文）：目标、决策、术语、总体架构、里程碑、风险
> - `docs/design/data-model.md`：MySQL 完整表结构（可直接执行的 DDL）、种子数据、迁移约定
> - `docs/design/api.md`：REST API 完整规格（全部端点请求/响应/权限/错误码）
> - `docs/design/protocol.md`：WebSocket 实时协议（mux 帧、follow 订阅、StreamChunk、会话事件词表、重连对账）
> - `docs/design/runtime.md`：Agent 运行时（eino 集成、工具规格、task 委派、Skills、审批、沙箱管理）
> - `docs/design/frontend.md`：前端工程与界面详细规格

---

## 1. 概述

Wispjar 是一个多租户智能体平台：

- **多租户（机构）**：平台托管多个机构；一个用户可同时是多个机构的成员，在机构间切换工作区。
- **多模型供应商/多模型**：平台统一管理模型供应商与密钥（v1 至少支持 DeepSeek、火山引擎 Ark、阿里千问/百炼），机构智能体按需选用。
- **多智能体**：机构管理员创建/发布智能体（提示词 + 模型 + 工具 + Skills + 子智能体 + 沙箱镜像）；普通用户仅对话使用。
- **子智能体委派**：主 Agent 通过一个统一的 `task` 工具把自包含任务委派给机构内其他智能体（supervisor 模式）。
- **Agent Skills**：遵循 agentskills.io 规范，上传 zip 配置，渐进式披露。
- **沙箱**：基于 OpenSandbox，每会话独占一个沙箱，Agent 可在沙箱内 exec / read / write / edit / glob / grep。
- **会话与事件**：事件溯源（event sourcing）会话日志（MySQL append-only 事件表）+ 单条 WebSocket 多路复用实时协议；协议骨架、事件词表、增量块协议借鉴 DeepSeek Harness 开源项目并做多租户适配。

### 1.1 非目标（v1 不做）

- 插件体系、可视化 workflow 画布、机构自配供应商密钥（BYOK）
- 配额硬限制（仅统计展示）、公开注册、SSO/LDAP、邮箱找回密码
- MCP 工具接入（留扩展口）、会话分享/多人协作

## 2. 需求决策记录

| # | 主题 | 决策 |
|---|------|------|
| 1 | 管理形态 | 内置"平台管理机构"租户，其管理员即平台超管；超管使用独立管理后台界面 |
| 2 | 认证注册 | 用户名+密码；无公开注册；平台超管创建用户，机构管理员可添加已有用户进本机构 |
| 3 | 密钥归属 | 平台统一配置，加密存储，对机构不可见 |
| 4 | 模型可见性 | 平台上架模型全局共享，所有机构可用 |
| 5 | Agent 形态 | 单 Agent ReAct + `task` 工具委派子 Agent（子 Agent 独立上下文执行、返回最终结果） |
| 6 | 沙箱粒度 | 每会话独占；首次调用沙箱工具时惰性创建，会话内复用，空闲超时自动暂停（按需恢复），硬 TTL 到期回收 |
| 7 | 前后端协议 | 所有 API 以 `/api` 开头：CRUD/动作走 REST，实时走单条 WebSocket 多路复用流（`/api/ws`） |
| 8 | 工具确认 | 会话级三模式，用户自行切换：`strict`（exec/write/edit 全确认）/ `default`（仅 exec 确认）/ `auto`（全免确认，新会话默认） |
| 9 | 普通用户边界 | 仅对话使用 + 管理自己的会话；会话对机构管理员也不可见（仅本人） |
| 10 | 用量 | 记录 token 用量并按机构/用户/智能体/模型聚合，管理后台报表展示，无硬配额 |
| 11 | 前端技术栈 | React 18 + Vite + TypeScript + Tailwind + shadcn/ui + Zustand + React Router |
| 12 | Skills 格式 | 遵循 agentskills.io 规范（SKILL.md + YAML frontmatter + scripts/references/assets） |
| 13 | 沙箱网络/镜像 | 平台超管统一配置 egress 黑白名单，创建沙箱时应用；每个智能体可配置不同镜像 |
| 14 | 数据库 | MySQL 8；主键 UUIDv7 时序版、去掉 `-`（32 位 hex，`CHAR(32)`） |
| 15 | 后端框架 | Go + gin + cloudwego/eino（`adk.ChatModelAgent` + `adk.Runner`）+ eino-ext 模型组件 |
| 16 | 沙箱接入 | OpenSandbox 官方 Go SDK（lifecycle REST + execd API） |
| 17 | 缓存命中优先 | 模型上下文缓存按输入前缀命中，system/历史前缀稳定优先于一切动态内容注入；原则清单与评审基线见 runtime.md §12《缓存命中优先设计原则》 |

## 3. 术语

| 术语 | 含义 |
|------|------|
| 机构（Org / Tenant） | 租户。平台管理机构是内置的特殊机构 |
| 平台超管 | 平台管理机构中角色为 admin 的成员 |
| 机构管理员 / 普通用户 | 机构内角色 admin / member |
| 供应商（Provider） | 模型供应商配置（base_url + 加密密钥 + 类型） |
| 模型（Model） | 供应商下上架的具体模型 |
| 智能体（Agent） | 机构内可被对话使用的实体 |
| 子智能体（Subagent） | 被主 Agent 通过 `task` 工具委派的智能体；独立上下文执行 |
| Skill | 遵循 agentskills.io 规范的能力包（zip 上传） |
| 会话（Session） | 某用户在某机构下与某 Agent 的对话，append-only 事件日志 |
| 事件（Event） | 会话日志条目（`seq` 单调递增），唯一事实源 |
| Turn / Step | 一轮对话 / 一次模型请求及其触发的工具调用 |
| 沙箱（Sandbox） | OpenSandbox 容器实例，会话独占 |
| 确认模式 | 会话级工具审批策略：strict / default / auto |
| mux 流 | 单条 WebSocket 上按 `streamId` 多路复用的逻辑流 |

## 4. 总体架构

### 4.1 部署视图

```txt
┌─────────────────────────── 用户浏览器 ───────────────────────────┐
│  React SPA（REST /api/v1/* + 单条 WebSocket /api/ws）            │
└───────────────┬─────────────────────────────────────────────────┘
                │ HTTP / WSS（生产建议前置反向代理终结 TLS）
┌───────────────▼─────────────────────────────────────────────────┐
│  wispjar 单二进制（Go, CGO_ENABLED=0）                            │
│  ├─ gin HTTP 服务：/api/* REST + /api/ws WS 升级 + SPA 静态资源  │
│  ├─ Agent 运行时（eino adk）                                     │
│  ├─ 会话事件存储 / 投影器                                        │
│  ├─ 沙箱管理器（OpenSandbox Go SDK 客户端）                      │
│  └─ 供应商密钥加解密（AES-256-GCM，主密钥来自环境变量）          │
└───────┬──────────────────────────────┬──────────────────────────┘
        │ MySQL 协议                   │ HTTP REST（OPEN-SANDBOX-API-KEY）
┌───────▼────────┐           ┌─────────▼────────────────────────────┐
│ MySQL 8        │           │ OpenSandbox lifecycle server         │
│ (docker 部署,  │           │  ├─ Docker 后端创建沙箱容器           │
│  named volume) │           │  └─ 每沙箱内 execd(44772) 数据面     │
└────────────────┘           └──────────────────────────────────────┘
```

- wispjar 二进制经 `scripts/build.sh` 构建，版本号 `-ldflags -X main.version` 注入；前端 `web/dist` 经 go:embed 嵌入。
- 路由约定：**所有动态接口以 `/api` 开头**（REST `/api/v1/...`、WS `/api/ws`）；其余路径一律回退到 SPA 的 `index.html`（前端路由）。

### 4.2 后端组件（internal 分层）

```txt
cmd/wispjar/            # 入口：加载配置、组装依赖、启动 gin（不含业务逻辑）
internal/
├── config/            # 配置加载（yaml + 环境变量覆盖）
├── id/                # UUIDv7 32-hex ID 生成
├── store/             # MySQL 连接、迁移（golang-migrate）、GORM 模型与查询
├── auth/              # 登录、bcrypt、JWT 签发/校验、中间件、机构上下文解析、登录限流
├── tenant/            # 机构/用户/成员 管理服务
├── provider/          # 供应商与模型管理、密钥加解密、eino ChatModel 工厂（缓存）
├── skill/             # skill zip 上传/校验/解析/存储（agentskills.io 规范）
├── agent/             # 智能体 CRUD 与发布、子 Agent DAG 校验
├── runtime/           # eino 集成核心：Agent 构建、ReAct 循环、事件翻译、中断/恢复
│   ├── tools/         # 沙箱工具六件套 + skill + task 委派工具
│   ├── approval/      # 确认模式、interrupt/resume、pending 审批管理
│   └── checkpoint/    # adk CheckPointStore 的 MySQL 实现
├── sandbox/           # OpenSandbox 客户端封装、会话沙箱池、空闲暂停/恢复、egress/镜像应用
├── session/           # 会话事件日志（append/read/derive）、投影器（标题/用量/列表）
├── gateway/           # gin 路由：REST handlers + WS mux server + 协议编解码
├── usage/             # 用量聚合与报表查询
└── audit/             # 管理操作审计日志
```

主要第三方依赖（go.mod，落地时锁定版本）：`gin-gonic/gin`、`cloudwego/eino`、`cloudwego/eino-ext/components/model/{openai,ark,qwen}`（各为独立 module，按需引入）、`alibaba/OpenSandbox/sdks/sandbox/go`、`gorm.io/gorm` + `gorm.io/driver/mysql`、`gorilla/websocket`、`google/uuid`（v7）、`golang-migrate/migrate`、`golang.org/x/crypto`（bcrypt）。

### 4.3 关键不变量（全部子文档的前提）

1. **事件日志是唯一事实源**：任何进入模型请求的内容必须能从 `session_event` 重建（`deriveMessages()`）；瞬态流式增量提交后必有对应持久事件。
2. **append-only**：`session_event` 永不 UPDATE/DELETE；投影列（session 表）与事件写入同事务。
3. **一切实时推送走 mux 流**，首帧永远是完整快照/基线，客户端原子替换后按序追加。
4. **租户隔离在服务端强制**：所有业务查询带 org_id；资源归属在 service 层校验。
5. **密钥不出 provider 包**：解密仅在 ChatModel 工厂内部发生，API 永不回显明文。

## 5. 里程碑与验收

| 阶段 | 内容 | 验收标准 |
|---|---|---|
| **M1 平台基座** | 工程骨架、MySQL 迁移、认证/JWT、机构/用户/成员、供应商/模型/密钥管理、平台设置、管理后台最小 UI | 超管可登录并完整 CRUD 机构/用户/供应商/模型；机构管理员可管理成员；密钥加密落库且 API 不回显 |
| **M2 会话核心** | eino 运行时（单 Agent ReAct + 六沙箱工具）、OpenSandbox 接入、会话事件存储与投影、WS mux 协议（follow/control/审批）、聊天前端（流式/工具卡片/审批卡片/确认三模式/停止） | 创建智能体并对话；exec 等按模式触发审批；断线重连状态一致；事件日志可完整回放；沙箱独占与空闲回收 |
| **M3 Skills 与编排** | Skill 上传/校验/挂载/渐进式披露、`task` 委派子 Agent（DAG 校验、嵌套事件轨迹）、智能体编辑器完整版、沙箱文件面板、轨迹视图 | zip 上传激活生效；主 Agent 可委派子 Agent 且轨迹可展开；编辑器全字段可用 |
| **M4 报表与加固** | 用量聚合与两级报表、审计页、fork/steer、上下文摘要（视进度）、性能与并发测试、总验收 | 报表数据与事件流对账一致；完整走查多机构/多用户/多 Agent 场景 |

每阶段完成后 git commit（Conventional Commits）；M4 通过即 v1 验收。

## 6. 风险与对策

| 项 | 说明 | 对策 |
|---|---|---|
| eino 版本差异 | 调研基于较新主干（泛型 BaseModel、adk）；release 版 API 可能有差异 | 开工时 `go get` 锁定版本，以锁定版源码为准校正 runtime.md 的 API 调用 |
| OpenSandbox 迭代快 | 仍在快速演进 | 以部署版本的 `specs/` OpenAPI 为准核对 execd 端点 |
| Ark/Qwen 接入 | 官方组件 vs OpenAI 兼容模式的选择 | M2 首周做三家供应商连通性 spike，择优固化工厂实现 |
| 审批 + interrupt 恢复 | adk checkpoint 与多副本部署的亲和性 | v1 单实例部署；checkpoint 落 MySQL 已为水平扩展留路 |
| 用量对账 | 流式中断时 usage 可能缺失 | usage 以 finish/usage 块到达为准；缺失时记 0 并在报表注明口径 |
| 沙箱泄漏 | 崩溃可能残留沙箱 | reaper 兜底扫描（对比 lifecycle 列表与库内记录） |
| 前端流式 Markdown | 增量解析复杂度 | M2 先做原型验证选型（详见 frontend.md） |
