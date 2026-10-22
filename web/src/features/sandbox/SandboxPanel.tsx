import {
  ChevronDown,
  ChevronRight,
  FileCode2,
  FileText,
  Folder,
  Image as ImageIcon,
  Loader2,
  RotateCw,
  X,
} from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { Button } from "@/components/ui/button"
import { CloudComputerIcon } from "@/components/CloudComputerIcon"
import { formatBytes } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { SandboxFileItem, SandboxStatus } from "@/lib/types"
import { useSessionStore } from "@/stores/session"
import { FilePreview } from "./FilePreview"
import { FilePanel } from "@/features/computers/FilePanel"
import { previewKind } from "./previewUtils"
import { SandboxStatusIndicator } from "./SandboxStatusIndicator"

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

function collectExpanded(nodes: TreeNodeState[] | null, out = new Set<string>()): Set<string> {
  if (!nodes) return out
  for (const n of nodes) {
    if (n.item.type === "dir" && n.expanded) {
      out.add(n.item.path)
      if (n.children) collectExpanded(n.children, out)
    }
  }
  return out
}

export function SandboxPanel({ sessionId, onClose }: { sessionId: string; onClose: () => void }) {
  const header = useSessionStore((s) => s.header)
  const events = useSessionStore((s) => s.events)
  const [root, setRoot] = useState<TreeNodeState[] | null>(null)
  const [treeError, setTreeError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [tab, setTab] = useState<"files" | "preview">("files")
  const [previewItem, setPreviewItem] = useState<SandboxFileItem | null>(null)
  const [previewReload, setPreviewReload] = useState(0)
  const status: SandboxStatus = header?.sandboxStatus ?? "none"
  const running = header?.running ?? false
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

  // 初始加载：沙箱进入 running（或切换聊天）时重置并拉取根目录
  useEffect(() => {
    setRoot(null)
    setTreeError(null)
    setPreviewItem(null)
    setTab("files")
    if (status !== "running") return
    setRefreshing(true)
    loadDir()
      .then(setRoot)
      .catch((e) => setTreeError(errorMessage(e)))
      .finally(() => setRefreshing(false))
  }, [status, loadDir, sessionId])

  // 刷新：保留展开状态（按目录路径集合逐层重拉，已消失的目录丢弃），失败保留旧树
  const refreshTree = useCallback(async (): Promise<boolean> => {
    if (status !== "running") return false
    setRefreshing(true)
    try {
      const expanded = collectExpanded(root)
      const restore = async (nodes: TreeNodeState[]): Promise<TreeNodeState[]> =>
        Promise.all(
          nodes.map(async (n) => {
            if (n.item.type !== "dir" || !expanded.has(n.item.path)) return n
            try {
              const children = await loadDir(n.item.path)
              return { ...n, children: await restore(children), expanded: true }
            } catch {
              return { ...n, children: null, expanded: false }
            }
          }),
        )
      const fresh = await restore(await loadDir())
      setRoot(fresh)
      setTreeError(null)
      return true
    } catch (e) {
      toast.error(errorMessage(e))
      return false
    } finally {
      setRefreshing(false)
    }
  }, [status, loadDir, root])

  // 自动刷新：agent 工作完成（running true→false）时刷新文件树，并静默重载当前预览文件
  const prevRunningRef = useRef(running)
  useEffect(() => {
    const prev = prevRunningRef.current
    prevRunningRef.current = running
    if (!prev || running || status !== "running") return
    void refreshTree().then((ok) => {
      if (ok) setPreviewReload((n) => n + 1)
    })
  }, [running, status, refreshTree])

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

  function openFile(item: SandboxFileItem) {
    setPreviewItem(item)
    setTab("preview")
  }

  function renderNodes(nodes: TreeNodeState[], depth: number) {
    return nodes.map((node) => (
      <div key={node.item.path}>
        <button
          type="button"
          onClick={() => (node.item.type === "dir" ? void toggleDir(node) : openFile(node.item))}
          className={cn(
            "flex w-full items-center gap-1.5 rounded-sm px-1.5 py-1 text-left text-xs hover:bg-accent/60",
            previewItem?.path === node.item.path && "bg-accent",
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
    <FilePanel>
      <div className="flex h-11 shrink-0 items-center gap-2 border-b px-3">
        <CloudComputerIcon className="size-4" />
        <span className="text-sm font-medium">云电脑</span>
        <SandboxStatusIndicator status={status} />
        <Button variant="ghost" size="icon" className="ml-auto size-7" onClick={onClose}>
          <X className="size-4" />
        </Button>
      </div>
      {image && (
        <div className="shrink-0 border-b px-3 py-1.5 font-mono text-[11px] text-muted-foreground">{image}</div>
      )}

      {status === "none" && (
        <div className="flex flex-1 items-center justify-center p-4 text-center text-xs text-muted-foreground">
          尚未创建云电脑，聊天使用工具时将自动创建
        </div>
      )}
      {status === "creating" && (
        <div className="flex flex-1 items-center justify-center gap-2 p-4 text-xs text-muted-foreground">
          <Loader2 className="size-3.5 animate-spin" />
          云电脑创建中…
        </div>
      )}
      {status === "recycled" && (
        <div className="flex flex-1 items-center justify-center p-4 text-center text-xs text-muted-foreground">
          云电脑已回收，文件已清空
        </div>
      )}
      {status === "running" && (
        <>
          <div className="flex shrink-0 items-center gap-3 border-b px-3">
            <button
              type="button"
              onClick={() => setTab("files")}
              className={cn(
                "-mb-px border-b-2 border-transparent py-1.5 text-[13px] font-medium text-muted-foreground hover:text-foreground",
                tab === "files" && "border-primary text-link",
              )}
            >
              文件
            </button>
            <button
              type="button"
              onClick={() => setTab("preview")}
              className={cn(
                "-mb-px border-b-2 border-transparent py-1.5 text-[13px] font-medium text-muted-foreground hover:text-foreground",
                tab === "preview" && "border-primary text-link",
              )}
            >
              预览
            </button>
          </div>

          <div className={cn("flex min-h-0 flex-1 flex-col", tab !== "files" && "hidden")}>
            <div className="flex h-8 shrink-0 items-center border-b px-2">
              <span className="font-mono text-[11px] text-muted-foreground">/workspace</span>
              <Button
                variant="ghost"
                size="icon"
                className="ml-auto size-6"
                title="刷新文件树"
                disabled={refreshing}
                onClick={() => void refreshTree()}
              >
                <RotateCw className={cn("size-3.5", refreshing && "animate-spin")} />
              </Button>
            </div>
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
          </div>

          <div className={cn("flex min-h-0 flex-1 flex-col", tab !== "preview" && "hidden")}>
            {previewItem ? (
              <FilePreview sessionId={sessionId} item={previewItem} reloadToken={previewReload} />
            ) : (
              <div className="flex flex-1 items-center justify-center p-4 text-center text-xs text-muted-foreground">
                在文件 tab 选择文件预览
              </div>
            )}
          </div>
        </>
      )}
    </FilePanel>
  )
}
