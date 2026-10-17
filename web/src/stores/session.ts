import { create } from "zustand"
import type {
  AssistantStreamFrame,
  AssistantStreamSnapshot,
  Question,
  SessionEvent,
  SessionHeader,
  StreamChunk,
} from "@/lib/types"
import { useComposerStore } from "@/stores/composer"
import type { ToolOutput } from "@/lib/tool-output"

export type FollowStatus = "connecting" | "live" | "stalled"

export interface LiveAttempt {
  attemptId: string
  turn: number
  step: number
  startedAfterSeq: number
  chunks: StreamChunk[]
  nextIndex: number
}

export interface PendingApproval {
  callId: string
  name: string
  arguments: string
  risk: "high" | "medium"
  turn: number
  step: number
  askedAt: number
  parentCallId?: string
}

export interface PendingQuestion {
  callId: string
  turn: number
  step: number
  questions: Question[]
  askedAt: number
}

interface SessionState {
  sessionId: string | null
  header: SessionHeader | null
  events: SessionEvent[]
  hasMore: boolean
  liveAttempt: LiveAttempt | null
  pendingApprovals: Record<string, PendingApproval>
  pendingQuestions: Record<string, PendingQuestion>
  toolOutputs: Record<string, ToolOutput>
  followStatus: FollowStatus
  lastSeq: number
  applySnapshot: (
    sessionId: string,
    header: SessionHeader,
    records: SessionEvent[],
    hasMore: boolean,
    assistantStream?: AssistantStreamSnapshot,
  ) => void
  appendEvents: (events: SessionEvent[]) => void
  applyStreamFrame: (frame: AssistantStreamFrame) => void
  appendToolOutput: (callId: string, stream: keyof ToolOutput, delta: string) => void
  settlePendingQuestion: (callId: string) => void
  setFollowStatus: (status: FollowStatus) => void
  reset: () => void
}

function derivePendingApprovals(events: SessionEvent[]): Record<string, PendingApproval> {
  const pending: Record<string, PendingApproval> = {}
  for (const e of events) {
    if (e.type === "approval/asked") {
      pending[e.data.call_id] = {
        callId: e.data.call_id,
        name: e.data.name,
        arguments: e.data.arguments,
        risk: e.data.risk,
        turn: e.data.turn,
        step: e.data.step,
        askedAt: e.time,
        parentCallId: e.data.parent_call_id,
      }
    } else if (e.type === "approval/decided") {
      delete pending[e.data.call_id]
    }
  }
  return pending
}

// 未决提问 = asked 且无 answered；超时的 continued 提问保持可补答（protocol.md §10）
function derivePendingQuestions(events: SessionEvent[]): Record<string, PendingQuestion> {
  const pending: Record<string, PendingQuestion> = {}
  for (const e of events) {
    if (e.type === "question/asked") {
      pending[e.data.call_id] = {
        callId: e.data.call_id,
        turn: e.data.turn,
        step: e.data.step,
        questions: e.data.questions,
        askedAt: e.time,
      }
    } else if (e.type === "question/answered") {
      delete pending[e.data.call_id]
    }
  }
  return pending
}

function reconcileOptimistic(events: SessionEvent[]) {
  const { optimistic, clearOptimistic } = useComposerStore.getState()
  if (!optimistic) return
  for (const e of events) {
    if (e.type === "user/message" && e.data.request_id === optimistic.requestId) {
      clearOptimistic()
      return
    }
  }
}

