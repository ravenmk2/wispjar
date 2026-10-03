import * as DialogPrimitive from "@radix-ui/react-dialog"
import { Download, Eye, FileCode2, FileText, Image as ImageIcon, Loader2, X } from "lucide-react"
import { useEffect, useState } from "react"
import { ApiError } from "@/api/client"
import { Markdown } from "@/components/Markdown"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { DialogOverlay, DialogPortal } from "@/components/ui/dialog"
import { highlightCode } from "@/lib/highlight"
import type { SandboxFileItem } from "@/lib/types"
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

const KIND_LABEL: Record<Exclude<PreviewKind, "other">, string> = {
  image: "图片",
  html: "HTML",
  markdown: "Markdown",
}

export const IMAGE_MAX_BYTES = 4194304

type FileData = { type: "text"; content: string; truncated: boolean } | { type: "blob"; blob: Blob }

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

function fileName(path: string): string {
  return path.split("/").pop() ?? "download"
}

function fileExt(path: string): string | undefined {
  return path.includes(".") ? path.split(".").pop() : undefined
}

type LoadState =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; blob: Blob; objectUrl?: string; tooLarge?: boolean }

export function FilePreviewDialog({
  sessionId,
  item,
  onClose,
}: {
  sessionId: string
  item: SandboxFileItem
  onClose: () => void
}) {
  const kind = previewKind(item.name) as Exclude<PreviewKind, "other">
  const [state, setState] = useState<LoadState>({ status: "loading" })
  const [mode, setMode] = useState<"preview" | "source">("preview")
  const [sourceHtml, setSourceHtml] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    let objectUrl: string | undefined
    setState({ status: "loading" })
    setMode("preview")
    setSourceHtml(null)

    const maxBytes = kind === "image" ? IMAGE_MAX_BYTES : undefined
    fetchSandboxFileData(sessionId, item.path, maxBytes)
      .then((data) => {
        if (cancelled) return
        if (data.type === "text") {
          // UTF-8 文件统一走 JSON 通道（含 svg）；图片按文本到达时还原为 blob
          if (kind === "image" && data.truncated) {
            setState({
              status: "ready",
              blob: new Blob([data.content], { type: "application/octet-stream" }),
              tooLarge: true,
            })
            return
          }
          const mime = kind === "image" && item.name.toLowerCase().endsWith(".svg") ? "image/svg+xml" : "text/plain;charset=utf-8"
          const blob = new Blob([data.content], { type: mime })
          if (kind === "image") {
            objectUrl = URL.createObjectURL(blob)
            setState({ status: "ready", blob, objectUrl })
          } else {
            setState({ status: "ready", blob, tooLarge: data.truncated })
          }
          return
        }
        if (kind === "image" && data.blob.size < item.size) {
          setState({ status: "ready", blob: data.blob, tooLarge: true })
          return
        }
        objectUrl = URL.createObjectURL(data.blob)
        setState({ status: "ready", blob: data.blob, objectUrl })
      })
      .catch((e) => {
        if (!cancelled) setState({ status: "error", message: e instanceof Error ? e.message : String(e) })
      })

    return () => {
      cancelled = true
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [sessionId, item.path, item.size, item.name, kind])

  useEffect(() => {
    setSourceHtml(null)
    if (kind === "image" || mode !== "source" || state.status !== "ready") return
    let cancelled = false
    state.blob.text().then((text) => {
      if (cancelled) return
      return highlightCode(text, fileExt(item.path)).then((html) => {
        if (!cancelled) setSourceHtml(html)
      })
    })
    return () => {
      cancelled = true
    }
  }, [kind, mode, state, item.path])

  const truncated = state.status === "ready" && state.tooLarge && kind !== "image"

  return (
    <DialogPrimitive.Root open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Content
          className="fixed left-1/2 top-1/2 z-50 flex h-[85vh] w-[85vw] max-w-[85vw] -translate-x-1/2 -translate-y-1/2 flex-col overflow-hidden rounded-lg border bg-background shadow-lg duration-200 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95"
        >
          <DialogPrimitive.Title className="sr-only">{item.path}</DialogPrimitive.Title>
          <DialogPrimitive.Description className="sr-only">沙箱文件预览</DialogPrimitive.Description>

          <div className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
            {kind === "image" ? (
              <ImageIcon className="size-4 shrink-0 text-muted-foreground" />
            ) : kind === "html" ? (
              <FileCode2 className="size-4 shrink-0 text-muted-foreground" />
            ) : (
              <FileText className="size-4 shrink-0 text-muted-foreground" />
            )}
            <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground" title={item.path}>
              {item.path}
            </span>
            <Badge variant="secondary" className="shrink-0 text-[10px] font-normal">
              {KIND_LABEL[kind]}
            </Badge>
            {kind !== "image" && (
              <div className="flex shrink-0 items-center rounded-md border p-0.5">
                <Button
                  variant={mode === "preview" ? "secondary" : "ghost"}
                  size="sm"
                  className="h-6 gap-1 px-2 text-[11px]"
                  onClick={() => setMode("preview")}
                >
                  <Eye className="size-3" />
                  预览
                </Button>
                <Button
                  variant={mode === "source" ? "secondary" : "ghost"}
                  size="sm"
                  className="h-6 gap-1 px-2 text-[11px]"
                  onClick={() => setMode("source")}
                >
                  <FileCode2 className="size-3" />
                  源码
                </Button>
              </div>
            )}
            {state.status === "ready" && (
              <Button
                variant="ghost"
                size="icon"
                className="size-7 shrink-0"
                title="下载文件"
                onClick={() => downloadBlob(state.blob, fileName(item.path))}
              >
                <Download className="size-3.5" />
              </Button>
            )}
            <DialogPrimitive.Close asChild>
              <Button variant="ghost" size="icon" className="size-7 shrink-0" title="关闭">
                <X className="size-4" />
              </Button>
            </DialogPrimitive.Close>
          </div>

          {truncated && (
            <div className="shrink-0 border-b bg-amber-500/10 px-4 py-1.5 text-[11px] text-amber-700 dark:text-amber-400">
              文件过大已截断，预览可能不完整
            </div>
          )}

          <div className="flex min-h-0 flex-1 flex-col">
            {state.status === "loading" && (
              <div className="flex flex-1 items-center justify-center gap-2 text-xs text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                加载中…
              </div>
            )}
            {state.status === "error" && (
              <div className="flex flex-1 items-center justify-center px-6 text-center text-xs text-destructive">
                {state.message}
              </div>
            )}
            {state.status === "ready" && kind === "image" && (
              state.tooLarge ? (
                <div className="flex flex-1 flex-col items-center justify-center gap-3 text-xs text-muted-foreground">
                  <ImageIcon className="size-8 text-muted-foreground/50" />
                  <span>图片过大仅支持下载</span>
                  <Button variant="outline" size="sm" className="gap-1.5" onClick={() => downloadBlob(state.blob, fileName(item.path))}>
                    <Download className="size-3.5" />
                    下载文件
                  </Button>
                </div>
              ) : (
                <div className="min-h-0 flex-1 overflow-auto bg-muted/30">
                  <div className="flex min-h-full items-center justify-center p-4">
                    <img src={state.objectUrl} alt={item.name} className="max-h-full max-w-full rounded object-contain" />
                  </div>
                </div>
              )
            )}
            {state.status === "ready" && kind !== "image" && mode === "preview" && (
              <PreviewBody kind={kind} blob={state.blob} path={item.path} />
            )}
            {state.status === "ready" && kind !== "image" && mode === "source" && (
              <SourceBody blob={state.blob} sourceHtml={sourceHtml} />
            )}
          </div>
        </DialogPrimitive.Content>
      </DialogPortal>
    </DialogPrimitive.Root>
  )
}

