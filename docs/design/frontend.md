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
- 快照 `hasMore=true` 时自动用 REST events 从 seq=1 向前分页补齐快照之前的历史，每页最多 500 条，完成后与当前实时窗口一次合并；保留补载期间到达的事件、流式状态和工具输出，并基于合并事件重算待审批/提问。切换会话或重连快照使旧请求失效。聊天与轨迹共用完整事件；补载中显示提示，失败保留现有内容并提供重试，不把尾部窗口当作完整历史。
- `control` 处理器维护会话列表投影 store。

## 5. Zustand stores

```ts
useIdentityStore   // { user, orgs, currentOrgId, setOrg() }
useSessionListStore// { sessions: Record<id, SessionProjection>, order: id[], applyBaseline/applyUpsert/remove }
useSessionStore    // 当前会话窗口：
                   // { header, events: SessionEvent[], liveAttempt?: { chunks, nextIndex },
                   //   pendingApprovals: Record<callId, ApprovalAsked>, toolOutputs: Record<callId, { stdout, stderr }>,
                   //   followStatus: "connecting"|"live"|"stalled" }
useComposerStore   // { draft, approvalMode, sending, optimistic?: { requestId, content } }
useChatPresentationStore // 当前会话的手动展开状态：整轮 / 过程组 / 思考 / 工具详情；纯展示状态
```

装配规则（直播与回放一致的关键）：消息渲染节点从 `events`（持久）+ `liveAttempt`（瞬态）统一派生——`deriveConversationNodes(events, liveAttempt)` 输出节点数组（user / assistant / tool-call / approval / boundary），保留原始内容块顺序；流式节点携带真实 turn/step，助手块与工具调用标识在落库前后保持稳定，持久消息到达后不再重复投影同一步的 live 内容。ToolCallCard 既吃持久 `tool/call+tool/result` 也吃 liveAttempt 中的 tool-call 块 + `toolOutputs`。提交后瞬态丢弃，持久事件无缝接管。

## 6. 聊天视图（features/chat）

### 6.1 消息流

