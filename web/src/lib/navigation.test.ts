import test from "node:test"
import assert from "node:assert/strict"
import { mainSection, orgSwitchPath, platformOrganization, safeWorkspaceLocation, workspaceLocationKey } from "./navigation"
import { useComposerStore } from "@/stores/composer"
import type { UserOrg } from "./types"

const member: UserOrg = { id: "member", name: "Org", role: "member", is_platform: false }
const admin: UserOrg = { ...member, id: "admin", role: "admin" }
const platform: UserOrg = { ...admin, id: "platform", is_platform: true }

test("organization switching removes old tenant chat IDs and inaccessible admin pages", () => {
  assert.equal(orgSwitchPath("/c/old-session", member), "/")
  assert.equal(orgSwitchPath("/admin/agents/private", member), "/")
  assert.equal(orgSwitchPath("/admin/members", admin), "/admin/agents")
  assert.equal(orgSwitchPath("/platform/users", admin), "/")
  assert.equal(orgSwitchPath("/platform/users", platform), "/platform/orgs")
  assert.equal(orgSwitchPath("/personal/storage", member), "/personal/storage")
})

test("system entry requires platform membership with admin role", () => {
  assert.equal(platformOrganization([admin, { ...platform, role: "member" }]), undefined)
  assert.equal(platformOrganization([member, platform])?.id, "platform")
  assert.equal(mainSection("/personal/agents"), "personal")
  assert.equal(mainSection("/platform/users"), "system")
})

test("workspace return locations are scoped by account and organization and stay local", () => {
  assert.notEqual(workspaceLocationKey("u", "one"), workspaceLocationKey("u", "two"))
  assert.notEqual(workspaceLocationKey("u", "one"), workspaceLocationKey("v", "one"))
  assert.equal(safeWorkspaceLocation("/c/new?agent=a"), "/c/new?agent=a")
  for (const path of ["//example.com", "/platform/users", "https://example.com", null]) assert.equal(safeWorkspaceLocation(path), "/")
})

test("returning from personal settings keeps drafts separate across sessions and tenants", () => {
  const composer = useComposerStore.getState()
  composer.reset()
  composer.enterContext("u:org:agent:a")
  composer.setDraft("a draft before opening personal settings")
  composer.enterContext("u:org:agent:a")
  assert.equal(useComposerStore.getState().draft, "a draft before opening personal settings")
  composer.enterContext("u:other:agent:a")
  assert.equal(useComposerStore.getState().draft, "")
  composer.setDraft("other organization")
  composer.enterContext("u:org:agent:a")
  assert.equal(useComposerStore.getState().draft, "a draft before opening personal settings")
  composer.reset()
})
