import { ChevronDown, ChevronRight, Clock, GitBranch, ListTree, User } from "lucide-react"
import { useMemo, useState, type ReactNode } from "react"
import { EmptyState } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { ApprovalCard } from "@/features/chat/ApprovalCard"
import { AssistantMessage, TokenBadge } from "@/features/chat/AssistantMessage"
import { ToolCallCard } from "@/features/chat/ToolCallCard"
import { deriveConversationNodes, type AssistantNode, type ConversationNode } from "@/lib/conversation"
import { cn } from "@/lib/utils"
import type { SessionEvent, TurnEndReason } from "@/lib/types"
import { useSessionStore } from "@/stores/session"

interface StepBucket {
  step: number
  startSeq?: number
  startTime?: number
  endSeq?: number
  endTime?: number
  items: ConversationNode[]
}

interface TurnBucket {
  turn: number
  startSeq?: number
  startTime?: number
  endSeq?: number
  endTime?: number
  reason?: TurnEndReason
  error?: string
  preItems: ConversationNode[]
  steps: StepBucket[]
  liveItems: ConversationNode[]
}

function buildTurns(events: SessionEvent[], nodes: ConversationNode[]): TurnBucket[] {
  const turns: TurnBucket[] = []
  let current: TurnBucket | null = null

  for (const e of events) {
    if (e.type === "turn/start") {
      current = { turn: e.data.turn, startSeq: e.seq, startTime: e.time, preItems: [], steps: [], liveItems: [] }
      turns.push(current)
    } else if (e.type === "turn/end" && current) {
      current.endSeq = e.seq
      current.endTime = e.time
      current.reason = e.data.reason
      current.error = e.data.error
    } else if (e.type === "step/start" && current) {
      current.steps.push({ step: e.data.step, startSeq: e.seq, startTime: e.time, items: [] })
    } else if (e.type === "step/end" && current) {
      const step = current.steps.find((s) => s.step === e.data.step)
      if (step) {
        step.endSeq = e.seq
        step.endTime = e.time
      }
    }
  }

  const findTurn = (turn: number) => {
    let bucket = turns.find((t) => t.turn === turn)
    if (!bucket) {
      bucket = { turn, preItems: [], steps: [], liveItems: [] }
      turns.push(bucket)
      turns.sort((a, b) => a.turn - b.turn)
    }
    return bucket
  }
  const findStep = (bucket: TurnBucket, step: number) => {
    let s = bucket.steps.find((x) => x.step === step)
    if (!s) {
      s = { step, items: [] }
      bucket.steps.push(s)
      bucket.steps.sort((a, b) => a.step - b.step)
    }
    return s
  }

  for (const node of nodes) {
    if (node.kind === "boundary") continue
    if (node.id.startsWith("live-")) {
      if (turns.length > 0) turns[turns.length - 1].liveItems.push(node)
      continue
    }
    if (node.kind === "user") {
      findTurn(node.turn).preItems.push(node)
      continue
    }
    const bucket = findTurn(node.turn)
    findStep(bucket, node.step).items.push(node)
  }
  return turns
}

function formatClock(t?: number): string {
  if (!t) return "-"
  return new Date(t).toLocaleTimeString("zh-CN", { hour12: false })
}

