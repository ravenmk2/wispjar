import { CircleAlert, CircleStop, Loader2 } from "lucide-react"
import { useMemo } from "react"
import { ApprovalCard } from "@/features/chat/ApprovalCard"
import { actionBarCls, AssistantMessage } from "@/features/chat/AssistantMessage"
import { ForkButton } from "@/features/chat/ForkButton"
import { ToolCallCard } from "@/features/chat/ToolCallCard"
import { CopyIconButton } from "@/components/CopyIconButton"
import { formatMessageTime } from "@/lib/format"
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
}: {
  node: ConversationNode
  sessionId: string
  depth: number
  latestTurn: number
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
      return (
        <div className="group">
          <AssistantMessage
            node={node}
            sessionId={sessionId}
            showActions
            alwaysShowActions={inLatestTurn}
          />
        </div>
      )
    case "tool-call":
      return <ToolCallCard node={node} sessionId={sessionId} liveOutput={liveOutput} depth={depth} />
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
  return (
    <div className="space-y-2">
      {nodes.map((node) => (
        <NodeView key={node.id} node={node} sessionId={sessionId} depth={depth} latestTurn={latestTurn} />
      ))}
    </div>
  )
}

export function OptimisticBubble({ content }: { content: string }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[85%] rounded-2xl bg-bubble px-3.5 py-2 text-sm text-foreground">
        <span className="whitespace-pre-wrap">{content}</span>
        <Loader2 className="ml-1.5 inline size-3 animate-spin align-text-bottom text-fg-tertiary" />
      </div>
    </div>
  )
}
