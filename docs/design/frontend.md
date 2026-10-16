# 前端详细设计

> 本文是 `web/` 的权威设计。技术栈已定（决策 #11）：React 18 + Vite + TypeScript + Tailwind CSS + shadcn/ui + Zustand + React Router。界面语言中文。
> 交互对标 DeepSeek Harness 的产品形态（三栏布局、聊天/轨迹双视图、工具卡片、审批内嵌），但不照搬其自研 store/Slots/模块加载器。

## 1. 工程约定

| 项 | 约定 |
|---|---|
| 包管理 | pnpm |
| 构建 | Vite 6（`@vitejs/plugin-react`），产物 `web/dist`（gitignore，由 Go embed） |
| 语言 | TypeScript 5 strict |
| UI | Tailwind 3 + shadcn/ui（Radix 原语）；图标 lucide-react |
| 状态 | Zustand 5（transport 状态与组件状态分离，见 §5） |
| 路由 | react-router v7 |
| Markdown | react-markdown + remark-gfm；代码高亮 shiki（按需加载语言） |
| 请求 | 原生 fetch 封装（`src/api/client.ts`）：自动带 token、`X-Org-Id`（从 URL/状态推导）、统一错误信封解包、401 跳登录 |
| 测试 | vitest（仅关键工具函数与 mux 客户端） |

开发代理：vite dev server `server.proxy` 把 `/api` 转发到本地 Go 服务 `:8080`。

## 2. 目录结构

```txt
web/src/
├── main.tsx / App.tsx
├── routes.tsx                 # 路由表 + 守卫
├── api/                       # REST：client.ts + 按域 modules（auth/sessions/agents/admin/platform）
├── mux/                       # WS mux 客户端（§4）
├── stores/                    # Zustand stores（§5）
├── components/ui/             # shadcn/ui 生成物
├── components/                # 通用：Markdown、CodeBlock、DiffBlock、CopyIconButton、ConfirmDialog、EmptyState...
├── features/
│   ├── chat/                  # ChatView、MessageList、NodeList、AssistantMessage（含 TokenBadge）、
│   │                          #   ToolCallCard、ApprovalCard（含 ApprovalTakeover）、Composer（输入区）、
│   │                          #   ForkButton、ContextRing（§6）
│   ├── trajectory/            # TrajectoryView（turn/step 时间线）
│   ├── sessions/              # SessionList（搜索 + 行 hover "…" 菜单：重命名/分叉/归档/删除）
│   ├── agents/                # AgentPicker（侧栏智能体列表）
│   ├── sandbox/               # SandboxPanel（文件/预览双 tab + 刷新）、FilePreview（面板内文件预览）、previewUtils（取数/下载帮助函数）
│   └── admin/                 # 机构管理：AgentEditor（§8）、SkillManager、MemberManager、OrgUsage
├── platform/                  # 平台管理：Orgs、Users、Providers、Models、Settings、Usage、Audit
├── layouts/                   # WorkspaceLayout（三栏）、AdminLayout、PlatformLayout
└── lib/                       # utils、types（与 protocol.md 对齐的 TS 类型，单一事实源）
```

`src/lib/types.ts` 手写与 protocol.md/api.md 对齐的 TS 类型（事件词表、StreamChunk、帧、资源 DTO）——前后端契约以文档为准，类型从文档翻译。

## 3. 路由

| 路径 | 页面 | 守卫 |
|---|---|---|
| `/login` | 登录 | 已登录跳 `/` |
| `/` | 工作区（重定向到最近会话或空态） | 登录 |
| `/c/:sessionId` | 会话（聊天/轨迹 tab + 右侧面板） | owner（403 友好页） |
| `/admin/agents`、`/admin/agents/new`、`/admin/agents/:id` | 智能体管理/编辑器 | 机构 admin |
| `/admin/skills` | Skills 管理 | 机构 admin |
| `/admin/members` | 成员管理 | 机构 admin |
| `/admin/usage` | 机构用量 | 机构 admin |
| `/platform/orgs` `/platform/users` `/platform/providers` `/platform/models` `/platform/settings` `/platform/usage` `/platform/audit` | 平台后台 | 平台超管 |

