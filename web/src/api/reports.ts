import { api } from "./client"
import type { AuditItem, List, UsageGroupBy, UsageResponse } from "@/lib/types"

export interface UsageQueryParams {
  from: string
  to: string
  group_by: UsageGroupBy
}

export const platformUsageApi = {
  query(params: UsageQueryParams) {
    return api<UsageResponse>("/platform/usage", {
      params: { from: params.from, to: params.to, group_by: params.group_by },
    })
  },
}

export const platformAuditApi = {
  list(params?: { org_id?: string; cursor?: string; limit?: number }) {
    return api<List<AuditItem>>("/platform/audit", {
      params: { org_id: params?.org_id, cursor: params?.cursor, limit: params?.limit },
    })
  },
}
