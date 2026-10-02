import { Brain, ChevronDown, ChevronRight, OctagonX } from "lucide-react"
import { useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Markdown } from "@/components/Markdown"
import { formatNumber } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { AssistantNode } from "@/lib/conversation"
import type { Usage } from "@/lib/types"

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

function ReasoningBlock({ text, live }: { text: string; live?: boolean }) {
  const [open, setOpen] = useState(!!live)
  return (
    <div className="my-1 rounded-md border bg-muted/30">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-xs text-muted-foreground hover:text-foreground"
      >
        <Brain className="size-3.5" />
        <span>思考过程</span>
        {live && <span className="size-1.5 animate-pulse rounded-full bg-green-500" />}
        {open ? <ChevronDown className="ml-auto size-3.5" /> : <ChevronRight className="ml-auto size-3.5" />}
      </button>
      {open && (
        <div className="max-h-72 overflow-y-auto whitespace-pre-wrap border-t px-3 py-2 text-xs leading-relaxed text-muted-foreground">
          {text}
        </div>
      )}
    </div>
  )
}

export function AssistantMessage({ node }: { node: AssistantNode }) {
  const empty = node.content.length === 0
  return (
    <div className="group">
      {empty && node.live ? (
        <div className="flex items-center gap-1.5 py-1 text-muted-foreground">
          <span className="size-1.5 animate-pulse rounded-full bg-muted-foreground" />
          <span className="text-xs">正在生成…</span>
        </div>
      ) : (
        node.content.map((block, i) => {
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
        })
      )}
      {(node.usage || node.interrupted) && (
        <div className="mt-1.5 flex items-center gap-2">
          {node.usage && <TokenBadge usage={node.usage} />}
          {node.interrupted && (
            <Badge variant="secondary" className="gap-1 text-[11px] font-normal text-muted-foreground">
              <OctagonX className="size-3" />
              已停止
            </Badge>
          )}
        </div>
      )}
    </div>
  )
}