机构上下文：URL 不含机构；当前机构存 localStorage + `identity` store，API 客户端统一注入 `X-Org-Id`。侧栏机构切换器（下拉展示 `auth/me` 的 orgs；平台管理机构在列表中标注"平台"，选中且角色 admin 时导航进 `/platform/*`）。

## 4. mux 客户端（src/mux/）

```ts
// 连接单例：鉴权、心跳响应、断线指数退避重连（1s→2s→…→30s，±20% 抖动）
class MuxConnection {
  connect(token: string): void
  openStream(endpoint: string, payload: unknown, handlers: StreamHandlers): string // 返回 streamId
  sendItem(streamId: string, value: unknown): void
  closeStream(streamId: string): void
}
interface StreamHandlers { onItem(v: unknown): void; onError(e: RpcError): void; onEnd(): void }
```

- 重连成功后**自动重放 open**（登记表记录每条流的 endpoint+payload），payload 由流处理器提供"最新 afterSeq"回调——follow 流据此实现 protocol.md §8.2。
- `session.follow` 处理器（`followSession(sessionId)`）封装完整状态机：snapshot 原子替换 → event 按 seq 追加（跳号触发 REST 补洞）→ assistant-stream 去重（attemptId+index ≥ nextIndex 才应用）→ tool-output 瞬态附加。对外只暴露一个 Zustand store 更新函数。
- `control` 处理器维护会话列表投影 store。

## 5. Zustand stores

```ts
useIdentityStore   // { user, orgs, currentOrgId, setOrg() }
useSessionListStore// { sessions: Record<id, SessionProjection>, order: id[], applyBaseline/applyUpsert/remove }
useSessionStore    // 当前会话窗口：
                   // { header, events: SessionEvent[], liveAttempt?: { chunks, nextIndex },
                   //   pendingApprovals: Record<callId, ApprovalAsked>, toolOutputs: Record<callId, string>,
                   //   followStatus: "connecting"|"live"|"stalled" }
useComposerStore   // { draft, approvalMode, sending, optimistic?: { requestId, content } }
useChatPresentationStore // 当前会话的手动展开状态：整轮 / 过程组 / 思考 / 工具详情；纯展示状态
```

装配规则（直播与回放一致的关键）：消息渲染节点从 `events`（持久）+ `liveAttempt`（瞬态）统一派生——`deriveConversationNodes(events, liveAttempt)` 输出节点数组（user / assistant / tool-call / approval / boundary），保留原始内容块顺序；流式节点携带真实 turn/step，助手块与工具调用标识在落库前后保持稳定，持久消息到达后不再重复投影同一步的 live 内容。ToolCallCard 既吃持久 `tool/call+tool/result` 也吃 liveAttempt 中的 tool-call 块 + `toolOutputs`。提交后瞬态丢弃，持久事件无缝接管。

## 6. 聊天视图（features/chat）

### 6.1 消息流

