import { ApiError, api } from "@/api/client"
import { toast } from "sonner"
import { useIdentityStore } from "@/stores/identity"
import type { FileScope } from "@/api/sessions"

const IMAGE_EXTS = new Set(["png", "jpg", "jpeg", "webp", "gif", "svg"])
const HTML_EXTS = new Set(["html", "htm"])
const MARKDOWN_EXTS = new Set(["md", "markdown"])

export type PreviewKind = "image" | "html" | "markdown" | "other"

export function previewKind(name: string): PreviewKind {
  const ext = name.includes(".") ? (name.split(".").pop() ?? "").toLowerCase() : ""
  if (IMAGE_EXTS.has(ext)) return "image"
  if (HTML_EXTS.has(ext)) return "html"
  if (MARKDOWN_EXTS.has(ext)) return "markdown"
  return "other"
}

export const KIND_LABEL: Record<PreviewKind, string> = {
  image: "图片",
  html: "HTML",
  markdown: "Markdown",
  other: "文本",
}

export const IMAGE_MAX_BYTES = 4194304

export type FileData = { type: "text"; content: string; truncated: boolean } | { type: "blob"; blob: Blob }

export async function fetchSandboxFileData(sessionId: string, path: string, maxBytes?: number, scope: FileScope = "workspace"): Promise<FileData> {
  const { token, currentOrgId } = useIdentityStore.getState()
  const url = new URL(`/api/v1/sessions/${sessionId}/file`, window.location.origin)
  if (scope) url.searchParams.set("scope", scope)
  url.searchParams.set("path", path)
  if (maxBytes) url.searchParams.set("max_bytes", String(maxBytes))
  const headers: Record<string, string> = {}
  if (token) headers["Authorization"] = `Bearer ${token}`
  if (currentOrgId) headers["X-Org-Id"] = currentOrgId
  const res = await fetch(url.toString(), { headers })
  if (!res.ok) {
    let message = `请求失败（HTTP ${res.status}）`
    try {
      const data = await res.json()
      if (data?.error?.message) message = data.error.message
    } catch {
      // ignore
    }
    throw new ApiError(res.status, "file/upstream", message)
  }
  const contentType = res.headers.get("Content-Type") ?? ""
  if (contentType.includes("application/json")) {
    const data = (await res.json()) as { content: string; truncated: boolean }
    return { type: "text", content: data.content, truncated: data.truncated }
  }
  return { type: "blob", blob: await res.blob() }
}

export function downloadBlob(blob: Blob, filename: string) {
  const a = document.createElement("a")
  a.href = URL.createObjectURL(blob)
  a.download = filename
  a.click()
  URL.revokeObjectURL(a.href)
}

type DownloadJob = { id: string; status: "preparing" | "ready" | "failed" | "canceled"; name: string; url?: string; message?: string; expires_at?: string }

export async function downloadWorkspaceFile(sessionId: string, path: string, scope: FileScope, archive = false): Promise<void> {
  const base = `/sessions/${sessionId}/downloads`
  let job = await api<DownloadJob>(base, { method: "POST", body: { path, scope, archive } })
  let canceled = false
  let canceling = false
  const notification = toast.loading(`正在${archive ? "打包目录" : "准备下载"}：${job.name}`, {
    duration: Infinity,
    action: { label: "取消", onClick: () => {
      if (canceling) return
      canceling = true
      void api(`${base}/${job.id}`, { method: "DELETE" }).then(() => { canceled = true; toast.info("已取消下载准备") }).catch((e) => { canceling = false; toast.error(e instanceof Error ? e.message : "取消失败") })
    } },
  })
  try {
    while (job.status === "preparing" && !canceled) {
      await new Promise((resolve) => setTimeout(resolve, 1000))
      if (!canceled) job = await api<DownloadJob>(`${base}/${job.id}`)
    }
    if (canceled || job.status === "canceled") return
    if (job.status !== "ready" || !job.url) throw new Error(job.message || "下载准备失败")
    // Let the browser stream directly to disk; never materialize a multi-GB Blob.
    const ready = job
    const expiresAt = ready.expires_at ? Date.parse(ready.expires_at) : Date.now() + 5 * 60_000
    const begin = () => {
      if (Date.now() >= expiresAt) { toast.error("下载已过期，请重新准备"); return }
      const anchor = document.createElement("a")
      anchor.href = ready.url!
      anchor.download = ready.name
      anchor.rel = "noreferrer"
      document.body.append(anchor); anchor.click(); anchor.remove()
    }
    // A long preparation can outlive transient browser user activation. Keep
    // an explicit download action available instead of assuming auto-start.
    toast.success(`${archive ? "目录已打包" : "文件已准备好"}：${ready.name}`, {
      duration: Math.max(1000, expiresAt - Date.now()),
      action: { label: "下载", onClick: begin },
    })
  } finally { toast.dismiss(notification) }
}

export function fileName(path: string): string {
  return path.split("/").pop() ?? "download"
}

export function fileExt(path: string): string | undefined {
  return path.includes(".") ? path.split(".").pop() : undefined
}
