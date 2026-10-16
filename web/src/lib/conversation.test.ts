import assert from "node:assert/strict"
import { test } from "node:test"
import { deriveConversationNodes } from "./conversation"
import { groupByTurn, reasoningTail } from "./turn-grouping"
import type { ContentBlock, SessionEvent, StreamChunk, TurnEndReason } from "./types"
import { useChatPresentationStore } from "../stores/chatPresentation"
import { useSessionStore } from "../stores/session"
import { execOutput, toolCancelled, toolFailure } from "./tool-output"

const text = (value: string): ContentBlock => ({ type: "text", text: value })
const thought = (value: string): ContentBlock => ({ type: "reasoning", text: value, durationMs: 1200 })
const tool = (id: string): ContentBlock => ({ type: "tool-call", id, name: "read", arguments: '{"path":"a.go"}' })
function fixture() {
  const events: SessionEvent[] = []
  function add(event: SessionEvent extends infer E ? E extends SessionEvent ? Omit<E, "seq" | "time"> : never : never) {
    events.push({ ...event, seq: events.length + 1, time: (events.length + 1) * 1000 } as SessionEvent)
  }
  const message = (content: ContentBlock[], step = 1) => add({ type: "assistant/message", data: { turn: 1, step, message: { role: "assistant", content } } })
  const end = (reason: TurnEndReason = "completed") => add({ type: "turn/end", data: { turn: 1, reason } })
  const segment = () => groupByTurn(deriveConversationNodes(events, null)).segments[0]
  add({ type: "user/message", data: { turn: 1, content: "question", request_id: "req" } })
  add({ type: "turn/start", data: { turn: 1, request_id: "req" } })
  return { events, add, message, end, segment }
}

test("text separates consecutive reasoning/tools; final reasoning stays before final text", () => {
  const f = fixture()
  f.message([thought("a"), tool("a")])
  f.message([thought("b"), text("progress"), tool("b")], 2)
  f.message([thought("c"), text("answer")], 3)
  f.end()
  const seg = f.segment()
  assert.deepEqual(seg.parts.map((p) => p.kind), ["process", "reply", "process", "reply"])
  assert.equal(seg.parts[0].kind === "process" && seg.parts[0].nodes.length, 3)
  assert.equal(seg.answerId, seg.parts[3].kind === "reply" && seg.parts[3].node.id)
  assert.equal(seg.durationMs, 4000)
})

test("pure text has no empty process group", () => {
  const f = fixture(); f.message([text("answer")]); f.end()
  assert.deepEqual(f.segment().parts.map((p) => p.kind), ["reply"])
})

test("interleaved blocks preserve actual order", () => {
  const f = fixture(); f.message([text("before"), tool("a"), thought("reason"), text("after")]); f.end()
  const nodes = deriveConversationNodes(f.events, null)
  assert.deepEqual(nodes.filter((n) => n.kind !== "boundary" && n.kind !== "user").map((n) => n.kind), ["assistant", "tool-call", "assistant"])
  assert.deepEqual(f.segment().parts.map((p) => p.kind), ["reply", "process", "reply"])
})

test("tool result pairs with inline call without adding another group", () => {
  const f = fixture(); f.message([tool("a")])
  f.add({ type: "tool/call", data: { turn: 1, step: 1, call_id: "a", name: "read", arguments: '{}' } })
  f.add({ type: "tool/result", data: { turn: 1, step: 1, call_id: "a", name: "read", content: "ok" } })
  const p = f.segment().parts[0]
  assert.equal(p.kind, "process")
  if (p.kind === "process") {
    assert.equal(p.nodes.length, 1)
    assert.equal(p.nodes[0].kind === "tool-call" && p.nodes[0].result?.content, "ok")
  }
})

test("running text is visible but is not yet a settled answer", () => {
  const f = fixture(); f.message([text("progress")])
  assert.equal(f.segment().answerId, undefined)
  assert.equal(f.segment().parts[0].kind, "reply")
})

