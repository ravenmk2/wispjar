import test from "node:test"
import assert from "node:assert/strict"
import { authApi } from "@/api/auth"
import { ApiError } from "@/api/client"
import { useIdentityStore } from "@/stores/identity"

test("password field errors keep the login and allow a retry; expired login still redirects", async (t) => {
  const original = { fetch: globalThis.fetch, window: globalThis.window }
  globalThis.window = { location: { origin: "http://localhost", pathname: "/personal/account", href: "/personal/account" } } as unknown as Window & typeof globalThis
  t.after(() => { Object.assign(globalThis, original); useIdentityStore.getState().clear() })
  for (const path of ["/personal/account", "/change-password"]) {
    window.location.pathname = path
    window.location.href = path
    useIdentityStore.setState({ token: "valid-token" })
    globalThis.fetch = async () => Response.json({ error: { code: "request/invalid", message: "当前密码不正确", details: { old_password: "当前密码不正确，请重新输入" } } }, { status: 400 })
    await assert.rejects(authApi.changePassword("wrong", "newpass123"), (e: unknown) => e instanceof ApiError && e.details?.old_password === "当前密码不正确，请重新输入")
    assert.equal(useIdentityStore.getState().token, "valid-token")
    assert.equal(window.location.href, path)
    globalThis.fetch = async (_url, init) => {
      assert.equal(new Headers(init?.headers).get("Authorization"), "Bearer valid-token")
      return Response.json({ ok: true, token: "new-token" })
    }
    assert.equal((await authApi.changePassword("correct123", "newpass123")).token, "new-token")
  }
  globalThis.fetch = async () => Response.json({ error: { code: "auth/unauthorized", message: "登录状态已失效" } }, { status: 401 })
  await assert.rejects(authApi.changePassword("correct123", "newpass123"))
  assert.equal(useIdentityStore.getState().token, null)
  assert.equal(window.location.href, "/login")
})

test("profile updates preserve organization selection and cannot restore a signed-out identity", () => {
  const user = { id: "self", username: "admin", display_name: "Old", must_change_password: false }
  const orgs = [{ id: "one", name: "One", role: "member" as const, is_platform: false }, { id: "two", name: "Two", role: "member" as const, is_platform: false }]
  useIdentityStore.getState().setAuth({ token: "login", user, orgs })
  useIdentityStore.getState().setOrg("two")
  useIdentityStore.getState().updateUser({ ...user, display_name: "New" })
  assert.equal(useIdentityStore.getState().user?.display_name, "New")
  assert.equal(useIdentityStore.getState().currentOrgId, "two")
  assert.deepEqual(useIdentityStore.getState().orgs, orgs)
  useIdentityStore.getState().clear()
  useIdentityStore.getState().updateUser({ ...user, display_name: "Late" })
  assert.equal(useIdentityStore.getState().user, null)
})
