import { Loader2, MessageCircleQuestion, X } from "lucide-react"
import { useMemo } from "react"
import { ApprovalCard } from "@/features/chat/ApprovalCard"
import { actionBarCls, AssistantMessage } from "@/features/chat/AssistantMessage"
import { ForkButton } from "@/features/chat/ForkButton"
import { MessageImages } from "@/features/chat/MessageImages"
import { QuestionAnswerList } from "@/features/chat/QuestionCard"
import { ToolCallCard } from "@/features/chat/ToolCallCard"
import { TurnView } from "@/features/chat/TurnView"
import { CopyIconButton } from "@/components/CopyIconButton"
import { formatMessageTime } from "@/lib/format"
import { groupByTurn } from "@/lib/turn-grouping"
import { useSessionStore } from "@/stores/session"
import { cn } from "@/lib/utils"
import type { ConversationNode } from "@/lib/conversation"

function BoundaryView({ node }: { node: Extract<ConversationNode, { kind: "boundary" }> }) {
  if (node.phase === "start") return null
  if (node.reason === "completed") return null
  return (
    <div className="py-1 text-xs text-fg-secondary">
      {node.reason === "interrupted" ? (
        <span>已停止</span>
      ) : (
        <details>
          <summary className="flex w-fit cursor-pointer list-none items-center gap-1.5 rounded hover:bg-accent">
            运行出错<X className="size-3.5 text-destructive" strokeWidth={2.5} />{node.error && <span>查看详情</span>}
          </summary>
          {node.error && <div className="mt-2 rounded-lg bg-muted/50 p-3">
            <div className="flex items-center justify-between"><span>错误详情</span><CopyIconButton text={node.error} /></div>
            <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-words font-sans tabular-nums">{node.error}</pre>
          </div>}
        </details>
      )}
    </div>
  )
}

export function NodeView({
  node,
  sessionId,
  depth,
  latestTurn,
  isAnswer = false,
}: {
  node: ConversationNode
  sessionId: string
  depth: number
  latestTurn: number
  isAnswer?: boolean
}) {
  const liveOutput = useSessionStore((s) =>
    node.kind === "tool-call" ? s.toolOutputs[node.callId] : undefined,
  )
  // 最新一轮的用户/助手操作条常驻，更早轮次 hover 浮现
  const inLatestTurn = depth === 0 && node.turn > 0 && node.turn === latestTurn
  switch (node.kind) {
    case "user":
      return (
        <div className={cn("group flex flex-col gap-1", depth === 0 && "items-end")}>
          <div className="max-w-[85%] whitespace-pre-wrap break-words rounded-2xl bg-bubble px-4 py-2.5 text-chat text-bubble-foreground">
            {node.attachments && node.attachments.length > 0 && (
              <MessageImages attachments={node.attachments} className={cn(node.content && "mb-1.5")} />
            )}
            {node.content}
          </div>
          <div className={cn("flex items-center gap-0.5", actionBarCls(inLatestTurn))}>
            <span className="px-1 text-meta text-fg-caption">{formatMessageTime(node.time)}</span>
            <CopyIconButton text={node.content} />
            {depth !== 0 && <ForkButton sessionId={sessionId} seq={node.seq} />}
          </div>
        </div>
      )
    case "assistant":
      // 顶层操作条由 TurnView 在整轮末尾渲染；嵌套回复保留自身操作条。
      return (
        <div className="group">
          <AssistantMessage
            node={node}
            sessionId={sessionId}
            showActions={isAnswer}
            alwaysShowActions={isAnswer && inLatestTurn}
            showMetaFallback={isAnswer}
            showInterrupted={depth > 0 || !node.endReason}
          />
        </div>
      )
    case "tool-call":
      return <ToolCallCard node={node} sessionId={sessionId} liveOutput={liveOutput} depth={depth} />
    case "question-reply":
      // 迟到提问答复：按问答对渲染（不是普通气泡）
      return (
        <div className={cn("group flex flex-col gap-1", depth === 0 && "items-end")}>
          <div className="w-full max-w-[85%] rounded-2xl border bg-card px-3.5 py-2.5">
            <div className="mb-1.5 flex items-center gap-1.5 text-xs text-fg-caption">
              <MessageCircleQuestion className="size-3.5" />
              已补答提问
            </div>
            <QuestionAnswerList questions={node.questions} answers={node.answers} />
          </div>
          <div className={cn("flex items-center gap-0.5", actionBarCls(inLatestTurn))}>
            <span className="px-1 text-meta text-fg-caption">{formatMessageTime(node.time)}</span>
          </div>
        </div>
      )
    case "approval":
      return <ApprovalCard node={node} sessionId={sessionId} />
    case "boundary":
      return <BoundaryView node={node} />
  }
}

export function NodeList({
  nodes,
  sessionId,
  depth = 0,
}: {
  nodes: ConversationNode[]
  sessionId: string
  depth?: number
}) {
  const running = useSessionStore((s) => s.header ? s.header.running || s.liveAttempt !== null : undefined)
  const latestTurn = useMemo(
    () => nodes.reduce((max, n) => Math.max(max, n.turn), 0),
    [nodes],
  )
  const grouped = useMemo(() => (depth === 0 ? groupByTurn(nodes, running) : null), [nodes, depth, running])
  // 嵌套（depth>0）渲染时，只有最后一条带文本的 assistant 视为最终回复
  const nestedAnswerId = useMemo(() => {
    if (grouped) return null
    for (let i = nodes.length - 1; i >= 0; i--) {
      const n = nodes[i]
      if (n.kind === "assistant" && !n.live && n.content.some((b) => b.type === "text")) return n.id
    }
    return null
  }, [nodes, grouped])
  if (!grouped) {
    return (
      <div className="space-y-5">
        {nodes.map((node) => (
          <NodeView
            key={node.id}
            node={node}
            sessionId={sessionId}
            depth={depth}
            latestTurn={latestTurn}
            isAnswer={node.id === nestedAnswerId}
          />
        ))}
      </div>
    )
  }
  return (
    <div className="space-y-5">
      {grouped.segments.map((seg) => (
        <TurnView key={`turn-${seg.turn}`} segment={seg} sessionId={sessionId} latestTurn={latestTurn} />
      ))}
      {grouped.loose.map((node) => (
        <NodeView key={node.id} node={node} sessionId={sessionId} depth={depth} latestTurn={latestTurn} />
      ))}
    </div>
  )
}

export function OptimisticBubble({
  content,
  attachments,
}: {
  content: string
  attachments?: Array<{ url: string; name: string }>
}) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[85%] rounded-2xl bg-bubble px-4 py-2.5 text-chat text-bubble-foreground">
        {attachments && attachments.length > 0 && (
          <div className={cn("flex flex-wrap gap-1", content && "mb-1.5")}>
            {attachments.map((a, i) => (
              <img
                key={i}
                src={a.url}
                alt={a.name}
                className={
                  attachments.length === 1
                    ? "max-h-60 max-w-60 rounded-lg object-cover"
                    : "size-16 rounded-md object-cover"
                }
              />
            ))}
          </div>
        )}
        <span className="whitespace-pre-wrap break-words">{content}</span>
        <Loader2 className="ml-1.5 inline size-3 animate-spin align-text-bottom text-fg-tertiary" />
      </div>
    </div>
  )
}
