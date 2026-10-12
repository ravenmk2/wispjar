# 界面主题设计 Token

> 本项目的界面设计 token 规范：深浅双主题色板、字体、圆角、阴影与设计风格，
> 以及到 `web/src/index.css` CSS 变量的映射（见 §6）。

## 1. Token 架构

设计 token 按语义分层命名（`:root` 为浅色，`.dark` 覆盖为深色），**每个 token 都带
`-hover` / `-active` 两个交互态变体**：

- `--Colors-*`：品牌与功能色（蓝/红/绿/黄/橙/紫）
- `--Bg-*`：背景层级（Primary→Quaternary、Ground、Gp 分组背景）
- `--Labels-*`：文字/图标（Primary→Quaternary，白色或黑色的透明度梯度）
- `--Fills-F1~F4`：中性填充（hover 底、选中底等，透明度梯度）
- `--Separators-S1`：分隔线/边框
- `--MaskBg-*`：遮罩、Toast、Tooltip
- `--Always-*`：不随主题切换的恒定色（如主按钮上的白字）
- `--Others-*`：语义化场景（气泡、浅底色标签、选中区、禁用态）
- `--Syntax-*`：代码高亮
- `--ui-*` / `--markdown-*`：字号/行高阶梯

页面骨架绑定关系：

| 部位 | 绑定 token | 深色值 | 浅色值 |
|---|---|---|---|
| body / 侧边栏 | `--Bg-Ground` | `#181817` | `#fbfaf9` |
| 主内容区 | `--Bg-Primary` | `#121212` | `#ffffff` |
| 输入框、次级卡片 | `--Bg-Secondary` | `#1f1f1f` | `#f5f5f5` |
| 三级浮层（弹窗等） | `--Bg-Tertiary` | `#292929` | `#ffffff` |
| 正文文字 | `--Labels-Primary` | `#ffffffd6` | `#000000e6` |

## 2. 深色主题

### 功能色

| Token | 值 | 用途 |
|---|---|---|
| `--Colors-Brand` | `#1a88ff` | 品牌蓝（hover `#258eff` / active `#3194ff`） |
| `--Colors-Red` | `#ff4756` | 错误/危险 |
| `--Colors-PositiveGreen` | `#16c456` | 成功 |
| `--Colors-Orange` | `#ff9f0a` | 警告 |
| `--Colors-Yellow` | `#ffd230` | 提示 |
| `--Colors-Purple` | `#a16bff` | 辅助 |
| `--Colors-Red25` / `--Colors-PositiveGreen25` | 25% 透明版本 | 浅底标签 |

### 背景层级

| Token | 值 |
|---|---|
| `--Bg-Primary` | `#121212`（hover `#1e1e1e` / active `#2a2a2a`） |
| `--Bg-Secondary` | `#1f1f1f`（hover `#2a2a2a` / active `#353535`） |
| `--Bg-Tertiary` | `#292929`（hover `#343434` / active `#3e3e3e`） |
| `--Bg-Quaternary` | `#4d4d4d` |
| `--Bg-Ground` | `#181817`（hover `#242423` / active `#2f2f2e`） |
| 90% 透明变体 | 如 `--Bg-Secondary90: #1f1f1fe6`，用于毛玻璃浮层 |

### 文字（Labels，白色透明度梯度）

| Token | 值 | 折算不透明度 |
|---|---|---|
| `--Labels-Primary` | `#ffffffd6` | 84% |
| `--Labels-Secondary` | `#ffffff8f` | 56% |
| `--Labels-Tertiary` | `#ffffff6b` | 42% |
| `--Labels-Quaternary` | `#ffffff42` | 26% |

### 填充与分隔

| Token | 值 | 典型用途 |
|---|---|---|
| `--Fills-F1` | `#ffffff0d` |  hover 底色（5%） |
| `--Fills-F2` | `#ffffff1a` | 选中底色（10%）、侧边栏焦点 |
| `--Fills-F3` | `#ffffff2e` | 强调填充 |
| `--Fills-F4` | `#ffffff40` | 最强填充 |
| `--Separators-S1` | `#ffffff1f` | 边框/分隔线（12%） |
| `--Others-SubLayerBg` | `#ffffff0f` | 子层级卡片底 |

