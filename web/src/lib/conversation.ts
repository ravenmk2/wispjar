import { safeParseJson } from "@/lib/format"
import type { AttachmentRef, ContentBlock, Question, QuestionAnswer, SessionEvent, StreamChunk, ToolResultMeta, TurnEndReason, Usage } from "@/lib/types"

export interface UserNode {
  kind: "user"
  id: string
  seq: number
  time: number
  turn: number
  content: string
  requestId: string
  attachments?: AttachmentRef[]
}

export interface AssistantNode {
  kind: "assistant"
  id: string
  seq: number
  time: number
  turn: number
  step: number
  content: ContentBlock[]
  usage?: Usage
  interrupted?: boolean
  live?: boolean
}

export interface ToolResultData {
  content: string
  error?: string
  meta?: ToolResultMeta
  attachments?: AttachmentRef[]
}

// ask_user_question 工具挂接的提问状态：answers 缺省 = 未决（open 或 continued 均可补答）
export interface QuestionState {
  questions: Question[]
  answers?: QuestionAnswer[]
  answeredBy?: string
}

export interface ToolCallNode {
  kind: "tool-call"
  id: string
  seq: number
  time: number
  turn: number
  step: number
  callId: string
  name: string
  arguments: string
  result?: ToolResultData
  question?: QuestionState
  live?: boolean
  children: ConversationNode[]
}

export interface ApprovalNode {
  kind: "approval"
  id: string
  seq: number
  time: number
  turn: number
  step: number
  callId: string
  name: string
  arguments: string
  risk: "high" | "medium"
  status: "pending" | "allow" | "deny"
  auto?: boolean
  decidedBy?: string
}

export interface BoundaryNode {
  kind: "boundary"
  id: string
  seq: number
  time: number
  turn: number
  phase: "start" | "end"
  reason?: TurnEndReason
  error?: string
}

// 迟到提问答复合成的 user/message（source.kind==="question-reply"），按问答对渲染
export interface QuestionReplyNode {
  kind: "question-reply"
  id: string
  seq: number
  time: number
  turn: number
  callId: string
  questions: Question[]
  answers: QuestionAnswer[]
}

export type ConversationNode = UserNode | AssistantNode | ToolCallNode | ApprovalNode | BoundaryNode | QuestionReplyNode

interface PendingBlock {
  blockType: "text" | "reasoning" | "tool-call"
  text: string
  id?: string
  name?: string
  arguments: string
}

export function foldChunks(chunks: StreamChunk[]): { blocks: ContentBlock[]; usage?: Usage } {
  const finalized = new Map<number, ContentBlock>()
  const pending = new Map<number, PendingBlock>()
  let usage: Usage | undefined
  for (const chunk of chunks) {
    switch (chunk.type) {
      case "block-start":
        pending.set(chunk.index, { blockType: chunk.blockType, text: "", arguments: "" })
        break
      case "text-delta":
      case "reasoning-delta": {
        const b = pending.get(chunk.index)
        if (b) b.text += chunk.text
        break
      }
      case "tool-call-delta": {
        const b = pending.get(chunk.index)
        if (b) {
          if (chunk.id) b.id = chunk.id
          if (chunk.name) b.name = chunk.name
          if (chunk.argumentsDelta) b.arguments += chunk.argumentsDelta
        }
        break
      }
      case "block-end":
        finalized.set(chunk.index, chunk.block)
        pending.delete(chunk.index)
        break
      case "usage":
        usage = {
          input_tokens: chunk.usage.inputTokens,
          output_tokens: chunk.usage.outputTokens,
          cache_read_tokens: chunk.usage.cacheReadTokens,
          cache_write_tokens: chunk.usage.cacheWriteTokens,
          reasoning_tokens: chunk.usage.reasoningTokens,
          cache_reported: chunk.usage.cacheReported,
        }
        break
      case "finish":
        break
    }
  }
  const indices = new Set([...finalized.keys(), ...pending.keys()])
  const blocks: ContentBlock[] = []
  for (const index of [...indices].sort((a, b) => a - b)) {
    const done = finalized.get(index)
    if (done) {
      blocks.push(done)
      continue
    }
    const p = pending.get(index)
    if (!p) continue
    if (p.blockType === "tool-call") {
      blocks.push({ type: "tool-call", id: p.id ?? "", name: p.name ?? "", arguments: p.arguments })
    } else {
      blocks.push({ type: p.blockType, text: p.text })
    }
  }
  return { blocks, usage }
}

