import type { AssistantNode, BoundaryNode, ConversationNode, QuestionReplyNode, UserNode } from "@/lib/conversation"
import { toolFailure } from "./tool-output"

export interface ProcessGroup {
  kind: "process"
  id: string
  nodes: ConversationNode[]
  hasPending: boolean
  hasError: boolean
  running: boolean
}

export type TurnPart = ProcessGroup | { kind: "reply"; node: AssistantNode }

export interface TurnSegment {
  turn: number
  running: boolean
  start?: BoundaryNode
  user?: UserNode | QuestionReplyNode
  parts: TurnPart[]
  answerId?: string
  end?: BoundaryNode
  hasPendingApproval: boolean
  durationMs?: number
}

export function hasPendingApproval(nodes: ConversationNode[]): boolean {
  return nodes.some((n) => {
    if (n.kind === "approval") return n.status === "pending"
    if (n.kind === "tool-call") return !!(n.question && !n.question.answers) || hasPendingApproval(n.children)
    return false
  })
}

export function hasProcessError(nodes: ConversationNode[]): boolean {
  return nodes.some((n) => {
    if (n.kind === "tool-call") {
      if (toolFailure(n) || hasProcessError(n.children)) return true
    }
    return n.kind === "boundary" && n.phase === "end" && n.reason === "failed"
  })
}

// Split on visible text, never on stream frames. Reasoning and tools stay in original order.
export function groupByTurn(nodes: ConversationNode[], sessionRunning?: boolean): { segments: TurnSegment[]; loose: ConversationNode[] } {
  const segments: TurnSegment[] = []
  const byTurn = new Map<number, TurnSegment>()
  const loose: ConversationNode[] = []

  const addProcess = (seg: TurnSegment, node: ConversationNode) => {
    const last = seg.parts[seg.parts.length - 1]
    if (last?.kind === "process") last.nodes.push(node)
    else seg.parts.push({ kind: "process", id: `process-${node.id}`, nodes: [node], hasPending: false, hasError: false, running: true })
  }

  for (const node of nodes) {
    if (node.turn <= 0) { loose.push(node); continue }
    let seg = byTurn.get(node.turn)
    if (!seg) {
      seg = { turn: node.turn, running: true, parts: [], hasPendingApproval: false }
      byTurn.set(node.turn, seg)
      segments.push(seg)
    }
    if (node.kind === "user" || node.kind === "question-reply") seg.user = node
    else if (node.kind === "boundary") {
      if (node.phase === "start") seg.start = node
      else seg.end = node
    } else if (node.kind === "assistant") {
      let reply: AssistantNode | undefined
      node.content.forEach((block, index) => {
        if (block.type === "text" && block.text.trim()) {
          if (reply) reply.content.push(block)
          else {
            reply = { ...node, id: `${node.id}-text-${index}`, content: [block] }
            seg.parts.push({ kind: "reply", node: reply })
          }
        } else if (block.type === "reasoning") {
          reply = undefined
          addProcess(seg, { ...node, id: `${node.id}-reasoning-${index}`, content: [block] })
        }
      })
    } else addProcess(seg, node)
  }

  const latestTurn = segments.reduce((latest, seg) => Math.max(latest, seg.turn), 0)
  for (const seg of segments) {
    // Forked history may end at an assistant message, before the original turn/end.
    // An idle chat or a later turn settles it without inventing an end event.
    seg.running = !seg.end && seg.turn === latestTurn && sessionRunning !== false
    for (const part of seg.parts) {
      if (part.kind !== "process") continue
      part.running = seg.running
      part.hasPending = seg.running && hasPendingApproval(part.nodes)
      part.hasError = hasProcessError(part.nodes)
      seg.hasPendingApproval ||= part.hasPending
    }
    if (!seg.running) {
      const last = seg.parts[seg.parts.length - 1]
      if (last?.kind === "reply") seg.answerId = last.node.id
      else if (seg.end && seg.end.reason !== "completed") {
        // Keep the last text of a stopped turn without treating it as a completed answer.
        for (let i = seg.parts.length - 1; i >= 0; i--) {
          const part = seg.parts[i]
          if (part.kind === "reply") { seg.answerId = part.node.id; break }
        }
      }
    }
    if (seg.start && seg.end) seg.durationMs = Math.max(1000, seg.end.time - seg.start.time)
  }
  return { segments, loose }
}

export function reasoningTail(text: string, limit = 160): string {
  const tail = text.trimEnd().slice(-limit).replace(/\s+/g, " ").trim()
  return text.trimEnd().length > limit ? `…${tail}` : tail
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
