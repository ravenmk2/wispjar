import test from "node:test"
import assert from "node:assert/strict"
import { parseSearchResponse, searchResultURL } from "./web-search"
import { deriveConversationNodes } from "./conversation"
import { toolCancelled, toolFailure } from "./tool-output"
import type { SessionEvent } from "./types"

test("saved web search replays without a new request and keeps instance identity", () => {
  const content = JSON.stringify({ query: "release", provider_id: "instance-b", provider_name: "DeepSeek B", duration_ms: 12, results: [{ title: "Source", url: "https://example.com/a", snippet: "preview", content: "evidence" }], truncated: true })
  const events = [
    { seq: 1, time: 1, type: "tool/call", data: { turn: 1, step: 1, call_id: "search-one", name: "web_search", arguments: '{"query":"release"}' } },
    { seq: 2, time: 2, type: "tool/result", data: { turn: 1, step: 1, call_id: "search-one", name: "web_search", content, meta: { render: "web_search", duration_ms: 12, truncated: true } } },
  ] as SessionEvent[]
  const replay = JSON.parse(JSON.stringify(events)) as SessionEvent[]
  const node = deriveConversationNodes(replay, null).find((n) => n.kind === "tool-call")
  assert.ok(node?.kind === "tool-call")
  assert.equal(node.result?.meta?.render, "web_search")
  const result = parseSearchResponse(node.result!.content)
  assert.equal(result?.provider_id, "instance-b")
  assert.equal(result?.results[0].url, "https://example.com/a")
  assert.equal(result?.truncated, true)
})

test("history renderer rejects unsafe links and malformed responses", () => {
  for (const raw of ["javascript:alert(1)", "data:text/html,hello", "https://user:secret@example.com", "not a url"]) assert.equal(searchResultURL(raw), null)
  assert.equal(searchResultURL("https://example.com/a")?.hostname, "example.com")
  for (const raw of ["Tool execution failed: 搜索失败", "null", "{}", '{"results":{}}']) assert.equal(parseSearchResponse(raw), null)
  const result = parseSearchResponse('{"results":[null,{"url":"javascript:alert(1)"},{"url":"https://example.com","title":{}}]}')
  assert.equal(result?.results.length, 1)
  assert.equal(result?.results[0].title, "")
})

test("failed and cancelled searches stay distinct from successful empty results", () => {
  const events = [{ seq: 1, time: 1, type: "tool/call", data: { turn: 1, step: 1, call_id: "s", name: "web_search", arguments: '{}' } }, { seq: 2, time: 2, type: "tool/result", data: { turn: 1, step: 1, call_id: "s", name: "web_search", content: "Tool execution failed: 网络搜索已取消" } }] as SessionEvent[]
  const node = deriveConversationNodes(events, null).find((n) => n.kind === "tool-call")
  assert.ok(node?.kind === "tool-call")
  assert.match(toolFailure(node)!, /已取消/)
  node.endReason = "interrupted"
  assert.equal(toolCancelled(node), true)
  assert.equal(toolFailure(node), undefined)
  assert.deepEqual(parseSearchResponse('{"results":[]}')?.results, [])
})