- **user**：右置浅蓝气泡（`bg-bubble` 浅 #E8F2FF / 深 #1D4077，配套 `text-bubble-foreground`，max-w 85%）；下方操作条：时间戳 + 复制。操作条最新一轮常驻，更早轮次 hover/focus-within 浮现（无 hover 设备常显）。
- **Turn 折叠**：用户消息始终可见；其后顶部耗时行作为整轮开关，完成态为"已工作 X"，运行态为"工作中 · 用时 X"，停止/失败态保留状态与耗时。最新 Turn 默认展开，完成不自动收起；下一条用户消息成功写入事件流后，上一轮按历史 Turn 默认收起。历史轮收起只展示最终回复及操作条；用户手动展开的轮次不被默认规则覆盖。切换聊天/轨迹、重连保留手动状态，切换会话或刷新恢复默认。无起始事件的历史片段不猜测耗时；中断/失败以最后一条正文兜底，缺少正文时展示结束状态。
- **过程组**：展开 Turn 后，全部回复正文按原序可见；正文之间连续的思考、工具与审批归入一个过程组，正文（包括同条助手消息里的正文块）切开过程组。混合组显示"思考与行动 · N 次工具调用"，纯工具组为"工具调用 · N 次"，单个思考直接复用思考折叠行，避免多一层开关。组默认收起，出错默认展开；待处理审批/提问强制展开所属整轮、过程组与任务卡。运行中收起行显示当前活动或最新思考尾部摘要；工具计数不递归计算子任务。整轮、过程组、思考与工具详情的手动状态独立保存。
- **assistant**：Markdown 流式渲染（结尾光标闪烁）；`reasoning` 默认收起单行，完成后为"✦ 思考 · 用时X"（时长取服务端 `durationMs`，缺省时仅显示"思考"）。未完成时显示"思考中"并 shimmer：收起状态只展示最新文本末尾、从左侧省略；展开状态最大高度 18rem、内部吸底跟随，用户上滚后暂停，回到底部恢复。正文/工具块出现后停止 shimmer。仅最终回复展示复制 / 分叉 / token / 时间戳操作条，显隐规则同用户气泡；usage 徽标（`↑in ↓out`，hover 展开 cached/reasoning 明细）；`interrupted` 标记"已停止"。
- **ToolCallCard**：默认折叠单行（工具图标 + 中文显示名（exec→执行 / read→读取 / write→写入 / edit→编辑 / glob→查找 / grep→搜索 / read_media→查看 / skill→技能 / task→任务 / question→提问，英文原名入 tooltip）+ 参数摘要；运行中摘要 shimmer、失败显示红色小叉图标（exit code 等细节留在展开态）；write/edit 有 diff 数据显示 +N -M），点击展开灰底卡，内容按 `tool/result.meta.render` 或 live 工具名映射：
  | render | 工具 | 展开内容 |
  |---|---|---|
  | terminal | exec | 灰卡：`$命令` 头（状态点按退出码绿/红）+ exit 码徽标 + 复制；stdout/stderr 滚动（live 期间接 tool-output 帧），超 2000 字截断"查看全部" |
  | diff | write/edit | 文件路径头 + unified diff 着色（新增绿/删除红） |
  | search | glob/grep | 查询词头 + 结果列表（path:line） |
  | read | read/read_media | 路径头 + 内容代码块（read_media 附带 `attachments` 引用时渲染图片缩略图，经附件 objectURL 通道加载） |
  | generic | skill/task | 参数 + 结果等宽文本 |
- **task 子轨迹**：task 卡折叠行显示"委派给 {subagent}：任务摘要"，收起时附"N 条嵌套轨迹"；展开后渲染 `parent_call_id` 相同的嵌套事件序列（递归用同一套节点渲染，层级缩进 + 左侧竖线）。
- **技能**：`skill` 与旧名 `activate_skill` 的摘要从参数 `name` 提取，统一显示"技能 · 名称"，较长名称截断，悬停可查看完整名称。
- **过程结束态**：运行摘要以所属 Turn 是否结束为准；已停止或失败的 Turn 即使存在未返回结果的工具，过程组也恢复静态摘要，不继续显示"正在执行"等活动文案。
- **审批**：顶层待审批不在消息流渲染小卡，由 composer 位的 ApprovalTakeover 接管（见 §6.2）；嵌套子智能体审批（task 卡内）保持消息流内嵌小卡：工具名 + 参数展示（exec 命令高亮 / write/edit 显示 diff）+ 「允许」「拒绝」按钮 + 10 分钟倒计时。已 decided 或 auto-deny 的审批显示结果态一行。点击调 REST 或 uplink（实现任选，协议等价）。
- **消息分叉**（M4）：助手消息操作条与嵌套用户消息操作条内有分叉按钮（GitFork 图标，tooltip "从这条消息分叉出新会话"）；点击调 `POST /sessions/{id}/fork {at_seq}`，成功后跳转新会话（标题自动带"（分叉）"）。侧栏会话行菜单另有"分叉会话"（从末尾分叉）。
- **滚动与流式指示**：自己刚发消息或已在底部时吸底跟随新内容；上翻阅读时不拉底；离底出"回到底部"浮钮。手动整轮展开/收起保持顶部开关所在位置；运行计时在 Turn 顶部原位更新，列表底部不重复显示。

### 6.2 Composer（输入区）

