import { useCallback, useEffect, useState } from "react"
import { ScrollText } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { platformOrgsApi } from "@/api/orgs"
import { platformAuditApi } from "@/api/reports"
import { EmptyState, PageHeader, TableSkeleton } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
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
import { formatDateTime, safeParseJson } from "@/lib/format"
import type { AuditItem, OrgSummary } from "@/lib/types"

function detailObject(item: AuditItem): Record<string, unknown> | null {
  const d = item.detail
  if (!d) return null
  if (typeof d === "string") return safeParseJson(d) ?? { raw: d }
  return d
}

function DetailValue({ value }: { value: unknown }) {
  if (value === null || value === undefined) return <span className="text-muted-foreground">-</span>
  if (typeof value === "object") {
    return <code className="font-mono text-xs break-all">{JSON.stringify(value)}</code>
  }
  return <span className="break-all">{String(value)}</span>
}

function DetailDialog({ item, orgName, onClose }: { item: AuditItem | null; orgName: string; onClose: () => void }) {
  const detail = item ? detailObject(item) : null
  return (
    <Dialog open={item !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>审计详情</DialogTitle>
        </DialogHeader>
        {item && (
          <div className="max-h-[60vh] space-y-4 overflow-y-auto">
            <div className="grid grid-cols-[5rem_1fr] gap-y-1.5 text-sm">
              <span className="text-muted-foreground">时间</span>
              <span>{formatDateTime(item.created_at)}</span>
              <span className="text-muted-foreground">操作者</span>
              <span>{item.actor_name || item.actor_id}</span>
              <span className="text-muted-foreground">动作</span>
              <span>
                <Badge variant="secondary">{item.action}</Badge>
              </span>
              <span className="text-muted-foreground">目标</span>
              <span className="break-all">{item.target || "-"}</span>
              <span className="text-muted-foreground">组织</span>
              <span>{orgName || "-"}</span>
            </div>
            {detail && (
              <div className="rounded-md border">
                <div className="border-b px-3 py-1.5 text-xs font-medium text-muted-foreground">detail</div>
                <dl className="divide-y text-sm">
                  {Object.entries(detail).map(([k, v]) => (
                    <div key={k} className="grid grid-cols-[8rem_1fr] gap-2 px-3 py-1.5">
                      <dt className="font-mono text-xs text-muted-foreground">{k}</dt>
                      <dd className="min-w-0">
                        <DetailValue value={v} />
                      </dd>
                    </div>
                  ))}
                </dl>
              </div>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

export default function AuditPage() {
  const [items, setItems] = useState<AuditItem[] | null>(null)
  const [nextCursor, setNextCursor] = useState<string | undefined>()
  const [orgId, setOrgId] = useState("all")
  const [orgs, setOrgs] = useState<OrgSummary[]>([])
  const [loadingMore, setLoadingMore] = useState(false)
  const [detailTarget, setDetailTarget] = useState<AuditItem | null>(null)

  useEffect(() => {
    platformOrgsApi
      .list()
      .then((res) => setOrgs(res.items))
      .catch((e) => toast.error(errorMessage(e)))
  }, [])

  const orgNameOf = useCallback(
    (item: AuditItem) => item.org_name ?? orgs.find((o) => o.id === item.org_id)?.name ?? "",
    [orgs],
  )

  const load = useCallback(async (org: string) => {
    try {
      const res = await platformAuditApi.list({ org_id: org === "all" ? undefined : org, limit: 50 })
      setItems(res.items)
      setNextCursor(res.next_cursor)
    } catch (e) {
      toast.error(errorMessage(e))
      setItems([])
    }
  }, [])

  useEffect(() => {
    setItems(null)
    setNextCursor(undefined)
    load(orgId)
  }, [load, orgId])

  async function loadMore() {
    if (!nextCursor || loadingMore) return
    setLoadingMore(true)
    try {
      const res = await platformAuditApi.list({
        org_id: orgId === "all" ? undefined : orgId,
        cursor: nextCursor,
        limit: 50,
      })
      setItems((prev) => [...(prev ?? []), ...res.items])
      setNextCursor(res.next_cursor)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setLoadingMore(false)
    }
  }

  return (
    <div>
      <PageHeader title="审计" description="系统操作审计日志" />

      <div className="mb-4 flex items-end gap-3">
        <div className="space-y-1.5">
          <Label>组织</Label>
          <Select value={orgId} onValueChange={setOrgId}>
            <SelectTrigger className="w-48">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部组织</SelectItem>
              {orgs.map((o) => (
                <SelectItem key={o.id} value={o.id}>
                  {o.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      {items === null ? (
        <TableSkeleton />
      ) : items.length === 0 ? (
        <EmptyState icon={<ScrollText className="size-8" />} title="没有审计记录" />
      ) : (
        <>
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>时间</TableHead>
                  <TableHead>操作者</TableHead>
                  <TableHead>动作</TableHead>
                  <TableHead>目标</TableHead>
                  <TableHead>组织</TableHead>
                  <TableHead className="w-20">详情</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {formatDateTime(item.created_at)}
                    </TableCell>
                    <TableCell className="font-medium">{item.actor_name || item.actor_id.slice(0, 8)}</TableCell>
                    <TableCell>
                      <Badge variant="secondary">{item.action}</Badge>
                    </TableCell>
                    <TableCell className="max-w-48 truncate">{item.target || "-"}</TableCell>
                    <TableCell>{orgNameOf(item) || "-"}</TableCell>
                    <TableCell>
                      {detailObject(item) ? (
                        <Button variant="ghost" size="sm" onClick={() => setDetailTarget(item)}>
                          查看
                        </Button>
                      ) : (
                        <span className="text-muted-foreground">-</span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {nextCursor && (
            <div className="mt-4 flex justify-center">
              <Button variant="outline" size="sm" disabled={loadingMore} onClick={() => void loadMore()}>
                {loadingMore ? "加载中…" : "加载更多"}
              </Button>
            </div>
          )}
        </>
      )}

      <DetailDialog
        item={detailTarget}
        orgName={detailTarget ? orgNameOf(detailTarget) : ""}
        onClose={() => setDetailTarget(null)}
      />
    </div>
  )
}