function formatDuration(start?: number, end?: number): string | null {
  if (!start || !end || end < start) return null
  const ms = end - start
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`
}

function Meta({ children }: { children: ReactNode }) {
  return <span className="flex items-center gap-1 font-mono text-[11px] text-muted-foreground">{children}</span>
}

function CollapsibleBody({
  summary,
  defaultOpen = false,
  children,
}: {
  summary: ReactNode
  defaultOpen?: boolean
  children: ReactNode
}) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="rounded-md border">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left hover:bg-accent/50"
      >
        {summary}
        {open ? (
          <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" />
        ) : (
          <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" />
        )}
      </button>
      {open && <div className="border-t px-2.5 py-2">{children}</div>}
    </div>
  )
}

function ModelItem({ node }: { node: AssistantNode }) {
  return (
    <CollapsibleBody
      summary={
        <>
          <span className="size-1.5 shrink-0 rounded-full bg-primary" />
          <span className="text-xs font-medium">模型响应</span>
          <Meta>#{node.seq === Number.MAX_SAFE_INTEGER ? "live" : node.seq}</Meta>
          <Meta>
            <Clock className="size-3" />
            {formatClock(node.time)}
          </Meta>
          {node.usage && <TokenBadge usage={node.usage} />}
          <span className="flex-1" />
        </>
      }
    >
      <AssistantMessage node={node} />
    </CollapsibleBody>
  )
}

function UserItem({ node }: { node: Extract<ConversationNode, { kind: "user" }> }) {
  return (
    <CollapsibleBody
      summary={
        <>
          <User className="size-3.5 shrink-0 text-muted-foreground" />
          <span className="text-xs font-medium">用户消息</span>
          <Meta>#{node.seq}</Meta>
          <Meta>
            <Clock className="size-3" />
            {formatClock(node.time)}
          </Meta>
          <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">{node.content}</span>
        </>
      }
    >
      <div className="whitespace-pre-wrap text-sm">{node.content}</div>
    </CollapsibleBody>
  )
}

function TrajectoryItem({ node, sessionId }: { node: ConversationNode; sessionId: string }) {
  const liveOutput = useSessionStore((s) => (node.kind === "tool-call" ? s.toolOutputs[node.callId] : undefined))
  if (node.kind === "assistant") return <ModelItem node={node} />
  if (node.kind === "user") return <UserItem node={node} />
  if (node.kind === "tool-call") {
    return (
      <div>
        <div className="mb-1 flex items-center gap-2">
          <Meta>#{node.seq === Number.MAX_SAFE_INTEGER ? "live" : node.seq}</Meta>
          <Meta>
            <Clock className="size-3" />
            {formatClock(node.time)}
          </Meta>
        </div>
        <ToolCallCard node={node} sessionId={sessionId} liveOutput={liveOutput} />
      </div>
    )
  }
  if (node.kind === "approval") {
    return (
      <div>
        <div className="mb-1 flex items-center gap-2">
          <Meta>#{node.seq}</Meta>
          <Meta>
            <Clock className="size-3" />
            {formatClock(node.time)}
          </Meta>
        </div>
        <ApprovalCard node={node} sessionId={sessionId} />
      </div>
    )
  }
  return null
}

const TURN_REASON_LABEL: Record<TurnEndReason, string> = {
  completed: "完成",
  interrupted: "已中断",
  failed: "失败",
}

export function TrajectoryView({ sessionId }: { sessionId: string }) {
  const events = useSessionStore((s) => s.events)
  const liveAttempt = useSessionStore((s) => s.liveAttempt)

  const turns = useMemo(() => {
    const nodes = deriveConversationNodes(events, liveAttempt)
    return buildTurns(events, nodes)
  }, [events, liveAttempt])

  if (turns.length === 0) {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto p-6">
        <EmptyState
          className="border-none"
          icon={<ListTree className="size-10" />}
          title="暂无轨迹"
          description="发送消息后可在此查看 turn / step 时间线"
        />
      </div>
    )
  }

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto max-w-3xl space-y-4 px-4 py-6">
        {turns.map((turn) => {
          const turnDuration = formatDuration(turn.startTime, turn.endTime)
          return (
            <section key={turn.turn} className="rounded-md border">
              <header className="flex items-center gap-2 border-b bg-muted/30 px-3 py-2">
                <GitBranch className="size-3.5 text-muted-foreground" />
                <span className="text-sm font-medium">Turn {turn.turn}</span>
                <Meta>#{turn.startSeq ?? "-"}</Meta>
                <Meta>
                  <Clock className="size-3" />
                  {formatClock(turn.startTime)}
                </Meta>
                {turnDuration && <Meta>耗时 {turnDuration}</Meta>}
                {turn.reason && (
                  <Badge
                    variant={turn.reason === "failed" ? "destructive" : turn.reason === "interrupted" ? "secondary" : "outline"}
                    className="text-[11px] font-normal"
                  >
                    {TURN_REASON_LABEL[turn.reason]}
                  </Badge>
                )}
                {turn.error && <span className="min-w-0 truncate text-xs text-destructive">{turn.error}</span>}
              </header>
              <div className="space-y-3 px-3 py-3">
                {turn.preItems.map((node) => (
                  <TrajectoryItem key={node.id} node={node} sessionId={sessionId} />
                ))}
                {turn.steps.map((step) => {
                  const stepDuration = formatDuration(step.startTime, step.endTime)
                  return (
                    <div key={step.step} className="rounded-md border border-dashed">
                      <div className="flex items-center gap-2 px-2.5 py-1.5">
                        <span className="text-xs font-medium text-muted-foreground">Step {step.step}</span>
                        <Meta>#{step.startSeq ?? "-"}</Meta>
                        <Meta>
                          <Clock className="size-3" />
                          {formatClock(step.startTime)}
                        </Meta>
                        {stepDuration && <Meta>耗时 {stepDuration}</Meta>}
                      </div>
                      <div className={cn("space-y-2 px-2.5 pb-2.5", step.items.length === 0 && "hidden")}>
                        {step.items.map((node) => (
                          <TrajectoryItem key={node.id} node={node} sessionId={sessionId} />
                        ))}
                      </div>
                    </div>
                  )
                })}
                {turn.liveItems.length > 0 && (
                  <div className="rounded-md border border-dashed border-primary/40">
                    <div className="flex items-center gap-2 px-2.5 py-1.5">
                      <span className="size-1.5 animate-pulse rounded-full bg-green-500" />
                      <span className="text-xs font-medium text-muted-foreground">进行中</span>
                    </div>
                    <div className="space-y-2 px-2.5 pb-2.5">
                      {turn.liveItems.map((node) => (
                        <TrajectoryItem key={node.id} node={node} sessionId={sessionId} />
                      ))}
                    </div>
                  </div>
                )}
              </div>
            </section>
          )
        })}
      </div>
    </div>
  )
}