- 胶囊卡（rounded-[28px]、柔和投影）：多行输入自适应长高，约 14 行封顶后内部滚动；Enter 发送 / Shift+Enter 换行；右下角 34px 蓝圆发送钮，会话 running 且无草稿时变为**停止**（POST cancel）。
- 底行左侧：确认模式 chip 下拉（`严格：全部确认 / 默认：仅命令确认 / 自动：全部放行`，盾牌图标 + 描述，当前项加粗 + ✓）→ PATCH session。
- 底行右侧同一行依次展示：上下文环形进度条（ContextRing，见下）、模型下拉、推理等级下拉。两个下拉独立选择，无需进入二级菜单；模型列表取 `/orgs/{org}/models`，按供应商分组，模型数 >4 时顶部出搜索框。推理等级仅展示当前模型声明的档位，固定按 `none/minimal/low/medium/high/xhigh/max` 排列；不支持选档或会话已锁档时禁用等级下拉，模型仍沿用原有切换规则。
- 新对话草稿阶段允许选择推理等级：未选择时展示模型默认档（未配置则显示“默认”），显式选择仅保存在当前草稿本地状态，不发会话 PATCH；未配置模型默认档时，选择其它档位后仍可切回“默认”，清除本地选档，首条消息创建请求不带 `reasoning_effort`。显式选档则随 POST 创建会话一起提交，创建失败保留选择，切换智能体草稿后重置。草稿阶段切换模型与审批模式仍不可用；正式会话切回默认用 PATCH `reasoning_effort: ""`，沿用 `last_seq > 0` 锁档规则。
- 乐观对账：发送即插入乐观节点（`request_id`），事件帧到达后替换（protocol.md §8.4）。
- 会话 running 且来自**本会话**时不允许再发送（置灰 + 提示）；429/409 错误 toast。
- **ApprovalTakeover**：顶层待审批时整个胶囊位被审批卡接管——琥珀描边卡：「等待审批 + 倒计时」头 + 工具参数明细（exec 命令高亮 / write/edit diff）+ 「拒绝」「允许一次」按钮；Enter 允许 / Esc 拒绝。嵌套审批不触发 takeover；仅聊天标签页渲染（轨迹标签页不渲染）。
- **ContextRing**：模型选择器左侧的环形进度条，表示上下文占用（最近一次请求的 prompt tokens ÷ 模型 `context_size`；usage.input_tokens 即 OpenAI prompt_tokens，已含缓存命中；≥70% 转琥珀、≥90% 转红）。悬停出 HoverCard（popover 风格浅色卡片，非 Tooltip 黑块）：头部为「上下文 百分比 + 已用/上限 + 细进度条」（与圆环同色阈值），分隔线下为 label/value 对齐的统计行（对话轮/步、累计 tokens、缓存命中率、输入/输出），全部由会话事件投影在前端推导，无新增接口。tok 合计 = input + output + cache_write（缓存读为 input 子集，不重复计入）；命中率 = cache_read ÷ input_tokens。

### 6.3 轨迹视图（features/trajectory）

同一 store 数据的另一投影：按 `turn → step → (model request | tool calls)` 时间线展示；每节点显示 seq、时间、耗时（step 边界差值）、usage；消息正文与工具卡片默认折叠、点击展开；顶层待审批不在此渲染内嵌小卡（由聊天标签页的 ApprovalTakeover 处理），嵌套审批仍内嵌——用于调试 Agent 行为。tab 切换不重新取数。

### 6.4 云电脑面板（右侧，可折叠）

- 状态头：云电脑状态（无/创建中/运行中/已回收）+ 镜像名。
- 运行中时分 **文件 / 预览** 两个 tab（13px/500，选中蓝字 + 2px 下划线），两个 tab 常驻挂载、切换不丢状态。
- 文件 tab：工具行（/workspace + 刷新按钮）+ 文件树。文件树经 `GET /sessions/{id}/sandbox/files?path=` 懒加载目录；图标按格式区分（图片 Image / HTML FileCode2 / 其余 FileText）；当前预览的文件在树里保持选中高亮。
  - 手动刷新：工具行 RotateCw 图标钮（加载中转圈），重新拉取根目录并**保留展开态**——记录展开的目录路径集合，刷新后逐层重拉恢复（目录已消失或重拉失败的丢弃、折叠）；失败 toast 并保留旧树，刷新期间不清空旧树。
  - 自动刷新：`header.running` 由 true→false（agent 工作完成）时按同逻辑自动刷新一次，并静默重载预览 tab 当前打开的文件（保持预览/源码视图状态，二进制文件不重复触发下载）；沙箱状态变为 running 时做初始加载。
