import { api } from "./client"
import type { List, Model, ModelCapabilities, Status } from "@/lib/types"

export interface ModelBody {
  provider_id?: string
  model_code: string
  display_name: string
  capabilities: ModelCapabilities
  context_size: number
  default_params?: Record<string, unknown> | null
  status?: Status
}

export const modelsApi = {
  list(providerId?: string) {
    return api<List<Model>>("/platform/models", { params: { provider_id: providerId } })
  },
  create(body: ModelBody & { provider_id: string }) {
    return api<Model>("/platform/models", { method: "POST", body })
  },
  update(id: string, body: ModelBody) {
    return api<Model>(`/platform/models/${id}`, { method: "PUT", body })
  },
  remove(id: string) {
    return api<{ ok: boolean }>(`/platform/models/${id}`, { method: "DELETE" })
  },
}