- **user**：右置浅蓝气泡（`bg-bubble` 浅 #E8F2FF / 深 #1D4077，配套 `text-bubble-foreground`，max-w 85%）；下方操作条：时间戳 + 复制。操作条最新一轮常驻，更早轮次 hover/focus-within 浮现（无 hover 设备常显）。
- **Turn 折叠**：用户消息始终可见；其后顶部耗时行作为整轮开关，完成态为"已工作 X"，运行态为"工作中 · X"，停止/失败态为"已停止 · X"／"运行出错 · X"。箭头紧跟文字右侧，不对齐内容区右边缘；仅运行中使用高对比度黑白灰扫光，完成后静态。最新 Turn 默认展开，完成不自动收起；下一条用户消息成功写入事件流后，上一轮按历史 Turn 默认收起，保留其结束状态与耗时。输入草稿不改变历史状态，新轮成功也不覆盖旧轮。历史轮收起只展示最终回复及操作条；用户手动展开的轮次不被默认规则覆盖。切换聊天/轨迹、重连保留手动状态，切换会话或刷新恢复默认。无起始事件的历史片段不猜测耗时；中断/失败以最后一条正文兜底，缺少正文时仅保留顶部状态。失败行以小红叉局部提示，原始错误默认隐藏，点击"查看详情"展开中性色详情及复制入口，随整轮收起；不在底部重复展示报错、停止徽标或分隔提示。
- **过程组**：展开 Turn 后，全部回复正文按原序可见；正文之间连续的思考、工具与审批归入一个过程组，正文（包括同条助手消息里的正文块）切开过程组。含工具的过程组统一显示"行动 · N次工具使用"（数字与中文之间不加空格，中点两侧由布局留出间距），纯思考组显示"思考"，箭头紧跟文字右侧；单个思考直接复用思考折叠行，避免多一层开关。组默认收起，工具报错不触发自动展开，标题保持普通颜色；待处理审批/提问强制展开所属整轮、过程组与任务卡。仅收起时显示当前活动或最新思考尾部摘要，展开后恢复稳定标题与工具计数；工具计数按本组调用次数计算，同一工具的重复调用分别计数，不递归计算子任务。整轮、过程组、思考与工具详情的手动状态独立保存。
- **assistant**：Markdown 流式渲染（结尾光标闪烁）；`reasoning` 默认收起单行，完成后为"✦ 思考 · X"（时长取服务端 `durationMs`，缺省时仅显示"思考"）。未完成时显示"思考中"并使用黑白灰 shimmer：收起状态只展示最新文本末尾、从左侧省略；展开后不重复摘要，正文最大高度 18rem、内部吸底跟随，用户上滚后暂停，回到底部恢复。正文/工具块出现或所属 Turn 结束后停止 shimmer。仅最终回复展示复制 / 从这里开始新聊天 / token / 时间戳操作条，显隐规则同用户气泡；usage 徽标（`↑in ↓out`，hover 展开 cached/reasoning 明细）；顶层停止状态集中于 Turn 标题，嵌套轨迹保留 `interrupted` 徽标。
- **聊天副本历史**：从助手回复开始的新聊天可能未复制原轮次的 `turn/end`。聊天空闲或已有后续轮次时，这段历史按静态轮次展示，收起仍保留末尾最终回复及操作条；不补写结束事件，也不猜测耗时。当前仍运行的轮次保持流式展示规则。
- **ToolCallCard**：默认折叠单行（工具图标 + 中文显示名（exec→执行 / read→读取 / write→写入 / edit→编辑 / glob→查找 / grep→搜索 / read_media→查看 / skill→技能 / task→任务 / question→提问，英文原名入 tooltip）+ 参数摘要；运行中摘要 shimmer、失败显示红色小叉图标（exit code 等细节留在展开态）；write/edit 有 diff 数据显示 +N -M），点击展开灰底卡，内容按 `tool/result.meta.render` 或 live 工具名映射：
  | render | 工具 | 展开内容 |
  |---|---|---|
  | terminal | exec | 首行 cwd（缺省 `/workspace`，长路径省略并提供 tooltip）+ 耗时 + exit code／运行状态；下方命令代码区、stdout、stderr 上下分区，各自复制，复制按钮左侧为自动换行开关（错误详情同样支持）。默认不换行、长行横向滚动；启用后保留原有换行缩进并折行超长文本，复制始终为原文。各分区独立记忆选择，收起、切换聊天/轨迹及重连保留，切换会话或刷新重置。仅渲染非空输出区，stderr 使用普通文字颜色。超过可见高度提供"展开全部"及区内滚动，不再按前端字符数截断；服务端截断另行提示。实时输出保留 stream 分流，完成后由持久结果接管 |
  | diff | write/edit | 文件路径头 + unified diff 着色（新增绿/删除红）。write 成功后按 `meta.operation` 显示"创建文件"或"覆盖文件"，未知时仍显示"写入"；有 diff 默认展示"变更"，可切到"完整内容"，无 diff 直接展示全文。全文取该次调用的 `content` 参数，保留空白及换行，支持完整复制、自动换行和展开滚动；变更视图也提供复制及自动换行。视图选择与换行状态在本会话内保留。空文件显示"内容为空"；未完成或失败时标为"拟写入内容"，失败另展示错误详情；参数不完整时显示接收中或内容不可用提示。edit 保持 diff 展示 |
  | search | glob/grep | 查询词头 + 结果列表（path:line） |
  | read | read/read_media | 路径头 + 内容代码块（read_media 附带 `attachments` 引用时渲染图片缩略图，经附件 objectURL 通道加载） |
  | generic | skill/task | JSON 参数使用支持 CJK 回退的等宽字体，普通结果使用正文系统字体 |
