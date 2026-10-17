import assert from "node:assert/strict"
import { afterEach, test } from "node:test"
import { sessionsApi } from "@/api/sessions"
import { useSessionStore } from "@/stores/session"
import { deriveConversationNodes } from "./conversation"
import { groupByTurn } from "./turn-grouping"
import type { SessionEvent, SessionEventsResponse, SessionHeader } from "./types"

const originalEvents = sessionsApi.events
afterEach(() => {
  sessionsApi.events = originalEvents
  useSessionStore.getState().reset()
})

function fixture(count: number, sessionId = "pelican") {
  const events: SessionEvent[] = Array.from({ length: count }, (_, index) => ({
    seq: index + 1, time: (index + 1) * 1000, type: "assistant/message",
    data: { turn: 1, step: index, message: { role: "assistant", content: [{ type: "text", text: `step ${index}` }] } },
  }))
  events[0] = { seq: 1, time: 1000, type: "user/message", data: { turn: 1, content: "画一个鹈鹕骑自行车的 SVG 动画", request_id: "request" } }
  events[1] = { seq: 2, time: 2000, type: "turn/start", data: { turn: 1, request_id: "request" } }
  events[count - 1] = { seq: count, time: count * 1000, type: "turn/end", data: { turn: 1, reason: "completed" } }
  const header: SessionHeader = {
    sessionId, title: "鹈鹕骑自行车SVG动画", titlePinned: false, agentId: "agent", agentName: "Agent", agentAvatar: "",
    modelId: "model", modelSettingsLocked: true, approvalMode: "auto", sandboxStatus: "none", running: false,
    lastSeq: count, usageTotal: { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, reasoning_tokens: 0 }, createdAt: "",
  }
  useSessionStore.getState().applySnapshot(sessionId, header, events.slice(-200), count > 200)
  return { events, header }
}

function deferred() {
  let resolve!: (value: SessionEventsResponse) => void
  const promise = new Promise<SessionEventsResponse>((done) => { resolve = done })
  return { promise, resolve }
}

test("long turns restore the user message and turn duration outside the 200-event tail", async () => {
  for (const count of [204, 324, 1324]) {
    const { events } = fixture(count)
    assert.equal(deriveConversationNodes(useSessionStore.getState().events, null).some((node) => node.kind === "user"), false)
    const calls: number[] = []
    sessionsApi.events = async (id, after, limit = 500) => {
      assert.equal(id, "pelican")
      assert.ok(limit <= 500)
      calls.push(after)
      return { items: events.slice(after, after + limit), last_seq: count }
    }
    await useSessionStore.getState().loadHistory()
    const state = useSessionStore.getState()
    assert.deepEqual(state.events, events)
    assert.equal(state.hasMore, false)
    assert.equal(state.lastSeq, count)
    const segment = groupByTurn(deriveConversationNodes(state.events, null), false).segments[0]
    assert.equal(segment.user?.kind, "user")
    assert.equal(segment.durationMs, (count - 2) * 1000)
    assert.equal(calls.length, Math.ceil((count - 200) / 500))
  }
})

test("backfill preserves live events, stream state and tool output without reviving old approvals", async () => {
  const { events, header } = fixture(324)
  events[2] = { seq: 3, time: 3000, type: "approval/asked", data: { turn: 1, step: 1, call_id: "call", name: "exec", arguments: "{}", risk: "high" } }
  events[3] = { seq: 4, time: 4000, type: "question/asked", data: { turn: 1, step: 1, call_id: "question", questions: [] } }
  const request = deferred()
  sessionsApi.events = async () => request.promise
  const loading = useSessionStore.getState().loadHistory()
  useSessionStore.getState().appendEvents([
    { seq: 325, time: 325000, type: "approval/decided", data: { call_id: "call", decision: "allow", decided_by: "user", auto: false } },
    { seq: 326, time: 326000, type: "question/answered", data: { call_id: "question", answers: [], answered_by: "user" } },
    { seq: 327, time: 327000, type: "turn/start", data: { turn: 2, request_id: "next" } },
  ])
  useSessionStore.getState().applyStreamFrame({ type: "start", attemptId: "live", turn: 2, step: 1, startedAfterSeq: 327 })
  useSessionStore.getState().appendToolOutput("exec", "stdout", "live output")
  request.resolve({ items: events.slice(0, 124), last_seq: header.lastSeq })
  await loading
  const state = useSessionStore.getState()
  assert.equal(state.events.length, 327)
  assert.equal(state.lastSeq, 327)
  assert.equal(state.header?.running, true)
  assert.equal(state.liveAttempt?.attemptId, "live")
  assert.equal(state.toolOutputs.exec.stdout, "live output")
  assert.deepEqual(state.pendingApprovals, {})
  assert.deepEqual(state.pendingQuestions, {})
})

test("failed history can be retried and does not discard the visible tail", async () => {
  const { events } = fixture(324)
  sessionsApi.events = async () => { throw new Error("network failure") }
  await useSessionStore.getState().loadHistory()
  assert.equal(useSessionStore.getState().historyStatus, "error")
  assert.equal(useSessionStore.getState().hasMore, true)
  assert.deepEqual(useSessionStore.getState().events, events.slice(-200))
  sessionsApi.events = async () => ({ items: events.slice(0, 124), last_seq: 324 })
  await useSessionStore.getState().loadHistory()
  assert.equal(useSessionStore.getState().historyStatus, "idle")
  assert.deepEqual(useSessionStore.getState().events, events)
})

test("a missing history page stays retryable instead of reporting complete history", async () => {
  fixture(324)
  for (const items of [[], [{ seq: 2, time: 2, type: "turn/start", data: { turn: 1, request_id: "r" } }]] as SessionEvent[][]) {
    sessionsApi.events = async () => ({ items, last_seq: 324 })
    await useSessionStore.getState().loadHistory()
    assert.equal(useSessionStore.getState().hasMore, true)
    assert.equal(useSessionStore.getState().historyStatus, "error")
    assert.equal(useSessionStore.getState().events[0].seq, 125)
  }
})

test("switching sessions or receiving a new snapshot invalidates an in-flight backfill", async () => {
  for (const nextId of ["other-session", "pelican"]) {
    const { events } = fixture(324)
    const request = deferred()
    sessionsApi.events = async () => request.promise
    const loading = useSessionStore.getState().loadHistory()
    const next = fixture(204, nextId)
    request.resolve({ items: events.slice(0, 124), last_seq: 324 })
    await loading
    assert.equal(useSessionStore.getState().sessionId, nextId)
    assert.equal(useSessionStore.getState().historyStatus, "idle")
    assert.deepEqual(useSessionStore.getState().events, next.events.slice(-200))
  }
})

test("duplicate requests are suppressed and complete snapshots do not fetch history", async () => {
  const { events } = fixture(324)
  let calls = 0
  const request = deferred()
  sessionsApi.events = async () => { calls++; return request.promise }
  const loading = useSessionStore.getState().loadHistory()
  await useSessionStore.getState().loadHistory()
  assert.equal(calls, 1)
  request.resolve({ items: events.slice(0, 124), last_seq: 324 })
  await loading
  await useSessionStore.getState().loadHistory()
  fixture(100)
  await useSessionStore.getState().loadHistory()
  assert.equal(calls, 1)
})
