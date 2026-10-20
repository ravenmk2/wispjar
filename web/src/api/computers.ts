import { api } from "./client"
import type { AgentBinding, Computer, ComputerConfig, ComputerOperation, ComputerRun, OperationAccepted, UserVolume } from "@/lib/computers"

export const computersApi = {
  list: (org: string) => api<{ items: Computer[] }>(`/orgs/${org}/computers`),
  get: (id: string) => api<Computer>(`/computers/${id}`),
  recover: (id: string, key: string) => api<OperationAccepted>(`/computers/${id}/recover`, {
    method: "POST", body: { confirm_stop_all: true }, headers: { "Idempotency-Key": key },
  }),
  create: (org: string, body: { name: string; agent_id?: string; config?: ComputerConfig }, key: string) =>
    api<OperationAccepted>(`/orgs/${org}/computers`, { method: "POST", body, headers: { "Idempotency-Key": key } }),
  rename: (id: string, name: string) => api<Computer>(`/computers/${id}`, { method: "PATCH", body: { name } }),
  action: (id: string, action: "start" | "pause" | "rebuild" | "delete", key: string, config?: ComputerConfig) =>
    api<OperationAccepted>(`/computers/${id}${action === "delete" ? "" : `/${action}`}`, {
      method: action === "delete" ? "DELETE" : "POST", body: action === "rebuild" ? { config } : undefined, headers: { "Idempotency-Key": key },
    }),
  operation: (id: string) => api<ComputerOperation>(`/operations/${id}`),
  runs: (id: string) => api<{ items: ComputerRun[] }>(`/computers/${id}/runs`),
  bindings: (org: string) => api<{ items: AgentBinding[] }>(`/orgs/${org}/agent-bindings`),
  bind: (org: string, agent: string, computer: string) => api(`/orgs/${org}/agent-bindings/${agent}`, { method: "PUT", body: { computer_id: computer } }),
  unbind: (org: string, agent: string) => api(`/orgs/${org}/agent-bindings/${agent}`, { method: "DELETE" }),
  volumes: (org: string) => api<{ items: UserVolume[] }>(`/orgs/${org}/volumes`),
}