- **task 子轨迹**：task 卡折叠行显示"委派给 {subagent}：任务摘要"，收起时附"N 条嵌套轨迹"；展开后渲染 `parent_call_id` 相同的嵌套事件序列（递归用同一套节点渲染，层级缩进 + 左侧竖线）。
- **技能**：`skill` 与旧名 `activate_skill` 的摘要从参数 `name` 提取，统一显示"技能 · 名称"，较长名称截断，悬停可查看完整名称。
- **过程结束态**：运行摘要以所属 Turn 是否结束为准；Turn 结束后停止思考、正文与未返回结果工具的动画，未完成工具显示灰色"已中止"，覆盖子任务。失败调用保留普通命令／参数摘要，右侧无圆圈小红叉提示，具体错误展开查看；取消引起的错误显示中止状态。
- **审批**：顶层待审批不在消息流渲染小卡，由 composer 位的 ApprovalTakeover 接管（见 §6.2）；嵌套子智能体审批（task 卡内）保持消息流内嵌小卡：工具名 + 参数展示（exec 命令高亮 / write/edit 显示 diff）+ 「允许」「拒绝」按钮 + 10 分钟倒计时。已 decided 或 auto-deny 的审批显示结果态一行。点击调 REST 或 uplink（实现任选，协议等价）。
- **聊天副本**（M4）：助手消息操作条与嵌套用户消息操作条内使用 CopyPlus 图标，tooltip／无障碍名称为"从这里开始新聊天"；点击调 `POST /sessions/{id}/fork {at_seq}`，复制截至当前事件的记录，成功提示"已创建聊天副本"并跳转新聊天（标题自动带"（副本）"）。侧栏菜单使用"创建聊天副本"（复制到末尾）。接口及代码中的 fork 名称保留。
- **滚动与流式指示**：自己刚发消息或已在底部时吸底跟随新内容；上翻阅读时不拉底；离底出"回到底部"浮钮。手动整轮展开/收起保持顶部开关所在位置；运行计时在 Turn 顶部原位更新，列表底部不重复显示。

### 6.2 Composer（输入区）

- 胶囊卡（rounded-[28px]、柔和投影）：聚焦仅轻微加深中性阴影，不展示蓝色边框／光晕；深色主题适度增强灰色边缘。多行输入自适应长高，约 14 行封顶后内部滚动；Enter 发送 / Shift+Enter 换行；右下角 34px 蓝圆发送钮，会话 running 且无草稿时变为**停止**（POST cancel）。
- 底行左侧：确认模式 chip 下拉（`严格：全部确认 / 默认：仅命令确认 / 自动：全部放行`，盾牌图标 + 描述，当前项加粗 + ✓）→ PATCH session。
- 底行右侧同一行依次展示：上下文环形进度条（ContextRing，见下）、模型下拉、推理等级下拉。两个下拉独立选择，无需进入二级菜单；模型列表取 `/orgs/{org}/models`，按供应商分组，模型数 >4 时顶部出搜索框。推理等级仅展示当前模型声明的档位，固定按 `none/minimal/low/medium/high/xhigh/max` 排列；不支持选档或会话已锁档时禁用等级下拉，模型和推理等级均在首条用户消息被服务端接受后锁定，改为无下拉箭头的只读标签（不降低为禁用透明度），键盘聚焦或悬停提示“对话开始后不可更改，请新建对话”。
- 新对话草稿阶段允许选择模型及推理等级，默认沿用智能体模型，换模型时重置为新模型默认档；模型 ID 与档位随 POST 创建一起提交：未选择时展示模型默认档（未配置则显示“默认”），显式选择仅保存在当前草稿本地状态，不发会话 PATCH；未配置模型默认档时，选择其它档位后仍可切回“默认”，清除本地选档，首条消息创建请求不带 `reasoning_effort`。显式选档则随 POST 创建会话一起提交，创建失败保留选择，切换智能体草稿后重置。草稿阶段审批模式仍不可用；正式会话切回默认用 PATCH `reasoning_effort: ""`，按完整历史是否已有 `user/message` 锁定，不受标题、配置事件或历史分页影响；follow 快照用 `modelSettingsLocked`，收到首条用户消息后立即锁定，服务端接受发送后至事件到达前也保持锁定。
- 乐观对账：发送即插入乐观节点（`request_id`），事件帧到达后替换（protocol.md §8.4）。
- 会话 running 且来自**本会话**时不允许再发送（置灰 + 提示）；429/409 错误 toast。
- **ApprovalTakeover**：顶层待审批时整个胶囊位被审批卡接管——琥珀描边卡：「等待审批 + 倒计时」头 + 工具参数明细（exec 命令高亮 / write/edit diff）+ 「拒绝」「允许一次」按钮；Enter 允许 / Esc 拒绝。嵌套审批不触发 takeover；仅聊天标签页渲染（轨迹标签页不渲染）。
- **ContextRing**：模型选择器左侧的环形进度条，表示上下文占用（最近一次请求的 prompt tokens ÷ 模型 `context_size`；usage.input_tokens 即 OpenAI prompt_tokens，已含缓存命中；≥70% 转琥珀、≥90% 转红）。悬停出 HoverCard（随深浅主题切换的 popover 风格统计卡片）：宽 256px，不超出视口左右各 8px 的边界，保持现有字号；头部为「上下文 百分比 + 已用/上限 + 细进度条」（与圆环同色阈值），分隔线下为 label/value 对齐的统计行（对话轮/步、累计 tokens、缓存命中率、输入、输出）。标签左对齐，数值右对齐、使用等宽数字且不换行；输入和输出各占一行、保留完整千分位数值，累计 tokens 使用 k/M 简写。全部由会话事件投影在前端推导，无新增接口。tok 合计 = input + output + cache_write（缓存读为 input 子集，不重复计入）；命中率 = cache_read ÷ input_tokens。

