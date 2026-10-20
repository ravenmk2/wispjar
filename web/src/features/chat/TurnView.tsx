import { X } from "lucide-react"
import { useEffect, useId, useState } from "react"
import { CopyIconButton } from "@/components/CopyIconButton"
import { DisclosureChevron, DisclosureContent, useDisclosureMotion } from "@/components/DisclosureMotion"
import { NodeView } from "@/features/chat/NodeList"
import { AssistantActions } from "@/features/chat/AssistantMessage"
import { TurnProcessGroup } from "@/features/chat/TurnProcessGroup"
import { ActivityIcon } from "@/features/chat/ActivityIcon"
import { activeProcessNode, formatTurnDuration, type TurnSegment } from "@/lib/turn-grouping"
import { cn } from "@/lib/utils"
import { useChatDisclosure } from "@/stores/chatPresentation"

export function TurnView({ segment, sessionId, latestTurn }: {
  segment: TurnSegment; sessionId: string; latestTurn: number
}) {
  const running = segment.running
  const [open, setOpen] = useChatDisclosure(sessionId, `turn-${segment.turn}`, segment.turn === latestTurn)
  const expanded = open || segment.hasPendingApproval
  const [errorOpen, setErrorOpen] = useChatDisclosure(sessionId, `turn-error-${segment.turn}`)
  const errorId = useId()
  const motion = useDisclosureMotion()
  const errorMotion = useDisclosureMotion()
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    if (!running || !segment.start) return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [running, segment.start?.time])

  const duration = running && segment.start ? Math.max(1000, now - segment.start.time) : segment.durationMs
  const reason = segment.end?.reason
  const label = running ? "工作中" : reason === "interrupted" ? "已停止" : reason === "failed" ? "运行出错" : "已工作"
  const timeText = duration === undefined ? label : `${label}${reason === "completed" ? " " : " · "}${formatTurnDuration(duration)}`
  const answer = segment.parts.find((p) => p.kind === "reply" && p.node.id === segment.answerId)
  const activity = !expanded && running
    ? segment.parts.slice().reverse().map((part) => part.kind === "process" ? activeProcessNode(part) : undefined).find((node) => node !== undefined)
    : undefined
  return (
    <section className="space-y-4" data-turn={segment.turn}>
      {segment.user && <NodeView node={segment.user} sessionId={sessionId} depth={0} latestTurn={latestTurn} />}
      <div className="flex min-w-0 items-center gap-2">
        <button
          type="button" data-turn-header={segment.turn} aria-expanded={expanded}
          aria-label={`${expanded ? "收起" : "展开"}第 ${segment.turn} 轮：${timeText}`}
          disabled={segment.hasPendingApproval} onClick={(event) => motion.toggle(event, () => setOpen(!expanded))}
          className="chat-process-trigger flex min-h-9 min-w-0 items-center gap-1.5 rounded-md text-left text-chat text-fg-process hover:bg-accent/50 disabled:cursor-default"
        >
          {activity && <ActivityIcon name={activity.kind === "assistant" ? "reasoning" : activity.name} active />}
          <span className={cn("truncate tabular-nums", running && "text-shimmer")}>{timeText}</span>
          <DisclosureChevron open={expanded} revision={motion.revision} />
        </button>
        {reason === "failed" && <X className="size-3.5 shrink-0 text-destructive" strokeWidth={2.5} aria-label="运行出错" />}
        {reason === "failed" && segment.end?.error && <button type="button"
          aria-expanded={expanded && errorOpen} aria-controls={errorId}
          onClick={(event) => errorMotion.toggle(event, () => { setErrorOpen(!(expanded && errorOpen)); if (!expanded) setOpen(true) })}
          className="shrink-0 rounded px-1 py-1 text-meta text-fg-process hover:bg-accent hover:text-foreground">
          {expanded && errorOpen ? "收起详情" : "查看详情"}
        </button>}
      </div>
      <div className="group !mt-0">
        {reason === "failed" && segment.end?.error && <DisclosureContent open={expanded && errorOpen} revision={errorMotion.revision}>
          <div className="pt-4"><div id={errorId} className="rounded-lg bg-muted/50 px-3 py-2">
            <div className="mb-1 flex items-center justify-between gap-2 text-meta text-fg-process">
              <span>错误详情</span>
              <CopyIconButton text={segment.end.error} tooltip="复制错误详情" />
            </div>
            <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-words font-sans tabular-nums text-xs text-fg-process">{segment.end.error}</pre>
          </div></div>
        </DisclosureContent>}
        {segment.parts.map((part) => {
          const isAnswer = part.kind === "reply" && part.node.id === segment.answerId
          const content = <div className="pt-4">{part.kind === "reply" ? (
            <NodeView node={part.node} sessionId={sessionId} depth={0} latestTurn={latestTurn} />
          ) : <TurnProcessGroup group={part} sessionId={sessionId} latestTurn={latestTurn} />}</div>
          const key = part.kind === "reply" ? part.node.id : part.id
          return isAnswer ? <div key={key}>{content}</div> : (
            <DisclosureContent key={key} open={expanded} revision={motion.revision}>{content}</DisclosureContent>
          )
        })}
        {answer?.kind === "reply" && <AssistantActions node={answer.node} sessionId={sessionId}
          alwaysShow={segment.turn === latestTurn} showInterrupted={false} />}
        {!expanded && !answer && reason === "completed" && <div className="pt-4 text-xs text-fg-caption">本轮未生成最终回复</div>}
      </div>
    </section>
  )
}
