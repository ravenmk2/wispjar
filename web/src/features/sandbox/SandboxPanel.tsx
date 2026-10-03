import {
  Box,
  ChevronDown,
  ChevronRight,
  Download,
  FileCode2,
  FileText,
  Folder,
  Image as ImageIcon,
  Loader2,
  X,
} from "lucide-react"
import { useCallback, useEffect, useMemo, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { formatBytes } from "@/lib/format"
import { highlightCode } from "@/lib/highlight"
import { cn } from "@/lib/utils"
import type { SandboxFileItem, SandboxStatus } from "@/lib/types"
import { useSessionStore } from "@/stores/session"
import { downloadBlob, fetchSandboxFileData, FilePreviewDialog, previewKind } from "./FilePreviewDialog"

const STATUS_LABEL: Record<SandboxStatus, string> = {
  none: "无沙箱",
  creating: "创建中",
  running: "运行中",
  recycled: "已回收",
}

interface PreviewState {
  path: string
  loading: boolean
  content?: string
  truncated?: boolean
  binary?: boolean
}

interface TreeNodeState {
  item: SandboxFileItem
  children: TreeNodeState[] | null
  expanded: boolean
  loading: boolean
}

function sortItems(items: SandboxFileItem[]): SandboxFileItem[] {
  return [...items].sort((a, b) => {
    if (a.type !== b.type) return a.type === "dir" ? -1 : 1
    return a.name.localeCompare(b.name)
  })
}

export function SandboxPanel({ sessionId, onClose }: { sessionId: string; onClose: () => void }) {
  const header = useSessionStore((s) => s.header)
  const events = useSessionStore((s) => s.events)
  const [root, setRoot] = useState<TreeNodeState[] | null>(null)
  const [treeError, setTreeError] = useState<string | null>(null)
  const [preview, setPreview] = useState<PreviewState | null>(null)
  const [highlighted, setHighlighted] = useState<string | null>(null)
  const [dialogItem, setDialogItem] = useState<SandboxFileItem | null>(null)

  const status: SandboxStatus = header?.sandboxStatus ?? "none"
  const image = useMemo(() => {
    for (let i = events.length - 1; i >= 0; i--) {
      const e = events[i]
      if (e.type === "session/sandbox" && e.data.image) return e.data.image
    }
    return null
  }, [events])

  const loadDir = useCallback(
    async (path?: string): Promise<TreeNodeState[]> => {
      const res = await sessionsApi.sandboxFiles(sessionId, path)
      return sortItems(res.items).map((item) => ({ item, children: null, expanded: false, loading: false }))
    },
    [sessionId],
  )

  useEffect(() => {
    setRoot(null)
    setTreeError(null)
    setPreview(null)
    setDialogItem(null)
    if (status !== "running") return
    loadDir()
      .then(setRoot)
      .catch((e) => setTreeError(errorMessage(e)))
  }, [status, loadDir, sessionId])

  useEffect(() => {
    setHighlighted(null)
    if (!preview?.content) return
    const lang = preview.path.includes(".") ? preview.path.split(".").pop() : undefined
    let cancelled = false
    highlightCode(preview.content, lang).then((html) => {
      if (!cancelled) setHighlighted(html)
    })
    return () => {
      cancelled = true
    }
  }, [preview])

  function updateNode(nodes: TreeNodeState[], path: string, update: (n: TreeNodeState) => TreeNodeState): TreeNodeState[] {
    return nodes.map((n) => {
      if (n.item.path === path) return update(n)
      if (n.children) return { ...n, children: updateNode(n.children, path, update) }
      return n
    })
  }

  async function toggleDir(node: TreeNodeState) {
    if (!root) return
    if (node.expanded) {
      setRoot(updateNode(root, node.item.path, (n) => ({ ...n, expanded: false })))
      return
    }
    if (node.children) {
      setRoot(updateNode(root, node.item.path, (n) => ({ ...n, expanded: true })))
      return
    }
    setRoot(updateNode(root, node.item.path, (n) => ({ ...n, loading: true })))
    try {
      const children = await loadDir(node.item.path)
      setRoot((cur) => (cur ? updateNode(cur, node.item.path, (n) => ({ ...n, children, expanded: true, loading: false })) : cur))
    } catch (e) {
      toast.error(errorMessage(e))
      setRoot((cur) => (cur ? updateNode(cur, node.item.path, (n) => ({ ...n, loading: false })) : cur))
    }
  }

  async function openFile(item: SandboxFileItem) {
    if (previewKind(item.name) !== "other") {
      setPreview(null)
      setDialogItem(item)
      return
    }
    if (preview?.loading) return
    setPreview({ path: item.path, loading: true })
    try {
      const data = await fetchSandboxFileData(sessionId, item.path)
      if (data.type === "blob") {
        downloadBlob(data.blob, item.name)
        toast.info("二进制文件已开始下载")
        setPreview({ path: item.path, loading: false, binary: true })
      } else {
        setPreview({ path: item.path, loading: false, content: data.content, truncated: data.truncated })
      }
    } catch (e) {
      toast.error(errorMessage(e))
      setPreview(null)
    }
  }

  function renderNodes(nodes: TreeNodeState[], depth: number) {
    return nodes.map((node) => (
      <div key={node.item.path}>
        <button
          type="button"
          onClick={() => (node.item.type === "dir" ? void toggleDir(node) : void openFile(node.item))}
          className={cn(
            "flex w-full items-center gap-1.5 rounded-sm px-1.5 py-1 text-left text-xs hover:bg-accent/60",
            (preview?.path === node.item.path || dialogItem?.path === node.item.path) && "bg-accent",
          )}
          style={{ paddingLeft: `${depth * 14 + 6}px` }}
        >
          {node.item.type === "dir" ? (
            <>
              {node.loading ? (
                <Loader2 className="size-3 shrink-0 animate-spin text-muted-foreground" />
              ) : node.expanded ? (
                <ChevronDown className="size-3 shrink-0 text-muted-foreground" />
              ) : (
                <ChevronRight className="size-3 shrink-0 text-muted-foreground" />
              )}
              <Folder className="size-3.5 shrink-0 text-muted-foreground" />
            </>
          ) : (
            <>
              <span className="w-3 shrink-0" />
              {(() => {
                const kind = previewKind(node.item.name)
                if (kind === "image") return <ImageIcon className="size-3.5 shrink-0 text-muted-foreground" />
                if (kind === "html") return <FileCode2 className="size-3.5 shrink-0 text-muted-foreground" />
                return <FileText className="size-3.5 shrink-0 text-muted-foreground" />
              })()}
            </>
          )}
          <span className="min-w-0 flex-1 truncate font-mono">{node.item.name}</span>
          {node.item.type === "file" && node.item.size > 0 && (
            <span className="shrink-0 text-[10px] text-muted-foreground">{formatBytes(node.item.size)}</span>
          )}
        </button>
        {node.expanded && node.children && renderNodes(node.children, depth + 1)}
      </div>
    ))
  }

  return (
    <aside className="flex w-80 shrink-0 flex-col border-l">
      <div className="flex h-11 shrink-0 items-center gap-2 border-b px-3">
        <Box className="size-4 text-muted-foreground" />
        <span className="text-sm font-medium">沙箱</span>
        <Badge
          variant={status === "running" ? "default" : "secondary"}
          className="text-[11px] font-normal"
        >
          {status === "creating" && <Loader2 className="mr-1 size-3 animate-spin" />}
          {STATUS_LABEL[status]}
        </Badge>
        <Button variant="ghost" size="icon" className="ml-auto size-7" onClick={onClose}>
          <X className="size-4" />
        </Button>
      </div>
      {image && (
        <div className="shrink-0 border-b px-3 py-1.5 font-mono text-[11px] text-muted-foreground">{image}</div>
      )}

      {status === "none" && (
        <div className="flex flex-1 items-center justify-center p-4 text-center text-xs text-muted-foreground">
          尚未创建沙箱，会话使用沙箱工具时将自动创建
        </div>
      )}
      {status === "creating" && (
        <div className="flex flex-1 items-center justify-center gap-2 p-4 text-xs text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin" />
          沙箱创建中…
        </div>
      )}
      {status === "recycled" && (
        <div className="flex flex-1 items-center justify-center p-4 text-center text-xs text-muted-foreground">
          沙箱已回收，文件已清空
        </div>
      )}
      {status === "running" && (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="min-h-0 flex-1 overflow-y-auto py-1">
            {treeError ? (
              <div className="px-3 py-2 text-xs text-destructive">{treeError}</div>
            ) : root === null ? (
              <div className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
                <Loader2 className="size-3.5 animate-spin" />
                加载文件…
              </div>
            ) : root.length === 0 ? (
              <div className="px-3 py-2 text-xs text-muted-foreground">/workspace 为空</div>
            ) : (
              renderNodes(root, 0)
            )}
          </div>
          {preview && (
            <div className="flex max-h-[45%] shrink-0 flex-col border-t">
              <div className="flex shrink-0 items-center gap-2 border-b px-2.5 py-1.5">
                <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground" title={preview.path}>
                  {preview.path}
                </span>
                {preview.truncated && (
                  <Badge variant="secondary" className="text-[10px] font-normal">
                    已截断
                  </Badge>
                )}
                {!preview.loading && !preview.binary && (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-6"
                    title="下载文件"
                    onClick={() =>
                      downloadBlob(new Blob([preview.content ?? ""], { type: "text/plain;charset=utf-8" }), preview.path.split("/").pop() ?? "download")
                    }
                  >
                    <Download className="size-3.5" />
                  </Button>
                )}
                <Button variant="ghost" size="icon" className="size-6" onClick={() => setPreview(null)}>
                  <X className="size-3.5" />
                </Button>
              </div>
              <div className="min-h-0 flex-1 overflow-auto">
                {preview.loading ? (
                  <div className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
                    <Loader2 className="size-3.5 animate-spin" />
                    加载中…
                  </div>
                ) : preview.binary ? (
                  <div className="px-3 py-2 text-xs text-muted-foreground">二进制文件，已通过下载获取</div>
                ) : highlighted ? (
                  <div
                    className="markdown-body px-3 py-2 font-mono text-[11px] leading-relaxed [&_pre]:!bg-transparent"
                    dangerouslySetInnerHTML={{ __html: highlighted }}
                  />
                ) : (
                  <pre className="whitespace-pre-wrap px-3 py-2 font-mono text-[11px] leading-relaxed">
                    {preview.content}
                  </pre>
                )}
              </div>
            </div>
          )}
        </div>
      )}
      {dialogItem && (
        <FilePreviewDialog sessionId={sessionId} item={dialogItem} onClose={() => setDialogItem(null)} />
      )}
    </aside>
  )
}