### 6.3 轨迹视图（features/trajectory）

同一 store 数据的另一投影：按 `turn → step → (model request | tool calls)` 时间线展示；每节点显示 seq、时间、耗时（step 边界差值）、usage；消息正文与工具卡片默认折叠、点击展开；顶层待审批不在此渲染内嵌小卡（由聊天标签页的 ApprovalTakeover 处理），嵌套审批仍内嵌——用于调试 Agent 行为。tab 切换不重新取数。

### 6.4 云电脑面板（右侧，可折叠）

- 状态头：云电脑标题旁使用 6px 状态圆点，运行中为主题主色，创建中同色轻微呼吸（减少动态效果设置下静止），未创建/已暂停/已回收为灰色。圆点交互区域为 20px，悬浮或键盘聚焦显示主题适配的完整状态提示（如“云电脑：运行中”）；不常驻显示状态文字。镜像名保留。
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
- 聊天列表：搜索框（防抖调 REST，"搜索聊天"）+ 行项：标题（自动生成中显示"新聊天"）、running 呼吸点、紧凑相对时间（刚刚/N分/N时/N天/M月D日）；hover 浮现 "…" 菜单：重命名 / 创建聊天副本 / 归档 / 删除（删除需确认对话框）；悬停出随主题切换的 HoverCard（标题 / 时间 / 运行状态，浅色浅底深字、深色深灰底浅字）。数据源 `control` 流投影。当前聊天选中态 `bg-selected`。界面可见文案统一称"聊天"；技术领域与 API 保留 session／会话命名。HTML 文档标题为 `WispJar`。
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

- 侧栏、主内容区及思考／工具等原生滚动区域：细灰色滚动条，滚动时出现，停滚 500ms 后以 150ms 淡出；悬停滚动条边缘或拖动时持续可见，预留稳定 gutter，显隐不造成内容横向跳动。减少动态效果模式停用扫光与淡出过渡。