- 预览 tab：所有文件类型的查看统一在此（FilePreview 组件，原模态 FilePreviewDialog 已移除）。头部条为文件路径（等宽截断）+ 格式徽标 + 下载按钮 +（HTML/MD）预览/源码切换 + truncated「已截断」徽标；未选中文件时显示空态。点击文件树的文件自动切到预览 tab 并加载：
  - 图片（png/jpg/jpeg/webp/gif/svg）经 `sandbox/file?max_bytes=4194304` 取 blob 渲染 `<img object-contain>` 大图滚动区，blob 大小小于树节点 size 视为截断，提示"图片过大仅支持下载"。
  - HTML（html/htm）经 `<iframe sandbox="allow-scripts">` srcdoc 白底渲染；Markdown（md/markdown）用 `Markdown` 组件渲染；两者支持预览/源码切换，源码视图为高亮代码。
  - 其余文本/代码文件直接以 highlightCode 高亮展示；非 UTF-8 二进制文件直接触发下载并提示。
- 云电脑回收后提示"云电脑已回收，文件已清空"。

## 7. 侧栏（sessions + agents）

- 宽度 280px，背景 `bg-sidebar`（亮 #F8F8F8 / 暗 #1D1D1D）；支持折叠为 48px 窄栏（顶部展开按钮 + 底部头像 icon 变体用户菜单），折叠状态持久化 localStorage `wispjar-sidebar-collapsed`。工作台无全局顶栏——机构切换与用户菜单均收进侧栏，主内容区全高。
- 顶部：品牌行（Wispjar）→ 机构切换器行（variant=sidebar：全宽行，建筑图标 + 机构名 + ChevronsUpDown；下拉展示 `auth/me` 的 orgs，平台机构标注"平台"，选中且角色 admin 时可导航进 `/platform/*`）。
- 智能体列表（AgentPicker）：本机构 published 智能体；点击 → 跳转 `/c/new?agent=<id>` 草稿页（不立即创建会话），首条消息发送时才 POST 创建会话并紧接发 prompt，成功后 replace 跳 `/c/<id>`；创建失败停留在草稿页且输入内容不丢。工作区首页智能体卡片行为相同。
- 会话列表：搜索框（防抖调 REST）+ 行项：标题（自动生成中显示"新会话"）、running 呼吸点、紧凑相对时间（刚刚/N分/N时/N天/M月D日）；hover 浮现 "…" 菜单：重命名 / 分叉会话 / 归档 / 删除（删除需确认对话框）；悬停出深色 HoverCard（标题 / 时间 / 运行状态）。数据源 `control` 流投影。当前会话选中态 `bg-selected`。
- 底部：机构管理（admin 可见）、平台管理（超管可见）链接 + 用户菜单行（variant=sidebar：avatar + 显示名 + ChevronsUpDown，dropdown 向上弹出；外观（浅色/深色/跟随系统）/修改密码/退出登录）。机构/平台管理页（SecondaryLayout）保留自身页头（返回工作区 + 标题 + header 变体的机构切换与用户菜单）。

## 8. 智能体编辑器（features/admin/AgentEditor）

左侧分节导航、右侧表单，保存按钮底置固定：

1. **基本信息**：名称、描述（hint：将作为 task 委派时的能力说明）、头像（内置图标集选择）。
2. **模型**：下拉（`/orgs/{org}/models`，显示供应商/上下文长度）；参数：temperature 滑块（0-2）、max_tokens 数字。
3. **提示词**：等宽字体 textarea（行数自适应，支持全屏）。
4. **工具**：云电脑工具开关组（exec/read/write/edit/glob/grep/read_media，默认全开）。
5. **Skills**：本机构 skills 多选列表（名称+描述），可排序（拖拽）。
6. **子智能体**：本机构其他 published 智能体多选；保存时前端提示环/层级限制（服务端 409 校验为准）；hint 说明"通过 task 工具委派"。
7. **云电脑**：镜像下拉（`GET /orgs/{orgId}/admin/sandbox-settings` 的 images 只读展示）、资源覆盖（CPU/内存，留空用平台默认）。
8. **发布**：draft → 「发布」按钮（校验失败显示原因）；published → 「下线」；published 编辑后自动转 draft 并提示重新发布。

