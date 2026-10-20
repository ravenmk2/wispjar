import { api } from "./client"
import type { EnvPatch } from "@/lib/env"
import type { AdminAgentDetail, AdminAgentSummary, AgentBody, List } from "@/lib/types"

function normalizeIdList(raw: unknown): string[] {
  if (!Array.isArray(raw)) return []
  return raw
    .map((item) => {
      if (typeof item === "string") return item
      if (item && typeof item === "object") {
        const obj = item as Record<string, unknown>
        const id = obj.id ?? obj.skill_id ?? obj.agent_id
        if (typeof id === "string") return id
      }
      return null
    })
    .filter((v): v is string => typeof v === "string" && v.length > 0)
}

export function normalizeAgentDetail(raw: unknown): AdminAgentDetail {
  const obj = (raw ?? {}) as Record<string, unknown>
  const sandbox = (obj.sandbox_config ?? {}) as Record<string, unknown>
  return {
    ...(obj as unknown as AdminAgentDetail),
    name: typeof obj.name === "string" ? obj.name : "",
    description: typeof obj.description === "string" ? obj.description : "",
    avatar: typeof obj.avatar === "string" ? obj.avatar : "",
    system_prompt: typeof obj.system_prompt === "string" ? obj.system_prompt : "",
    model_id: typeof obj.model_id === "string" ? obj.model_id : "",
    model_params: (obj.model_params as AdminAgentDetail["model_params"]) ?? null,
    max_iterations: typeof obj.max_iterations === "number" ? obj.max_iterations : 20,
    tools_enabled: {
      exec: true,
      read: true,
      write: true,
      edit: true,
      glob: true,
      grep: true,
      ...((obj.tools_enabled as object) ?? {}),
    },
    skill_ids: normalizeIdList(obj.skill_ids ?? obj.skills),
    sub_agent_ids: normalizeIdList(obj.sub_agent_ids ?? obj.sub_agents),
    sandbox_config: {
      image: typeof sandbox.image === "string" ? sandbox.image : "",
      resources: (sandbox.resources as AdminAgentDetail["sandbox_config"]["resources"]) ?? null,
      env: (sandbox.env as AdminAgentDetail["sandbox_config"]["env"]) ?? null,
    },
  }
}

export const adminAgentsApi = {
  updateEnv: (orgId: string, id: string, body: EnvPatch) => api<{ ok: boolean }>(`/orgs/${orgId}/admin/agents/${id}/env`, { method: "PATCH", body }),
  list(orgId: string, params?: { cursor?: string; limit?: number }) {
    return api<List<AdminAgentSummary>>(`/orgs/${orgId}/admin/agents`, {
      params: { cursor: params?.cursor, limit: params?.limit },
    })
  },
  create(orgId: string, body: AgentBody) {
    return api<AdminAgentDetail>(`/orgs/${orgId}/admin/agents`, { method: "POST", body })
  },
  async detail(orgId: string, id: string) {
    return normalizeAgentDetail(await api<unknown>(`/orgs/${orgId}/admin/agents/${id}`))
  },
  update(orgId: string, id: string, body: AgentBody) {
    return api<AdminAgentDetail>(`/orgs/${orgId}/admin/agents/${id}`, { method: "PUT", body })
  },
  publish(orgId: string, id: string) {
    return api<AdminAgentDetail>(`/orgs/${orgId}/admin/agents/${id}/publish`, { method: "POST" })
  },
  archive(orgId: string, id: string) {
    return api<AdminAgentDetail>(`/orgs/${orgId}/admin/agents/${id}/archive`, { method: "POST" })
  },
  remove(orgId: string, id: string) {
    return api<{ ok: boolean }>(`/orgs/${orgId}/admin/agents/${id}`, { method: "DELETE" })
  },
}