- 主题：浅色默认 + 深色/跟随系统（Tailwind `class` 策略，localStorage `wispjar-theme`）。`primary` 浅 #3A83F7 / 深 #2C67C5，白色按钮内容；链接单独使用 `text-link`（浅 #2C67C5 / 深 #83B5FF）。完整语义色与变量格式见 docs/design/theme.md。
- 背景：主区白 / #181818，侧栏 #F8F8F8 / #1D1D1D，输入框独立 `bg-composer` 白 / #363636；用户消息为蓝色气泡并配套独立前景色。
- 对比度：主文字 #252525 / #EEEEEE，辅助文字 #666666 / #ABABAB，时间等元信息 #707070 / #999999；边框 #E7E7E7 / #353535，hover 与选中态使用独立灰阶，不再以低文字透明度区分内容层次。
- 字体：中文系统字体优先（PingFang SC / Microsoft YaHei UI / Microsoft YaHei / Noto Sans CJK SC / Source Han Sans SC，再回退平台默认）；等宽西文使用 SFMono-Regular / Consolas / Menlo / Liberation Mono，CJK 回退 PingFang SC / Microsoft YaHei UI / Microsoft YaHei / Noto Sans CJK SC / Source Han Sans SC。无外部字体下载。
- 排版：正文、用户消息、输入文本、思考摘要及详情、工具折叠行与参数/结果/代码、提问及选项统一 16px / 26px。侧栏“智能体”“会话”分组名与聊天名称同为 14px / 22px；通用菜单和按钮 14px，时间、工具耗时和统计 12px / 18px；管理表格保留 14px 密度。
- 间距与交互：居中 864px 阅读列，段落间距 12px；侧栏会话行高 36px；输入框圆角 28px、发送按钮 36px。阴影克制，焦点为 3px 软蓝 ring；输入框由外容器显示焦点，文本选区同色。
- 对齐：整轮、独立思考和过程组的前置图标与正文共用左边界，标题文字位于图标之后。“思考与行动／多次工具调用”组内节点保留一级 16px 缩进，内部思考、工具图标和工具卡外缘以此为基准，不额外叠加左内边距；独立思考详情与正文对齐，子任务仍通过竖线和缩进表达真实层级。
- 代码块：头部语言标签 + 32px 复制钮，工具栏至少 40px 高，留出上下间距；shiki 高亮按需加载语言。Markdown 表格不设独立工具栏，32px 复制钮放在最后一列表头右侧，与列标题同行且保留间距，不增加数据列；列标题保持单行，宽表格连同复制钮横向滚动。表格支持独立复制 Markdown 源文，保留对齐、链接及行内格式，去除外层引用／列表缩进，复制成功复用勾选反馈。
- 反馈：toast 用 sonner，深色横幅、顶部居中、3s；tooltip 深色底。
- 空态：工作区首页 hero（"开始新的对话"标题 + 智能体卡片网格 + 最近会话列表）；无智能体时引导去管理页创建（admin）或联系管理员（member）。
- 加载：会话切换骨架屏；followStatus=stalled 时界面提示"连接已断开，重连中…"。

### 聊天过程视觉与动效

整轮、过程组、思考与工具详情共用轻量折叠动效：仅手动点击的层级播放高度变化与透明度过渡，展开 200ms、收起 140ms，ease-out，箭头同步旋转。收起结束后移除详情内容，期间隐藏其键盘及读屏入口；最终回复保持挂载与原有顺序。首次加载、切换会话、自动展开审批及流式内容增长直接呈现，祖先展开不触发子层动画。手动折叠操作暂停外层吸底跟随并保持点击行位置，用户滚动或点击「回到底部」后恢复相应跟随行为。系统开启减少动态效果时立即切换；快速反向点击从当前显示高度继续。

思考、工具摘要、展开普通输出及过程折叠标题使用独立过程语义色，输入占位符使用独立 placeholder 色，色值见 theme.md。类型图标 18px（思考 Brain、技能魔杖＋星星），折叠箭头 14px。执行时图标轻微呼吸、技能星星错峰闪烁，文字弱扫光；待审批、待回答及结束后图标静止。减少动态效果时图标只保留缓慢透明度变化，取消缩放，文字扫光静止并保持过程文字色。运行中的过程组、整轮折叠后在可见摘要行显示当前阶段的类型图标，避免内部图标隐藏后失去运行反馈。上下文圆环使用独立轨道色，深色 #858585，普通进度使用 link 色，0% 时轨道也清晰可见。聊天普通内容（含思考、工具摘要、路径、普通输出）统一正文系统字体；代码、命令、JSON 参数及 diff 保留西文等宽字体，CJK 明确回退到苹方、微软雅黑及其它中文系统字体；数字以 tabular-nums 对齐。

过程行在浅色主题参考 #666666 中灰，思考及工具标题使用常规字重；整轮、过程组、思考和工具折叠按钮 hover/focus-visible 时，文字、摘要、图标及箭头统一增强对比度（浅色 #3D3D3D，深色 #C8C8C8），详情正文和语义状态色保持原层级。
