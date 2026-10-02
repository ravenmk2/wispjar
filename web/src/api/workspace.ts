import { api } from "./client"
import type { AgentSummary, List, OrgModel } from "@/lib/types"

export const workspaceApi = {
  agents(orgId: string) {
    return api<List<AgentSummary>>(`/orgs/${orgId}/agents`)
  },
  models(orgId: string) {
    return api<List<OrgModel>>(`/orgs/${orgId}/models`)
  },
}
