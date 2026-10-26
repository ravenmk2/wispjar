# 界面主题设计 Token

本规范对应 `web/src/index.css` 与 `web/tailwind.config.js`。界面按 Kimi 的系统字体顺序、排版密度与中性灰阶分层对齐；深色助手正文、两套主题的蓝色用户气泡及发送／停止按钮保留项目现状。`:root` 定义浅色，`.dark` 覆盖深色。

## 1. 字体与排版

正文与通用界面使用本机字体，不下载外部字体文件。平台系统字体优先，中文按字体栈回退：

- 正文：`-apple-system, BlinkMacSystemFont, Segoe UI, system-ui, Roboto, Noto Sans, Ubuntu, Cantarell, Helvetica Neue, Arial, PingFang SC, Source Han Sans SC, Microsoft YaHei UI, Microsoft YaHei, Noto Sans CJK SC, sans-serif`。通用 `sans-serif` 放在末尾，避免在显式中文字体之前提前回退；平台系统字体仍优先。
- 等宽：`SFMono-Regular, Consolas, Menlo, Liberation Mono, PingFang SC, Microsoft YaHei UI, Microsoft YaHei, Noto Sans CJK SC, Source Han Sans SC, monospace`；西文优先等宽，CJK 明确回退到苹方、微软雅黑等中文字体。
- 侧栏品牌字标：Clash Display Semibold（600），18px，字距 0.04em。`WispjarWordmark` 使用转为轮廓的 SVG 字标（字体来源：[Fontshare](https://www.fontshare.com/fonts/clash-display)，ITF FFL），以 `currentColor` 跟随深浅主题；不分发字体文件。

| 内容 | 字号 / 行高 | 实现 |
|---|---|---|
| 侧栏 WISPJAR 品牌字标 | 18px 字号等比轮廓 | `WispjarWordmark`，SVG 保留原字体度量 |
| 聊天正文、用户气泡 | 16px / 26px | `text-chat` |
| 消息输入文本与占位符 | 16px / 24px | `text-chat leading-6` |
| 思考摘要及详情、过程组与工具折叠行 | 16px / 26px | `text-chat`，以辅助色区分层级 |
| 工具参数、输出、搜索结果、diff、代码块及行内代码 | 16px / 26px | 普通内容与行内代码使用正文系统字体；代码块、命令和 diff 使用支持 CJK 回退的等宽字体 |
| 提问、选项及解释、已回答内容 | 16px / 26px | `text-chat` |
| 侧栏“智能体”“聊天”及聊天名称 | 14px / 20px | `text-sm leading-5`，分组以辅助色与间距区分 |
| 通用菜单、按钮、表单及管理表格 | 14px / 20px | `text-sm`，保留管理页密度 |
| 时间、工具耗时、token 统计与状态徽标 | 12px / 18px | `text-meta` |
| 辅助提示 | 13px / 20px | `text-xs` |

body 基准 14px；聊天正文使用 `--chat-font-size: 16px`、`--chat-line-height: 26px`，输入框为 16px / 24px。Markdown H1 为 24px / 36px，H2 为 20px / 32px，H3 为 18px / 28px，H4 为 16px / 26px；标题与粗体使用 600 字重，正文为 400。段落间距 16px，标题上 24px、下 16px。表格不缩小字号，宽内容在表格或代码块内横向滚动。

文字抗锯齿沿用浏览器与操作系统默认值，不全局强制 `-webkit-font-smoothing: antialiased` 或 `-moz-osx-font-smoothing: grayscale`；这些非标准属性主要作用于 macOS，不能用来改善 Windows 字体渲染。阅读区不通过缩放、文字描边或阴影模拟清晰度。字体回退调整后的实际观感需在目标系统与显示缩放下检查，不能仅凭 CSS 配置认定更清晰。

## 2. 语义颜色

个人头像在侧栏展开／折叠／资料卡片中分别为 36／32／48px，使用圆形裁切容器与 `UserRound` 默认图标（20／20／24px）。保留 1px 中性边框，浅色为黑色 15% 不透明度，深色为白色 20%；底色沿用 `muted`。侧栏显示名称为 14px 中等字重，下一行 `@用户名` 为 `text-meta`（12px / 18px）辅助色；用户行不放上下箭头。

下表展示最终颜色；`background/card/primary/ring/secondary/muted/input/destructive` 在 CSS 中存 HSL 分量，其他变量存完整颜色。Tailwind 按相应格式消费，不能混用。

| CSS 变量 | 浅色 | 深色 | 用途 |
|---|---|---|---|
| `--background` | #FFFFFF | #121212 | 主阅读区 |
| `--sidebar` | #FBFAF9 | #181817 | 侧栏与工作区外层 |
| `--card` | #FFFFFF | #292929 | 卡片 |
| `--composer` | #FFFFFF | #242424 | 输入框独立背景，深色略亮于内容区 |
| `--composer-border` | rgba(0,0,0,.09) | rgba(255,255,255,.06) | 输入框边缘 |
| `--composer-border-focus` | rgba(0,0,0,.24) | rgba(255,255,255,.20) | 输入框聚焦边缘 |
| `--context-track` | #C9C9C9 | #858585 | 上下文统计圆环与进度条轨道，0% 时仍可见 |
| `--popover` | #FFFFFF | #292929 | 不透明菜单与弹窗 |
| `--secondary` | #F5F5F5 | #1F1F1F | 次级填充 |
| `--muted` | #F5F5F5 | #1F1F1F | 工具详情与代码弱填充 |
| `--foreground` | rgba(0,0,0,.9) | rgba(255,255,255,.84) | 通用界面文字 |
| `--text-body` | rgba(0,0,0,.9) | #EEEEEE | 助手正文独立色，深色保留原色 |
| `--text-secondary` | rgba(0,0,0,.6) | rgba(255,255,255,.56) | 通用辅助说明 |
| `--text-process` | rgba(0,0,0,.6) | rgba(255,255,255,.56) | 思考、工具、过程标题及展开普通文本 |
| `--text-process-hover` | rgba(0,0,0,.9) | rgba(255,255,255,.84) | 可交互过程行 hover/focus 时增强对比度 |
| `--text-process-muted` | rgba(0,0,0,.45) | rgba(255,255,255,.42) | 过程摘要、耗时及次要信息 |
| `--text-placeholder` | rgba(0,0,0,.45) | rgba(255,255,255,.42) | 回答输入框占位符 |
| `--composer-placeholder` | rgba(0,0,0,.45) | rgba(255,255,255,.42) | 消息输入框占位符与运行提示 |
| `--text-tertiary` | rgba(0,0,0,.45) | rgba(255,255,255,.42) | 分组、时间与元信息；--text-caption 同值 |
| `--accent` | #F0F0F0 | rgba(255,255,255,.05) | hover |
| `--selected` | rgba(0,0,0,.05) | rgba(255,255,255,.1) | 侧栏选中 |
| `--border` | rgba(0,0,0,.09) | rgba(255,255,255,.1) | 普通边框 |
| `--divider` | rgba(0,0,0,.09) | rgba(255,255,255,.1) | 内部分隔 |
| `--input` | 0 0% 90% | 0 0% 20% | 通用输入控件边框 |
| `--bubble` | #E8F2FF | #1D4077 | 用户消息底色，两套主题保留原色 |
| `--bubble-foreground` | #18375D | #F1F6FF | 用户消息文字，两套主题保留原色 |
| `--primary` / `--ring` | #3A83F7 | #2C67C5 | 主按钮与交互强调 |
| `--primary-hover` | #2E76E8 | #3575D8 | 主按钮 hover |
| `--primary-active` | #2668D2 | #2458AD | 主按钮按下 |
| `--primary-foreground` | #FFFFFF | #FFFFFF | 主按钮内容 |
| `--primary-soft` | #EAF2FF | #20334F | 信息徽章柔和蓝底，搭配 `text-link` |
| `--link` | #2C67C5 | #83B5FF | 链接及文字强调，与实色按钮分开 |
| `--ring-focus` | rgba(58,131,247,.2) | rgba(44,103,197,.2) | 焦点与文本选区 |
| `--destructive` | 353 100% 61% | 354 100% 64% | 错误与危险 |
| `--success` | #16C456 | #16C456 | 成功 |
| `--warning` | #FF9500 | #FF9F0A | 警告 |
| `--tooltip-bg` | #FFFFFF | #292929 | Tooltip 与 HoverCard |
| `--toast-bg` | #2B2B2B | #404040 | Toast 深底提示 |

组织管理、系统管理的主按钮统一使用 `primary`／`primary-hover`／`primary-active`，以启用态为基准，禁用态保留透明度区分。已发布、管理员等信息徽章统一使用 `bg-primary-soft text-link border-primary/20`，不使用实心主按钮填充或无操作的 hover 反馈；成功、警告、危险等语义状态保持各自颜色。云电脑状态采用 6px 圆点，运行/创建使用 `bg-primary`，其他状态使用 `bg-fg-tertiary`，20px 悬浮与键盘聚焦区域通过 Tooltip 展示状态文字。文字链接在深色主题使用更亮的同系蓝以保证可读性。Tooltip 与聊天列表悬浮信息卡随主题切换底色、文字、边框与阴影，内部时间／状态／空闲圆点使用主题辅助色，不固定白字。

界面以黑／白的分层 alpha 色表达主次，不在整块容器上叠加低 opacity；助手正文通过独立 `--text-body` 保留深色 #EEEEEE，避免界面主色调整连带改变正文。禁用控件及按需浮现的操作条仍保留禁用、hover、focus 状态。用户气泡必须配套使用 `text-bubble-foreground`；链接使用 `text-link`。

## 3. 布局、形状与交互

- 文件列表及预览标题的文件夹与文件类型图标统一使用 `text-muted-foreground` 中性辅助色，随深浅主题切换；保留各类型轮廓，不以彩色区分扩展名。
- 阅读区居中；正文容器上限 752px（`max-w-chat-body`，含左右各 16px，实际正文上限 720px），输入框和通用聊天容器上限 800px（`max-w-chat`，实际输入卡上限 768px）。
- 侧栏展开宽 240px、折叠宽 48px；会话与智能体行高 36px，分组间距 24px。标题与列表名称同字号。
- 聊天输入框圆角 24px，模型与推理等级在同一行展示，首条消息前独立选择，首条消息被服务端接受后改为无下拉箭头的只读标签；发送／停止按钮保留 36px 蓝色圆形与白色图标。
- 思考、工具及过程组折叠行最小高度 36px；文字 26px 行高，不使用固定高度挤压文本。
- 手动折叠使用高度与透明度过渡：展开 200ms、收起 140ms，`cubic-bezier(0, 0, 0.2, 1)`，箭头同步旋转 90°；只作用于点击的层级，无位移和缩放。初始、自动及流式更新不播放折叠动画；系统减少动态效果时直接切换。
- 静态整轮标题文字左边缘与正文对齐，工具过程组前置 18px、描边 1.65 的 Waypoints 节点图标，与正文左边界对齐；运行时收起的折叠行显示当前阶段图标，折叠箭头紧跟文字右侧，不推到容器右边缘。独立思考和工具保留前置 18px 类型图标，折叠箭头保持 14px；思考使用圆润细线灯泡，技能（skill/activate_skill）使用魔杖和星星。过程组展开内容不缩进，内部思考和工具图标与正文左边界对齐，不再叠加节点自身的展示性左内边距；工具详情卡外缘与正文左边界对齐，卡内保留内容内边距。独立思考详情与正文对齐，子任务轨迹保留真实层级缩进。
- 基础圆角 `--radius: .75rem`；菜单外缘 16px、菜单项 10px、内边距 8px。两套主题的用户气泡均保留圆角 16px、内边距 10px 16px、宽度上限 85%。工作区主内容区保持原有直角与贴边布局。
- 输入阴影：浅色 `0 4px 12px rgba(0,0,0,.03), 0 5px 16px -4px rgba(0,0,0,.07)`；深色仅保留后项。聚焦使用 `0 5px 18px -4px rgba(0,0,0,.13)`，过渡 150ms。
- 菜单使用 0.5px 内描边（浅黑 .13／深白 .12）与 `0 4px 16px rgba(0,0,0,.1)` 阴影。
- 普通控件键盘焦点为 3px 蓝色软 ring；聊天输入框仅保留 `0 1px 4px rgba(0,0,0,.03)` 极轻投影，聚焦时阴影不变，仅通过 `--composer-border-focus` 稍微增强中性边框对比度（150ms 过渡），textarea 不叠加内框或光晕。
- 登录、首次登录强制改密及个人中心改密表单的自动填充输入沿用 Card 背景和主题正文／光标色；兼容 `:autofill` 与 `:-webkit-autofill`，用内阴影覆盖浏览器高亮，键盘聚焦时同时保留 3px 软 ring。不禁用浏览器自动填充或修改密码管理器语义。
- 全局焦点 ring 排除菜单、子菜单、列表框及其选项、自动获焦的详情 group 和标记 `data-focus-ring="none"` 的内嵌控件，避免覆盖菜单投影或叠出粗蓝框；菜单项沿用中性焦点底色。提问卡沿用输入区中性边缘与投影，标题用 `bg-accent` / `text-fg-secondary`，选中项用 `bg-selected` 与中性边框，仅勾选和主要按钮保留主色；自定义回答输入由外层 `focus-within` 边框提示，键盘操作仍可辨认焦点。
- 弹层使用不透明中性色，不依赖毛玻璃；嵌套子菜单仍使用 Portal，避免动画变换影响定位。
- 保留原生滚动与文字缩放；滚动条使用 thin 尺寸、透明轨道和稳定 gutter，滑块色 `--scrollbar-color` 为浅色 `#a3a3a3`／深色 `#737373`。滚动时显示，停滚 500ms 后以 150ms 淡出，悬停滚动条或拖动时保持显示。`color-scheme` 随深浅主题切换。长代码、路径和表格在所属容器滚动，正文正常换行。
- 运行文字扫光统一使用中性灰阶：`--shimmer-base` 浅色 `#666666`／深色白色 .56，`--shimmer-highlight` 浅色 `#404040`／深色 `#B0B0B0`。类型图标运行时提亮至 `--text-process-hover` 并播放语义动画：思考使用加宽灯罩、短灯颈的细线灯泡，`viewBox=0 0 24 24`、描边 1.65，灯罩宽 16.6 个坐标单位；轮廓保持稳定，灯丝与内部柔光以 2.4s 周期同步呼吸，灯丝不透明度在 .32–1 间变化，柔光在 .015–.15 间变化；静止时柔光隐藏、灯丝不透明度固定为 .7；搜索图标以 1.8s 周期沿小圆弧移动（最大 2px）；读取文字线以 1.8s 周期逐行扫描；写入／编辑笔尖以 1.8s 周期短距离往复；命令提示符以 1.8s 周期轻推、光标以 1s 周期闪烁；委派机器人双眼以 1.4s 周期交替亮起；技能星星以 1.8s 周期错峰闪烁；图片轮廓提亮并伴随光点闪烁。未知工具使用 1.8s 透明度提示。待审批、待回答、完成、失败和停止时图标静止，工具行不重复叠加加载圈。系统减少动态效果时，灯泡与其它类型活动图标均保留轻微语义动画以指示运行状态；文字扫光静止并恢复过程辅助色。运行中的过程组或整轮被折叠时，可见折叠行展示当前阶段的类型图标及动效；展开时由内部节点展示，待审批、待回答不播放。上下文环形进度使用 link 色，70%/90% 仍分别使用琥珀/危险色。失败仅以 14px 无圆圈红色 X 局部提示，stderr 不整段标红。

- 运行反馈动画默认强制启用：思考预览翻滚与 `ActivityIcon` 的工具／思考图标动画不受系统 `prefers-reduced-motion: reduce` 影响，无需用户开启系统动画或额外勾选强制播放。该规则仅覆盖上述运行反馈；手动折叠、文字扫光和滚动条过渡仍沿用各自的减少动态效果策略。
- 折叠思考预览在新句段切换时整行向上翻滚：旧行向上退出、新行从下方进入，各移动自身一行高度，持续 150ms，曲线 `cubic-bezier(.22, 1, .36, 1)`。窗口裁切且高度稳定，图标、标题与箭头不移动。同一句逐字追加不重启动画，初次显示不播放；最多保留一条退出中的旧行，禁止排队补播。动画只作用于前端显示，不得阻塞流式接收、智能体执行、正文呈现或结束状态切换。

## 4. 代码高亮

Shiki 按需加载语言，使用 `github-light` / `github-dark`；背景跟随代码容器，深色通过 `--shiki-dark` 应用语法色。聊天普通内容（含思考、摘要、路径、普通工具输出）统一使用正文系统字体；代码块、命令、JSON 参数与 diff 使用支持 CJK 回退的等宽字体，行内代码沿用正文系统字体，数字可使用 tabular-nums。高亮前后的代码保持同一字体、16px / 26px 字号与行高，避免加载后字号跳动。

思考和工具标题使用常规字重。可交互的整轮、过程组、思考和工具折叠行 hover 或键盘 focus-visible 时，通过局部过程色变量统一增强文字、耗时、图标、箭头和运行摘要的对比度；展开详情正文不连带变色，错误、审批及 diff 状态色保留语义。禁用的等待状态不提供可点击 hover 反馈。
