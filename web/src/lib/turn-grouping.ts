import type { AssistantNode, BoundaryNode, ConversationNode, QuestionReplyNode, UserNode } from "@/lib/conversation"
import type { ContentBlock } from "@/lib/types"

type ReasoningBlockData = Extract<ContentBlock, { type: "reasoning" }>

export interface TurnSegment {
  turn: number
  start?: BoundaryNode
  user?: UserNode | QuestionReplyNode
  process: ConversationNode[]
  answer?: AssistantNode
  // 最终回复剥离出的思考块，随折叠组展开渲染（answer 节点本身只留 text）
  answerReasoning: ReasoningBlockData[]
  end?: BoundaryNode
  hasPendingApproval: boolean
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
      seg = { turn, process: [], answerReasoning: [], hasPendingApproval: false }
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
    // 最终回答：仅当该轮最后一个节点是含 text block 的 assistant 时才算；
    // 否则（轮次以工具调用收尾：中断/达到迭代上限/被停止）所有内容留在过程段穿插展示，
    // 不把中间回复误当最终回复
    const n = seg.process[seg.process.length - 1]
    if (n && n.kind === "assistant" && n.content.some((b) => b.type === "text")) {
      seg.process.splice(seg.process.length - 1, 1)
      seg.answerReasoning = n.content.filter((b): b is ReasoningBlockData => b.type === "reasoning")
      seg.answer =
        seg.answerReasoning.length > 0
          ? { ...n, content: n.content.filter((b) => b.type !== "reasoning") }
          : n
    }
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