export function deriveConversationNodes(
  events: SessionEvent[],
  liveAttempt: { chunks: StreamChunk[] } | null,
): ConversationNode[] {
  const nodes: ConversationNode[] = []
  const toolNodes = new Map<string, ToolCallNode>()
  const approvalNodes = new Map<string, ApprovalNode>()

  const parentOf = (data: unknown): string | undefined =>
    (data as { parent_call_id?: string }).parent_call_id

  const targetList = (parentCallId?: string): ConversationNode[] => {
    if (parentCallId) {
      const parent = toolNodes.get(parentCallId)
      if (parent) return parent.children
    }
    return nodes
  }

  const ensureToolNode = (
    callId: string,
    seed: Omit<ToolCallNode, "kind" | "id" | "callId" | "result" | "children">,
    parentCallId?: string,
  ): ToolCallNode => {
    let node = toolNodes.get(callId)
    if (node) {
      if (seed.arguments) node.arguments = seed.arguments
      if (seed.name) node.name = seed.name
      return node
    }
    node = { kind: "tool-call", id: `tool-${callId}`, callId, children: [], ...seed }
    toolNodes.set(callId, node)
    targetList(parentCallId).push(node)
    return node
  }

  for (const e of events) {
    switch (e.type) {
      case "turn/start":
        nodes.push({ kind: "boundary", id: `boundary-${e.seq}`, seq: e.seq, time: e.time, turn: e.data.turn, phase: "start" })
        break
      case "turn/end":
        nodes.push({
          kind: "boundary",
          id: `boundary-${e.seq}`,
          seq: e.seq,
          time: e.time,
          turn: e.data.turn,
          phase: "end",
          reason: e.data.reason,
          error: e.data.error,
        })
        break
      case "user/message": {
        const source = e.data.source
        if (source?.kind === "question-reply") {
          const body = safeParseJson(e.data.content)
          if (
            body?.kind === "answer_to_pending_question" &&
            Array.isArray(body.questions) &&
            Array.isArray(body.answers)
          ) {
            nodes.push({
              kind: "question-reply",
              id: `qreply-${e.seq}`,
              seq: e.seq,
              time: e.time,
              turn: e.data.turn,
              callId: typeof body.call_id === "string" ? body.call_id : source.call_id,
              questions: body.questions as Question[],
              answers: body.answers as QuestionAnswer[],
            })
            break
          }
        }
        nodes.push({
          kind: "user",
          id: `user-${e.seq}`,
          seq: e.seq,
          time: e.time,
          turn: e.data.turn,
          content: e.data.content,
          requestId: e.data.request_id,
          attachments: e.data.attachments,
        })
        break
      }
      case "assistant/message": {
        const textBlocks = e.data.message.content.filter((b) => b.type !== "tool-call")
        if (textBlocks.length > 0) {
          nodes.push({
            kind: "assistant",
            id: `asst-${e.seq}`,
            seq: e.seq,
            time: e.time,
            turn: e.data.turn,
            step: e.data.step,
            content: textBlocks,
            usage: e.data.usage,
            interrupted: e.data.interrupted,
          })
        }
        for (const block of e.data.message.content) {
          if (block.type !== "tool-call") continue
          ensureToolNode(block.id, {
            seq: e.seq,
            time: e.time,
            turn: e.data.turn,
            step: e.data.step,
            name: block.name,
            arguments: block.arguments,
            live: false,
          })
        }
        break
      }
      case "tool/call":
        ensureToolNode(
          e.data.call_id,
          { seq: e.seq, time: e.time, turn: e.data.turn, step: e.data.step, name: e.data.name, arguments: e.data.arguments, live: false },
          parentOf(e.data),
        )
        break
      case "tool/result": {
        const node = ensureToolNode(
          e.data.call_id,
          { seq: e.seq, time: e.time, turn: e.data.turn, step: e.data.step, name: e.data.name, arguments: "", live: false },
          parentOf(e.data),
        )
        node.result = { content: e.data.content, error: e.data.error, meta: e.data.meta, attachments: e.data.attachments }
        node.live = false
        break
      }
      case "approval/asked": {
        const node: ApprovalNode = {
          kind: "approval",
          id: `approval-${e.data.call_id}`,
          seq: e.seq,
          time: e.time,
          turn: e.data.turn,
          step: e.data.step,
          callId: e.data.call_id,
          name: e.data.name,
          arguments: e.data.arguments,
          risk: e.data.risk,
          status: "pending",
        }
        approvalNodes.set(e.data.call_id, node)
        targetList(parentOf(e.data)).push(node)
        break
      }
      case "approval/decided": {
        const node = approvalNodes.get(e.data.call_id)
        if (node) {
          node.status = e.data.decision
          node.auto = e.data.auto
          node.decidedBy = e.data.decided_by
        }
        break
      }
      case "question/asked": {
        const node = ensureToolNode(
          e.data.call_id,
          { seq: e.seq, time: e.time, turn: e.data.turn, step: e.data.step, name: "ask_user_question", arguments: "", live: false },
          parentOf(e.data),
        )
        // asked 恒先于 answered（runtime.md §5.3），直接覆盖
        node.question = { questions: e.data.questions }
        break
      }
      case "question/answered": {
        const node = toolNodes.get(e.data.call_id)
        if (node?.question) {
          node.question.answers = e.data.answers
          node.question.answeredBy = e.data.answered_by
        }
        break
      }
      default:
        break
    }
  }

  if (liveAttempt) {
    const { blocks, usage } = foldChunks(liveAttempt.chunks)
    const textBlocks = blocks.filter((b) => b.type !== "tool-call")
    if (textBlocks.length > 0 || blocks.length === 0) {
      nodes.push({
        kind: "assistant",
        id: "live-asst",
        seq: Number.MAX_SAFE_INTEGER,
        time: Date.now(),
        turn: 0,
        step: 0,
        content: textBlocks,
        usage,
        live: true,
      })
    }
    for (const block of blocks) {
      if (block.type !== "tool-call" || !block.id) continue
      const node: ToolCallNode = {
        kind: "tool-call",
        id: `live-tool-${block.id}`,
        seq: Number.MAX_SAFE_INTEGER,
        time: Date.now(),
        turn: 0,
        step: 0,
        callId: block.id,
        name: block.name,
        arguments: block.arguments,
        live: true,
        children: [],
      }
      toolNodes.set(block.id, node)
      nodes.push(node)
    }
  }

  return nodes
}
