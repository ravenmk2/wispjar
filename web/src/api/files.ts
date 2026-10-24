import { api, apiRaw } from "./client"
import type { FilePreviewGrant, FileScope } from "./sessions"
import type { SandboxFileItem } from "@/lib/types"
import { downloadFileJob, type FileData } from "@/features/sandbox/previewUtils"

export interface FileSpaceTarget { orgId: string; computerId: string; scope: FileScope; targetId?: string }
export interface FileAgent { id: string; name: string; status: string }
export interface FilePreviewSource {
  key: string
  read(path: string, maxBytes?: number, signal?: AbortSignal): Promise<FileData>
  preview(path: string): Promise<FilePreviewGrant>
  revoke(id: string): Promise<unknown>
  download(path: string, archive?: boolean): Promise<void>
}

export function fileSpaceURL(target: FileSpaceTarget, endpoint: string): string {
  const params = new URLSearchParams({ computer_id: target.computerId, scope: target.scope })
  if (target.targetId) params.set("target_id", target.targetId)
  return `/orgs/${target.orgId}/${endpoint}?${params}`
}

export const filesApi = {
  agents: (org: string) => api<{ items: FileAgent[] }>(`/orgs/${org}/file-agents`),
  list: (target: FileSpaceTarget, path = ".") => api<{ root: string; path: string; items: SandboxFileItem[] }>(fileSpaceURL(target, "files"), { params: { path } }),
  mkdir: (target: FileSpaceTarget, path: string) => api(fileSpaceURL(target, "file-directories"), { method: "POST", body: { path } }),
  async upload(target: FileSpaceTarget, file: File, path: string) {
    const body = new FormData(); body.append("file", file); body.append("path", path)
    return (await apiRaw(fileSpaceURL(target, "files"), { method: "POST", body })).json()
  },
  source(target: FileSpaceTarget): FilePreviewSource {
    return {
      key: JSON.stringify(target),
      async read(path, maxBytes, signal) {
        const params = new URLSearchParams({ path })
        if (maxBytes) params.set("max_bytes", String(maxBytes))
        const response = await apiRaw(`${fileSpaceURL(target, "file")}&${params}`, { signal })
        if (response.headers.get("Content-Type")?.includes("application/json")) {
          const data = await response.json() as { content: string; truncated: boolean }
          return { type: "text", ...data }
        }
        return { type: "blob", blob: await response.blob() }
      },
      preview: (entry) => api(fileSpaceURL(target, "file-previews"), { method: "POST", body: { scope: target.scope, root: ".", entry } }),
      revoke: (id) => api(`/orgs/${target.orgId}/file-previews/${id}`, { method: "DELETE" }),
      download: (path, archive = false) => downloadFileJob(`/orgs/${target.orgId}/file-downloads`, { path, scope: target.scope, archive }, fileSpaceURL(target, "file-downloads")),
    }
  },
}
