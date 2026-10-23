import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { platformOrgsApi } from "@/api/orgs"
import { ConfirmDialog, EmptyState, PageHeader, TableSkeleton } from "@/components/common"
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
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { formatDateTime } from "@/lib/format"
import type { OrgDetail, OrgSummary } from "@/lib/types"

export default function OrgsPage() {
  const [orgs, setOrgs] = useState<OrgSummary[] | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [detail, setDetail] = useState<OrgDetail | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)
  const [toggleTarget, setToggleTarget] = useState<OrgSummary | null>(null)
  const [toggling, setToggling] = useState(false)

  const load = useCallback(async () => {
    try {
      const res = await platformOrgsApi.list()
      setOrgs(res.items)
    } catch (e) {
      toast.error(errorMessage(e))
      setOrgs([])
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  async function openDetail(org: OrgSummary) {
    try {
      setDetail(await platformOrgsApi.detail(org.id))
      setDetailOpen(true)
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  async function toggleStatus() {
    if (!toggleTarget) return
    setToggling(true)
    try {
      await platformOrgsApi.update(toggleTarget.id, {
        status: toggleTarget.status === "active" ? "disabled" : "active",
      })
      toast.success(toggleTarget.status === "active" ? "已停用" : "已启用")
      setToggleTarget(null)
      load()
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setToggling(false)
    }
  }

  return (
    <div>
      <PageHeader title="组织" description="系统内全部组织">
        <Button size="sm" onClick={() => setCreateOpen(true)}>
          新建组织
        </Button>
      </PageHeader>

      {orgs === null ? (
        <TableSkeleton />
      ) : orgs.length === 0 ? (
        <EmptyState title="暂无组织" />
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>名称</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="text-right">成员数</TableHead>
                <TableHead className="text-right">智能体数</TableHead>
                <TableHead>创建时间</TableHead>
                <TableHead className="w-40">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {orgs.map((org) => (
                <TableRow key={org.id}>
                  <TableCell className="font-medium">
                    <span className="mr-2">{org.name}</span>
                    {org.is_platform && <Badge variant="secondary">系统</Badge>}
                  </TableCell>
                  <TableCell>
                    <Badge variant={org.status === "active" ? "default" : "outline"}>
                      {org.status === "active" ? "启用" : "停用"}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-right">{org.member_count}</TableCell>
                  <TableCell className="text-right">{org.agent_count}</TableCell>
                  <TableCell className="text-muted-foreground">{formatDateTime(org.created_at)}</TableCell>
                  <TableCell>
                    <div className="flex gap-1">
                      <Button variant="ghost" size="sm" onClick={() => openDetail(org)}>
                        详情
                      </Button>
                      {!org.is_platform && (
                        <Button variant="ghost" size="sm" onClick={() => setToggleTarget(org)}>
                          {org.status === "active" ? "停用" : "启用"}
                        </Button>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <CreateOrgDialog open={createOpen} onOpenChange={setCreateOpen} onCreated={load} />

      <Dialog open={detailOpen} onOpenChange={setDetailOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{detail?.name}</DialogTitle>
            <DialogDescription>
              {detail ? `成员 ${detail.member_count} · 智能体 ${detail.agent_count} · 创建于 ${formatDateTime(detail.created_at)}` : ""}
            </DialogDescription>
          </DialogHeader>
          {detail && (
            <div className="max-h-80 overflow-y-auto rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>用户名</TableHead>
                    <TableHead>显示名</TableHead>
                    <TableHead>角色</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {detail.members.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={3} className="text-center text-muted-foreground">
                        暂无成员
                      </TableCell>
                    </TableRow>
                  ) : (
                    detail.members.map((m) => (
                      <TableRow key={m.user_id}>
                        <TableCell>{m.username}</TableCell>
                        <TableCell>{m.display_name || "-"}</TableCell>
                        <TableCell>
                          <Badge variant={m.role === "admin" ? "default" : "secondary"}>
                            {m.role === "admin" ? "管理员" : "成员"}
                          </Badge>
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </div>
          )}
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={toggleTarget !== null}
        onOpenChange={(open) => !open && setToggleTarget(null)}
        title={toggleTarget?.status === "active" ? "停用组织" : "启用组织"}
        description={
          toggleTarget?.status === "active"
            ? `停用后「${toggleTarget?.name}」的成员将无法使用该组织，确定继续？`
            : `确定启用「${toggleTarget?.name}」？`
        }
        confirmText={toggleTarget?.status === "active" ? "停用" : "启用"}
        destructive={toggleTarget?.status === "active"}
        loading={toggling}
        onConfirm={toggleStatus}
      />
    </div>
  )
}

function CreateOrgDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: () => void
}) {
  const [name, setName] = useState("")
  const [loading, setLoading] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    setLoading(true)
    try {
      await platformOrgsApi.create(name.trim())
      toast.success("组织已创建")
      setName("")
      onOpenChange(false)
      onCreated()
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
          <DialogTitle>新建组织</DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="org-name">组织名称</Label>
            <Input id="org-name" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
          </div>
          <DialogFooter>
            <Button type="submit" disabled={loading || !name.trim()}>
              {loading ? "创建中…" : "创建"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