export const useSessionStore = create<SessionState>()((set, get) => ({
  sessionId: null,
  header: null,
  events: [],
  hasMore: false,
  liveAttempt: null,
  pendingApprovals: {},
  pendingQuestions: {},
  toolOutputs: {},
  followStatus: "connecting",
  lastSeq: 0,

  applySnapshot: (sessionId, header, records, hasMore, assistantStream) => {
    reconcileOptimistic(records)
    set({
      sessionId,
      header: { ...header, modelSettingsLocked: header.modelSettingsLocked || records.some((e) => e.type === "user/message") },
      events: records,
      hasMore,
      liveAttempt: assistantStream
        ? {
            attemptId: assistantStream.attemptId,
            turn: assistantStream.turn,
            step: assistantStream.step,
            startedAfterSeq: assistantStream.startedAfterSeq,
            chunks: assistantStream.compactChunks,
            nextIndex: assistantStream.nextIndex,
          }
        : null,
      pendingApprovals: derivePendingApprovals(records),
      pendingQuestions: derivePendingQuestions(records),
      toolOutputs: {},
      followStatus: "live",
      lastSeq: records.length > 0 ? records[records.length - 1].seq : header.lastSeq,
    })
  },

  appendEvents: (incoming) => {
    if (incoming.length === 0) return
    const state = get()
    const events = [...state.events]
    const pendingApprovals = { ...state.pendingApprovals }
    const pendingQuestions = { ...state.pendingQuestions }
    let header = state.header
    let lastSeq = state.lastSeq
    const applied: SessionEvent[] = []
    for (const e of incoming) {
      if (e.seq <= lastSeq) continue
      events.push(e)
      applied.push(e)
      lastSeq = e.seq
      if (e.type === "user/message" && header) {
        header = { ...header, modelSettingsLocked: true }
      }
      if (e.type === "approval/asked") {
        pendingApprovals[e.data.call_id] = {
          callId: e.data.call_id,
          name: e.data.name,
          arguments: e.data.arguments,
          risk: e.data.risk,
          turn: e.data.turn,
          step: e.data.step,
          askedAt: e.time,
          parentCallId: e.data.parent_call_id,
        }
      } else if (e.type === "approval/decided") {
        delete pendingApprovals[e.data.call_id]
      } else if (e.type === "question/asked") {
        pendingQuestions[e.data.call_id] = {
          callId: e.data.call_id,
          turn: e.data.turn,
          step: e.data.step,
          questions: e.data.questions,
          askedAt: e.time,
        }
      } else if (e.type === "question/answered") {
        delete pendingQuestions[e.data.call_id]
      } else if (e.type === "session/title" && header) {
        header = { ...header, title: e.data.title, titlePinned: !e.data.auto }
      } else if (e.type === "session/approval-mode" && header) {
        header = { ...header, approvalMode: e.data.mode }
      } else if (e.type === "model/selection" && header) {
        header = { ...header, modelId: e.data.model_id }
      } else if (e.type === "turn/start" && header) {
        header = { ...header, running: true }
      } else if (e.type === "turn/end" && header) {
        header = { ...header, running: false }
      } else if (e.type === "session/sandbox" && header) {
        header = {
          ...header,
          sandboxStatus: e.data.action === "created" ? "running" : e.data.action === "recycled" ? "recycled" : "none",
        }
      }
    }
    if (applied.length === 0) return
    reconcileOptimistic(applied)
    set({ events, pendingApprovals, pendingQuestions, header, lastSeq })
  },

  applyStreamFrame: (frame) => {
    const state = get()
    if (frame.type === "start") {
      if (state.liveAttempt?.attemptId === frame.attemptId) return
      set({
        liveAttempt: {
          attemptId: frame.attemptId,
          turn: frame.turn,
          step: frame.step,
          startedAfterSeq: frame.startedAfterSeq,
          chunks: [],
          nextIndex: 0,
        },
      })
      return
    }
    const live = state.liveAttempt
    if (!live || live.attemptId !== frame.attemptId) return
    if (frame.type === "chunk") {
      if (frame.index < live.nextIndex) return
      set({
        liveAttempt: { ...live, chunks: [...live.chunks, frame.chunk], nextIndex: frame.index + 1 },
      })
      return
    }
    set({ liveAttempt: null })
  },

  appendToolOutput: (callId, stream, delta) =>
    set((state) => {
      const output = state.toolOutputs[callId] ?? { stdout: "", stderr: "" }
      return { toolOutputs: { ...state.toolOutputs, [callId]: { ...output, [stream]: output[stream] + delta } } }
    }),

  // 服务端判定已作答（如 question/not_pending）时本地撤卡；事件流随后会对账
  settlePendingQuestion: (callId) =>
    set((state) => {
      if (!(callId in state.pendingQuestions)) return state
      const pendingQuestions = { ...state.pendingQuestions }
      delete pendingQuestions[callId]
      return { pendingQuestions }
    }),

  setFollowStatus: (followStatus) => set({ followStatus }),

  reset: () =>
    set({
      sessionId: null,
      header: null,
      events: [],
      hasMore: false,
      liveAttempt: null,
      pendingApprovals: {},
      pendingQuestions: {},
      toolOutputs: {},
      followStatus: "connecting",
      lastSeq: 0,
    }),
}))
