import test, { afterEach } from "node:test"
import assert from "node:assert/strict"
import { canMutateComputer, computerFailure, computerOperationError, resolveComputerSelection, type Computer } from "./computers"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { ComputerFailure } from "@/features/computers/ComputerFailure"
import { useComputerStore } from "@/stores/computers"
import { computersApi } from "@/api/computers"
import { ApiError, errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { adminSkillsApi } from "@/api/skills"
import { adminAgentsApi } from "@/api/agents"
import { useIdentityStore } from "@/stores/identity"

const fixture = (id = "computer", org = "org", status = "running"): Computer => ({
  id, org_id: org, status, name: id, generation: 1, config: { image: "python:3.12", resources: {}, ttl_seconds: 3600, has_env: false },
  last_error: "", last_active_at: null, created_at: "",
})
const originalFetch = globalThis.fetch
const originalWindow = globalThis.window

test("variable edits use direct PATCH, while rebuild carries the same patch atomically", async () => {
  const patch = { set: { NEW: "fixture" }, unset: ["OLD"] }
  const requests: Array<{ url: string; method: string | undefined; body: unknown }> = []
  intercept((url, init) => {
    requests.push({ url, method: init.method, body: init.body ? JSON.parse(String(init.body)) : undefined })
    return Response.json(url.endsWith("/env") && !init.body ? { keys: ["SECRET"] } : {})
  })
  assert.deepEqual(await computersApi.env("computer"), { keys: ["SECRET"] })
  await computersApi.patch("computer", { env: patch })
  await computersApi.action("computer", "rebuild", "key", { image: "python:3.12" }, patch)
  await adminAgentsApi.updateEnv("org", "agent", patch)
  assert.equal(requests[1].method, "PATCH")
  assert.deepEqual(requests[1].body, { env: patch })
  assert.deepEqual(requests[2].body, { config: { image: "python:3.12" }, env: patch })
  assert.ok(requests[3].url.endsWith("/orgs/org/admin/agents/agent/env"))
  assert.deepEqual(requests[3].body, patch)
})

test("computer failures show the cause, remedy and a traceable operation", () => {
  const computer = { ...fixture("computer", "org", "failed"), last_error: "实例不存在", error_detail: {
    code: "computer/instance_missing", message: "云电脑实例不存在，上次创建或启动未成功。", suggestion: "请使用编辑 / 重建，已有持久文件会保留。", operation_id: "operation-123",
  } }
  const html = renderToStaticMarkup(createElement(ComputerFailure, { computer }))
  for (const expected of ["实例不存在", "重建", "持久文件", "operation-123", "computer/instance_missing"]) assert.ok(html.includes(expected))
  assert.match(computerOperationError({ id: "op", resource_id: "computer", action: "computer/start", status: "failed", error: computer.last_error, error_detail: computer.error_detail }), /实例不存在.*重建/)
  assert.equal(renderToStaticMarkup(createElement(ComputerFailure, { computer: fixture() })), "")
})

test("legacy errors do not invent a cause or render raw backend credentials", () => {
  const detail = computerFailure({ last_error: "http://admin:secret@private.invalid/internal" })
  assert.match(detail!.message, /旧记录未保存具体原因/)
  assert.ok(!JSON.stringify(detail).includes("secret"))
})
afterEach(() => {
  globalThis.fetch = originalFetch
  globalThis.window = originalWindow
  useComputerStore.getState().reset("")
})
function intercept(handler: (url: string, init: RequestInit) => Response) {
  globalThis.window = { location: { origin: "http://localhost", pathname: "/computers" } } as Window & typeof globalThis
  useIdentityStore.setState({ token: "test-token", currentOrgId: "org" })
  globalThis.fetch = async (input, init) => handler(String(input), init ?? {})
}
test("default binding never silently selects another or deleted computer", () => {
  const items = [fixture("first"), fixture("bound"), fixture("gone", "org", "deleting")]
  assert.equal(resolveComputerSelection(items, [], "agent"), "")
  assert.equal(resolveComputerSelection(items, [{ agent_id: "agent", computer_id: "bound" }], "agent"), "bound")
  assert.equal(resolveComputerSelection(items, [{ agent_id: "agent", computer_id: "missing" }], "agent"), "")
  assert.equal(resolveComputerSelection(items, [{ agent_id: "agent", computer_id: "gone" }], "agent"), "")
})
test("active tasks including expired maintenance leases disable destructive controls", () => {
  assert.equal(canMutateComputer(fixture(), []), true)
  assert.equal(canMutateComputer(fixture("id", "org", "rebuilding"), []), false)
  assert.equal(canMutateComputer(fixture(), [{ id: "run", session_id: "", agent_id: "", parent_run_id: null, status: "running", lease_until: "2000-01-01" }]), false)
})
test("late responses from a previous organization cannot repopulate private computers", () => {
  const store = useComputerStore.getState()
  store.reset("old"); store.baseline("old", [fixture("old", "old")])
  store.reset("new")
  store.baseline("old", [fixture("leak", "old")])
  store.upsert("old", fixture("leak", "old"))
  assert.deepEqual(useComputerStore.getState().items, [])
  store.baseline("new", [fixture("mine", "new"), fixture("other", "old")])
  assert.deepEqual(useComputerStore.getState().items.map((c) => c.id), ["mine"])
  store.baseline("new", [])
  assert.equal(useComputerStore.getState().items.length, 0)
})
test("computer create uses idempotency header and respects asynchronous acceptance", async () => {
  intercept((url, init) => {
    assert.equal(new URL(url).pathname, "/api/v1/orgs/org/computers")
    assert.equal(new Headers(init.headers).get("Authorization"), "Bearer test-token")
    assert.equal(new Headers(init.headers).get("Idempotency-Key"), "stable-key")
    assert.deepEqual(JSON.parse(String(init.body)), { name: "开发电脑", config: {} })
    return new Response(JSON.stringify({ operation_id: "operation", status: "pending", resource_id: "computer" }), { status: 202 })
  })
  const result = await computersApi.create("org", { name: "开发电脑", config: {} }, "stable-key")
  assert.equal(result.status, "pending")
})
test("busy operation errors remain errors with an actionable message", async () => {
  intercept(() => new Response(JSON.stringify({ error: { code: "computer/in_use", message: "computer/in_use" } }), { status: 409 }))
  await assert.rejects(computersApi.action("computer", "delete", "key"), (error: unknown) => {
    assert.ok(error instanceof ApiError)
    assert.match(errorMessage(error), /切换.*绑定/)
    return true
  })
})
test("clearing an agent default targets only the current user's binding", async () => {
  intercept((url, init) => {
    assert.equal(new URL(url).pathname, "/api/v1/orgs/org/agent-bindings/agent")
    assert.equal(init.method, "DELETE")
    assert.equal(init.body, undefined)
    assert.equal(new Headers(init.headers).get("Authorization"), "Bearer test-token")
    return new Response(null, { status: 204 })
  })
  await computersApi.unbind("org", "agent")
})
test("recovery explicitly confirms termination and keeps a stable idempotency key", async () => {
  intercept((url, init) => {
    assert.match(url, /\/computers\/computer\/recover$/)
    assert.deepEqual(JSON.parse(String(init.body)), { confirm_stop_all: true })
    assert.equal(new Headers(init.headers).get("Idempotency-Key"), "recover-key")
    return new Response(JSON.stringify({ operation_id: "recover", resource_id: "computer", status: "running" }), { status: 202 })
  })
  assert.equal((await computersApi.recover("computer", "recover-key")).status, "running")
  assert.equal(canMutateComputer(fixture("computer", "org", "recovering"), []), false)
})
test("file scope is explicit, uploads target the current directory and saving preserves source", async () => {
  const uploads: Array<{ scope: string | null; path: FormDataEntryValue | null }> = []
  intercept((url, init) => {
    if (url.includes("save-to-storage")) {
      assert.deepEqual(JSON.parse(String(init.body)), { source: "outputs/report.pdf", destination: "reports/report.pdf" })
      return new Response("{}")
    }
    if (init.method === "POST") {
      assert.ok(init.body instanceof FormData)
      uploads.push({ scope: new URL(url, "http://localhost").searchParams.get("scope"), path: init.body.get("path") })
      assert.ok(init.body.get("file") instanceof Blob)
      return new Response("{}")
    }
    const query = new URL(url).searchParams
    assert.equal(query.get("scope"), "storage"); assert.equal(query.get("path"), ".")
    return new Response(JSON.stringify({ root: "/storage", path: ".", items: [] }))
  })
  await sessionsApi.files("session", ".", "storage")
  await sessionsApi.uploadFile("session", new File(["hello"], "input.txt"))
  await sessionsApi.uploadFile("session", new File(["hello"], "input.txt"), "reports/2026", "storage")
  await sessionsApi.uploadFile("session", new File(["hello"], "input.txt"), "data", "agent")
  assert.deepEqual(uploads, [{ scope: "workspace", path: "." }, { scope: "storage", path: "reports/2026" }, { scope: "agent", path: "data" }])
  await sessionsApi.saveToStorage("session", "outputs/report.pdf", "reports/report.pdf")
})
test("skill replacement sends expected revision and preserves a concurrent-update conflict", async () => {
  intercept((url, init) => {
    assert.match(url, /\/admin\/skills\/skill\/package$/)
    assert.equal(init.method, "PUT"); assert.ok(init.body instanceof FormData)
    assert.equal(init.body.get("expected_revision"), "7")
    return new Response(JSON.stringify({ error: { code: "skill/conflict", message: "changed", details: { revision: 8 } } }), { status: 409 })
  })
  await assert.rejects(adminSkillsApi.replace("org", "skill", 7, new File(["zip"], "skill.zip")), (e: unknown) => e instanceof ApiError && e.details?.revision === 8)
})