test("forked final reply survives a new turn and replay without an end event", () => {
  const f = fixture(); f.message([thought("thinking"), text("forked final reply")])
  const idle = groupByTurn(deriveConversationNodes(f.events, null), false).segments[0]
  assert.ok(idle.answerId)
  assert.equal(idle.running, false)
  assert.equal(idle.end, undefined)
  assert.equal(idle.durationMs, undefined)
  assert.equal(idle.parts[0].kind === "process" && idle.parts[0].running, false)

  f.add({ type: "user/message", data: { turn: 2, content: "continue", request_id: "next" } })
  f.add({ type: "turn/start", data: { turn: 2, request_id: "next" } })
  f.add({ type: "assistant/message", data: { turn: 2, step: 1, message: { role: "assistant", content: [text("new progress")] } } })
  const replay = JSON.parse(JSON.stringify(f.events)) as SessionEvent[]
  for (const events of [f.events, replay]) {
    const [previous, current] = groupByTurn(deriveConversationNodes(events, null), true).segments
    assert.equal(previous.answerId, idle.answerId)
    assert.equal(previous.running, false)
    assert.equal(previous.end, undefined)
    assert.equal(previous.durationMs, undefined)
    assert.equal(current.running, true)
    assert.equal(current.answerId, undefined)
  }
})

test("a later turn settles truncated history even when session status is unavailable", () => {
  const f = fixture(); f.message([text("forked final reply")])
  f.add({ type: "user/message", data: { turn: 2, content: "continue", request_id: "next" } })
  const [previous, current] = groupByTurn(deriveConversationNodes(f.events, null)).segments
  assert.ok(previous.answerId)
  assert.equal(previous.running, false)
  assert.equal(current.running, true)
})

test("idle truncated history ending in a tool does not promote progress to a final reply", () => {
  const f = fixture(); f.message([text("progress"), tool("unfinished")])
  const seg = groupByTurn(deriveConversationNodes(f.events, null), false).segments[0]
  assert.equal(seg.running, false)
  assert.equal(seg.answerId, undefined)
  assert.equal(seg.end, undefined)
})

test("completed turn ending in a tool does not misidentify progress as the final answer", () => {
  const f = fixture(); f.message([text("progress"), tool("a")]); f.end()
  assert.equal(f.segment().answerId, undefined)
})

test("stopped/failed turns retain last text as a fallback", () => {
  for (const reason of ["failed", "interrupted"] as const) {
    const f = fixture(); f.message([text("progress"), tool("a")]); f.end(reason)
    const seg = f.segment()
    assert.equal(seg.answerId, seg.parts[0].kind === "reply" && seg.parts[0].node.id)
  }
})

test("unreturned tools stop advertising running activity when their turn ends", () => {
  for (const reason of ["completed", "failed", "interrupted"] as const) {
    const f = fixture(); f.message([tool("unfinished")])
    const runningGroup = f.segment().parts[0]
    assert.equal(runningGroup.kind === "process" && runningGroup.running, true)
    f.end(reason)
    const settledGroup = f.segment().parts[0]
    assert.equal(settledGroup.kind === "process" && settledGroup.running, false)
    const node = settledGroup.kind === "process" && settledGroup.nodes[0]
    assert.equal(node && node.kind === "tool-call" && node.endReason, reason)
  }
})

test("a new turn does not clear the previous stopped or failed boundary", () => {
  for (const reason of ["interrupted", "failed"] as const) {
    const f = fixture(); f.message([text("partial"), tool("unfinished")]); f.end(reason)
    f.add({ type: "user/message", data: { turn: 2, content: "continue", request_id: "next" } })
    f.add({ type: "turn/start", data: { turn: 2, request_id: "next" } })
    f.add({ type: "tool/call", data: { turn: 2, step: 1, call_id: "new-tool", name: "read", arguments: '{}' } })
    const segments = groupByTurn(deriveConversationNodes(f.events, null)).segments
    assert.equal(segments[0].end?.reason, reason)
    assert.equal(segments[0].durationMs, 2000)
    assert.equal(segments[1].end, undefined)
    const nextGroup = segments[1].parts[0]
    assert.equal(nextGroup.kind === "process" && nextGroup.running, true)
    const nextTool = nextGroup.kind === "process" && nextGroup.nodes[0]
    assert.equal(nextTool && nextTool.kind === "tool-call" && nextTool.endReason, undefined)
  }
})

