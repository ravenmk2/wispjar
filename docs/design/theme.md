# 界面主题设计 Token

本规范对应 `web/src/index.css` 与 `web/tailwind.config.js`。界面按 Kimi 的系统字体顺序、排版密度与中性灰阶分层对齐；深色助手正文、两套主题的蓝色用户气泡及发送／停止按钮保留项目现状。`:root` 定义浅色，`.dark` 覆盖深色。

## 1. 字体与排版

使用本机字体，不下载外部字体文件。平台系统字体优先，中文按字体栈回退：

- 正文：`-apple-system, BlinkMacSystemFont, Segoe UI, system-ui, Roboto, Noto Sans, Ubuntu, Cantarell, Helvetica Neue, sans-serif, Arial, PingFang SC, Source Han Sans SC, Microsoft YaHei UI, Microsoft YaHei, Noto Sans CJK SC`。
- 等宽：`SFMono-Regular, Consolas, Menlo, Liberation Mono, PingFang SC, Microsoft YaHei UI, Microsoft YaHei, Noto Sans CJK SC, Source Han Sans SC, monospace`；西文优先等宽，CJK 明确回退到苹方、微软雅黑等中文字体。

| 内容 | 字号 / 行高 | 实现 |
|---|---|---|
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

## 2. 语义颜色

下表展示最终颜色；`background/card/primary/ring/secondary/muted/input/destructive` 在 CSS 中存 HSL 分量，其他变量存完整颜色。Tailwind 按相应格式消费，不能混用。

| CSS 变量 | 浅色 | 深色 | 用途 |
|---|---|---|---|
| `--background` | #FFFFFF | #121212 | 主阅读区 |
| `--sidebar` | #FBFAF9 | #181817 | 侧栏与工作区外层 |
| `--card` | #FFFFFF | #292929 | 卡片 |
| `--composer` | #FFFFFF | #1F1F1F | 输入框独立背景 |
| `--composer-border` | rgba(0,0,0,.09) | rgba(255,255,255,.06) | 输入框边缘 |
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

机构管理、系统管理的主按钮统一使用 `primary`／`primary-hover`／`primary-active`，以启用态为基准，禁用态保留透明度区分。已发布、管理员等信息徽章统一使用 `bg-primary-soft text-link border-primary/20`，不使用实心主按钮填充或无操作的 hover 反馈；成功、警告、危险等语义状态保持各自颜色。云电脑状态采用 6px 圆点，运行/创建使用 `bg-primary`，其他状态使用 `bg-fg-tertiary`，20px 悬浮与键盘聚焦区域通过 Tooltip 展示状态文字。文字链接在深色主题使用更亮的同系蓝以保证可读性。Tooltip 与聊天列表悬浮信息卡随主题切换底色、文字、边框与阴影，内部时间／状态／空闲圆点使用主题辅助色，不固定白字。

界面以黑／白的分层 alpha 色表达主次，不在整块容器上叠加低 opacity；助手正文通过独立 `--text-body` 保留深色 #EEEEEE，避免界面主色调整连带改变正文。禁用控件及按需浮现的操作条仍保留禁用、hover、focus 状态。用户气泡必须配套使用 `text-bubble-foreground`；链接使用 `text-link`。

## 3. 布局、形状与交互

