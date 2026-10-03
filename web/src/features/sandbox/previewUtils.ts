import { ApiError } from "@/api/client"
import { useIdentityStore } from "@/stores/identity"

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

export async function fetchSandboxFileData(sessionId: string, path: string, maxBytes?: number): Promise<FileData> {
  const { token, currentOrgId } = useIdentityStore.getState()
  const url = new URL(`/api/v1/sessions/${sessionId}/sandbox/file`, window.location.origin)
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
    throw new ApiError(res.status, "sandbox/file", message)
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

export function fileName(path: string): string {
  return path.split("/").pop() ?? "download"
}

export function fileExt(path: string): string | undefined {
  return path.includes(".") ? path.split(".").pop() : undefined
}
