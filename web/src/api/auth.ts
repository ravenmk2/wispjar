import { api } from "./client"
import type { ChangePasswordResponse, List, LoginResponse, MeResponse, UsageGroupBy, UsageResponse } from "@/lib/types"

export const authApi = {
  login(username: string, password: string) {
    return api<LoginResponse>("/auth/login", { method: "POST", body: { username, password } })
  },
  changePassword(oldPassword: string, newPassword: string) {
    return api<ChangePasswordResponse>("/auth/change-password", {
      method: "POST",
      body: { old_password: oldPassword, new_password: newPassword },
    })
  },
  me() {
    return api<MeResponse>("/auth/me")
  },
}

export const orgsApi = {
  listModels(orgId: string) {
    return api<List<unknown>>(`/orgs/${orgId}/models`)
  },
  usage(orgId: string, params: { from: string; to: string; group_by: UsageGroupBy }) {
    return api<UsageResponse>(`/orgs/${orgId}/admin/usage`, {
      params: { from: params.from, to: params.to, group_by: params.group_by },
    })
  },
}
