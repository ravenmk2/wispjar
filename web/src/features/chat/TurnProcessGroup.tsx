import { ChevronDown, ChevronRight } from "lucide-react"
import { useState } from "react"
import { NodeView } from "@/features/chat/NodeList"
import { ReasoningBlock } from "@/features/chat/AssistantMessage"
import { formatTurnDuration, type TurnSegment } from "@/lib/turn-grouping"
import { cn } from "@/lib/utils"

export function TurnProcessGroup({
  segment,
  sessionId,
  latestTurn,
}: {
  segment: TurnSegment
  sessionId: string
  latestTurn: number
}) {
  const [open, setOpen] = useState(false)
  const reason = segment.end?.reason
  const failed = reason === "failed"
  // 无 end 边界（运行中）‖ 中断/失败 ‖ 含 pending 审批 → 常显不折叠
  const forceOpen = !segment.end || reason !== "completed" || segment.hasPendingApproval
  const expanded = forceOpen || open

  const text = (() => {
    if (reason === "interrupted") return "已停止"
    if (failed) return "失败"
    const parts: string[] = []
    if (segment.toolCallCount > 0) parts.push(`${segment.toolCallCount} 次工具调用`)
    if (segment.taskCount > 0) parts.push(`${segment.taskCount} 次任务委派`)
    const base = parts.length > 0 ? parts.join(" · ") : "已思考"
    if (segment.durationMs !== undefined && reason === "completed") {
      return `${base} · 用时 ${formatTurnDuration(segment.durationMs)}`
    }
    return base
  })()

  return (
    <div>
      <button
        type="button"
        disabled={forceOpen}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          "flex h-8 w-full items-center gap-1.5 rounded-md px-1 text-left",
          forceOpen ? "cursor-default" : "hover:bg-accent/50",
        )}
      >
        <span
          className={cn(
            "min-w-0 flex-1 truncate text-[13px] tabular-nums",
            failed ? "text-destructive" : "text-fg-tertiary",
          )}
        >
          {text}
        </span>
        {!forceOpen &&
          (open ? (
            <ChevronDown className="size-3.5 shrink-0 text-fg-tertiary" />
          ) : (
            <ChevronRight className="size-3.5 shrink-0 text-fg-tertiary" />
          ))}
      </button>
      {expanded && (
        <div className="space-y-2 py-1">
          {segment.process.map((node) => (
            <NodeView key={node.id} node={node} sessionId={sessionId} depth={0} latestTurn={latestTurn} />
          ))}
          {segment.answerReasoning.map((text, i) => (
            <ReasoningBlock key={`answer-reasoning-${i}`} text={text} />
          ))}
        </div>
      )}
    </div>
  )
}
