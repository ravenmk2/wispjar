import { api } from "./client"
import type { List, Provider, ProviderType } from "@/lib/types"

export interface ProviderBody {
  name: string
  type: ProviderType
  base_url: string
  api_key?: string
  remark?: string
}

export const providersApi = {
  list() {
    return api<List<Provider>>("/platform/providers")
  },
  create(body: ProviderBody) {
    return api<Provider>("/platform/providers", { method: "POST", body })
  },
  update(id: string, body: Partial<ProviderBody>) {
    return api<Provider>(`/platform/providers/${id}`, { method: "PUT", body })
  },
  remove(id: string) {
    return api<{ ok: boolean }>(`/platform/providers/${id}`, { method: "DELETE" })
  },
  check(id: string) {
    return api<{ ok: boolean; latency_ms: number }>(`/platform/providers/${id}/check`, { method: "POST" })
  },
}
