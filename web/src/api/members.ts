import { api } from "./client"
import type { List, OrgMember, Role } from "@/lib/types"

export const membersApi = {
  list(orgId: string) {
    return api<List<OrgMember>>(`/orgs/${orgId}/admin/members`)
  },
  add(orgId: string, username: string, role: Role) {
    return api<OrgMember>(`/orgs/${orgId}/admin/members`, {
      method: "POST",
      body: { username, role },
    })
  },
  updateRole(orgId: string, userId: string, role: Role) {
    return api<OrgMember>(`/orgs/${orgId}/admin/members/${userId}`, {
      method: "PATCH",
      body: { role },
    })
  },
  remove(orgId: string, userId: string) {
    return api<{ ok: boolean }>(`/orgs/${orgId}/admin/members/${userId}`, { method: "DELETE" })
  },
}
