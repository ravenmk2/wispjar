import { Archive, ChevronDown, ChevronUp, CopyPlus, MoreHorizontal, Pencil, Search, Trash2 } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
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
import { useCurrentOrg } from "@/stores/identity"
import { useSessionStore } from "@/stores/session"
import { useSessionListStore } from "@/stores/sessionList"
import { SESSION_PAGE_SIZE, useSessionSearch } from "./useSessionSearch"

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
  const searchQuery = query.trim()
  const search = useSessionSearch(currentOrg?.id, searchQuery)
  const [expanded, setExpanded] = useState(false)
  const [visibleCount, setVisibleCount] = useState(SESSION_PAGE_SIZE)
  const [hiddenIds, setHiddenIds] = useState<Set<string>>(() => new Set())
  const viewportRef = useRef<HTMLDivElement>(null)
  const sentinelRef = useRef<HTMLDivElement>(null)
  const [renameTarget, setRenameTarget] = useState<ListItem | null>(null)
  const [renameValue, setRenameValue] = useState("")
  const [deleteTarget, setDeleteTarget] = useState<ListItem | null>(null)
  const [acting, setActing] = useState(false)

  const projected = useMemo<ListItem[]>(
    () =>
      order
        .map((id) => sessions[id])
        .filter((s) => s && s.status === "active")
        .map((s) => ({ id: s.sessionId, title: s.title, running: s.running, lastActiveAt: s.lastActiveAt })),
    [sessions, order],
  )

  useEffect(() => {
    setExpanded(false)
    setVisibleCount(SESSION_PAGE_SIZE)
    viewportRef.current?.scrollTo({ top: 0 })
  }, [searchQuery])

  const items = (searchQuery ? search.items.flatMap((item) => {
    const live = sessions[item.id]
    if (live && live.status !== "active") return []
    return [{
      id: item.id,
      title: live?.title ?? item.title,
      running: live?.running ?? item.running,
      lastActiveAt: live?.lastActiveAt ?? item.last_active_at,
    }]
  }) : projected).filter((item) => !hiddenIds.has(item.id))
  const hasMore = items.length > visibleCount || (!!searchQuery && !!search.nextCursor)
  const loading = !!searchQuery && search.loading
  const loadError = searchQuery ? search.error : null

  function revealMore() {
    if (loading) return
    setExpanded(true)
    if (items.length > visibleCount) {
      setVisibleCount((count) => count + SESSION_PAGE_SIZE)
    } else if (searchQuery && (search.nextCursor || loadError)) {
      setVisibleCount((count) => count + SESSION_PAGE_SIZE)
      void search.loadMore()
    }
  }

  useEffect(() => {
    if (!expanded || !hasMore || loading || loadError) return
    const root = viewportRef.current
    const sentinel = sentinelRef.current
    if (!root || !sentinel) return
    const observer = new IntersectionObserver(([entry]) => {
      if (!entry.isIntersecting) return
      if (items.length > visibleCount) {
        setVisibleCount((count) => count + SESSION_PAGE_SIZE)
      } else if (search.nextCursor) {
        setVisibleCount((count) => count + SESSION_PAGE_SIZE)
        void search.loadMore()
      }
    }, { root, rootMargin: "0px 0px 64px 0px" })
    observer.observe(sentinel)
    return () => observer.disconnect()
  }, [expanded, hasMore, loading, loadError, items.length, visibleCount, search.nextCursor, search.loadMore])

  function hideItem(id: string) {
    removeFromStore(id)
    setHiddenIds((ids) => new Set([...ids, id]))
  }

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
      hideItem(item.id)
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
      hideItem(deleteTarget.id)
      setDeleteTarget(null)
      if (deleteTarget.id === activeId) navigate("/")
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setActing(false)
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-1.5">
      <div className="relative shrink-0 px-1">
        <Search className="absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索聊天"
          className="h-9 pl-8 text-sm"
        />
      </div>
      <div ref={viewportRef} role="region" aria-label="聊天列表" tabIndex={0} className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        {items.length === 0 && !loading && !loadError ? (
          <div className="px-2 py-1 text-sm text-muted-foreground">
            {searchQuery ? "没有匹配的聊天" : "暂无聊天"}
          </div>
        ) : (
          <div className="space-y-0.5 px-1">
            {items.slice(0, visibleCount).map((item) => (
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
        <div ref={sentinelRef} className="h-px" />
        {loading ? (
          <div role="status" className="px-3 py-2 text-xs text-fg-tertiary">加载中…</div>
        ) : loadError ? (
          <div className="px-1">
            <p role="alert" className="px-2 py-1 text-xs text-fg-tertiary">加载失败：{loadError}</p>
            <Button variant="ghost" className="h-8 w-full justify-start px-2 text-xs text-fg-tertiary" onClick={() => void search.loadMore()}>重试</Button>
          </div>
        ) : hasMore ? (
          <Button variant="ghost" className="h-8 w-full justify-start gap-2 px-3 text-xs text-fg-tertiary hover:text-foreground"
            aria-label={expanded ? "加载更多聊天" : "展开更多聊天"} aria-expanded={expanded} onClick={revealMore}>
            <ChevronDown className="size-3.5" />{expanded ? "加载更多" : "展开更多"}
          </Button>
        ) : expanded && items.length > 0 ? (
          <div role="status" className="px-3 py-2 text-xs text-fg-tertiary">已显示全部聊天</div>
        ) : null}
      </div>
      {expanded && (
        <Button variant="ghost" className="h-8 w-full shrink-0 justify-start gap-2 px-3 text-xs text-fg-tertiary hover:text-foreground"
          aria-label="收起聊天" aria-expanded={expanded} onClick={() => {
            setExpanded(false)
            setVisibleCount(SESSION_PAGE_SIZE)
            viewportRef.current?.scrollTo({ top: 0 })
          }}><ChevronUp className="size-3.5" />收起</Button>
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