test("ending a turn settles nested tools and stale live reasoning", () => {
  const f = fixture(); f.message([{ type: "tool-call", id: "task", name: "task", arguments: '{}' }])
  f.add({ type: "tool/call", data: { turn: 0, step: 1, parent_call_id: "task", call_id: "child", name: "exec", arguments: '{}' } })
  f.end("interrupted")
  const nodes = deriveConversationNodes(f.events, { turn: 1, step: 2, startedAfterSeq: 3, chunks: [
    { type: "block-start", index: 0, blockType: "reasoning" },
    { type: "reasoning-delta", index: 0, text: "partial thought" },
  ] })
  const task = nodes.find((n) => n.kind === "tool-call")!
  assert.equal(task.kind === "tool-call" && task.children[0].kind === "tool-call" && task.children[0].endReason, "interrupted")
  const assistant = nodes.find((n) => n.kind === "assistant")!
  assert.equal(assistant.kind === "assistant" && assistant.live, false)
})

test("stdout and stderr stay separate while streaming and after completion", () => {
  const store = useSessionStore.getState()
  store.reset()
  store.appendToolOutput("exec", "stdout", "line 1\n")
  store.appendToolOutput("exec", "stderr", "progress\n")
  store.appendToolOutput("exec", "stdout", "line 2\n")
  const live = useSessionStore.getState().toolOutputs.exec
  assert.deepEqual(live, { stdout: "line 1\nline 2\n", stderr: "progress\n" })
  const final = execOutput({ content: JSON.stringify({ ...live, exit_code: 0 }) }, live)
  assert.equal(final.stdout, live.stdout)
  assert.equal(final.stderr, live.stderr)
  assert.equal(final.exitCode, 0)
  assert.equal(final.error, undefined)
  const cancelled = execOutput({ content: '{"error":"context canceled","stderr":""}' }, live)
  assert.equal(cancelled.stdout, live.stdout)
  assert.equal(cancelled.stderr, live.stderr)
  store.reset()
})

test("exec failures include structured tool errors, but stderr alone is not a failure", () => {
  const f = fixture()
  f.add({ type: "tool/result", data: { turn: 1, step: 1, call_id: "exec", name: "exec", content: '{"error":"sandbox unavailable","stderr":""}' } })
  const node = deriveConversationNodes(f.events, null).find((n) => n.kind === "tool-call")!
  assert.equal(node.kind === "tool-call" && toolFailure(node), "sandbox unavailable")
  const group = f.segment().parts[0]
  assert.equal(group.kind === "process" && group.hasError, true)
  if (node.kind !== "tool-call") return
  node.result = { content: '{"stdout":"","stderr":"progress","exit_code":0}' }
  assert.equal(toolFailure(node), undefined)
  node.endReason = "interrupted"
  node.result = { content: '{"error":"context canceled"}' }
  assert.equal(toolCancelled(node), true)
  assert.equal(toolFailure(node), undefined)
  assert.equal(execOutput({ content: '{"stdout":"tail","truncated":true}' }).truncated, true)
})

test("partial history does not invent a duration", () => {
  const f = fixture(); f.message([text("answer")]); f.end()
  const seg = groupByTurn(deriveConversationNodes(f.events.slice(2), null)).segments[0]
  assert.equal(seg.durationMs, undefined)
  assert.ok(seg.answerId)
})

