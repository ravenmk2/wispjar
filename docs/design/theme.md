# 界面主题设计 Token

本规范对应 `web/src/index.css` 与 `web/tailwind.config.js`。界面参考 Codex Desktop 的阅读密度、灰阶分层与蓝色强调，兼顾中文系统字体。`:root` 定义浅色，`.dark` 覆盖深色。

## 1. 字体与排版

使用本机字体，不下载外部字体文件。中文友好的系统字体优先：

- 正文：`PingFang SC, Microsoft YaHei UI, Microsoft YaHei, Noto Sans CJK SC, Source Han Sans SC, -apple-system, BlinkMacSystemFont, Segoe UI, system-ui, Helvetica Neue, Arial, sans-serif`。
- 等宽：`SFMono-Regular, Consolas, Menlo, Liberation Mono, monospace`。

| 内容 | 字号 / 行高 | 实现 |
|---|---|---|
| 聊天正文、用户气泡、输入文本 | 16px / 26px | `text-chat` |
| 思考摘要及详情、过程组与工具折叠行 | 16px / 26px | `text-chat`，以辅助色区分层级 |
| 工具参数、输出、搜索结果、diff、代码块及行内代码 | 16px / 26px | 等宽字体，行内代码继承正文字号 |
| 提问、选项及解释、已回答内容 | 16px / 26px | `text-chat` |
| 侧栏“智能体”“会话”及聊天名称 | 14px / 22px | `text-sm leading-[22px]`，分组以字重、颜色与间距区分 |
| 通用菜单、按钮、表单及管理表格 | 14px / 20px | `text-sm`，保留管理页密度 |
| 时间、工具耗时、token 统计与状态徽标 | 12px / 18px | `text-meta` |
| 辅助提示 | 13px / 20px | `text-xs` |

body 基准 16px；聊天统一使用 `--chat-font-size: 16px`、`--chat-line-height: 26px`。Markdown H1 为 20px，H2 为 18px，H3/H4 为 16px；段落上下间距 12px。表格不缩小字号，宽内容在表格或代码块内横向滚动。

## 2. 语义颜色

下表展示最终颜色；`background/card/primary/ring/secondary/muted/input/destructive` 在 CSS 中存 HSL 分量，其他变量存完整颜色。Tailwind 按相应格式消费，不能混用。

| CSS 变量 | 浅色 | 深色 | 用途 |
|---|---|---|---|
| `--background` | #FFFFFF | #181818 | 主阅读区 |
| `--sidebar` | #F8F8F8 | #1D1D1D | 侧栏 |
| `--card` | #FFFFFF | #242424 | 卡片 |
| `--composer` | #FFFFFF | #363636 | 输入框独立背景 |
| `--popover` | rgba(255,255,255,.95) | rgba(36,36,36,.97) | 菜单与弹窗 |
| `--secondary` | #F5F5F5 | 0 0% 16% | 次级填充 |
| `--muted` | #F5F5F5 | #242424 | 工具详情与代码弱填充 |
| `--foreground` | #252525 | #EEEEEE | 主文字 |
| `--text-secondary` | #666666 | #ABABAB | 思考、工具摘要、说明 |
| `--text-tertiary` / `--text-caption` | #707070 | #999999 | 时间与元信息 |
| `--accent` | #F0F0F0 | #292929 | hover |
| `--selected` | #EAEAEA | #303030 | 侧栏选中 |
| `--border` | #E7E7E7 | #353535 | 普通边框 |
| `--divider` | #EEEEEE | #2C2C2C | 内部分隔 |
| `--input` | 0 0% 90% | 0 0% 20% | 通用输入控件边框 |
| `--bubble` | #E8F2FF | #1D4077 | 用户消息底色 |
| `--bubble-foreground` | #18375D | #F1F6FF | 用户消息文字 |
| `--primary` / `--ring` | #3A83F7 | #2C67C5 | 主按钮与交互强调 |
| `--primary-hover` | #2E76E8 | #3575D8 | 主按钮 hover |
| `--primary-active` | #2668D2 | #2458AD | 主按钮按下 |
| `--primary-foreground` | #FFFFFF | #FFFFFF | 主按钮内容 |
| `--link` | #2C67C5 | #83B5FF | 链接及文字强调，与实色按钮分开 |
| `--ring-focus` | rgba(58,131,247,.2) | rgba(44,103,197,.2) | 焦点与文本选区 |
| `--destructive` | 353 100% 61% | 354 100% 64% | 错误与危险 |
| `--success` | #16C456 | #16C456 | 成功 |
| `--warning` | #FF9500 | #FF9F0A | 警告 |
| `--tooltip-bg` / `--toast-bg` | #2B2B2B | #404040 | 深底提示 |

普通文字使用明确灰阶，避免继续通过低透明度削弱可读性。禁用控件及按需浮现的操作条仍保留禁用、hover、focus 状态。用户气泡必须配套使用 `text-bubble-foreground`；链接使用 `text-link`。

## 3. 布局、形状与交互

- 阅读列继续居中，最大宽度 864px（`max-w-chat`），左右内边距 16px。
- 侧栏展开宽 280px、折叠宽 48px；会话与智能体行高 36px，分组间距 24px。标题与列表名称同字号。
- 聊天输入框圆角 28px，模型与推理等级在同一行独立选择；发送按钮为 36px 圆形、白色上箭头。
- 思考、工具及过程组折叠行最小高度 36px；文字 26px 行高，不使用固定高度挤压文本。
- 基础圆角 `--radius: .75rem`；用户气泡圆角 16px。
- 输入阴影：浅色 `0 5px 16px -4px rgba(0,0,0,.07)`；深色同尺寸、透明度 .3。
- 菜单阴影：浅色 `0 12px 24px rgba(15,23,42,.12)`；深色 `rgba(0,0,0,.4)`。
- 键盘焦点统一为 3px 蓝色软 ring；输入框聚焦时由外容器展示蓝色边框和 ring，textarea 不叠加内框。
- 弹层仅在 `.popper-blur` 标记处应用 `blur(24px) saturate(1.8)`，嵌套子菜单使用 Portal。
- 保留浏览器滚动条与文字缩放；`color-scheme` 随深浅主题切换，使滚动条和原生控件保持一致。长代码、路径和表格在所属容器滚动，正文正常换行。

## 4. 代码高亮

Shiki 按需加载语言，使用 `github-light` / `github-dark`；背景跟随代码容器，深色通过 `--shiki-dark` 应用语法色。高亮前后的代码保持同一 16px / 26px 字号与行高，避免加载后字号跳动。
