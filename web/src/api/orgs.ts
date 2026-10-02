import { api } from "./client"
import type { List, OrgDetail, OrgSummary, Status } from "@/lib/types"

export const platformOrgsApi = {
  list() {
    return api<List<OrgSummary>>("/platform/orgs")
  },
  create(name: string) {
    return api<OrgSummary>("/platform/orgs", { method: "POST", body: { name } })
  },
  update(id: string, body: { name?: string; status?: Status }) {
    return api<OrgSummary>(`/platform/orgs/${id}`, { method: "PATCH", body })
  },
  detail(id: string) {
    return api<OrgDetail>(`/platform/orgs/${id}`)
  },
}