## 9. 机构/平台管理页（规格摘要）

- **SkillManager**：上传按钮（拖拽 zip，进度条）、列表（name/description/引用数/上传者/时间）、详情抽屉（frontmatter + 文件树 + 文件查看）、删除（被引用时禁用并提示）。
- **MemberManager**：表格 + 「添加成员」（输入 username，角色下拉）、角色修改、移出（二次确认）。
- **OrgUsage / PlatformUsage**：日期范围 + group_by 切换 + 表格 + 简单柱状图（recharts 或轻量自绘；token 三色系 input/output/cached）。
- **Providers**：卡片列表（类型徽标、base_url、密钥掩码、连通状态点、最近检查时间）+ 新建/编辑对话框（类型下拉动态字段：deepseek/qwen/ark/openai_compat 各自 base_url 默认值）+ 「测试连通」按钮。
- **Models**：按供应商分组的表格；上架对话框（model_code、显示名、能力复选：工具/推理/视觉/流式、上下文长度、默认参数 JSON）。勾选推理能力后可多选 `none/minimal/low/medium/high/xhigh/max` 并从已选档位中指定默认档（或“默认”，沿用提供方默认）；取消默认档的勾选时清空默认值。选择的是平台档位，调用时由供应商适配器转换（runtime.md §3）；各模型只勾选其支持的档位，空集表示不提供选档。
- **Settings**：云电脑分区（默认镜像、镜像列表编辑器、egress 策略编辑器：defaultAction 切换 + 规则表格 allow/deny×target、资源默认、TTL）。
- **Audit**：时间倒序表格（操作者/action/target/时间，detail 展开 JSON）。

## 10. 视觉与体验基线

- 主题：浅色默认 + 深色/跟随系统（Tailwind `class` 策略，localStorage `wispjar-theme`）。`primary` 浅 #3A83F7 / 深 #2C67C5，白色按钮内容；链接单独使用 `text-link`（浅 #2C67C5 / 深 #83B5FF）。完整语义色与变量格式见 docs/design/theme.md。
- 背景：主区白 / #181818，侧栏 #F8F8F8 / #1D1D1D，输入框独立 `bg-composer` 白 / #363636；用户消息为蓝色气泡并配套独立前景色。
- 对比度：主文字 #252525 / #EEEEEE，辅助文字 #666666 / #ABABAB，时间等元信息 #707070 / #999999；边框 #E7E7E7 / #353535，hover 与选中态使用独立灰阶，不再以低文字透明度区分内容层次。
- 字体：中文系统字体优先（PingFang SC / Microsoft YaHei UI / Microsoft YaHei / Noto Sans CJK SC / Source Han Sans SC，再回退平台默认）；等宽使用 SFMono-Regular / Consolas / Menlo / Liberation Mono。无外部字体下载。
- 排版：正文、用户消息、输入文本、思考摘要及详情、工具折叠行与参数/结果/代码、提问及选项统一 16px / 26px。侧栏“智能体”“会话”分组名与聊天名称同为 14px / 22px；通用菜单和按钮 14px，时间、工具耗时和统计 12px / 18px；管理表格保留 14px 密度。
- 间距与交互：居中 864px 阅读列，段落间距 12px；侧栏会话行高 36px；输入框圆角 28px、发送按钮 36px。阴影克制，焦点为 3px 软蓝 ring；输入框由外容器显示焦点，文本选区同色。
- 对齐：整轮、独立思考和过程组的前置图标与正文共用左边界，标题文字位于图标之后。“思考与行动／多次工具调用”组内节点保留一级 16px 缩进，内部思考、工具图标和工具卡外缘以此为基准，不额外叠加左内边距；独立思考详情与正文对齐，子任务仍通过竖线和缩进表达真实层级。
- 代码块：头部语言标签 + 复制钮；shiki 高亮按需加载语言。
- 反馈：toast 用 sonner，深色横幅、顶部居中、3s；tooltip 深色底。
- 空态：工作区首页 hero（"开始新的对话"标题 + 智能体卡片网格 + 最近会话列表）；无智能体时引导去管理页创建（admin）或联系管理员（member）。
- 加载：会话切换骨架屏；followStatus=stalled 时界面提示"连接已断开，重连中…"。
