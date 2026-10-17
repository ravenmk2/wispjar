import test from "node:test"
import assert from "node:assert/strict"
import { useSessionStore } from "@/stores/session"
import type { SessionHeader, SessionEvent } from "./types"

const header = (locked: boolean): SessionHeader => ({
  sessionId: "s", title: "", titlePinned: false, agentId: "a", agentName: "A", agentAvatar: "",
  modelId: "m", modelSettingsLocked: locked, approvalMode: "auto", sandboxStatus: "none", running: false,
  lastSeq: 400, usageTotal: { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0, reasoning_tokens: 0 }, createdAt: "",
})

test("settings-only events do not lock an empty conversation", () => {
  const event: SessionEvent = { type: "model/selection", seq: 400, time: 1, data: { model_id: "m", changed_by: "u" } }
  useSessionStore.getState().applySnapshot("s", header(false), [event], false)
  assert.equal(useSessionStore.getState().header?.modelSettingsLocked, false)
})

test("a truncated history and reconnect keep the authoritative lock", () => {
  useSessionStore.getState().applySnapshot("s", header(true), [], true)
  assert.equal(useSessionStore.getState().header?.modelSettingsLocked, true)
  useSessionStore.getState().applySnapshot("s", header(true), [], true)
  assert.equal(useSessionStore.getState().header?.modelSettingsLocked, true)
})

test("first user message locks both settings and a stopped turn does not unlock", () => {
  useSessionStore.getState().applySnapshot("s", { ...header(false), lastSeq: 0 }, [], false)
  useSessionStore.getState().appendEvents([
    { type: "user/message", seq: 1, time: 1, data: { turn: 1, request_id: "r", content: "hello" } },
    { type: "turn/end", seq: 2, time: 2, data: { turn: 1, reason: "interrupted" } },
  ])
  assert.equal(useSessionStore.getState().header?.modelSettingsLocked, true)
})
