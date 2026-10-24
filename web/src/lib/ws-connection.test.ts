import test, { type TestContext } from "node:test"
import assert from "node:assert/strict"
import { MuxConnection } from "@/mux/connection"
import { useIdentityStore } from "@/stores/identity"

class FakeSocket {
  static OPEN = 1
  static instances: FakeSocket[] = []
  readyState = 0
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((event: { data: string }) => void) | null = null
  sent: string[] = []
  constructor(readonly url: string) { FakeSocket.instances.push(this) }
  open() { this.readyState = 1; this.onopen?.() }
  close() { this.readyState = 3; this.onclose?.() }
  send(data: string) { this.sent.push(data) }
}

const flush = () => new Promise<void>((resolve) => setImmediate(resolve))
const ticketResponse = (ticket: string) => Response.json({ ticket, expiresAt: new Date(Date.now() + 30000).toISOString() })

function setup(t: TestContext) {
  const original = { fetch: globalThis.fetch, window: globalThis.window, WebSocket: globalThis.WebSocket }
  globalThis.window = { location: { protocol: "https:", host: "localhost", pathname: "/chat", href: "/chat" } } as unknown as Window & typeof globalThis
  globalThis.WebSocket = FakeSocket as unknown as typeof WebSocket
  FakeSocket.instances = []
  useIdentityStore.setState({ token: "login-token" })
  t.mock.timers.enable({ apis: ["setTimeout"] })
  const mux = new MuxConnection()
  t.after(() => {
    mux.disconnect()
    Object.assign(globalThis, original)
    useIdentityStore.getState().clear()
  })
  return mux
}

test("every connection fetches a fresh ticket and replays streams with current payload", async (t) => {
  const mux = setup(t)
  let requests = 0
  globalThis.fetch = async (url, init) => {
    assert.equal(url, "/api/v1/auth/ws-ticket")
    assert.equal(init?.method, "POST")
    assert.equal(new Headers(init?.headers).get("Authorization"), "Bearer login-token")
    assert.equal(init?.cache, "no-store")
    return ticketResponse(`ticket-${++requests}`)
  }
  let seq = 1
  mux.openStream("session.follow", () => ({ afterSeq: seq }), { onItem() {}, onError() {}, onEnd() {} })
  mux.connect("login-token")
  await flush()
  const first = FakeSocket.instances[0]
  assert.equal(first.url, "wss://localhost/api/ws?ticket=ticket-1")
  first.open()
  assert.equal(JSON.parse(first.sent[0]).payload.afterSeq, 1)
  seq = 2
  first.close()
  mux.connect("login-token")
  assert.equal(requests, 1)
  t.mock.timers.tick(1200)
  await flush()
  assert.equal(requests, 2)
  const second = FakeSocket.instances[1]
  assert.equal(second.url, "wss://localhost/api/ws?ticket=ticket-2")
  second.open()
  assert.equal(JSON.parse(second.sent[0]).payload.afterSeq, 2)
  assert.equal(mux.getStatus(), "open")
})

test("disconnect cancels ticket acquisition and ignores a late response", async (t) => {
  const mux = setup(t)
  let resolve!: (response: Response) => void
  let signal: AbortSignal | undefined
  globalThis.fetch = (_url, init) => { signal = init?.signal ?? undefined; return new Promise((r) => { resolve = r }) }
  mux.connect("login-token")
  mux.disconnect()
  assert.equal(signal?.aborted, true)
  resolve(ticketResponse("abandoned"))
  await flush()
  assert.equal(FakeSocket.instances.length, 0)
  assert.equal(mux.getStatus(), "closed")
})

test("switching accounts ignores late authentication errors", async (t) => {
  const mux = setup(t)
  const pending: Array<(response: Response) => void> = []
  globalThis.fetch = () => new Promise((r) => pending.push(r))
  mux.connect("old-login")
  useIdentityStore.setState({ token: "new-login" })
  mux.connect("new-login")
  pending[1](ticketResponse("new-ticket"))
  await flush()
  pending[0](Response.json({ error: { code: "auth/unauthorized" } }, { status: 401 }))
  await flush()
  assert.equal(FakeSocket.instances.length, 1)
  assert.ok(FakeSocket.instances[0].url.endsWith("ticket=new-ticket"))
  assert.equal(useIdentityStore.getState().token, "new-login")
  assert.equal(window.location.href, "/chat")
})

test("transient failures back off and ticket timeouts can recover", async (t) => {
  const mux = setup(t)
  let requests = 0
  globalThis.fetch = async (_url, init) => {
    requests++
    if (requests === 1) return Response.json({}, { status: 503 })
    if (requests === 2) return new Promise((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new Error("aborted")), { once: true })
    })
    return ticketResponse("recovered")
  }
  mux.connect("login-token")
  await flush()
  assert.equal(mux.getStatus(), "reconnecting")
  assert.equal(FakeSocket.instances.length, 0)
  t.mock.timers.tick(1200)
  await flush()
  t.mock.timers.tick(10000)
  await flush()
  t.mock.timers.tick(2400)
  await flush()
  assert.equal(requests, 3)
  assert.equal(FakeSocket.instances.length, 1)
})

test("authentication failure stops retries, while required password change keeps login", async (t) => {
  const mux = setup(t)
  for (const code of ["auth/password_change_required", "auth/unauthorized"]) {
    useIdentityStore.setState({ token: "login-token" })
    let requests = 0
    globalThis.fetch = async () => { requests++; return Response.json({ error: { code } }, { status: 401 }) }
    mux.connect("login-token")
    await flush()
    t.mock.timers.tick(30000)
    await flush()
    assert.equal(requests, 1)
    assert.equal(mux.getStatus(), "closed")
    assert.equal(useIdentityStore.getState().token, code === "auth/unauthorized" ? null : "login-token")
    assert.equal(window.location.href, code === "auth/unauthorized" ? "/login" : "/change-password")
  }
})
