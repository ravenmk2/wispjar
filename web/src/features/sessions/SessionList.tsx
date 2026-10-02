import { Archive, GitFork, MoreHorizontal, Pencil, Search, Trash2 } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { NavLink, useNavigate, useParams } from "react-router"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import { ConfirmDialog } from "@/components/common"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { formatRelativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { SessionStatus } from "@/lib/types"
import { useCurrentOrg } from "@/stores/identity"
import { useSessionListStore } from "@/stores/sessionList"

interface ListItem {
  id: string
  title: string
  running: boolean
  lastActiveAt: string
}

export function SessionList() {
  const currentOrg = useCurrentOrg()
  const navigate = useNavigate()
  const { sessionId: activeId } = useParams()
  const sessions = useSessionListStore((s) => s.sessions)
  const order = useSessionListStore((s) => s.order)
  const removeFromStore = useSessionListStore((s) => s.remove)

  const [query, setQuery] = useState("")
  const [searchResults, setSearchResults] = useState<ListItem[] | null>(null)
  const [renameTarget, setRenameTarget] = useState<ListItem | null>(null)
  const [renameValue, setRenameValue] = useState("")
  const [deleteTarget, setDeleteTarget] = useState<ListItem | null>(null)
  const [acting, setActing] = useState(false)

  const projected = useMemo<ListItem[]>(
    () =>
      order
        .map((id) => sessions[id])
        .filter((s) => s && s.status !== ("deleted" satisfies SessionStatus))
        .map((s) => ({ id: s.sessionId, title: s.title, running: s.running, lastActiveAt: s.lastActiveAt })),
    [sessions, order],
  )

  useEffect(() => {
    const q = query.trim()
    if (!q || !currentOrg) {
      setSearchResults(null)
      return
    }
    let cancelled = false
    const timer = setTimeout(() => {
      sessionsApi
        .list(currentOrg.id, { q })
        .then((res) => {
          if (cancelled) return
          setSearchResults(
            res.items.map((s) => ({ id: s.id, title: s.title, running: s.running, lastActiveAt: s.last_active_at })),
          )
        })
        .catch(() => {
          if (!cancelled) setSearchResults([])
        })
    }, 300)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [query, currentOrg])

  const items = searchResults ?? projected

  async function rename() {
    if (!renameTarget || !renameValue.trim() || acting) return
    setActing(true)
    try {
      await sessionsApi.update(renameTarget.id, { title: renameValue.trim() })
      setRenameTarget(null)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setActing(false)
    }
  }

  async function archive(item: ListItem) {
    try {
      await sessionsApi.update(item.id, { status: "archived" })
      removeFromStore(item.id)
      if (item.id === activeId) navigate("/")
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  async function forkFromEnd(item: ListItem) {
    try {
      const { last_seq } = await sessionsApi.events(item.id, Number.MAX_SAFE_INTEGER)
      const res = await sessionsApi.fork(item.id, last_seq)
      toast.success("已创建分叉会话")
      navigate(`/c/${res.id}`)
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  async function remove() {
    if (!deleteTarget || acting) return
    setActing(true)
    try {
      await sessionsApi.remove(deleteTarget.id)
      removeFromStore(deleteTarget.id)
      setDeleteTarget(null)
      if (deleteTarget.id === activeId) navigate("/")
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setActing(false)
    }
  }

  return (
    <div className="space-y-1.5">
      <div className="relative px-1">
        <Search className="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索会话"
          className="h-7 pl-7 text-xs"
        />
      </div>
      {items.length === 0 ? (
        <div className="px-2 py-1 text-xs text-muted-foreground">
          {searchResults ? "没有匹配的会话" : "暂无会话"}
        </div>
      ) : (
        <div className="space-y-0.5 px-1">
          {items.map((item) => (
            <div key={item.id} className="group relative">
              <NavLink
                to={`/c/${item.id}`}
                className={cn(
                  "flex items-center gap-2 rounded-md px-2 py-1.5 pr-7 text-sm text-muted-foreground hover:bg-accent hover:text-foreground",
                  item.id === activeId && "bg-accent font-medium text-foreground",
                )}
              >
                <span
                  className={cn(
                    "size-1.5 shrink-0 rounded-full",
                    item.running ? "animate-pulse bg-green-500" : "bg-transparent",
                  )}
                />
                <span className="min-w-0 flex-1 truncate">{item.title || "新会话"}</span>
                <span className="shrink-0 text-[11px] text-muted-foreground group-hover:opacity-0">
                  {formatRelativeTime(item.lastActiveAt)}
                </span>
              </NavLink>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="absolute right-0.5 top-1/2 size-6 -translate-y-1/2 opacity-0 group-hover:opacity-100"
                  >
                    <MoreHorizontal className="size-3.5" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start" className="w-36">
                  <DropdownMenuItem
                    onClick={() => {
                      setRenameTarget(item)
                      setRenameValue(item.title)
                    }}
                  >
                    <Pencil className="size-3.5" />
                    重命名
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => void archive(item)}>
                    <Archive className="size-3.5" />
                    归档
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => void forkFromEnd(item)}>
                    <GitFork className="size-3.5" />
                    从结尾分叉
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    className="text-destructive focus:text-destructive"
                    onClick={() => setDeleteTarget(item)}
                  >
                    <Trash2 className="size-3.5" />
                    删除
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          ))}
        </div>
      )}

      <Dialog open={renameTarget !== null} onOpenChange={(open) => !open && setRenameTarget(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>重命名会话</DialogTitle>
          </DialogHeader>
          <Input
            value={renameValue}
            onChange={(e) => setRenameValue(e.target.value)}
            placeholder="会话标题"
            onKeyDown={(e) => {
              if (e.key === "Enter") void rename()
            }}
          />
          <DialogFooter>
            <Button onClick={() => void rename()} disabled={!renameValue.trim() || acting}>
              {acting ? "保存中…" : "保存"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="删除会话"
        description={`确定删除「${deleteTarget?.title || "新会话"}」吗？删除后不可恢复。`}
        confirmText="删除"
        destructive
        loading={acting}
        onConfirm={() => void remove()}
      />
    </div>
  )
}
