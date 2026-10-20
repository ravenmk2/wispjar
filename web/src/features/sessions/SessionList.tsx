import { Archive, CopyPlus, MoreHorizontal, Pencil, Search, Trash2 } from "lucide-react"
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
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"
import { Input } from "@/components/ui/input"
import { formatCompactTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { SessionStatus } from "@/lib/types"
import { useCurrentOrg } from "@/stores/identity"
import { useSessionStore } from "@/stores/session"
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
      toast.success("已创建聊天副本")
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
          placeholder="搜索聊天"
          className="h-9 pl-8 text-sm"
        />
      </div>
      {items.length === 0 ? (
        <div className="px-2 py-1 text-sm text-muted-foreground">
          {searchResults ? "没有匹配的聊天" : "暂无聊天"}
        </div>
      ) : (
        <div className="space-y-0.5 px-1">
          {items.map((item) => (
            <SessionRow
              key={item.id}
              item={item}
              active={item.id === activeId}
              onRename={() => {
                setRenameTarget(item)
                setRenameValue(item.title)
              }}
              onFork={() => void forkFromEnd(item)}
              onArchive={() => void archive(item)}
              onDelete={() => setDeleteTarget(item)}
            />
          ))}
        </div>
      )}

      <Dialog open={renameTarget !== null} onOpenChange={(open) => !open && setRenameTarget(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>重命名聊天</DialogTitle>
          </DialogHeader>
          <Input
            value={renameValue}
            onChange={(e) => setRenameValue(e.target.value)}
            placeholder="聊天标题"
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
        title="删除聊天"
        description={`确定删除「${deleteTarget?.title || "新聊天"}」吗？删除后不可恢复。`}
        confirmText="删除"
        destructive
        loading={acting}
        onConfirm={() => void remove()}
      />
    </div>
  )
}

function SessionRow({
  item,
  active,
  onRename,
  onFork,
  onArchive,
  onDelete,
}: {
  item: ListItem
  active: boolean
  onRename: () => void
  onFork: () => void
  onArchive: () => void
  onDelete: () => void
}) {
  const [menuOpen, setMenuOpen] = useState(false)
  // 待审批/待回答只能由 follow 中的聊天事件推导（control 投影不含 pending 字段），故仅活动聊天可见
  const pending = useSessionStore((s) => {
    if (!active) return null
    if (Object.values(s.pendingApprovals).some((a) => !a.parentCallId)) return "approval" as const
    if (Object.keys(s.pendingQuestions).length > 0) return "question" as const
    return null
  })

  return (
    <div className="group relative">
      <HoverCard openDelay={500} closeDelay={100} open={menuOpen ? false : undefined}>
        <HoverCardTrigger asChild>
          <NavLink
            to={`/c/${item.id}`}
            className={cn(
              "flex h-9 items-center gap-2 rounded-xl px-2 pr-7 text-sm leading-5 text-foreground hover:bg-accent",
              active && "bg-selected",
            )}
          >
            <span
              className={cn(
                "size-1.5 shrink-0 rounded-full",
                pending
                  ? "animate-pulse bg-amber-500"
                  : item.running
                    ? "animate-pulse bg-primary"
                    : "bg-transparent",
              )}
            />
            <span className="min-w-0 flex-1 truncate">{item.title || "新聊天"}</span>
            <span className="shrink-0 text-meta text-fg-caption group-hover:opacity-0">
              {formatCompactTime(item.lastActiveAt)}
            </span>
          </NavLink>
        </HoverCardTrigger>
        <HoverCardContent side="right" sideOffset={8}>
          <div className="text-[13px] font-medium leading-5">{item.title || "新聊天"}</div>
          <div className="mt-1 text-fg-tertiary">{formatCompactTime(item.lastActiveAt)}</div>
          <div className="mt-1.5 flex items-center gap-1.5 text-fg-secondary">
            <span
              className={cn(
                "size-1.5 rounded-full",
                pending ? "bg-amber-500" : item.running ? "bg-primary" : "bg-fg-tertiary",
              )}
            />
            {pending === "approval" ? "待审批" : pending === "question" ? "待回答" : item.running ? "运行中" : "空闲"}
          </div>
        </HoverCardContent>
      </HoverCard>
      <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon"
            className={cn(
              "absolute right-0.5 top-1/2 size-6 -translate-y-1/2 opacity-0 group-hover:opacity-100",
              menuOpen && "opacity-100",
            )}
          >
            <MoreHorizontal className="size-3.5" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-36">
          <DropdownMenuItem onClick={onRename}>
            <Pencil className="size-3.5" />
            重命名
          </DropdownMenuItem>
          <DropdownMenuItem onClick={onFork}>
            <CopyPlus className="size-3.5" />
            创建聊天副本
          </DropdownMenuItem>
          <DropdownMenuItem onClick={onArchive}>
            <Archive className="size-3.5" />
            归档聊天
          </DropdownMenuItem>
          <DropdownMenuItem className="text-destructive focus:text-destructive" onClick={onDelete}>
            <Trash2 className="size-3.5" />
            删除聊天
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
