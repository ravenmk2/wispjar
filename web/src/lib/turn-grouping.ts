import type { AssistantNode, BoundaryNode, ConversationNode, QuestionReplyNode, UserNode } from "@/lib/conversation"

export interface TurnSegment {
  turn: number
  start?: BoundaryNode
  user?: UserNode | QuestionReplyNode
  process: ConversationNode[]
  answer?: AssistantNode
  // 最终回复剥离出的思考块，随折叠组展开渲染（answer 节点本身只留 text）
  answerReasoning: string[]
  end?: BoundaryNode
  hasPendingApproval: boolean
  toolCallCount: number
  taskCount: number
  durationMs?: number
}

// 含未决审批或未决提问（asked 且无 answered）的子树强制展开，复用同一 forceOpen 通道
export function hasPendingApproval(nodes: ConversationNode[]): boolean {
  return nodes.some((n) => {
    if (n.kind === "approval") return n.status === "pending"
    if (n.kind === "tool-call") {
      if (n.question && !n.question.answers) return true
      return hasPendingApproval(n.children)
    }
    return false
  })
}

// 按 turn 把顶层节点流切段；turn===0 的 live 节点不进组，保持逐条可见
export function groupByTurn(nodes: ConversationNode[]): { segments: TurnSegment[]; loose: ConversationNode[] } {
  const segments: TurnSegment[] = []
  const byTurn = new Map<number, TurnSegment>()
  const loose: ConversationNode[] = []

  const segmentOf = (turn: number): TurnSegment => {
    let seg = byTurn.get(turn)
    if (!seg) {
      seg = { turn, process: [], answerReasoning: [], hasPendingApproval: false, toolCallCount: 0, taskCount: 0 }
      byTurn.set(turn, seg)
      segments.push(seg)
    }
    return seg
  }

  for (const node of nodes) {
    if (node.turn === 0) {
      loose.push(node)
      continue
    }
    const seg = segmentOf(node.turn)
    switch (node.kind) {
      case "user":
      case "question-reply":
        seg.user = node
        break
      case "boundary":
        if (node.phase === "start") seg.start = node
        else seg.end = node
        break
      default:
        seg.process.push(node)
    }
  }

  for (const seg of segments) {
    // 最终回答：该轮最后一个含 text block 的 assistant 节点，其前的 assistant 节点留在过程段
    for (let i = seg.process.length - 1; i >= 0; i--) {
      const n = seg.process[i]
      if (n.kind === "assistant" && n.content.some((b) => b.type === "text")) {
        seg.process.splice(i, 1)
        seg.answerReasoning = n.content.filter((b) => b.type === "reasoning").map((b) => b.text)
        seg.answer =
          seg.answerReasoning.length > 0
            ? { ...n, content: n.content.filter((b) => b.type !== "reasoning") }
            : n
        break
      }
    }
    seg.toolCallCount = seg.process.filter((n) => n.kind === "tool-call" && n.name !== "task").length
    seg.taskCount = seg.process.filter((n) => n.kind === "tool-call" && n.name === "task").length
    seg.hasPendingApproval = hasPendingApproval(seg.process)
    if (seg.start && seg.end) seg.durationMs = Math.max(1000, seg.end.time - seg.start.time)
  }

  return { segments, loose }
}

export function formatTurnDuration(ms: number): string {
  const total = Math.max(1, Math.round(ms / 1000))
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = total % 60
  if (hours > 0) return minutes > 0 ? `${hours}小时${minutes}分` : `${hours}小时`
  if (minutes > 0) return seconds > 0 ? `${minutes}分${seconds}秒` : `${minutes}分`
  return `${seconds}秒`
}
