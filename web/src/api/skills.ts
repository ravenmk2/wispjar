import { ApiError, api, apiRaw } from "./client"
import { useIdentityStore } from "@/stores/identity"
import type { ErrorEnvelope, List, SkillDetail, SkillFileEntry, SkillFileResponse, SkillReference, SkillSummary } from "@/lib/types"

function normalizeReferences(raw: unknown): SkillReference[] {
  if (!Array.isArray(raw)) return []
  return raw
    .map((item) => {
      if (typeof item === "string") return { agent_id: "", agent_name: item }
      if (item && typeof item === "object") {
        const obj = item as Record<string, unknown>
        const name = obj.agent_name ?? obj.name
        const id = obj.agent_id ?? obj.id
        if (typeof name === "string") return { agent_id: typeof id === "string" ? id : "", agent_name: name }
      }
      return null
    })
    .filter((v): v is SkillReference => v !== null)
}

function normalizeSkill(raw: unknown): SkillSummary {
  const obj = (raw ?? {}) as Record<string, unknown>
  const size = obj.size_bytes ?? obj.size
  let uploader = obj.uploader_name ?? obj.uploaded_by ?? obj.uploader
  if (uploader && typeof uploader === "object") {
    const u = uploader as Record<string, unknown>
    uploader = u.display_name ?? u.username ?? u.name
  }
  return {
    id: typeof obj.id === "string" ? obj.id : "",
    name: typeof obj.name === "string" ? obj.name : "",
    description: typeof obj.description === "string" ? obj.description : "",
    size_bytes: typeof size === "number" ? size : 0,
    uploader_name: typeof uploader === "string" ? uploader : "-",
    created_at: typeof obj.created_at === "string" ? obj.created_at : "",
    referenced_by: normalizeReferences(obj.referenced_by),
    revision: typeof obj.revision === "number" ? obj.revision : 1,
    content_hash: typeof obj.content_hash === "string" ? obj.content_hash : undefined,
  }
}

function normalizeDetail(raw: unknown): SkillDetail {
  const obj = (raw ?? {}) as Record<string, unknown>
  const files: SkillFileEntry[] = Array.isArray(obj.files)
    ? (obj.files as unknown[])
        .map((f) => {
          if (typeof f === "string") return { path: f, size: 0 }
          if (f && typeof f === "object") {
            const fo = f as Record<string, unknown>
            if (typeof fo.path === "string") {
              return {
                path: fo.path,
                size: typeof fo.size === "number" ? fo.size : 0,
                type: fo.type === "dir" ? ("dir" as const) : ("file" as const),
              }
            }
          }
          return null
        })
        .filter((f): f is SkillFileEntry => f !== null)
    : []
  const meta =
    obj.meta && typeof obj.meta === "object" && !Array.isArray(obj.meta)
      ? (obj.meta as Record<string, unknown>)
      : obj.frontmatter && typeof obj.frontmatter === "object" && !Array.isArray(obj.frontmatter)
        ? (obj.frontmatter as Record<string, unknown>)
        : {}
  return { ...normalizeSkill(obj), meta, files }
}

function parseErrorBody(status: number, text: string): ApiError {
  try {
    const data = JSON.parse(text) as ErrorEnvelope
    if (data?.error?.code) {
      return new ApiError(status, data.error.code, data.error.message || "请求失败", data.error.details)
    }
  } catch {
    // ignore
  }
  return new ApiError(status, "internal", `请求失败（HTTP ${status}）`)
}

export const adminSkillsApi = {
  async replace(orgId: string, id: string, revision: number, file: File): Promise<SkillSummary> {
    const body = new FormData()
    body.append("file", file); body.append("expected_revision", String(revision))
    const response = await apiRaw(`/orgs/${orgId}/admin/skills/${id}/package`, { method: "PUT", body })
    return normalizeSkill(await response.json())
  },
  async list(orgId: string): Promise<SkillSummary[]> {
    const data = await api<unknown>(`/orgs/${orgId}/admin/skills`)
    const items =
      data && typeof data === "object" && Array.isArray((data as List<unknown>).items)
        ? (data as List<unknown>).items
        : Array.isArray(data)
          ? (data as unknown[])
          : []
    return items.map(normalizeSkill)
  },

  upload(orgId: string, file: File, onProgress?: (percent: number) => void): Promise<SkillSummary> {
    return new Promise((resolve, reject) => {
      const { token, currentOrgId } = useIdentityStore.getState()
      const xhr = new XMLHttpRequest()
      xhr.open("POST", `/api/v1/orgs/${orgId}/admin/skills`)
      if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`)
      xhr.setRequestHeader("X-Org-Id", currentOrgId ?? orgId)
      xhr.upload.onprogress = (e) => {
        if (e.lengthComputable) onProgress?.(Math.round((e.loaded / e.total) * 100))
      }
      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          try {
            resolve(normalizeSkill(JSON.parse(xhr.responseText)))
          } catch {
            reject(new ApiError(xhr.status, "internal", "响应解析失败"))
          }
        } else if (xhr.status === 401) {
          useIdentityStore.getState().clear()
          window.location.href = "/login"
          reject(parseErrorBody(xhr.status, xhr.responseText))
        } else {
          reject(parseErrorBody(xhr.status, xhr.responseText))
        }
      }
      xhr.onerror = () => reject(new ApiError(0, "internal", "网络错误，上传失败"))
      const form = new FormData()
      form.append("file", file)
      xhr.send(form)
    })
  },

  async detail(orgId: string, id: string): Promise<SkillDetail> {
    return normalizeDetail(await api<unknown>(`/orgs/${orgId}/admin/skills/${id}`))
  },

  async file(orgId: string, id: string, path: string): Promise<SkillFileResponse> {
    const data = await api<unknown>(`/orgs/${orgId}/admin/skills/${id}/file`, { params: { path } })
    if (typeof data === "string") return { path, content: data }
    const obj = (data ?? {}) as Record<string, unknown>
    return {
      path: typeof obj.path === "string" ? obj.path : path,
      content: typeof obj.content === "string" ? obj.content : "",
      truncated: obj.truncated === true,
    }
  },

  remove(orgId: string, id: string) {
    return api<{ ok: boolean }>(`/orgs/${orgId}/admin/skills/${id}`, { method: "DELETE" })
  },
}
