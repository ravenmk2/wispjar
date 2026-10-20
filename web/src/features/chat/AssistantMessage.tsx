import { OctagonX } from "lucide-react"
import { useLayoutEffect, useRef } from "react"
import { ActivityIcon } from "@/features/chat/ActivityIcon"
import { Badge } from "@/components/ui/badge"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { CopyIconButton } from "@/components/CopyIconButton"
import { DisclosureChevron, DisclosureContent, useDisclosureMotion } from "@/components/DisclosureMotion"
import { Markdown } from "@/components/Markdown"
import { ForkButton } from "@/features/chat/ForkButton"
import { formatMessageTime, formatNumber } from "@/lib/format"
import { formatTurnDuration, reasoningTail } from "@/lib/turn-grouping"
import { useChatDisclosure } from "@/stores/chatPresentation"
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
        <span className={cn("cursor-default font-sans tabular-nums text-meta tabular-nums text-fg-caption", className)}>
          ↑{formatNumber(usage.input_tokens)} ↓{formatNumber(usage.output_tokens)}
        </span>
      </TooltipTrigger>
      <TooltipContent className="font-sans tabular-nums text-xs">
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

export function AssistantActions({ node, sessionId, alwaysShow, showInterrupted = true }: {
  node: AssistantNode
  sessionId?: string
  alwaysShow?: boolean
  showInterrupted?: boolean
}) {
  if (node.live) return null
  const copyTextValue = node.content.filter((b) => b.type === "text").map((b) => b.text).join("\n\n")
  return (
    <div className={cn("mt-1 flex items-center gap-0.5", actionBarCls(!!alwaysShow))}>
      {copyTextValue && <CopyIconButton text={copyTextValue} />}
      {sessionId && <ForkButton sessionId={sessionId} seq={node.seq} />}
      {node.usage && <TokenBadge usage={node.usage} className="ml-1" />}
      {showInterrupted && node.interrupted && (
        <Badge variant="secondary" className="ml-1 gap-1 text-meta font-normal text-muted-foreground">
          <OctagonX className="size-3" />
          已停止
        </Badge>
      )}
      <span className="px-1 text-meta text-fg-caption">{formatMessageTime(node.time)}</span>
    </div>
  )
}

export function ReasoningBlock({
  text,
  streaming,
  durationMs,
  sessionId = "",
  disclosureKey = "reasoning",
}: {
  text: string
  streaming?: boolean
  durationMs?: number
  sessionId?: string
  disclosureKey?: string
}) {
  const [open, setOpen] = useChatDisclosure(sessionId, disclosureKey)
  const motion = useDisclosureMotion()
  const bodyRef = useRef<HTMLDivElement>(null)
  const followRef = useRef(true)
  useLayoutEffect(() => {
    const el = bodyRef.current
    if (el && open && streaming && followRef.current) el.scrollTop = el.scrollHeight
  }, [text, open, streaming])
  const detail = streaming && !open ? reasoningTail(text)
    : durationMs !== undefined ? formatTurnDuration(durationMs) : ""
  return (
    <div className="my-0.5">
      <button
        type="button"
        aria-expanded={open}
        aria-label={`${open ? "收起" : "展开"}${streaming ? "思考中" : "思考"}`}
        onClick={(event) => motion.toggle(event, () => { followRef.current = true; setOpen(!open) })}
        className="chat-process-trigger flex min-h-9 w-full items-center gap-1.5 rounded-md text-left text-chat hover:bg-accent/50"
      >
        <ActivityIcon name="reasoning" active={streaming} />
        <span className={cn("shrink-0 text-fg-process", streaming && "text-shimmer")}>{streaming ? "思考中" : "思考"}</span>
        <span className="shrink-0 text-fg-process-muted">{detail && "·"}</span>
        <span className={cn("min-w-0 flex-1 overflow-hidden whitespace-nowrap text-fg-process-muted", streaming && !open ? "[direction:rtl] [text-overflow:ellipsis] text-left" : "text-ellipsis")} title={detail}>
          <bdi>{detail}</bdi>
        </span>
        <DisclosureChevron open={open} revision={motion.revision} className="text-fg-process-muted" />
      </button>
      <DisclosureContent open={open} revision={motion.revision}>
        <div ref={bodyRef} data-reasoning-body onScroll={() => {
          const el = bodyRef.current
          if (el) followRef.current = el.scrollHeight - el.clientHeight - el.scrollTop < 24
        }} className="max-h-72 overflow-y-auto whitespace-pre-wrap break-words py-1 text-chat text-fg-process">
          {text}
        </div>
      </DisclosureContent>
    </div>
  )
}

export function AssistantMessage({
  node,
  sessionId,
  showActions,
  alwaysShowActions,
  showMetaFallback = true,
  showInterrupted = true,
}: {
  node: AssistantNode
  sessionId?: string
  showActions?: boolean
  alwaysShowActions?: boolean
  showMetaFallback?: boolean
  showInterrupted?: boolean
}) {
  const empty = node.content.length === 0
  return (
    <div>
      {empty && node.live
        ? null
        : node.content.map((block, i) => {
            if (block.type === "reasoning") {
              return (
                <ReasoningBlock
                  key={i}
                  text={block.text}
                  streaming={node.live === true && block.streaming === true}
                  durationMs={block.durationMs}
                  sessionId={sessionId}
                  disclosureKey={`reasoning-${node.id}-${i}`}
                />
              )
            }
            if (block.type === "text") {
              return (
                <div key={i}>
                  <Markdown content={block.text} className="assistant-body" />
                  {node.live && i === node.content.length - 1 && (
                    <span className="ml-0.5 inline-block h-4 w-1.5 animate-pulse bg-foreground/70 align-text-bottom" />
                  )}
                </div>
              )
            }
            return null
          })}
      {showActions && !node.live ? (
        <AssistantActions node={node} sessionId={sessionId} alwaysShow={alwaysShowActions} showInterrupted={showInterrupted} />
      ) : (
        showMetaFallback &&
        (node.usage || (showInterrupted && node.interrupted)) && (
          <div className="mt-1.5 flex items-center gap-2">
            {node.usage && <TokenBadge usage={node.usage} />}
            {showInterrupted && node.interrupted && (
              <Badge variant="secondary" className="gap-1 text-meta font-normal text-muted-foreground">
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