test("live block keys and process keys survive commit; event before stream-end is not duplicated", () => {
  const f = fixture()
  const chunks: StreamChunk[] = [
    { type: "block-start", index: 0, blockType: "reasoning" },
    { type: "reasoning-delta", index: 0, text: "thinking" },
  ]
  const live = { turn: 1, step: 1, startedAfterSeq: 2, chunks }
  const before = groupByTurn(deriveConversationNodes(f.events, live)).segments[0]
  f.message([thought("thinking"), text("answer")])
  const after = groupByTurn(deriveConversationNodes(f.events, live)).segments[0]
  assert.equal(before.parts[0].kind === "process" && before.parts[0].id, after.parts[0].kind === "process" && after.parts[0].id)
  assert.equal(after.parts.length, 2)
})

test("live tools use stable call ids and the real turn", () => {
  const f = fixture()
  const live = { turn: 1, step: 1, startedAfterSeq: 2, chunks: [
    { type: "block-start", index: 0, blockType: "tool-call" },
    { type: "tool-call-delta", index: 0, id: "a", name: "read", argumentsDelta: '{}' },
  ] as StreamChunk[] }
  const before = deriveConversationNodes(f.events, live).find((n) => n.kind === "tool-call")!
  f.message([tool("a")])
  const after = deriveConversationNodes(f.events, null).find((n) => n.kind === "tool-call")!
  assert.equal(before.id, after.id)
  assert.equal(before.turn, 1)
})

test("nested pending approval forces its parent turn/group visible", () => {
  const f = fixture(); f.message([{ type: "tool-call", id: "task", name: "task", arguments: '{}' }])
  f.add({ type: "approval/asked", data: { turn: 0, step: 1, call_id: "nested", name: "exec", arguments: '{}', risk: "high", parent_call_id: "task" } })
  const seg = f.segment()
  assert.equal(seg.hasPendingApproval, true)
  assert.equal(seg.parts[0].kind === "process" && seg.parts[0].hasPending, true)
})

test("nonzero command exit marks the group as failed", () => {
  const f = fixture()
  f.add({ type: "tool/result", data: { turn: 1, step: 1, call_id: "a", name: "exec", content: '{"exit_code":1}' } })
  const p = f.segment().parts[0]
  assert.equal(p.kind === "process" && p.hasError, true)
})

test("nested assistant replies stay inside the task and have distinct identities", () => {
  const f = fixture()
  f.message([{ type: "tool-call", id: "task", name: "task", arguments: '{}' }])
  for (let subStep = 1; subStep <= 2; subStep++) {
    const event = {
      type: "assistant/message", seq: f.events.length + 1, time: 5000,
      data: { turn: 0, step: 0, sub_step: subStep, parent_call_id: "task", message: { role: "assistant", content: [text(`child ${subStep}`)] } },
    } as SessionEvent
    f.events.push(event)
  }
  const nodes = deriveConversationNodes(f.events, null)
  const task = nodes.find((n) => n.kind === "tool-call")!
  assert.equal(nodes.filter((n) => n.kind === "assistant").length, 0)
  if (task.kind === "tool-call") {
    assert.equal(task.children.length, 2)
    assert.notEqual(task.children[0].id, task.children[1].id)
  }
})

test("reasoning preview shows the tail, flattening newlines", () => {
  assert.equal(reasoningTail("earlier content\nlatest line", 11), "…latest line")
  assert.equal(reasoningTail("one\ntwo"), "one two")
})

test("manual disclosure survives remount/reconnect; switching sessions resets it", () => {
  const s = useChatPresentationStore.getState()
  s.enterSession("a"); s.setExpanded("turn-1", true); s.setExpanded("tool-a", true)
  s.enterSession("a")
  assert.equal(useChatPresentationStore.getState().expanded["turn-1"], true)
  assert.equal(useChatPresentationStore.getState().expanded["tool-a"], true)
  s.enterSession("b")
  assert.deepEqual(useChatPresentationStore.getState().expanded, {})
})
