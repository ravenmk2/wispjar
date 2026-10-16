import { ChevronDown, ChevronRight } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { NodeView } from "@/features/chat/NodeList"
import { TurnProcessGroup } from "@/features/chat/TurnProcessGroup"
import { formatTurnDuration, type TurnSegment } from "@/lib/turn-grouping"
import { cn } from "@/lib/utils"
import { useChatDisclosure } from "@/stores/chatPresentation"

export function TurnView({ segment, sessionId, latestTurn }: {
  segment: TurnSegment; sessionId: string; latestTurn: number
}) {
  const running = !segment.end
  const [open, setOpen] = useChatDisclosure(sessionId, `turn-${segment.turn}`, segment.turn === latestTurn)
  const expanded = open || segment.hasPendingApproval
  const headerRef = useRef<HTMLButtonElement>(null)
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    if (!running || !segment.start) return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [running, segment.start?.time])

  const duration = running && segment.start ? Math.max(1000, now - segment.start.time) : segment.durationMs
  const reason = segment.end?.reason
  const label = running ? "工作中" : reason === "interrupted" ? "已停止" : reason === "failed" ? "失败" : "已工作"
  const timeText = duration === undefined ? label : `${label}${running || reason !== "completed" ? " · 用时 " : " "}${formatTurnDuration(duration)}`
  const answer = segment.parts.find((p) => p.kind === "reply" && p.node.id === segment.answerId)
  const toggle = () => {
    const el = headerRef.current
    const scroll = el?.closest<HTMLElement>("[data-chat-scroll]")
    const top = el?.getBoundingClientRect().top
    setOpen(!expanded)
    if (el && scroll && top !== undefined) requestAnimationFrame(() => {
      scroll.scrollTop += el.getBoundingClientRect().top - top
    })
  }

  return (
    <section className="space-y-4" data-turn={segment.turn}>
      {segment.user && <NodeView node={segment.user} sessionId={sessionId} depth={0} latestTurn={latestTurn} />}
      <button
        ref={headerRef} type="button" data-turn-header={segment.turn} aria-expanded={expanded}
        aria-label={`${expanded ? "收起" : "展开"}第 ${segment.turn} 轮：${timeText}`}
        disabled={segment.hasPendingApproval} onClick={toggle}
        className="flex min-h-9 w-full items-center gap-1.5 rounded-md px-1 text-left text-chat text-fg-secondary hover:bg-accent/50 disabled:cursor-default"
      >
        {expanded ? <ChevronDown className="size-3.5 shrink-0" /> : <ChevronRight className="size-3.5 shrink-0" />}
        <span className={cn("tabular-nums", running && "text-shimmer-blue", reason === "failed" && "text-destructive")}>{timeText}</span>
      </button>
      {expanded ? segment.parts.map((part) => part.kind === "reply" ? (
        <NodeView key={part.node.id} node={part.node} sessionId={sessionId} depth={0} latestTurn={latestTurn} isAnswer={part.node.id === segment.answerId} />
      ) : <TurnProcessGroup key={part.id} group={part} sessionId={sessionId} latestTurn={latestTurn} />) : (
        answer?.kind === "reply" && <NodeView node={answer.node} sessionId={sessionId} depth={0} latestTurn={latestTurn} isAnswer />
      )}
      {reason === "failed" && segment.end?.error && <div className="px-1 text-xs text-destructive">{segment.end.error}</div>}
      {!expanded && !answer && !running && <div className="px-1 text-xs text-fg-caption">本轮未生成最终回复</div>}
    </section>
  )
}
