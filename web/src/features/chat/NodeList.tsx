import { CircleAlert, CircleStop, Loader2 } from "lucide-react"
import { ApprovalCard } from "@/features/chat/ApprovalCard"
import { AssistantMessage } from "@/features/chat/AssistantMessage"
import { ForkButton } from "@/features/chat/ForkButton"
import { ToolCallCard } from "@/features/chat/ToolCallCard"
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

export function NodeView({ node, sessionId, depth }: { node: ConversationNode; sessionId: string; depth: number }) {
  const liveOutput = useSessionStore((s) =>
    node.kind === "tool-call" ? s.toolOutputs[node.callId] : undefined,
  )
  switch (node.kind) {
    case "user":
      return (
        <div className={cn("group flex items-center gap-1.5", depth === 0 && "justify-end")}>
          {depth === 0 && <ForkButton sessionId={sessionId} seq={node.seq} />}
          <div className="max-w-[85%] whitespace-pre-wrap rounded-lg bg-primary px-3.5 py-2 text-sm text-primary-foreground">
            {node.content}
          </div>
          {depth !== 0 && <ForkButton sessionId={sessionId} seq={node.seq} />}
        </div>
      )
    case "assistant":
      return (
        <div className="group relative">
          <AssistantMessage node={node} />
          {!node.live && <ForkButton sessionId={sessionId} seq={node.seq} className="absolute -top-1 right-0" />}
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
  return (
    <div className="space-y-2">
      {nodes.map((node) => (
        <NodeView key={node.id} node={node} sessionId={sessionId} depth={depth} />
      ))}
    </div>
  )
}

export function OptimisticBubble({ content }: { content: string }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[85%] rounded-lg bg-primary/70 px-3.5 py-2 text-sm text-primary-foreground">
        <span className="whitespace-pre-wrap">{content}</span>
        <Loader2 className="ml-1.5 inline size-3 animate-spin align-text-bottom" />
      </div>
    </div>
  )
}
