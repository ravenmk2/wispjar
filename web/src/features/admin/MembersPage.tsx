import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { membersApi } from "@/api/members"
import { ConfirmDialog, EmptyState, PageHeader, TableSkeleton } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
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
import type { OrgMember, Role } from "@/lib/types"
import { useIdentityStore } from "@/stores/identity"

export default function MembersPage() {
  const currentOrgId = useIdentityStore((s) => s.currentOrgId)
  const currentUserId = useIdentityStore((s) => s.user?.id)
  const [members, setMembers] = useState<OrgMember[] | null>(null)
  const [addOpen, setAddOpen] = useState(false)
  const [removeTarget, setRemoveTarget] = useState<OrgMember | null>(null)
  const [removing, setRemoving] = useState(false)

  const load = useCallback(async () => {
    if (!currentOrgId) return
    try {
      const res = await membersApi.list(currentOrgId)
      setMembers(res.items)
    } catch (e) {
      toast.error(errorMessage(e))
      setMembers([])
    }
  }, [currentOrgId])

  useEffect(() => {
    setMembers(null)
    load()
  }, [load])

  async function changeRole(m: OrgMember, role: Role) {
    if (!currentOrgId || role === m.role) return
    try {
      await membersApi.updateRole(currentOrgId, m.user_id, role)
      toast.success("角色已更新")
      load()
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  async function remove() {
    if (!currentOrgId || !removeTarget) return
    setRemoving(true)
    try {
      await membersApi.remove(currentOrgId, removeTarget.user_id)
      toast.success("已移出")
      setRemoveTarget(null)
      load()
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setRemoving(false)
    }
  }

  return (
    <div>
      <PageHeader title="成员" description="本机构成员与角色">
        <Button size="sm" onClick={() => setAddOpen(true)}>
          添加成员
        </Button>
      </PageHeader>

      {members === null ? (
        <TableSkeleton />
      ) : members.length === 0 ? (
        <EmptyState title="暂无成员" />
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>用户名</TableHead>
                <TableHead>显示名</TableHead>
                <TableHead>角色</TableHead>
                <TableHead>加入时间</TableHead>
                <TableHead className="w-20">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {members.map((m) => (
                <TableRow key={m.user_id}>
                  <TableCell className="font-medium">
                    {m.username}
                    {m.user_id === currentUserId && (
                      <Badge variant="outline" className="ml-2">
                        我
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell>{m.display_name || "-"}</TableCell>
                  <TableCell>
                    <Select value={m.role} onValueChange={(v) => changeRole(m, v as Role)}>
                      <SelectTrigger className="h-8 w-28">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="admin">管理员</SelectItem>
                        <SelectItem value="member">成员</SelectItem>
                      </SelectContent>
                    </Select>
                  </TableCell>
                  <TableCell className="text-muted-foreground">{formatDateTime(m.created_at)}</TableCell>
                  <TableCell>
                    <Button variant="ghost" size="sm" onClick={() => setRemoveTarget(m)}>
                      移出
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <AddMemberDialog orgId={currentOrgId} open={addOpen} onOpenChange={setAddOpen} onAdded={load} />

      <ConfirmDialog
        open={removeTarget !== null}
        onOpenChange={(open) => !open && setRemoveTarget(null)}
        title="移出成员"
        description={`确定将「${removeTarget?.username}」移出本机构？`}
        confirmText="移出"
        destructive
        loading={removing}
        onConfirm={remove}
      />
    </div>
  )
}

function AddMemberDialog({
  orgId,
  open,
  onOpenChange,
  onAdded,
}: {
  orgId: string | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onAdded: () => void
}) {
  const [username, setUsername] = useState("")
  const [role, setRole] = useState<Role>("member")
  const [loading, setLoading] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!orgId || !username.trim()) return
    setLoading(true)
    try {
      await membersApi.add(orgId, username.trim(), role)
      toast.success("成员已添加")
      setUsername("")
      setRole("member")
      onOpenChange(false)
      onAdded()
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
          <DialogTitle>添加成员</DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="am-username">用户名</Label>
            <Input
              id="am-username"
              placeholder="平台已有用户的用户名"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoFocus
            />
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
              {loading ? "添加中…" : "添加"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
