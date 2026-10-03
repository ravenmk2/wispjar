import { apiRaw } from "./client"
import type { AttachmentRef } from "@/lib/types"

// <img> 无法带 Authorization 头，附件字节流一律 fetch→blob→objectURL；
// 模块级 Map 缓存 attachmentId→objectURL（内容寻址、不可变，会话生命周期内有效）
const objectURLCache = new Map<string, Promise<string>>()

async function loadObjectURL(attachmentId: string): Promise<string> {
  let lastError: unknown
  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      const res = await apiRaw(`/attachments/${encodeURIComponent(attachmentId)}`)
      const blob = await res.blob()
      return URL.createObjectURL(blob)
    } catch (e) {
      lastError = e
      if (attempt === 0) await new Promise((r) => setTimeout(r, 500))
    }
  }
  throw lastError
}

export const attachmentsApi = {
  async upload(sessionId: string, file: File): Promise<AttachmentRef> {
    const form = new FormData()
    form.append("file", file)
    const res = await apiRaw(`/sessions/${sessionId}/attachments`, { method: "POST", body: form })
    return (await res.json()) as AttachmentRef
  },
  // 失败条目会从缓存剔除，调用方重试即重新拉取
  getObjectURL(attachmentId: string): Promise<string> {
    const cached = objectURLCache.get(attachmentId)
    if (cached) return cached
    const promise = loadObjectURL(attachmentId)
    objectURLCache.set(attachmentId, promise)
    promise.catch(() => {
      if (objectURLCache.get(attachmentId) === promise) objectURLCache.delete(attachmentId)
    })
    return promise
  },
}