### 遮罩 / Toast / 恒定色

| Token | 值 |
|---|---|
| `--MaskBg-Base` / `--MaskBg-Dark` | `#0009`（模态遮罩） |
| `--MaskBg-Toast` | `#404040`（Toast/Tooltip 底） |
| `--Always-White` | `#fff`（主按钮文字等恒定白） |
| `--Others-Brand10` | `#1a88ff1a`（品牌蓝 10% 浅底） |
| `--Others-TextSelected` | `#1a88ff33`（文本选区） |
| `--Others-LightBlueBg` | `#1e2a38`（深蓝浅底标签） |
| `--Others-BubbleGray` | `#292929`（深色下用户气泡，由蓝色退化为灰） |

### 代码高亮（Syntax）

`#d55fde` keyword · `#89ca78` string · `#d19a66` literal · `#5c6370` comment ·
`#e5c07b` metadata · `#ef596f` punctuation · `#2bbac5` mark

## 3. 浅色主题

### 功能色

| Token | 值 |
|---|---|
| `--Colors-Brand` | `#1783ff`（hover `#167ff7` / active `#167cf2`） |
| `--Colors-Red` | `#ff3849` |
| `--Colors-PositiveGreen` | `#16c456` |
| `--Colors-Orange` | `#ff9500` |
| `--Colors-Yellow` | `#ffd230` |
| `--Colors-Purple` | `#985ffb` |

### 背景层级

| Token | 值 |
|---|---|
| `--Bg-Primary` | `#ffffff`（hover `#f7f7f7` / active `#f2f2f2`） |
| `--Bg-Secondary` | `#f5f5f5`（hover `#eeeeee` / active `#e9e9e9`） |
| `--Bg-Tertiary` | `#ffffff`（三级浮层回到白色，与 Primary 相同） |
| `--Bg-Ground` | `#fbfaf9`（hover `#f3f3f2`） |

### 文字（Labels，黑色透明度梯度）

| Token | 值 | 折算不透明度 |
|---|---|---|
| `--Labels-Primary` | `#000000e6` | 90% |
| `--Labels-Secondary` | `#0009` | 60% |
| `--Labels-Tertiary` | `#00000073` | 45% |
| `--Labels-Quaternary` | `#0000004d` | 30% |

### 填充与分隔

| Token | 值 |
|---|---|
| `--Fills-F1` | `#00000008`（3%） |
| `--Fills-F2` | `#0000000d`（5%，选中底） |
| `--Fills-F3` | `#00000026`（15%） |
| `--Fills-F4` | `#00000040`（25%） |
| `--Separators-S1` | `#00000021`（13%） |
| `--Others-SubLayerBg` | `#00000014` |

### 其他

| Token | 值 |
|---|---|
| `--MaskBg-Toast` | `#2b2b2b` |
| `--Others-LightBlueBg` | `#e8f3ff`（浅蓝标签底） |
| `--Others-BubbleBlue` | `#1783ff`（浅色下用户气泡为品牌蓝） |
| `--Others-BubbleGray` | `#f5f5f5` |
| `--Others-TextSelected` | `#1783ff33` |
| `--Others-Brand10` | `#1783ff1a` |

### 代码高亮（Syntax，One Light 系）

`#a626a4` keyword · `#50a14f` string · `#986801` literal · `#a1a1a1` comment ·
`#c18401` metadata · `#e45649` punctuation · `#526fff` mark

## 4. 字体与排版

**字体栈**（无自托管正文字体；代码用 Geist Mono）：