function PreviewBody({ kind, blob, path }: { kind: "html" | "markdown"; blob: Blob; path: string }) {
  const [text, setText] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    blob.text().then((t) => {
      if (!cancelled) setText(t)
    })
    return () => {
      cancelled = true
    }
  }, [blob])

  if (text === null) {
    return (
      <div className="flex flex-1 items-center justify-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-4 animate-spin" />
        加载中…
      </div>
    )
  }
  if (kind === "html") {
    return <iframe sandbox="allow-scripts" srcDoc={text} title={path} className="h-full w-full flex-1 rounded bg-white" />
  }
  return (
    <div className="min-h-0 flex-1 overflow-auto">
      <Markdown content={text} className="mx-auto max-w-3xl px-6 py-4" />
    </div>
  )
}

function SourceBody({ blob, sourceHtml }: { blob: Blob; sourceHtml: string | null }) {
  const [text, setText] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    blob.text().then((t) => {
      if (!cancelled) setText(t)
    })
    return () => {
      cancelled = true
    }
  }, [blob])

  if (sourceHtml) {
    return (
      <div className="min-h-0 flex-1 overflow-auto">
        <div
          className="markdown-body px-4 py-3 font-mono text-xs leading-relaxed [&_pre]:!bg-transparent"
          dangerouslySetInnerHTML={{ __html: sourceHtml }}
        />
      </div>
    )
  }
  return (
    <div className="min-h-0 flex-1 overflow-auto">
      <pre className="whitespace-pre-wrap px-4 py-3 font-mono text-xs leading-relaxed">{text ?? ""}</pre>
    </div>
  )
}
