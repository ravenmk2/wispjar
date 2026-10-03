import { api } from "./client"
import type {
  AnswerQuestionRequest,
  AnswerQuestionResponse,
  ApprovalDecisionRequest,
  CreateSessionRequest,
  ForkResponse,
  List,
  PromptRequest,
  PromptResponse,
  SandboxFileResponse,
  SandboxFilesResponse,
  SessionDetail,
  SessionEventsResponse,
  SessionSummary,
  UpdateSessionRequest,
} from "@/lib/types"

export const sessionsApi = {
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
  fork(id: string, atSeq: number) {
    return api<ForkResponse>(`/sessions/${id}/fork`, { method: "POST", body: { at_seq: atSeq } })
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
  sandboxFiles(id: string, path?: string) {
    return api<SandboxFilesResponse>(`/sessions/${id}/sandbox/files`, { params: { path } })
  },
  sandboxFile(id: string, path: string, maxBytes?: number) {
    return api<SandboxFileResponse>(`/sessions/${id}/sandbox/file`, {
      params: { path, max_bytes: maxBytes },
    })
  },
}