- 正文：`-apple-system, BlinkMacSystemFont, Segoe UI, system-ui, Roboto, PingFang SC, Source Han Sans SC, Microsoft YaHei UI, Microsoft YaHei, Noto Sans CJK SC, Helvetica Neue, Arial, sans-serif`
- 等宽：`Geist Mono, SF Mono, Menlo, JetBrains Mono, Fira Code, Consolas, DejaVu Sans Mono, monospace`
- body 基准字号 **14px**

**UI 字号阶梯**（`--ui-*`）：

| 阶梯 | 字号 / 行高 | 典型用途 |
|---|---|---|
| T1 | 18 / 26 | 大标题 |
| T2 | 16 / 24 | 标题（含 Emphasized 加粗变体） |
| B1 | 15 / 22 | 正文强调 |
| B2 | 14 / 20 | 正文（基准） |
| C1 | 12 / 18 | 辅助说明 |
| C2 | 10 / 14 | 角标 |

**Markdown 内容阶梯**：H1 22/36 · H2 20/32 · H3 18/28 · 正文 16/26 ·
次级 15/24 · 三级 14/22 · 代码块 14/22 · 行内代码 16/26。

## 5. 形状、阴影与细节风格

- **圆角**：聊天输入框 24px（`--chat-input-radius`）、模态框 20px（`--modal-radius`）、
  图标按钮 20px；常用档位 8 / 10 / 12 / 14 / 16 / 20 / 24px；胶囊 100px。整体比典型
  shadcn 默认（8px）更圆润，本项目基础圆角取 12px。
- **阴影**：极轻，浅色下几乎统一为 `#0000001a` 家族（如 `0 2px 8px`、
  `0 5px 16px -4px #00000012`）；弹层用 `0 12px 24px #0f172a1f`；深色下依赖
  明度分层而非投影。
- **焦点**：侧边栏焦点 `inset 0 0 0 2px var(--Labels-Primary)`；全局交互过渡
  `.15s ease` / `cubic-bezier(.23,1,.32,1)`。
