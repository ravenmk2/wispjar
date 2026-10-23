import assert from "node:assert/strict"
import test from "node:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { parseFetchResponse } from "./web-fetch"
import { WebFetchResult } from "../features/chat/WebFetchResult"
import { normalizeAgentDetail } from "../api/agents"

const fixture = { url: "https://example.com/a", title: "Source", content: "# 正文\n\n![tracking](https://example.com/pixel)\n\n<script>evil()</script>", format: "markdown", provider_name: "Native", duration_ms: 12, truncated: true }
test("fetch history validates URLs and content format", () => {
 const parsed = parseFetchResponse(JSON.stringify(fixture))
 assert.equal(parsed?.content, fixture.content)
 assert.equal(parsed?.truncated, true)
 for (const p of [null, {}, { ...fixture, url: "javascript:alert(1)" }, { ...fixture, url: "https://u:p@example.com/" }, { ...fixture, content: {} }, { ...fixture, format: "html" }]) assert.equal(parseFetchResponse(JSON.stringify(p)), null)
 assert.equal(parseFetchResponse("Tool execution failed: 读取超时"), null)
})
test("external page rendering never loads images or executes HTML", () => {
 const parsed = parseFetchResponse(JSON.stringify(fixture))!
 const html = renderToStaticMarkup(createElement(WebFetchResult, { result: parsed }))
 assert.ok(!html.includes("<img"))
 assert.ok(!html.includes("<script"))
 assert.ok(html.includes("正文已截断"))
 const text = renderToStaticMarkup(createElement(WebFetchResult, { result: { ...parsed, format: "text", content: "<script>evil()</script>" } }))
 assert.ok(text.includes("&lt;script&gt;"))
})
test("existing agents keep fetch disabled and inherit default", () => {
 const agent = normalizeAgentDetail({ tools_enabled: { read: true } })
 assert.equal(agent.tools_enabled.web_fetch, false)
 assert.equal(agent.fetch_provider_id, null)
})
