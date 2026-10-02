import { api } from "./client"
import type { CreateUserResponse, List, OrgMember, PlatformUser, Role, Status } from "@/lib/types"

export interface CreateUserBody {
  username: string
  display_name: string
  password?: string
  org_ids: string[]
  role: Role
}

export const platformUsersApi = {
  list(params?: { q?: string; cursor?: string; limit?: number }) {
    return api<List<PlatformUser>>("/platform/users", {
      params: { q: params?.q, cursor: params?.cursor, limit: params?.limit },
    })
  },
  create(body: CreateUserBody) {
    return api<CreateUserResponse>("/platform/users", { method: "POST", body })
  },
  update(id: string, body: { display_name?: string; status?: Status }) {
    return api<{ user: PlatformUser }>(`/platform/users/${id}`, { method: "PATCH", body })
  },
  resetPassword(id: string) {
    return api<CreateUserResponse>(`/platform/users/${id}`, {
      method: "PATCH",
      body: { reset_password: true },
    })
  },
  addToOrg(id: string, orgId: string, role: Role) {
    return api<OrgMember>(`/platform/users/${id}/orgs`, {
      method: "POST",
      body: { org_id: orgId, role },
    })
  },
  updateOrgRole(id: string, orgId: string, role: Role) {
    return api<OrgMember>(`/platform/users/${id}/orgs/${orgId}`, {
      method: "PATCH",
      body: { role },
    })
  },
  removeFromOrg(id: string, orgId: string) {
    return api<{ ok: boolean }>(`/platform/users/${id}/orgs/${orgId}`, { method: "DELETE" })
  },
}
