# 前端详细设计

> 用户云电脑整体契约见 [computers.md §9](computers.md#9-实时协议与前端)，界面实现见本文“用户云电脑与持久文件”。所有会话使用 WorkspaceFiles 工作区文件面板，复用文件预览组件及 theme.md token。

> 本文是 `web/` 的权威设计。技术栈已定（决策 #11）：React 18 + Vite + TypeScript + Tailwind CSS + shadcn/ui + Zustand + React Router。界面语言中文。
> 交互对标 DeepSeek Harness 的产品形态（三栏布局、聊天/轨迹双视图、工具卡片、审批内嵌），但不照搬其自研 store/Slots/模块加载器。

所有刷新按钮（云电脑、个人智能体、个人存储、会话／存储文件面板）共用 `RefreshButton`：点击立即旋转图标，至少保留 600ms 的反馈，之后持续到请求结束；处理期间禁用重复点击并设置 `aria-busy`，失败后停止旋转并恢复按钮。后台轮询不触发云电脑页刷新按钮动画；减少动态效果偏好下仍保留本次请求的图标动画。云电脑异步操作数量显示为刷新按钮右上角不占布局空间的数字标记，完整处理提示收进 tooltip，并保留屏幕阅读器状态播报；操作开始／结束不增减页面提示行。

持久存储卡片在目录名右侧用与云电脑一致的状态圆点代替独立状态文字行：静态绿点为可用，蓝色波纹为等待／准备中，红点为缺失／异常，未创建为灰点；悬停／聚焦显示完整状态，辅助技术可读取状态名称，错误原因继续显示。

## 用户云电脑与持久文件

云电脑是用户级资源，前端通过电脑投影显示状态。REST DTO 与错误码见 [api.md §8](api.md#8-用户云电脑绑定卷与操作)。

| 界面 | 实现与交互 |
|---|---|
| `/personal/computers` 云电脑 | 左下角用户菜单进入「个人中心」，个人资源菜单使用「云电脑、智能体、存储」，不重复加「我的」。展开与折叠状态均可访问。标题栏右侧依次为状态圆点、启动／暂停图标和「…」菜单；运行中显示暂停，其余稳定状态显示启动，处理期间禁用，有活动任务或检查未完成时禁用暂停并用 tooltip 解释原因。移除独立操作按钮行。资源行按「CPU · 内存 · 镜像」排列，资源不收缩，镜像按 `/` 取最后一段，仍超长则省略；悬停／键盘聚焦显示完整镜像地址。提供创建、仅改名称、编辑后重建、强制停止、删除 |
| 云电脑图标 | 共用 `CloudComputerIcon`，采用圆润线框云和左侧短内弧线表现层次，使用 `text-foreground`：浅色主题为黑色，深色主题为浅色，不使用主题蓝 |
| 创建／编辑对话框 | 创建可选已发布 Agent 的镜像/资源模板，不复制其变量。电脑变量支持新增、替换、删除；既有值不回显，未编辑则保留。只改名称/变量直接保存，下一轮任务生效；镜像/资源变更才重建，变量补丁可随重建原子保存。创建/重建请求持有幂等键，修改输入后才更换 |
| 生命周期操作 | 202 只表示已接收，记录 operation_id 并轮询至 completed/failed。操作中的电脑禁用重复维护；失败保持可见并允许重试，不显示成功假象 |
| 活动任务 | 任务链接回对应会话；维护占用单独展示。失效租约仍视为占用，暂停／重建／删除禁用；服务器继续执行最终校验 |
| 强制停止 | 「…」菜单中的独立危险操作，即使存在活动任务也可进入确认；明确提示会终止这台电脑所有正常任务／子任务／维护、丢失系统临时层，保留三卷与绑定。完成后为未启动，下次启动创建新实例，中断任务不会自动续跑。提交 confirm_stop_all:true 和稳定幂等键；recovering 显示「正在强制停止」，禁用其他操作并显示后台重试状态，只有完成后才可重新启动 |
| 持久存储摘要 | 固定展示 `/storage`、`/agents`、`/sessions` 的状态和保留定位；未创建卷显示首次创建时准备，故障不当作空目录 |
| 智能体个人配置 | `/personal/agents` 标题为「智能体」，操作统一为「配置」，当前支持默认云电脑和清除绑定，后续其他个人配置在此扩展。侧栏每个智能体的「… → 云电脑设置」保留快捷入口；残留的下线 Agent 绑定仍列出以便清理。配置为当前用户、当前组织的个人设置，不修改已有会话 |
| 个人存储 | `/personal/storage` 复用现有会话文件接口和 FilePreview，固定 `scope=storage`。用户选择本人已有会话后点击「浏览文件」，先读 Detail 确认已绑定云电脑，再加载文件；不会创建会话或发送消息。支持会话分页和归档筛选。说明浏览会启动已暂停的电脑。复用上传、目录导航、预览、文件与目录下载，不提供后端尚未支持的新建目录。个人存储仍按 `(org,user)` 隔离；同一组织内本人电脑共享，其他成员不可见 |
| 新会话 | 草稿页不显示电脑选择器，首次发送不传显式 computer_id，由服务端解析个人绑定。不自动选第一台；缺少绑定时保留草稿，提示绑定并提供「前往设置」入口 |
| 已有会话 | 聊天主内容区移除整行云电脑状态／选择栏，保留原电脑绑定。聊天／轨迹切换采用 24px 高、12px 字号的紧凑按钮 |
| 会话文件面板 | 统一使用 `WorkspaceFiles`。顶部合并目录选择及上传、刷新、关闭图标按钮，下方合并返回按钮、可逐级点击的地址面包屑和当前目录打包下载；根目录禁用上级按钮，预览文件时返回所在文件列表。范围为当前会话、我的存储、智能体文件，所有请求传显式 scope 和相对 path。目录行也有独立打包入口；列表及预览标题共用常见文件类型图标，统一使用随深浅主题切换的中性辅助色，保留类型轮廓。文件面板使用 `FilePanel`：桌面拖拽左边界调整宽度，最小 320px、最大主内容区 60%，空间不足时缩小下限；支持方向键微调、双击恢复 420px，并用 localStorage 记忆宽度。窄屏使用右侧覆盖面板 |
| 输入与交付 | 上传到当前 scope 下打开的目录，默认根目录，不再固定 inputs；不能覆盖已存在文件。上传开始时固定目标目录，期间切换目录不改变目标或用旧目录结果覆盖新列表。会话根目录优先展示 outputs。“保存到我的存储”图标位于预览下载按钮旁，仅会话范围显示；采用明确目标相对路径，复制校验成功后才提示完成 |
| 预览／下载 | 复用 FilePreview；当前工作区完整下载通过后台任务获得限时凭证，再交由浏览器原生下载，不创建整个文件 Blob。目录包使用 ZIP64，准备期间显示可取消提示；完成后显示带“下载”操作的常驻通知，由用户点击启动浏览器下载，避免长时间准备后丢失浏览器用户激活状态。失败保留原因，不宣称已经下载完成，实际进度在浏览器查看。二进制工作文件不再点击即自动下载。图片文件以内联原图物理像素尺寸为上限（自然尺寸除以 max(1, devicePixelRatio)），同时限制宽度不超过面板可用宽度，超出等比缩小、小图不放大，长图允许纵向滚动；点击图片或“查看原图”进入已有缩放遮罩，支持原图 1:1、适应窗口与平移。文件预览时目录导航与文件标题合并为一行，面包屑包含文件名，右侧统一放置文件类型、预览模式、保存和下载操作；返回按钮回到所在目录。原始尺寸不会因面板拖宽自动放大，DPI 或浏览器缩放改变时重新计算。大文件限制和后端错误原样保留为失败 |
| 技能管理 | 管理员技能列表显示 revision，提供“更新包”；PUT multipart file/expected_revision；并发冲突要求刷新重选，成功提示任务结束后同步安装 |

`stores/computers.ts` 承接 control baseline 的 computers 与 computer/upsert/remove，按当前 org 及每项 org_id 校验投影。切换组织清空旧投影，迟到的旧请求不重新填入。电脑页还周期刷新 REST 状态和活动运行，用于显示操作、任务和卷摘要；WS 仍负责及时状态投影及重连覆盖。电脑配置只读取安全视图，不回显 env 值。

Agent 编辑器提供“仅保存环境变量”，调用独立 env 补丁接口，不提交提示词或切换发布状态；已有会话下一轮任务生效。exec 的变量优先级为单次命令 > 当前 Agent > 电脑；子 Agent 使用自己的变量。系统 env 提示为新建电脑默认值，不宣称重建会自动读取系统或 Agent 变量。

`session/computer`、`workspace/ready`、`runtime/environment` 更新会话 header 中的 computerId/workspaceDir/computerStatus。其他客户端切换电脑后，当前聊天重新读取 Detail，不需要用户手动刷新页面。技能更新等待、同步失败、电脑忙、卷缺失等错误转为可行动的中文提示，不自动取消正在运行的任务。

验收：`pnpm test` 覆盖默认绑定解析、不自动选机、活动／失效维护占用的控制禁用、跨组织迟到投影、幂等请求头、202 状态、409 错误、scope/inputs/跨卷保存与技能修订冲突；`pnpm build` 执行 TypeScript strict 与 Vite 构建。真实 HTTP、WS 权限与交互验收由独立测试实例完成，不以请求 mock 代替后端验收。

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
| 测试 | Node test runner + esbuild（关键状态、请求契约、权限边界和 mux 投影） |

开发代理：vite dev server `server.proxy` 把 `/api` 转发到本地 Go 服务 `:8080`。

### 1.1 品牌图形资源

应用默认使用三星瓶身 C 版：三颗星在 32px 画布上整体左移 1.2px，保留粗描边与单道瓶口线，适配侧栏和 favicon 等小尺寸。深浅主题使用同一几何造型。

| 用途 | 资源 |
|---|---|
| 应用 LOGO | `web/public/logo.svg`（浅色）、`web/public/logo-dark.svg`（深色）；`docs/logo.svg` 同步保留默认浅色版 |
| 浏览器与桌面图标 | `web/public/favicon.svg` 根据浏览器深浅配色切换；`favicon-32x32.png` 和 `apple-touch-icon.png` 使用同版图形，Apple 图标带白色背景 |
| 大尺寸备用图形 | [精灵细节版（浅色）](../logo-detailed.svg)、[精灵细节版（深色）](../logo-detailed-dark.svg)，保留精灵火焰、双星芒与玻璃高光，供大图展示使用 |

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
│   ├── sandbox/               # FilePreview（面板内文件预览）、previewUtils（取数/下载帮助函数）
│   └── admin/                 # 组织管理：AgentEditor（§8）、SkillManager、MemberManager、OrgUsage
├── platform/                  # 系统管理：Orgs、Users、Providers、Models、Settings、Usage、Audit
├── layouts/                   # WorkspaceLayout（共享侧栏外框）、SectionNavigation、个人/组织/系统内容布局
└── lib/                       # utils、types（与 protocol.md 对齐的 TS 类型，单一事实源）
```

`src/lib/types.ts` 手写与 protocol.md/api.md 对齐的 TS 类型（事件词表、StreamChunk、帧、资源 DTO）——前后端契约以文档为准，类型从文档翻译。

## 3. 路由

| 路径 | 页面 | 守卫 |
|---|---|---|
| `/login` | 登录 | 已登录跳 `/` |
| `/` | 工作区（重定向到最近会话或空态） | 登录 |
| `/c/:sessionId` | 会话（聊天/轨迹 tab + 右侧面板） | owner（403 友好页） |
| `/personal/computers`、`/personal/agents`、`/personal/storage` | 个人资源：云电脑、智能体个人配置、存储 | 登录；资源仍归当前组织及本人 |
| `/personal/account`、`/personal/appearance` | 账号与安全（复用改密接口）、外观偏好（浏览器本地） | 登录；账号级设置 |
| `/admin/agents`、`/admin/agents/new`、`/admin/agents/:id` | 智能体管理/编辑器 | 组织 admin |
| `/admin/skills` | Skills 管理 | 组织 admin |
| `/admin/members` | 成员管理 | 组织 admin |
| `/admin/usage` | 组织用量 | 组织 admin |
| `/platform/orgs` `/platform/users` `/platform/providers` `/platform/web-search` `/platform/models` `/platform/settings` `/platform/usage` `/platform/audit` | 系统后台 | 系统管理员 |

四个主界面共用现有侧栏外框：顶部品牌与折叠按钮，中间为当前界面的导航，底部固定用户菜单。工作区保留智能体及聊天列表布局；个人、组织、系统界面中间区域改用各自菜单。无全局顶部页签，主界面切换及组织切换均在左下角用户菜单；当前界面带选中标记，普通成员看不到组织管理，无系统管理员资格时看不到系统管理。独立改密路由 `/change-password` 保留强制改密流程；旧 `/computers` 跳转个人云电脑，`/computers#agent-bindings` 跳转个人智能体。

组织上下文：URL 不含组织；当前组织存 localStorage + `identity` store，API 客户端统一注入 `X-Org-Id`。用户菜单展示 `auth/me` 的 orgs；「系统管理」组织标注"系统"。系统管理入口根据系统组织 admin 成员资格显示，进入时前端切换到该组织，后台 API 与路由守卫的权限要求保持不变。返回工作区恢复原业务组织和会话位置。主动切换组织不得保留旧组织会话 ID；组织管理切到非管理员组织、系统管理切到业务组织时回工作区。位置按账号和组织隔离，文字草稿按账号、组织及会话/智能体保存在内存，避免进入个人设置后丢失；附件及浏览器刷新不承诺恢复。

已有登录态在页面启动及 token 变化时通过 `auth/me` 刷新用户和组织信息，使服务端名称修改在刷新页面后生效。请求结束时检查 token 与取消状态，退出或切换账号后的旧响应不得覆盖当前身份；网络失败保留缓存，认证失效仍由 API 客户端统一处理。

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
useComposerStore   // { draft, sending, optimistic?: { requestId, content } }
useChatPresentationStore // 当前会话的手动展开状态：整轮 / 过程组 / 思考 / 工具详情；纯展示状态
```

装配规则（直播与回放一致的关键）：消息渲染节点从 `events`（持久）+ `liveAttempt`（瞬态）统一派生——`deriveConversationNodes(events, liveAttempt)` 输出节点数组（user / assistant / tool-call / approval / boundary），保留原始内容块顺序；流式节点携带真实 turn/step，助手块与工具调用标识在落库前后保持稳定，持久消息到达后不再重复投影同一步的 live 内容。ToolCallCard 既吃持久 `tool/call+tool/result` 也吃 liveAttempt 中的 tool-call 块 + `toolOutputs`。提交后瞬态丢弃，持久事件无缝接管。

## 6. 聊天视图（features/chat）

已有聊天与新聊天的主内容区顶部栏均保留 1px 下边框，使用主题 `border` 色分隔标题栏和消息流；不依赖云电脑整栏提供分隔线。

### 6.1 消息流

- **工具过程组图标**："已使用工具 N 次"前显示 18px、描边 1.65 的 Waypoints 节点图标，沿用过程语义色与图标视觉中线校正；展开和收起均保留。运行中收起时改用当前活动的类型图标及动效。
- **Turn 分隔线**：整轮耗时折叠标题底部绘制 0.5px 灰色细实线，复用 divider 主题色，展开和收起时均可见；分隔线不占布局高度，不额外增加标题行高或上下间距。
- **正文图片**：智能体消息及文件 Markdown 预览共用图片展示规则：按原图物理像素 1:1 显示，小图不放大；原始像素上限除以 max(1, devicePixelRatio) 换算为 CSS 尺寸，再限制宽度不超过容器和 720px、高度不超过 480px 和视口高度的 60%，超限等比缩小，不裁切。DPI 或浏览器缩放变化时重新计算；例如 640×360 原图在像素比 1.25 时最多显示为 512×288 CSS 像素，实际占 640×360 屏幕像素。点击或键盘 Enter/空格打开深色遮罩查看完整图片，初始按原图 1:1 或适应窗口显示；遮罩提供放大、缩小、原图 1:1、适应窗口及百分比工具栏，支持鼠标滚轮以指针位置为中心缩放，缩放范围为 10%–800%（极长图允许缩至初始适应窗口比例），图片超出视口后可拖动平移且边界受限。滚轮不带动底层聊天滚动，拖动结束不误触关闭；支持 +/−、0、F 快捷键。每次重新打开恢复初始视图；支持关闭按钮、点击空白处和 Esc 关闭，关闭后焦点返回原图。带链接的图片点击优先打开图片预览。用户附件和 read_media 工具结果保留单图长边 240px、多图 64px 方形缩略图及既有遮罩行为；所有支持遮罩查看的图片使用 zoom-in 鼠标指针。
- **user**：右置蓝色气泡（`bg-bubble` 浅 #E8F2FF / 深 #1D4077，配套 `text-bubble-foreground`；两套主题均保留圆角 16px、上限 85%）；下方操作条：时间戳 + 复制。操作条最新一轮常驻，更早轮次 hover/focus-within 浮现（无 hover 设备常显）。
- **Turn 折叠**：用户消息始终可见；其后顶部耗时行作为整轮开关，完成态为"已工作 X"，运行态为"工作中 · X"，停止/失败态为"已停止 · X"／"运行出错 · X"。箭头紧跟文字右侧，不对齐内容区右边缘；仅运行中使用高对比度黑白灰扫光，完成后静态。最新 Turn 默认展开，完成不自动收起；下一条用户消息成功写入事件流后，上一轮按历史 Turn 默认收起，保留其结束状态与耗时。输入草稿不改变历史状态，新轮成功也不覆盖旧轮。历史轮收起只展示最终回复及操作条；用户手动展开的轮次不被默认规则覆盖。切换聊天/轨迹、重连保留手动状态，切换会话或刷新恢复默认。无起始事件的历史片段不猜测耗时；中断/失败以最后一条正文兜底，缺少正文时仅保留顶部状态。失败行以小红叉局部提示，原始错误默认隐藏，点击"查看详情"展开中性色详情及复制入口，随整轮收起；不在底部重复展示报错、停止徽标或分隔提示。
- **过程组**：展开 Turn 后，全部回复正文按原序可见；正文之间连续的思考、工具与审批归入一个过程组，正文（包括同条助手消息里的正文块）切开过程组。含工具的过程组统一显示"已使用工具 N 次"（数字两侧各留一个空格），纯思考组显示"思考"，箭头紧跟文字右侧；单个思考直接复用思考折叠行，避免多一层开关。组默认收起，工具报错不触发自动展开，标题保持普通颜色；待处理审批/提问强制展开所属整轮、过程组与任务卡。仅收起时显示当前活动或最新思考尾部摘要，展开后恢复稳定标题与工具计数；工具计数按本组调用次数计算，同一工具的重复调用分别计数，不递归计算子任务。整轮、过程组、思考与工具详情的手动状态独立保存。
- **assistant**：Markdown 流式渲染（结尾光标闪烁）；`reasoning` 默认收起单行，完成后显示思考图标及"思考 · X"（时长取服务端 `durationMs`，缺省时仅显示"思考"）。未完成时显示"思考中"并使用黑白灰 shimmer：收起状态只展示最新文本末尾、从左侧省略；展开后不重复摘要，正文最大高度 18rem、内部吸底跟随，用户上滚后暂停，回到底部恢复。正文/工具块出现或所属 Turn 结束后停止 shimmer。仅最终回复展示复制 / 从这里开始新聊天 / token / 时间戳操作条，显隐规则同用户气泡；操作条统一位于整轮内容末尾，中断/失败以较早正文兜底时仍排在后续工具过程之后，正文和工具保持原序，整轮收起后操作条仍可见；usage 徽标（`↑in ↓out`，hover 展开 cached/reasoning 明细）；顶层停止状态集中于 Turn 标题，嵌套轨迹保留 `interrupted` 徽标。
- **聊天副本历史**：从助手回复开始的新聊天可能未复制原轮次的 `turn/end`。聊天空闲或已有后续轮次时，这段历史按静态轮次展示，收起仍保留末尾最终回复及操作条；不补写结束事件，也不猜测耗时。当前仍运行的轮次保持流式展示规则。
- **ToolCallCard**：默认折叠单行（工具图标 + 中文显示名（exec→执行 / read→读取 / write→写入 / edit→编辑 / glob→查找 / grep→搜索 / read_media→查看 / skill→技能 / task→任务 / question→提问，英文原名入 tooltip）+ 参数摘要；运行中摘要 shimmer、失败显示红色小叉图标（exit code 等细节留在展开态）；write/edit 有 diff 数据显示 +N -M），点击展开灰底卡，内容按 `tool/result.meta.render` 或 live 工具名映射：
  | render | 工具 | 展开内容 |
  |---|---|---|
  | terminal | exec | 首行 cwd（缺省 `/sessions/<sid>`，长路径省略并提供 tooltip）+ 耗时 + exit code／运行状态；下方命令代码区、stdout、stderr 上下分区，各自复制，复制按钮左侧为自动换行开关（错误详情同样支持）。默认不换行、长行横向滚动；启用后保留原有换行缩进并折行超长文本，复制始终为原文。各分区独立记忆选择，收起、切换聊天/轨迹及重连保留，切换会话或刷新重置。仅渲染非空输出区，stderr 使用普通文字颜色。超过可见高度提供"展开全部"及区内滚动，不再按前端字符数截断；服务端截断另行提示。实时输出保留 stream 分流，完成后由持久结果接管 |
  | diff | write/edit | 文件路径头 + unified diff 着色（新增绿/删除红）。write 成功后按 `meta.operation` 显示"创建文件"或"覆盖文件"，未知时仍显示"写入"；有 diff 默认展示"变更"，可切到"完整内容"，无 diff 直接展示全文。全文取该次调用的 `content` 参数，保留空白及换行，支持完整复制、自动换行和展开滚动；变更视图也提供复制及自动换行。视图选择与换行状态在本会话内保留。空文件显示"内容为空"；未完成或失败时标为"拟写入内容"，失败另展示错误详情；参数不完整时显示接收中或内容不可用提示。edit 保持 diff 展示 |
  | search | glob/grep | 查询词头 + 结果列表（path:line） |
  | read | read/read_media | 路径头 + 内容代码块（read_media 附带 `attachments` 引用时渲染图片缩略图，经附件 objectURL 通道加载） |
  | generic | skill/task | JSON 参数使用支持 CJK 回退的等宽字体，普通结果使用正文系统字体 |
- **task 子轨迹**：task 卡折叠行显示"委派给 {subagent}：任务摘要"，收起时附"N 条嵌套轨迹"；展开后渲染 `parent_call_id` 相同的嵌套事件序列（递归用同一套节点渲染，层级缩进 + 左侧竖线）。
- **技能**：`skill` 与旧名 `activate_skill` 的摘要从参数 `name` 提取，统一显示"技能 · 名称"，较长名称截断，悬停可查看完整名称。
- **提问记录**：展开后按组展示只读问答，每组用“问／答”文字标记区分角色；问题使用次要文字色，实际答案使用正文前景色与中等字重，答标记用柔和主色底／link 色点缀。多组之间以中性细线分隔；保留换行，长文本可折行。已跳过维持灰色状态，待回答维持琥珀色，均不显示已作答的蓝色标记。迟到补答消息复用同一展示，深浅主题一致。
- **过程结束态**：运行摘要以所属 Turn 是否结束为准；Turn 结束后停止思考、正文与未返回结果工具的动画，未完成工具显示灰色"已中止"，覆盖子任务。失败调用保留普通命令／参数摘要，右侧无圆圈小红叉提示，具体错误展开查看；取消引起的错误显示中止状态。
- **审批**：顶层待审批不在消息流渲染小卡，由 composer 位的 ApprovalTakeover 接管（见 §6.2）；嵌套子智能体审批（task 卡内）保持消息流内嵌小卡：工具名 + 参数展示（exec 命令高亮 / write/edit 显示 diff）+ 「允许」「拒绝」按钮 + 10 分钟倒计时。已 decided 或 auto-deny 的审批显示结果态一行。点击调 REST 或 uplink（实现任选，协议等价）。
- **聊天副本**（M4）：助手消息操作条与嵌套用户消息操作条内使用 GitFork 图标，tooltip／无障碍名称为"从这里开始新聊天"；点击先弹出确认对话框，说明将保留截至当前消息的记录。标题默认填入原标题加"（副本）"，可编辑（最长 256 个 Unicode 字符，清空则使用默认标题）；提供取消和"创建新聊天"按钮。确认后调 `POST /sessions/{id}/fork {at_seq,title}`，提交期间禁止重复操作，失败保留输入并允许重试，成功提示"已创建聊天副本"并跳转新聊天。服务端提交后广播 control `upsert`，侧栏聊天列表自动更新。侧栏菜单使用相同 GitFork 图标和"创建聊天副本"文案（复制到末尾），复用确认与标题输入对话框。接口及代码中的 fork 名称保留。
- **滚动与流式指示**：自己刚发消息或已在底部时吸底跟随新内容；上翻阅读时不拉底；离底出"回到底部"浮钮。手动整轮展开/收起保持顶部开关所在位置；运行计时在 Turn 顶部原位更新，列表底部不重复显示。

### 6.2 Composer（输入区）

- 胶囊卡（rounded-[24px]、极轻投影）：聚焦增强中性边框对比度，阴影保持不变，不展示光晕；浅色边框黑色 .09 → .24，深色边框白色 .06 → .20。深色输入区背景使用 #242424，略亮于内容区。多行输入自适应长高，约 14 行封顶后内部滚动；Enter 发送 / Shift+Enter 换行；右下角 36px 蓝色圆形发送钮，会话 running 且无草稿时变为**停止**（POST cancel）。
- 底行左侧仅保留图片附件入口，不显示严格／默认／自动审批等级选择。新会话沿用服务端默认 `auto`（自动，适配云电脑），工具直接执行；后端三种审批模式、PATCH `approval_mode`、审批事件与决定接口以及前端审批卡片均保留，已有会话的审批配置继续生效。
- 底行右侧同一行依次展示：上下文环形进度条（ContextRing，见下）、模型下拉、推理等级下拉。两个下拉独立选择，无需进入二级菜单；模型列表取 `/orgs/{org}/models`，按供应商分组，模型数 >4 时顶部出搜索框。推理等级仅展示当前模型声明的档位，固定按 `none/minimal/low/medium/high/xhigh/max` 排列；不支持选档或会话已锁档时禁用等级下拉，模型和推理等级均在首条用户消息被服务端接受后锁定，改为无下拉箭头的只读标签（不降低为禁用透明度），键盘聚焦或悬停提示“对话开始后不可更改，请新建对话”。
- 新对话草稿阶段允许选择模型及推理等级，默认沿用智能体模型，换模型时重置为新模型默认档；模型 ID 与档位随 POST 创建一起提交：未选择时展示模型默认档（未配置则显示“默认”），显式选择仅保存在当前草稿本地状态，不发会话 PATCH；未配置模型默认档时，选择其它档位后仍可切回“默认”，清除本地选档，首条消息创建请求不带 `reasoning_effort`。显式选档则随 POST 创建会话一起提交，创建失败保留选择，切换智能体草稿后重置。正式会话切回默认用 PATCH `reasoning_effort: ""`，按完整历史是否已有 `user/message` 锁定，不受标题、配置事件或历史分页影响；follow 快照用 `modelSettingsLocked`，收到首条用户消息后立即锁定，服务端接受发送后至事件到达前也保持锁定。
- 乐观对账：发送即插入乐观节点（`request_id`），事件帧到达后替换（protocol.md §8.4）。
- 会话 running 且来自**本会话**时不允许再发送（置灰 + 提示）；429/409 错误 toast。
- **ApprovalTakeover**：顶层待审批时整个胶囊位被审批卡接管——琥珀描边卡：「等待审批 + 倒计时」头 + 工具参数明细（exec 命令高亮 / write/edit diff）+ 「拒绝」「允许一次」按钮；Enter 允许 / Esc 拒绝。嵌套审批不触发 takeover；仅聊天标签页渲染（轨迹标签页不渲染）。
- **QuestionTakeover**：提问占用同一输入区位置，沿用输入框中性细边框和极轻投影；标题栏使用中性灰底、次要文字及灰色状态点，选中项使用中性边框和选中底色，仅勾选标记与主要操作按钮保留蓝色。“其他”内嵌输入不显示独立焦点 ring，聚焦时仅增强所在选项外框；选项键盘焦点使用中性边框与底色，自由回答文本域同样使用中性焦点边框。保留单选／多选、推荐标记、翻题和跳过／提交行为。卡片内输入框或详情区域 Enter 下一题、末题提交；Esc 跳过当前题（末题按原跳过逻辑提交），Alt+← 返回上一题并保留草稿，首题不移动；底部常显快捷键提示，上一题按钮提供提示。按钮保留原生 Enter 激活，选项用 Enter／空格选择；自由回答 Shift+Enter 换行，输入法组合输入、长按连发和提交中不触发快捷操作，卡片外不拦截。
- **ContextRing**：模型选择器左侧的环形进度条，表示上下文占用（最近一次请求的 prompt tokens ÷ 模型 `context_size`；usage.input_tokens 即 OpenAI prompt_tokens，已含缓存命中；≥70% 转琥珀、≥90% 转红）。悬停出 HoverCard（随深浅主题切换的 popover 风格统计卡片）：宽 256px，不超出视口左右各 8px 的边界，保持现有字号；头部为「上下文 百分比 + 已用/上限 + 细进度条」（与圆环同色阈值），分隔线下为 label/value 对齐的统计行（对话轮/步、累计 tokens、缓存命中率、输入、输出）。标签左对齐，数值右对齐、使用等宽数字且不换行；输入和输出各占一行、保留完整千分位数值，累计 tokens 使用 k/M 简写。全部由会话事件投影在前端推导，无新增接口。tok 合计 = input + output + cache_write（缓存读为 input 子集，不重复计入）；命中率 = cache_read ÷ input_tokens。

### 6.3 轨迹视图（features/trajectory）

同一 store 数据的另一投影：按 `turn → step → (model request | tool calls)` 时间线展示；每节点显示 seq、时间、耗时（step 边界差值）、usage；消息正文与工具卡片默认折叠、点击展开；顶层待审批不在此渲染内嵌小卡（由聊天标签页的 ApprovalTakeover 处理），嵌套审批仍内嵌——用于调试 Agent 行为。tab 切换不重新取数。

### 6.4 云电脑面板（右侧，可折叠）

- 状态头：云电脑标题旁使用 6px 状态圆点，运行中为主题主色，创建中同色轻微呼吸（减少动态效果设置下静止），未创建/已暂停/已回收为灰色。圆点交互区域为 20px，悬浮或键盘聚焦显示主题适配的完整状态提示（如“云电脑：运行中”）；不常驻显示状态文字。镜像名保留。
- 运行中时分 **文件 / 预览** 两个 tab（13px/500，选中蓝字 + 2px 下划线），两个 tab 常驻挂载、切换不丢状态。
- 文件 tab：工具行（当前 scope 目录 + 刷新按钮）+ 文件树。文件树经 `GET /sessions/{id}/files?scope=workspace&path=` 懒加载目录；图标按格式区分（图片 Image / HTML FileCode2 / 其余 FileText）；当前预览的文件在树里保持选中高亮。
  - 手动刷新：工具行 RotateCw 图标钮（加载中转圈），重新拉取根目录并**保留展开态**——记录展开的目录路径集合，刷新后逐层重拉恢复（目录已消失或重拉失败的丢弃、折叠）；失败 toast 并保留旧树，刷新期间不清空旧树。
  - 自动刷新：`header.running` 由 true→false（agent 工作完成）时按同逻辑自动刷新一次，并静默重载预览 tab 当前打开的文件（保持预览/源码视图状态，二进制文件不重复触发下载）；沙箱状态变为 running 时做初始加载。
- 预览 tab：所有文件类型的查看统一在此（FilePreview 组件，原模态 FilePreviewDialog 已移除）。头部条为文件路径（等宽截断）+ 格式徽标 + 下载按钮 +（HTML/MD）预览/源码切换 + truncated「已截断」徽标；未选中文件时显示空态。点击文件树的文件自动切到预览 tab 并加载：
  - 图片（png/jpg/jpeg/webp/gif/svg）经 `file?scope=workspace&max_bytes=4194304` 取 blob 渲染 `<img object-contain>` 大图滚动区，blob 大小小于树节点 size 视为截断，提示"图片过大仅支持下载"。
  - HTML（html/htm）经 `<iframe sandbox="allow-scripts">` srcdoc 白底渲染；Markdown（md/markdown）用 `Markdown` 组件渲染；两者支持预览/源码切换，源码视图为高亮代码。
  - 其余文本/代码文件直接以 highlightCode 高亮展示；非 UTF-8 二进制文件直接触发下载并提示。
- 云电脑回收后提示"云电脑已回收，文件已清空"。

## 7. 侧栏（sessions + agents）

- 宽度 240px，背景 `bg-sidebar`（亮 #FBFAF9 / 暗 #181817）；支持折叠为 48px 窄栏（顶部展开按钮 + 底部头像 icon 变体用户菜单），折叠状态持久化 localStorage `wispjar-sidebar-collapsed`。工作台无全局顶栏——组织切换与用户菜单均收进侧栏，主内容区保持原有直角与贴边布局。
- 顶部：品牌行（Wispjar）→ 组织切换器行（variant=sidebar：全宽行，建筑图标 + 组织名 + ChevronsUpDown；下拉展示 `auth/me` 的 orgs，系统组织标注"系统"，选中且角色 admin 时可导航进 `/platform/*`）。
- 智能体列表（AgentPicker）：本组织 published 智能体；点击 → 跳转 `/c/new?agent=<id>` 草稿页（不立即创建会话），首条消息发送时才 POST 创建会话并紧接发 prompt，成功后 replace 跳 `/c/<id>`；创建失败停留在草稿页且输入内容不丢。工作区首页智能体卡片行为相同。
- 侧栏智能体默认最多显示 5 个，超出时显示灰色「展开更多」，每次追加 5 个；展开后可「收起」至 5 个。智能体区最高占侧栏内容区的 45%，超出时区内滚动，为聊天保留空间。
- 聊天列表：搜索框（防抖调 REST，"搜索聊天"）+ 行项：标题（自动生成中显示"新聊天"）、running 呼吸点、紧凑相对时间（刚刚/N分/N时/N天/M月D日）；hover 浮现 "…" 菜单：重命名 / 创建聊天副本 / 归档 / 删除（删除需确认对话框）；悬停出随主题切换的 HoverCard（标题 / 时间 / 运行状态，浅色浅底深字、深色深灰底浅字）。数据源 `control` 流投影。当前聊天选中态 `bg-selected`。界面可见文案统一称"聊天"；技术领域与 API 保留 session／会话命名。HTML 文档标题为 `WispJar`。
- 聊天默认最多显示 10 个活动会话，超出时显示灰色「展开更多」；点击后在独立滚动区内每次追加 10 个，接近底部自动续页，保留「加载更多」供键盘操作。普通列表基于完整 `control` 投影分批渲染；搜索保留 300ms 防抖，按 REST `next_cursor` 每页 10 条加载、按 ID 去重，切换查询或组织丢弃旧响应。搜索加载中、失败重试和全部加载完成均有提示。「收起」恢复前 10 个并回到列表顶部；修改搜索或切换组织重置展开状态。品牌、搜索框和底部用户菜单不随聊天滚动。
- 底部：组织管理（admin 可见）、系统管理（系统管理员可见）链接 + 用户菜单行（variant=sidebar：avatar + 显示名 + ChevronsUpDown，dropdown 向上弹出；外观（浅色/深色/跟随系统）/修改密码/退出登录）。组织管理/系统管理页（SecondaryLayout）保留自身页头（返回工作区 + 标题 + header 变体的组织切换与用户菜单）。

## 8. 智能体编辑器（features/admin/AgentEditor）

左侧分节导航、右侧表单，保存按钮底置固定：

1. **基本信息**：名称、描述（hint：将作为 task 委派时的能力说明）、头像（内置图标集选择）。
2. **模型**：下拉（`/orgs/{org}/models`，显示供应商/上下文长度）；参数：temperature 滑块（0-2）、max_tokens 数字。
3. **提示词**：等宽字体 textarea（行数自适应，支持全屏）。
4. **工具**：云电脑工具开关组（exec/read/write/edit/glob/grep/read_media，默认全开）；另设联网搜索开关（默认关闭）和搜索实例选择器，支持跟随全局默认或指定实例，见 [search.md](search.md)。
5. **Skills**：本组织 skills 多选列表（名称+描述），可排序（拖拽）。
6. **子智能体**：本组织其他 published 智能体多选；保存时前端提示环/层级限制（服务端 409 校验为准）；hint 说明"通过 task 工具委派"。
7. **云电脑**：镜像下拉（`GET /orgs/{orgId}/admin/sandbox-settings` 的 images 只读展示）、资源覆盖（CPU/内存，留空用系统默认）。
8. **发布**：draft → 「发布」按钮（校验失败显示原因）；published → 「下线」；published 编辑后自动转 draft 并提示重新发布。

## 9. 组织管理/系统管理页（规格摘要）

- **SkillManager**：上传按钮（拖拽 zip，进度条）、列表（name/description/引用数/上传者/时间）、详情抽屉（frontmatter + 文件树 + 文件查看）、删除（被引用时禁用并提示）。
- **MemberManager**：表格 + 「添加成员」（输入 username，角色下拉）、角色修改、移出（二次确认）。
- **OrgUsage / PlatformUsage**：日期范围 + group_by 切换 + 表格 + 简单柱状图（recharts 或轻量自绘；token 三色系 input/output/cached）。
- **Providers**：卡片列表（类型徽标、base_url、密钥掩码、连通状态点、最近检查时间）+ 新建/编辑对话框（类型下拉动态字段：deepseek/qwen/ark/openai_compat 各自 base_url 默认值）+ 「测试连通」按钮。
- **Models**：按供应商分组的表格；上架对话框（model_code、显示名、能力复选：工具/推理/视觉/流式、上下文长度、默认参数 JSON）。勾选推理能力后可多选 `none/minimal/low/medium/high/xhigh/max` 并从已选档位中指定默认档（或“默认”，沿用提供方默认）；取消默认档的勾选时清空默认值。选择的是系统档位，调用时由供应商适配器转换（runtime.md §3）；各模型只勾选其支持的档位，空集表示不提供选档。
- **Settings**：云电脑分区（默认镜像、镜像列表编辑器、egress 策略编辑器：defaultAction 切换 + 规则表格 allow/deny×target、资源默认、TTL）。
- **Audit**：时间倒序表格（操作者/action/target/时间，detail 展开 JSON）。

## 10. 视觉与体验基线

模型编辑增加 API 协议选择（Chat Completions / Responses）和“工具读图”能力；仅在 Responses、视觉、工具调用均启用时允许工具读图。提供方未知或未验证时不默认开启。读图结果仍从系统附件接口预览，不向浏览器暴露供应商 file_id。

- 侧栏、主内容区及思考／工具等原生滚动区域：细灰色滚动条，滚动时出现，停滚 500ms 后以 150ms 淡出；悬停滚动条边缘或拖动时持续可见，预留稳定 gutter，显隐不造成内容横向跳动。减少动态效果模式停用扫光与淡出过渡。

- 主题：浅色默认 + 深色/跟随系统（Tailwind `class` 策略，localStorage `wispjar-theme`）。`primary` 浅 #3A83F7 / 深 #2C67C5，白色按钮内容；链接单独使用 `text-link`（浅 #2C67C5 / 深 #83B5FF）。完整语义色与变量格式见 docs/design/theme.md。
- 背景：主区白 / #121212，侧栏 #FBFAF9 / #181817，输入框独立 `bg-composer` 白 / #1F1F1F；两套主题的蓝色用户气泡与原有文字颜色保持。菜单白 / #292929，使用不透明背景。
- 对比度：界面主文字浅黑 .9 / 深白 .84，辅助 .6 / .56、次要 .45 / .42；助手正文使用独立 `--text-body`，浅黑 .9 / 深 #EEEEEE，深色正文不随界面文字调整。边框浅黑 .09 / 深白 .1。
- 字体：按平台系统字体优先（-apple-system / BlinkMacSystemFont / Segoe UI / system-ui，后续中文回退见 theme.md）；等宽西文使用 SFMono-Regular / Consolas / Menlo / Liberation Mono，CJK 回退中文系统字体。侧栏 WISPJAR 字标使用 Clash Display Semibold（600）、18px、0.04em 字距的 SVG 轮廓，随主题变色。无外部字体下载，不额外强制字体平滑设置。
- 排版：正文、用户消息、思考摘要及详情、工具折叠行与参数/结果/代码、提问及选项为 16px / 26px；消息输入文本为 16px / 24px。侧栏分组与聊天名称为 14px / 20px；通用菜单和按钮 14px，时间、工具耗时和统计 12px / 18px；管理表格保留 14px 密度。
- 间距与交互：正文容器 752px（含左右各 16px，正文上限 720px），输入区上限 800px（卡片 768px）；段落间距 16px，H2 为 20px / 32px、上 24px 下 16px，粗体与标题为 600 字重。侧栏会话行高 36px；输入框圆角 24px、发送按钮 36px。菜单外缘 16px、菜单项 10px、内边距 8px，36px 行高。普通控件键盘焦点为 3px 软蓝 ring；消息输入框及提问内嵌输入由外容器中性边框提示，菜单容器不叠加焦点 ring，菜单项和选择项使用底色提示焦点。
- 对齐：整轮、独立思考和过程组的前置图标与正文共用左边界，标题文字位于图标之后。过程组展开内容不缩进，内部思考、工具图标和工具卡外缘与正文左边界对齐，不额外叠加左内边距；独立思考详情与正文对齐，子任务仍通过竖线和缩进表达真实层级。
- 代码块：头部语言标签 + 32px 复制钮，工具栏至少 40px 高，留出上下间距；shiki 高亮按需加载语言。Markdown 表格不设独立工具栏，32px 复制钮放在最后一列表头右侧，与列标题同行且保留间距，不增加数据列；列标题保持单行，宽表格连同复制钮横向滚动。表格支持独立复制 Markdown 源文，保留对齐、链接及行内格式，去除外层引用／列表缩进，复制成功复用勾选反馈。
- 反馈：toast 用 sonner，深色横幅、顶部居中、3s；tooltip 深色底。
- 空态：工作区首页 hero（"开始新的对话"标题 + 智能体卡片网格 + 最近会话列表）；无智能体时引导去管理页创建（admin）或联系管理员（member）。
- 加载：会话切换骨架屏；followStatus=stalled 时界面提示"连接已断开，重连中…"。

### 聊天过程视觉与动效

整轮、过程组、思考与工具详情共用轻量折叠动效：仅手动点击的层级播放高度变化与透明度过渡，展开 200ms、收起 140ms，ease-out，箭头同步旋转。收起结束后移除详情内容，期间隐藏其键盘及读屏入口；最终回复保持挂载与原有顺序。首次加载、切换会话、自动展开审批及流式内容增长直接呈现，祖先展开不触发子层动画。手动折叠操作暂停外层吸底跟随并保持点击行位置，用户滚动或点击「回到底部」后恢复相应跟随行为。系统开启减少动态效果时立即切换；快速反向点击从当前显示高度继续。

思考、工具摘要、展开普通输出及过程折叠标题使用独立过程语义色，输入占位符使用独立 placeholder 色，色值见 theme.md。类型图标 18px（思考为圆润细线灯泡、技能为魔杖＋星星），灯泡使用加宽灯罩与短灯颈，`viewBox=0 0 24 24`、描边 1.65，继承过程图标颜色，折叠箭头 14px。共用 `ActivityIcon` 以 SVG 内部元素播放语义动画：思考灯泡轮廓保持稳定，活动时灯丝与内部柔光以 2.4s 周期同步呼吸，静止时隐藏柔光并保留灯丝，搜索沿小圆弧移动，读取文字逐行扫描，写入／编辑笔尖往复，执行命令提示符轻推与光标闪烁，任务机器人双眼交替亮起，技能星星错峰闪烁，图片轮廓与光点明暗变化。运行图标提亮，文字保留弱扫光；待审批、待回答及结束后图标静止。减少动态效果时灯泡与其它类型图标均保留轻微活动动画以指示运行状态，文字扫光静止并保持过程文字色；动作周期与幅度见 theme.md。运行中的过程组、整轮折叠后在可见摘要行显示当前阶段的类型图标，避免内部图标隐藏后失去运行反馈。上下文圆环使用独立轨道色，深色 #858585，普通进度使用 link 色，0% 时轨道也清晰可见。聊天普通内容（含思考、工具摘要、路径、普通输出）统一正文系统字体；代码、命令、JSON 参数及 diff 保留西文等宽字体，CJK 明确回退到苹方、微软雅黑及其它中文系统字体；数字以 tabular-nums 对齐。

过程行使用浅黑 .6 / 深白 .56，过程次要信息使用浅黑 .45 / 深白 .42，思考及工具标题使用常规字重；hover/focus-visible 时统一增强至浅黑 .9 / 深白 .84，详情正文和语义状态色保持原层级。


## 网络搜索界面

系统管理新增独立「网络搜索」菜单 `/platform/web-search`，原供应商菜单标为「模型供应商」。网络搜索页管理同类型多实例和全局默认；智能体工具区配置开关及实例选择。`ToolCallCard` 新增 `web_search` 渲染，展示来源链接、域名、日期、短摘要、数量、耗时和截断提示；严格区分失败、取消、空结果。完整交互见 [search.md §5](search.md#5-ui-与验收)。

网页读取：同一网络搜索菜单增加“搜索 / 网页读取”标签和独立默认；智能体独立读取开关/选择器；工具卡片增加 web_fetch，预览屏蔽外部图片和原始 HTML。完整交互见 [search.md §7](search.md#7-网页读取000010)。

网页读取工具图标：浏览器窗口轮廓＋两条正文横线；执行中两条横线以 1.8s 周期交替明暗、第二行错相 -.9s，外框固定；系统启用减少动态效果时仍保留这一轻微运行提示，待审批与结束后静止。使用现有工具图标尺寸与主题色。

工具图标共享 18px 图标盒，以相对定位向下校正 1px，贴合中文文字的视觉中线；校正不占用 transform，搜索移动及正文扫描继续使用原动画。
