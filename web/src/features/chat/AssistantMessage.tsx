import { ChevronDown, ChevronRight, OctagonX, Sparkles } from "lucide-react"
import { useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { CopyIconButton } from "@/components/CopyIconButton"
import { Markdown } from "@/components/Markdown"
import { ForkButton } from "@/features/chat/ForkButton"
import { formatMessageTime, formatNumber } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { AssistantNode } from "@/lib/conversation"
import type { Usage } from "@/lib/types"

// 操作条显隐规则：最新一轮常驻；更早轮次 hover/focus-within 浮现；无 hover 设备常显
export function actionBarCls(alwaysShow: boolean): string {
  return alwaysShow
    ? ""
    : "opacity-0 transition-opacity duration-100 group-hover:opacity-100 group-focus-within:opacity-100 [@media(hover:none)]:opacity-100"
}

export function TokenBadge({ usage, className }: { usage: Usage; className?: string }) {
  const total = usage.input_tokens + usage.output_tokens + usage.cache_read_tokens + usage.cache_write_tokens
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge variant="outline" className={cn("cursor-default gap-1 font-mono text-[11px] font-normal", className)}>
          ↑{formatNumber(usage.input_tokens)} ↓{formatNumber(usage.output_tokens)}
        </Badge>
      </TooltipTrigger>
      <TooltipContent className="font-mono text-xs">
        <div>输入 {formatNumber(usage.input_tokens)}</div>
        <div>输出 {formatNumber(usage.output_tokens)}</div>
        <div>缓存读 {formatNumber(usage.cache_read_tokens)}</div>
        <div>缓存写 {formatNumber(usage.cache_write_tokens)}</div>
        <div>推理 {formatNumber(usage.reasoning_tokens)}</div>
        <div>合计 {formatNumber(total)}</div>
      </TooltipContent>
    </Tooltip>
  )
}

function reasoningSummary(text: string): string {
  const paras = text
    .split(/\n{2,}/)
    .map((p) => p.trim())
    .filter(Boolean)
  const last = paras[paras.length - 1] ?? ""
  return last.split("\n")[0] ?? ""
}

function ReasoningBlock({ text, live }: { text: string; live?: boolean }) {
  const [open, setOpen] = useState(false)
  const summary = reasoningSummary(text)
  return (
    <div className="my-0.5">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex h-8 w-full items-center gap-1.5 rounded-md px-1 text-left text-[13px] hover:bg-accent/50"
      >
        <Sparkles className="size-3.5 shrink-0 text-fg-tertiary" />
        <span className="shrink-0 font-medium text-fg-secondary">思考</span>
        <span
          className={cn(
            "min-w-0 flex-1 truncate text-fg-tertiary",
            live && "text-shimmer",
          )}
        >
          {summary ? `· ${summary}` : live ? "· 思考中…" : ""}
        </span>
        {open ? (
          <ChevronDown className="size-3.5 shrink-0 text-fg-tertiary" />
        ) : (
          <ChevronRight className="size-3.5 shrink-0 text-fg-tertiary" />
        )}
      </button>
      {open && (
        <div className="max-h-72 overflow-y-auto whitespace-pre-wrap py-1 pl-6 text-[13px] leading-relaxed text-fg-secondary">
          {text}
        </div>
      )}
    </div>
  )
}

export function AssistantMessage({
  node,
  sessionId,
  showActions,
  alwaysShowActions,
}: {
  node: AssistantNode
  sessionId?: string
  showActions?: boolean
  alwaysShowActions?: boolean
}) {
  const empty = node.content.length === 0
  const copyTextValue = node.content
    .filter((b) => b.type === "text")
    .map((b) => b.text)
    .join("\n\n")
  return (
    <div>
      {empty && node.live
        ? null
        : node.content.map((block, i) => {
            if (block.type === "reasoning") {
              return <ReasoningBlock key={i} text={block.text} live={node.live} />
            }
            if (block.type === "text") {
              return (
                <div key={i}>
                  <Markdown content={block.text} />
                  {node.live && i === node.content.length - 1 && (
                    <span className="ml-0.5 inline-block h-4 w-1.5 animate-pulse bg-foreground/70 align-text-bottom" />
                  )}
                </div>
              )
            }
            return null
          })}
      {showActions && !node.live ? (
        <div className={cn("mt-1 flex items-center gap-0.5", actionBarCls(!!alwaysShowActions))}>
          {copyTextValue && <CopyIconButton text={copyTextValue} />}
          {sessionId && <ForkButton sessionId={sessionId} seq={node.seq} />}
          {node.usage && <TokenBadge usage={node.usage} className="ml-1" />}
          {node.interrupted && (
            <Badge variant="secondary" className="ml-1 gap-1 text-[11px] font-normal text-muted-foreground">
              <OctagonX className="size-3" />
              已停止
            </Badge>
          )}
          <span className="px-1 text-[13px] text-fg-caption">{formatMessageTime(node.time)}</span>
        </div>
      ) : (
        (node.usage || node.interrupted) && (
          <div className="mt-1.5 flex items-center gap-2">
            {node.usage && <TokenBadge usage={node.usage} />}
            {node.interrupted && (
              <Badge variant="secondary" className="gap-1 text-[11px] font-normal text-muted-foreground">
                <OctagonX className="size-3" />
                已停止
              </Badge>
            )}
          </div>
        )
      )}
    </div>
  )
}
