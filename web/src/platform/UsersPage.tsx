import { useCallback, useEffect, useState } from "react"
import { MoreHorizontal, Search } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { platformOrgsApi } from "@/api/orgs"
import { platformUsersApi } from "@/api/users"
import { ConfirmDialog, CopyButton, EmptyState, PageHeader, TableSkeleton } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { formatDateTime } from "@/lib/format"
import type { OrgSummary, PlatformUser, Role } from "@/lib/types"

export default function UsersPage() {
  const [users, setUsers] = useState<PlatformUser[] | null>(null)
  const [q, setQ] = useState("")
  const [createOpen, setCreateOpen] = useState(false)
  const [passwordInfo, setPasswordInfo] = useState<{ username: string; password: string } | null>(null)
  const [toggleTarget, setToggleTarget] = useState<PlatformUser | null>(null)
  const [toggling, setToggling] = useState(false)
  const [resetTarget, setResetTarget] = useState<PlatformUser | null>(null)
  const [resetting, setResetting] = useState(false)
  const [orgsTarget, setOrgsTarget] = useState<PlatformUser | null>(null)

  const load = useCallback(
    async (keyword: string) => {
      try {
        const res = await platformUsersApi.list({ q: keyword || undefined, limit: 100 })
        setUsers(res.items)
      } catch (e) {
        toast.error(errorMessage(e))
        setUsers([])
      }
    },
    [],
  )

  useEffect(() => {
    load("")
  }, [load])

  async function toggleStatus() {
    if (!toggleTarget) return
    setToggling(true)
    try {
      await platformUsersApi.update(toggleTarget.id, {
        status: toggleTarget.status === "active" ? "disabled" : "active",
      })
      toast.success(toggleTarget.status === "active" ? "已停用" : "已启用")
      setToggleTarget(null)
      load(q)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setToggling(false)
    }
  }

  async function resetPassword() {
    if (!resetTarget) return
    setResetting(true)
    try {
      const res = await platformUsersApi.resetPassword(resetTarget.id)
      setResetTarget(null)
      if (res.password) {
        setPasswordInfo({ username: resetTarget.username, password: res.password })
      }
      load(q)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setResetting(false)
    }
  }

  return (
    <div>
      <PageHeader title="用户" description="平台全部用户">
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          新建用户
        </Button>
      </PageHeader>

      <form
        className="mb-4 flex max-w-sm gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          setUsers(null)
          load(q.trim())
        }}
      >
        <Input placeholder="搜索用户名 / 显示名" value={q} onChange={(e) => setQ(e.target.value)} />
        <Button type="submit" variant="outline" size="icon" aria-label="搜索">
          <Search className="size-4" />
        </Button>
      </form>

      {users === null ? (
        <TableSkeleton />
      ) : users.length === 0 ? (
        <EmptyState title="没有匹配的用户" />
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>用户名</TableHead>
                <TableHead>显示名</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>所属机构</TableHead>
                <TableHead>创建时间</TableHead>
                <TableHead className="w-14">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((u) => (
                <TableRow key={u.id}>
                  <TableCell className="font-medium">{u.username}</TableCell>
                  <TableCell>{u.display_name || "-"}</TableCell>
                  <TableCell>
                    <Badge variant={u.status === "active" ? "default" : "outline"}>
                      {u.status === "active" ? "启用" : "停用"}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <div className="flex max-w-72 flex-wrap gap-1">
                      {(u.orgs ?? []).length === 0 ? (
                        <span className="text-muted-foreground">-</span>
                      ) : (
                        u.orgs.map((o) => (
                          <Badge key={o.org_id} variant="secondary">
                            {o.org_name}
                            {o.role === "admin" ? " · 管理员" : ""}
                          </Badge>
                        ))
                      )}
                    </div>
                  </TableCell>
                  <TableCell className="text-muted-foreground">{formatDateTime(u.created_at)}</TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon" aria-label="操作">
                          <MoreHorizontal className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => setOrgsTarget(u)}>编辑机构归属</DropdownMenuItem>
                        <DropdownMenuItem onClick={() => setResetTarget(u)}>重置密码</DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem onClick={() => setToggleTarget(u)}>
                          {u.status === "active" ? "停用" : "启用"}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <CreateUserDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onCreated={(info) => {
          load(q)
          if (info) setPasswordInfo(info)
        }}
      />

      <PasswordDialog info={passwordInfo} onClose={() => setPasswordInfo(null)} />

      <ConfirmDialog
        open={toggleTarget !== null}
        onOpenChange={(open) => !open && setToggleTarget(null)}
        title={toggleTarget?.status === "active" ? "停用用户" : "启用用户"}
        description={
          toggleTarget?.status === "active"
            ? `停用后「${toggleTarget?.username}」将无法登录，确定继续？`
            : `确定启用「${toggleTarget?.username}」？`
        }
        confirmText={toggleTarget?.status === "active" ? "停用" : "启用"}
        destructive={toggleTarget?.status === "active"}
        loading={toggling}
        onConfirm={toggleStatus}
      />

      <ConfirmDialog
        open={resetTarget !== null}
        onOpenChange={(open) => !open && setResetTarget(null)}
        title="重置密码"
        description={`将为「${resetTarget?.username}」生成新密码，该用户下次登录需修改密码。`}
        confirmText="重置"
        loading={resetting}
        onConfirm={resetPassword}
      />

      {orgsTarget && (
        <UserOrgsDialog
          user={orgsTarget}
          onClose={(changed) => {
            setOrgsTarget(null)
            if (changed) load(q)
          }}
        />
      )}
    </div>
  )
}

function PasswordDialog({
  info,
  onClose,
}: {
  info: { username: string; password: string } | null
  onClose: () => void
}) {
  return (
    <Dialog open={info !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>初始密码</DialogTitle>
          <DialogDescription>此密码仅展示一次，请立即复制并转交用户。首次登录需修改密码。</DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <code className="flex-1 rounded-md border bg-muted px-3 py-2 font-mono text-sm">
            {info?.password}
          </code>
          <CopyButton
            text={info?.password ?? ""}
            onCopied={(ok) => (ok ? toast.success("已复制") : toast.error("复制失败"))}
          />
        </div>
        <DialogFooter>
          <Button onClick={onClose}>我已保存</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function CreateUserDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (info: { username: string; password: string } | null) => void
}) {
  const [username, setUsername] = useState("")
  const [displayName, setDisplayName] = useState("")
  const [password, setPassword] = useState("")
  const [role, setRole] = useState<Role>("member")
  const [orgIds, setOrgIds] = useState<string[]>([])
  const [orgs, setOrgs] = useState<OrgSummary[]>([])
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!open) return
    platformOrgsApi
      .list()
      .then((res) => setOrgs(res.items.filter((o) => !o.is_platform)))
      .catch((e) => toast.error(errorMessage(e)))
  }, [open])

  function reset() {
    setUsername("")
    setDisplayName("")
    setPassword("")
    setRole("member")
    setOrgIds([])
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!username.trim()) return
    setLoading(true)
    try {
      const res = await platformUsersApi.create({
        username: username.trim(),
        display_name: displayName.trim(),
        password: password || undefined,
        org_ids: orgIds,
        role,
      })
      toast.success("用户已创建")
      onOpenChange(false)
      reset()
      onCreated(res.password ? { username: res.user.username, password: res.password } : null)
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建用户</DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="cu-username">用户名</Label>
              <Input id="cu-username" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="cu-display">显示名</Label>
              <Input id="cu-display" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="cu-password">初始密码</Label>
            <Input
              id="cu-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="留空则自动生成随机密码"
            />
          </div>
          <div className="space-y-1.5">
            <Label>初始机构</Label>
            <div className="max-h-36 space-y-1 overflow-y-auto rounded-md border p-2">
              {orgs.length === 0 ? (
                <p className="px-1 py-2 text-sm text-muted-foreground">暂无可选机构</p>
              ) : (
                orgs.map((org) => (
                  <label key={org.id} className="flex cursor-pointer items-center gap-2 rounded px-1 py-1 text-sm hover:bg-accent">
                    <input
                      type="checkbox"
                      className="size-3.5 accent-primary"
                      checked={orgIds.includes(org.id)}
                      onChange={(e) =>
                        setOrgIds((prev) =>
                          e.target.checked ? [...prev, org.id] : prev.filter((id) => id !== org.id),
                        )
                      }
                    />
                    {org.name}
                  </label>
                ))
              )}
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>角色</Label>
            <Select value={role} onValueChange={(v) => setRole(v as Role)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="member">成员</SelectItem>
                <SelectItem value="admin">管理员</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <DialogFooter>
            <Button type="submit" disabled={loading || !username.trim()}>
              {loading ? "创建中…" : "创建"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function UserOrgsDialog({ user, onClose }: { user: PlatformUser; onClose: (changed: boolean) => void }) {
  const [orgs, setOrgs] = useState<OrgSummary[]>([])
  const [memberships, setMemberships] = useState(user.orgs ?? [])
  const [busy, setBusy] = useState<string | null>(null)
  const [changed, setChanged] = useState(false)

  useEffect(() => {
    platformOrgsApi
      .list()
      .then((res) => setOrgs(res.items))
      .catch((e) => toast.error(errorMessage(e)))
  }, [])

  function membershipOf(orgId: string) {
    return memberships.find((m) => m.org_id === orgId)
  }

  async function join(org: OrgSummary, role: Role) {
    setBusy(org.id)
    try {
      await platformUsersApi.addToOrg(user.id, org.id, role)
      setMemberships((prev) => [
        ...prev.filter((m) => m.org_id !== org.id),
        { org_id: org.id, org_name: org.name, role },
      ])
      setChanged(true)
      toast.success(`已加入「${org.name}」`)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setBusy(null)
    }
  }

  async function changeRole(org: OrgSummary, role: Role) {
    setBusy(org.id)
    try {
      await platformUsersApi.updateOrgRole(user.id, org.id, role)
      setMemberships((prev) => prev.map((m) => (m.org_id === org.id ? { ...m, role } : m)))
      setChanged(true)
      toast.success(`已更新「${org.name}」角色`)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setBusy(null)
    }
  }

  async function leave(org: OrgSummary) {
    setBusy(org.id)
    try {
      await platformUsersApi.removeFromOrg(user.id, org.id)
      setMemberships((prev) => prev.filter((m) => m.org_id !== org.id))
      setChanged(true)
      toast.success(`已移出「${org.name}」`)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setBusy(null)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose(changed)}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>机构归属 · {user.username}</DialogTitle>
          <DialogDescription>将用户加入机构、调整角色或移出机构</DialogDescription>
        </DialogHeader>
        <div className="max-h-96 space-y-1 overflow-y-auto">
          {orgs.map((org) => {
            const m = membershipOf(org.id)
            return (
              <div key={org.id} className="flex items-center gap-2 rounded-md border px-3 py-2">
                <div className="min-w-0 flex-1">
                  <span className="text-sm font-medium">{org.name}</span>
                  {org.is_platform && <Badge variant="secondary" className="ml-2">平台</Badge>}
                </div>
                {m ? (
                  <>
                    <Select
                      value={m.role}
                      onValueChange={(v) => changeRole(org, v as Role)}
                      disabled={busy === org.id}
                    >
                      <SelectTrigger className="h-8 w-28">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="member">成员</SelectItem>
                        <SelectItem value="admin">管理员</SelectItem>
                      </SelectContent>
                    </Select>
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={busy === org.id}
                      onClick={() => leave(org)}
                    >
                      移出
                    </Button>
                  </>
                ) : (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={busy === org.id}
                    onClick={() => join(org, "member")}
                  >
                    加入
                  </Button>
                )}
              </div>
            )
          })}
        </div>
        <DialogFooter>
          <Button onClick={() => onClose(changed)}>完成</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