- **滚动条**：默认隐藏（`display:none` / 1px），悬浮场景才出现。
- **侧边栏**：固定宽 240px，背景同 body（Ground），与主区仅靠明度差分层，无描边。
- **深色主题策略**：不是简单反色——文字/边框用「白色透明度梯度」，背景用「中性灰阶
  分层」(#121212 → #181817 → #1f1f1f → #292929)，品牌蓝在深色下提亮
  (#1783ff → #1a88ff)，用户气泡从蓝退化为灰 (#292929)。

## 6. 到本项目变量的映射

目标文件 `web/src/index.css`（`:root` 浅色 / `.dark` 深色）。注意现有变量分两种
消费方式：HSL 分量（`hsl(var(--x))`）与完整颜色值（`var(--x)`），映射时保持原格式。

| 项目变量 | 浅色（token 来源） | 深色（token 来源） |
|---|---|---|
| `--background` | `0 0% 100%`（Bg-Primary #fff） | `0 0% 7%`（Bg-Primary #121212） |
| `--foreground` | `rgba(0,0,0,0.9)`（Labels-Primary） | `rgba(255,255,255,0.9)`（Labels-Primary） |
| `--card` | `0 0% 100%`（浅色浮层保持白色，Bg-Secondary 是输入/填充层而非卡片） | `0 0% 12%`（Bg-Secondary #1f1f1f） |
| `--popover` | `rgba(255,255,255,0.95)` | `rgba(31,31,31,0.95)`（Bg-Secondary90） |
| `--primary` | `214 100% 55%`（Brand #1783ff） | `215 100% 55%`（Brand #1a88ff） |
| `--primary-foreground` | `0 0% 100%`（Always-White） | `0 0% 100%`（Always-White） |
| `--secondary` | `0 0% 96.1%`（#f5f5f5） | `0 0% 12%`（#1f1f1f） |
| `--muted` | `0 0% 96.1%`（保持 #f5f5f5 弱填充） | `0 0% 9%`（≈Bg-Ground #181817） |
| `--accent` | `rgba(0,0,0,0.03)`（Fills-F1 #00000008） | `rgba(255,255,255,0.05)`（Fills-F1 #ffffff0d） |
| `--selected` | `rgba(0,0,0,0.05)`（Fills-F2 #0000000d） | `rgba(255,255,255,0.1)`（Fills-F2 #ffffff1a） |
| `--destructive` | `353 100% 61%`（Red #ff3849） | `354 100% 64%`（Red #ff4756） |
| `--border` | `rgba(0,0,0,0.13)`（Separators-S1 #00000021） | `rgba(255,255,255,0.12)`（Separators-S1 #ffffff1f） |
| `--divider` | `rgba(0,0,0,0.03)`（Fills-F1） | `rgba(255,255,255,0.05)`（Fills-F1） |
| `--input` | `0 0% 90%`（shadcn 中为输入框边框语义，保持不变） | `0 0% 20%`（同语义，去蓝化） |
| `--ring` | `214 100% 55%` | `215 100% 55%` |
| `--ring-focus` | `rgba(23,131,255,0.2)`（TextSelected） | `rgba(26,136,255,0.2)`（TextSelected） |
| `--sidebar` | `#fbfaf9`（Bg-Ground） | `#181817`（Bg-Ground） |
| `--text-secondary` | `rgba(0,0,0,0.6)`（Labels-Secondary） | `rgba(255,255,255,0.56)`（Labels-Secondary） |
| `--text-tertiary` | `rgba(0,0,0,0.45)`（Labels-Tertiary） | `rgba(255,255,255,0.42)`（Labels-Tertiary） |
| `--text-caption` | `rgba(0,0,0,0.30)`（Labels-Quaternary） | `rgba(255,255,255,0.26)`（Labels-Quaternary） |
| `--bubble` | `#f5f5f5`（BubbleGray，PC 端用户气泡为灰底；蓝底白字是移动端样式） | `#292929`（BubbleGray） |
| `--success` | `#16c456` | `#16c456` |
| `--warning` | `#ff9500` | `#ff9f0a` |
| `--tooltip-bg` | `#2b2b2b`（MaskBg-Toast） | `#404040`（MaskBg-Toast） |
| `--toast-bg` | `#2b2b2b` | `#404040` |
| `--shadow-input` | `0 5px 16px -4px rgba(0,0,0,0.07)` | `0 5px 16px -4px rgba(0,0,0,0.3)` |
| `--shadow-menu` | `0 12px 24px rgba(15,23,42,0.12)` | `0 12px 24px rgba(0,0,0,0.4)` |

主要变化点（相对旧版色板）：

1. 主色由蓝紫（`221 77% 58%` / `#7AAAFF`）改为**品牌蓝 #1783ff / #1a88ff**，
   深色下 `primary-foreground` 由深底改为白字。
2. 深色背景灰阶由偏蓝（`240 5% 9%`）改为**纯中性灰**（#121212/#181817/#1f1f1f）。
3. 浅色侧边栏 `#f9fbfc` → `#fbfaf9`（带一丝暖调）。
4. 文字四级梯度透明度微调（如深色 tertiary 42% 不变，caption 34% → 26%）。
5. 用户气泡浅色 `#edf3fe` → `#f5f5f5`（中性灰，与深色 `#292929` 同语义）。
6. 正文/代码字体栈扩充（增补 system-ui、Source Han Sans SC、
   Microsoft YaHei UI、Noto Sans CJK SC；等宽换 Geist Mono 优先）。
7. 基础圆角 `--radius` 8px → 12px，整体更圆润。
8. 危险/成功/警告/Toast/Tooltip 色同步功能色板（红 `#ff3849`/`#ff4756`、
   绿 `#16c456`、橙 `#ff9500`/`#ff9f0a`、Toast 底 `#2b2b2b`/`#404040`）。
9. `::selection` 改为跟随 `--ring-focus`（即 TextSelected 蓝 20%）。

以上已应用于 `web/src/index.css` 与 `web/tailwind.config.js`。
