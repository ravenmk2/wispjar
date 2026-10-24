import { Download, Eye, FileCode2, FileText, Image as ImageIcon, Loader2 } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import { sessionsApi, type FilePreviewGrant, type FileScope } from "@/api/sessions"
import { Markdown } from "@/components/Markdown"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { FileIcon } from "@/components/FileIcon"
import { PixelImage } from "@/components/PixelImage"
import type { ReactNode } from "react"
import { highlightCode } from "@/lib/highlight"
import type { SandboxFileItem } from "@/lib/types"
import { resolvePreviewReference } from "@/lib/file-preview"
import {
  downloadBlob,
  downloadWorkspaceFile,
  fetchSandboxFileData,
  fileExt,
  fileName,
  IMAGE_MAX_BYTES,
  KIND_LABEL,
  previewKind,
} from "./previewUtils"

type LoadState =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; blob: Blob; text?: string; objectUrl?: string; tooLarge?: boolean; binary?: boolean }

export function FilePreview({
  sessionId,
  item,
  reloadToken,
  scope,
  actions,
  navigation,
  onNavigate,
}: {
  sessionId: string
  item: SandboxFileItem
  reloadToken: number
  scope?: FileScope
  actions?: ReactNode
  navigation?: ReactNode
  onNavigate?: (path: string) => void
}) {
  const kind = previewKind(item.name)
  const [state, setState] = useState<LoadState>({ status: "loading" })
  const [mode, setMode] = useState<"preview" | "source">("preview")
  const [sourceHtml, setSourceHtml] = useState<string | null>(null)
  const loadedPathRef = useRef<string | null>(null)
  const [previewRevision, setPreviewRevision] = useState(0)
  const previewKey = JSON.stringify([sessionId, scope, item.path, reloadToken, previewRevision])
  const [grantState, setGrantState] = useState<{ key: string; grant?: FilePreviewGrant; content?: string; error?: string }>()
  const preview = grantState?.key === previewKey ? grantState : undefined

  useEffect(() => {
    if (kind !== "html" && kind !== "markdown") return
    let canceled = false
    let grant: FilePreviewGrant | undefined
    let timer: ReturnType<typeof setTimeout> | undefined
    const revoke = (value: FilePreviewGrant) => { void sessionsApi.revokePreview(sessionId, value.id).catch(() => { /* TTL covers offline/unload cleanup. */ }) }
    setGrantState({ key: previewKey })
    void sessionsApi.createPreview(sessionId, scope ?? "workspace", ".", item.path).then(async (value) => {
      if (canceled) { revoke(value); return }
      grant = value
      let content: string | undefined
      if (kind === "markdown") {
        const response = await fetch(value.entry_url, { credentials: "omit", referrerPolicy: "no-referrer" })
        if (!response.ok) throw new Error(`Markdown 预览读取失败（HTTP ${response.status}）`)
        content = await response.text()
      }
      if (canceled) return
      setGrantState({ key: previewKey, grant: value, content })
      timer = setTimeout(() => setPreviewRevision((n) => n + 1), Math.max(1000, Date.parse(value.expires_at) - Date.now()))
    }).catch((error) => {
      if (!canceled) setGrantState({ key: previewKey, error: error instanceof Error ? error.message : "预览创建失败" })
    })
    return () => { canceled = true; clearTimeout(timer); if (grant) revoke(grant) }
  }, [sessionId, scope, item.path, kind, previewKey])

  const resolveReference = (url: string) => preview?.grant ? resolvePreviewReference(url, preview.grant.entry_url, preview.grant.files_url, window.location.origin) : undefined
  async function download() {
    try {
      if (scope) await downloadWorkspaceFile(sessionId, item.path, scope)
      else if (state.status === "ready") downloadBlob(state.blob, fileName(item.path))
    } catch (e) { toast.error(e instanceof Error ? e.message : "下载失败") }
  }

  useEffect(() => {
    setMode("preview")
  }, [item.path])

  useEffect(() => {
    let cancelled = false
    let objectUrl: string | undefined
    setState({ status: "loading" })
    setSourceHtml(null)
    const isReload = loadedPathRef.current === item.path

    const maxBytes = kind === "image" ? IMAGE_MAX_BYTES : undefined
    fetchSandboxFileData(sessionId, item.path, maxBytes, scope)
      .then(async (data) => {
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
            setState({ status: "ready", blob, text: data.content, tooLarge: data.truncated })
          }
          return
        }
        if (kind === "other") {
          // 二进制文件：首次打开直接触发下载，自动刷新重载时不重复下载
          if (!scope && !isReload) {
            downloadBlob(data.blob, fileName(item.path))
            toast.info("二进制文件已开始下载")
          }
          setState({ status: "ready", blob: data.blob, binary: true })
          return
        }
        if (kind === "image" && data.blob.size < item.size) {
          setState({ status: "ready", blob: data.blob, tooLarge: true })
          return
        }
        objectUrl = URL.createObjectURL(data.blob)
        setState({ status: "ready", blob: data.blob, objectUrl })
      })
      .then(() => {
        if (!cancelled) loadedPathRef.current = item.path
      })
      .catch((e) => {
        if (!cancelled) setState({ status: "error", message: e instanceof Error ? e.message : String(e) })
      })

    return () => {
      cancelled = true
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [sessionId, item.path, item.size, item.name, kind, reloadToken, scope])

  useEffect(() => {
    setSourceHtml(null)
    if (kind === "image" || state.status !== "ready" || state.text === undefined) return
    if (kind !== "other" && mode !== "source") return
    let cancelled = false
    highlightCode(state.text, fileExt(item.path)).then((html) => {
      if (!cancelled) setSourceHtml(html)
    })
    return () => {
      cancelled = true
    }
  }, [kind, mode, state, item.path])

  const truncated = state.status === "ready" && state.tooLarge && kind !== "image"

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b px-3">
        {navigation ?? <>
        <FileIcon name={item.name} className="size-3.5" />
        <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground" title={item.path}>
          {item.path}
        </span>
        </>}
        <Badge variant="secondary" className="shrink-0 text-[10px] font-normal">
          {kind === "other" ? (fileExt(item.name)?.toUpperCase() ?? KIND_LABEL.other) : KIND_LABEL[kind]}
        </Badge>
        {truncated && (
          <Badge variant="secondary" className="shrink-0 text-[10px] font-normal text-amber-700 dark:text-amber-400">
            源码已截断
          </Badge>
        )}
        {(kind === "html" || kind === "markdown") && (
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
        {actions}
        {(scope || state.status === "ready") && (
          <Button
            variant="ghost"
            size="icon"
            className="size-7 shrink-0"
            title="下载文件"
            aria-label="下载文件"
            onClick={() => void download()}
          >
            <Download className="size-3.5" />
          </Button>
        )}
      </div>

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
        {state.status === "ready" && state.binary && (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 text-xs text-muted-foreground">
            <FileText className="size-8 text-muted-foreground/50" />
            <span>{scope ? "此文件暂不支持在线预览，可下载获取" : "二进制文件，已通过下载获取"}</span>
            <Button variant="outline" size="sm" className="gap-1.5" onClick={() => void download()}>
              <Download className="size-3.5" />
              {scope ? "下载文件" : "重新下载"}
            </Button>
          </div>
        )}
        {state.status === "ready" && !state.binary && kind === "image" && (
          state.tooLarge ? (
            <div className="flex flex-1 flex-col items-center justify-center gap-3 text-xs text-muted-foreground">
              <ImageIcon className="size-8 text-muted-foreground/50" />
              <span>图片过大仅支持下载</span>
              <Button variant="outline" size="sm" className="gap-1.5" onClick={() => void download()}>
                <Download className="size-3.5" />
                下载文件
              </Button>
            </div>
          ) : (
            <PixelImage key={state.objectUrl} src={state.objectUrl!} alt={item.name} />
          )
        )}
        {state.status === "ready" && !state.binary && (kind === "html" || kind === "markdown") && mode === "preview" && !preview?.grant && (
          <div role={preview?.error ? "alert" : "status"} className="flex flex-1 items-center justify-center px-4 text-xs text-muted-foreground">{preview?.error ?? "正在准备预览…"}</div>
        )}
        {state.status === "ready" && !state.binary && kind === "html" && mode === "preview" && preview?.grant && (
          <iframe sandbox="allow-scripts" referrerPolicy="no-referrer" src={preview.grant.entry_url} title={item.path} className="h-full w-full flex-1 bg-white" />
        )}
        {state.status === "ready" && !state.binary && kind === "markdown" && mode === "preview" && preview?.grant && (
          <div className="min-h-0 flex-1 overflow-auto">
            <Markdown content={preview.content ?? state.text ?? ""} className="px-4 py-3" resolveUrl={(url) => resolveReference(url)?.url} onLink={(url) => {
              const files = new URL(preview.grant!.files_url, window.location.origin)
              const target = new URL(url, window.location.origin)
              if (target.origin !== files.origin || !target.pathname.startsWith(files.pathname) || !onNavigate) return false
              const local = target.pathname.slice(files.pathname.length).split("/").map(decodeURIComponent).join("/")
              onNavigate(local)
              return true
            }} />
          </div>
        )}
        {state.status === "ready" && !state.binary && (kind === "other" || mode === "source") && (
          <div className="min-h-0 flex-1 overflow-auto">
            {sourceHtml ? (
              <div
                className="markdown-body px-3 py-2 font-mono text-[11px] leading-relaxed [&_pre]:!bg-transparent"
                dangerouslySetInnerHTML={{ __html: sourceHtml }}
              />
            ) : (
              <pre className="whitespace-pre-wrap px-3 py-2 font-mono text-[11px] leading-relaxed">{state.text ?? ""}</pre>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
