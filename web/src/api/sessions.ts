import { api, apiRaw } from "./client"
import type {
  AnswerQuestionRequest,
  AnswerQuestionResponse,
  ApprovalDecisionRequest,
  CreateSessionRequest,
  ForkResponse,
  List,
  PromptRequest,
  PromptResponse,
  SandboxFilesResponse,
  SessionDetail,
  SessionEventsResponse,
  SessionSummary,
  UpdateSessionRequest,
} from "@/lib/types"

export const sessionsApi = {
  createPreview(id: string, scope: FileScope, root: string, entry: string) {
    return api<FilePreviewGrant>(`/sessions/${id}/previews`, { method: "POST", body: { scope, root, entry } })
  },
  revokePreview(id: string, previewId: string) {
    return api(`/sessions/${id}/previews/${previewId}`, { method: "DELETE" })
  },
  files(id: string, path = ".", scope: FileScope = "workspace") {
    return api<SandboxFilesResponse & { root: string }>(`/sessions/${id}/files`, { params: { path, scope } })
  },
  async uploadFile(id: string, file: File, path = ".", scope: FileScope = "workspace") {
    const body = new FormData(); body.append("file", file); body.append("path", path)
    return (await apiRaw(`/sessions/${id}/files?${new URLSearchParams({ scope })}`, { method: "POST", body })).json()
  },
  saveToStorage(id: string, source: string, destination: string) {
    return api(`/sessions/${id}/files/save-to-storage`, { method: "POST", body: { source, destination } })
  },
  create(orgId: string, body: CreateSessionRequest) {
    return api<SessionDetail>(`/orgs/${orgId}/sessions`, { method: "POST", body })
  },
  list(orgId: string, params?: { cursor?: string; limit?: number; q?: string; status?: "active" | "archived"; agent_id?: string }) {
    return api<List<SessionSummary>>(`/orgs/${orgId}/sessions`, {
      params: {
        cursor: params?.cursor,
        limit: params?.limit,
        q: params?.q,
        status: params?.status,
        agent_id: params?.agent_id,
      },
    })
  },
  detail(id: string) {
    return api<SessionDetail>(`/sessions/${id}`)
  },
  update(id: string, body: UpdateSessionRequest) {
    return api<SessionDetail>(`/sessions/${id}`, { method: "PATCH", body })
  },
  remove(id: string) {
    return api<{ ok: boolean }>(`/sessions/${id}`, { method: "DELETE" })
  },
  prompt(id: string, body: PromptRequest) {
    return api<PromptResponse>(`/sessions/${id}/prompts`, { method: "POST", body })
  },
  cancel(id: string) {
    return api<{ ok: boolean }>(`/sessions/${id}/cancel`, { method: "POST" })
  },
  fork(id: string, atSeq: number, title?: string) {
    return api<ForkResponse>(`/sessions/${id}/fork`, { method: "POST", body: { at_seq: atSeq, title } })
  },
  events(id: string, afterSeq: number, limit = 500) {
    return api<SessionEventsResponse>(`/sessions/${id}/events`, {
      params: { after_seq: afterSeq, limit },
    })
  },
  decideApproval(id: string, callId: string, decision: ApprovalDecisionRequest["decision"]) {
    return api<{ ok: boolean }>(`/sessions/${id}/approvals/${callId}`, {
      method: "POST",
      body: { decision },
    })
  },
  answerQuestion(id: string, callId: string, answers: AnswerQuestionRequest["answers"]) {
    return api<AnswerQuestionResponse>(`/sessions/${id}/questions/${callId}/answer`, {
      method: "POST",
      body: { answers },
    })
  },

}
export type FileScope = "workspace" | "storage" | "agent"
export interface FilePreviewGrant { id: string; entry_url: string; files_url: string; expires_at: string }
