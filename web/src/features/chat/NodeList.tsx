import { CircleAlert, CircleStop, Loader2, MessageCircleQuestion } from "lucide-react"
import { Fragment, useMemo } from "react"
import { ApprovalCard } from "@/features/chat/ApprovalCard"
import { actionBarCls, AssistantMessage } from "@/features/chat/AssistantMessage"
import { ForkButton } from "@/features/chat/ForkButton"
import { MessageImages } from "@/features/chat/MessageImages"
import { QuestionAnswerList } from "@/features/chat/QuestionCard"
import { ToolCallCard } from "@/features/chat/ToolCallCard"
import { TurnProcessGroup } from "@/features/chat/TurnProcessGroup"
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
    <div className="flex items-center gap-2 py-1 text-xs text-muted-foreground">
      <div className="h-px flex-1 bg-border" />
      {node.reason === "interrupted" ? (
        <span className="flex items-center gap-1">
          <CircleStop className="size-3.5" />
          已中断
        </span>
      ) : (
        <span className="flex items-center gap-1 text-destructive">
          <CircleAlert className="size-3.5" />
          失败{node.error ? `：${node.error}` : ""}
        </span>
      )}
      <div className="h-px flex-1 bg-border" />
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
          <div className="max-w-[85%] whitespace-pre-wrap rounded-2xl bg-bubble px-3.5 py-2 text-sm text-foreground">
            {node.attachments && node.attachments.length > 0 && (
              <MessageImages attachments={node.attachments} className={cn(node.content && "mb-1.5")} />
            )}
            {node.content}
          </div>
          <div className={cn("flex items-center gap-0.5", actionBarCls(inLatestTurn))}>
            <span className="px-1 text-[13px] text-fg-caption">{formatMessageTime(node.time)}</span>
            <CopyIconButton text={node.content} />
            {depth !== 0 && <ForkButton sessionId={sessionId} seq={node.seq} />}
          </div>
        </div>
      )
    case "assistant":
      // 只有一轮的最终回复展示操作条与 token 统计；中间过程回复不展示
      return (
        <div className="group">
          <AssistantMessage
            node={node}
            sessionId={sessionId}
            showActions={isAnswer}
            alwaysShowActions={isAnswer && inLatestTurn}
            showMetaFallback={isAnswer}
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
            <span className="px-1 text-[13px] text-fg-caption">{formatMessageTime(node.time)}</span>
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
  const latestTurn = useMemo(
    () => nodes.reduce((max, n) => (n.id.startsWith("live-") ? max : Math.max(max, n.turn)), 0),
    [nodes],
  )
  const grouped = useMemo(() => (depth === 0 ? groupByTurn(nodes) : null), [nodes, depth])
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
      <div className="space-y-2">
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
    <div className="space-y-2">
      {grouped.segments.map((seg) => (
        <Fragment key={`turn-${seg.turn}`}>
          {seg.user && <NodeView node={seg.user} sessionId={sessionId} depth={depth} latestTurn={latestTurn} />}
          {seg.process.length > 0 && (
            <TurnProcessGroup segment={seg} sessionId={sessionId} latestTurn={latestTurn} />
          )}
          {seg.answer && (
            <NodeView node={seg.answer} sessionId={sessionId} depth={depth} latestTurn={latestTurn} isAnswer />
          )}
          {seg.end && <NodeView node={seg.end} sessionId={sessionId} depth={depth} latestTurn={latestTurn} />}
        </Fragment>
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
      <div className="max-w-[85%] rounded-2xl bg-bubble px-3.5 py-2 text-sm text-foreground">
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
        <span className="whitespace-pre-wrap">{content}</span>
        <Loader2 className="ml-1.5 inline size-3 animate-spin align-text-bottom text-fg-tertiary" />
      </div>
    </div>
  )
}