- 阅读区居中；正文容器上限 752px（`max-w-chat-body`，含左右各 16px，实际正文上限 720px），输入框和通用聊天容器上限 800px（`max-w-chat`，实际输入卡上限 768px）。
- 侧栏展开宽 240px、折叠宽 48px；会话与智能体行高 36px，分组间距 24px。标题与列表名称同字号。
- 聊天输入框圆角 24px，模型与推理等级在同一行展示，首条消息前独立选择，首条消息被服务端接受后改为无下拉箭头的只读标签；发送／停止按钮保留 36px 蓝色圆形与白色图标。
- 思考、工具及过程组折叠行最小高度 36px；文字 26px 行高，不使用固定高度挤压文本。
- 手动折叠使用高度与透明度过渡：展开 200ms、收起 140ms，`cubic-bezier(0, 0, 0.2, 1)`，箭头同步旋转 90°；只作用于点击的层级，无位移和缩放。初始、自动及流式更新不播放折叠动画；系统减少动态效果时直接切换。
- 静态整轮与过程组标题文字左边缘与正文对齐，运行时折叠行可带当前阶段图标，折叠箭头紧跟文字右侧，不推到容器右边缘；独立思考和工具保留前置 18px 类型图标，折叠箭头保持 14px；思考使用双轨原子，技能（skill/activate_skill）使用魔杖和星星。“思考与行动／多次工具调用”展开后内部节点统一缩进一级（16px），内部思考和工具继承该层级，不再叠加节点自身的展示性左内边距；工具详情卡外缘与所属层级对齐，卡内保留内容内边距。独立思考详情与正文对齐，子任务轨迹保留真实层级缩进。
- 基础圆角 `--radius: .75rem`；菜单外缘 16px、菜单项 10px、内边距 8px。两套主题的用户气泡均保留圆角 16px、内边距 10px 16px、宽度上限 85%。工作区主内容区保持原有直角与贴边布局。
- 输入阴影：浅色 `0 4px 12px rgba(0,0,0,.03), 0 5px 16px -4px rgba(0,0,0,.07)`；深色仅保留后项。聚焦使用 `0 5px 18px -4px rgba(0,0,0,.13)`，过渡 150ms。
- 菜单使用 0.5px 内描边（浅黑 .13／深白 .12）与 `0 4px 16px rgba(0,0,0,.1)` 阴影。
- 普通控件键盘焦点为 3px 蓝色软 ring；聊天输入框聚焦仅加深外容器中性阴影，深色边框增强至白色 .1，textarea 不叠加内框。
- 弹层使用不透明中性色，不依赖毛玻璃；嵌套子菜单仍使用 Portal，避免动画变换影响定位。
- 保留原生滚动与文字缩放；滚动条使用 thin 尺寸、透明轨道和稳定 gutter，滑块色 `--scrollbar-color` 为浅色 `#a3a3a3`／深色 `#737373`。滚动时显示，停滚 500ms 后以 150ms 淡出，悬停滚动条或拖动时保持显示。`color-scheme` 随深浅主题切换。长代码、路径和表格在所属容器滚动，正文正常换行。
- 运行文字扫光统一使用中性灰阶：`--shimmer-base` 浅色 `#666666`／深色白色 .56，`--shimmer-highlight` 浅色 `#404040`／深色 `#B0B0B0`。类型图标运行时提亮至 `--text-process-hover` 并播放语义动画：思考使用两条倾斜 ±45° 的椭圆轨道（rx=9、ry=5），以 `viewBox=3 3 18 18` 收紧内部留白，18px 图标槽内的实际轮廓约 14.6px，与其它工具图标匹配，轨道直接继承过程图标颜色，不额外降低不透明度，与其它类型图标保持一致，电子以 2.8s 周期沿轨道运行、相位相差 1.4s，中心以 1.8s 周期轻微明暗变化，仅在活动状态显示两个电子沿轨道运动，静止时隐藏两个电子、仅保留轨道与中心；搜索图标以 1.8s 周期沿小圆弧移动（最大 2px）；读取文字线以 1.8s 周期逐行扫描；写入／编辑笔尖以 1.8s 周期短距离往复；命令提示符以 1.8s 周期轻推、光标以 1s 周期闪烁；委派机器人双眼以 1.4s 周期交替亮起；技能星星以 1.8s 周期错峰闪烁；图片轮廓提亮并伴随光点闪烁。未知工具使用 1.8s 透明度提示。待审批、待回答、完成、失败和停止时图标静止，工具行不重复叠加加载圈。系统减少动态效果时，仍保留各类活动图标的语义动画以指示运行状态，仅停止原子中心脉动；文字扫光静止并恢复过程辅助色。运行中的过程组或整轮被折叠时，可见折叠行展示当前阶段的类型图标及动效；展开时由内部节点展示，待审批、待回答不播放。上下文环形进度使用 link 色，70%/90% 仍分别使用琥珀/危险色。失败仅以 14px 无圆圈红色 X 局部提示，stderr 不整段标红。

## 4. 代码高亮

Shiki 按需加载语言，使用 `github-light` / `github-dark`；背景跟随代码容器，深色通过 `--shiki-dark` 应用语法色。聊天普通内容（含思考、摘要、路径、普通工具输出）统一使用正文系统字体；代码块、命令、JSON 参数与 diff 使用支持 CJK 回退的等宽字体，行内代码沿用正文系统字体，数字可使用 tabular-nums。高亮前后的代码保持同一字体、16px / 26px 字号与行高，避免加载后字号跳动。

思考和工具标题使用常规字重。可交互的整轮、过程组、思考和工具折叠行 hover 或键盘 focus-visible 时，通过局部过程色变量统一增强文字、耗时、图标、箭头和运行摘要的对比度；展开详情正文不连带变色，错误、审批及 diff 状态色保留语义。禁用的等待状态不提供可点击 hover 反馈。
