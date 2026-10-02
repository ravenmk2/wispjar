import { useCallback, useEffect, useState } from "react"
import { BarChart3 } from "lucide-react"
import { toast } from "sonner"
import { ApiError, errorMessage } from "@/api/client"
import { orgsApi } from "@/api/auth"
import { EmptyState, PageHeader, TableSkeleton } from "@/components/common"
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
import { formatNumber } from "@/lib/format"
import type { UsageGroupBy, UsageResponse } from "@/lib/types"
import { useIdentityStore } from "@/stores/identity"

function today(): string {
  return new Date().toISOString().slice(0, 10)
}

function daysAgo(n: number): string {
  return new Date(Date.now() - n * 86400_000).toISOString().slice(0, 10)
}

const GROUP_LABELS: Array<{ value: UsageGroupBy; label: string }> = [
  { value: "user", label: "按用户" },
  { value: "agent", label: "按智能体" },
  { value: "model", label: "按模型" },
  { value: "day", label: "按天" },
]

export default function OrgUsagePage() {
  const currentOrgId = useIdentityStore((s) => s.currentOrgId)
  const [from, setFrom] = useState(daysAgo(30))
  const [to, setTo] = useState(today())
  const [groupBy, setGroupBy] = useState<UsageGroupBy>("day")
  const [data, setData] = useState<UsageResponse | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    if (!currentOrgId) return
    setLoading(true)
    setUnavailable(false)
    try {
      setData(await orgsApi.usage(currentOrgId, { from, to, group_by: groupBy }))
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        setUnavailable(true)
        setData(null)
      } else {
        toast.error(errorMessage(e))
        setData(null)
      }
    } finally {
      setLoading(false)
    }
  }, [currentOrgId, from, to, groupBy])

  useEffect(() => {
    load()
  }, [load])

  return (
    <div>
      <PageHeader title="用量" description="本机构 token 用量统计" />

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <div className="space-y-1.5">
          <Label htmlFor="us-from">开始日期</Label>
          <Input id="us-from" type="date" className="w-40" value={from} onChange={(e) => setFrom(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="us-to">结束日期</Label>
          <Input id="us-to" type="date" className="w-40" value={to} onChange={(e) => setTo(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label>聚合维度</Label>
          <Select value={groupBy} onValueChange={(v) => setGroupBy(v as UsageGroupBy)}>
            <SelectTrigger className="w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {GROUP_LABELS.map((g) => (
                <SelectItem key={g.value} value={g.value}>
                  {g.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      {loading ? (
        <TableSkeleton />
      ) : unavailable ? (
        <EmptyState
          icon={<BarChart3 className="size-8" />}
          title="用量统计即将上线"
          description="该功能将在后续里程碑提供"
        />
      ) : !data || data.items.length === 0 ? (
        <EmptyState title="所选范围内没有用量数据" />
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{GROUP_LABELS.find((g) => g.value === groupBy)?.label ?? "维度"}</TableHead>
                <TableHead className="text-right">请求数</TableHead>
                <TableHead className="text-right">输入 tokens</TableHead>
                <TableHead className="text-right">输出 tokens</TableHead>
                <TableHead className="text-right">缓存读</TableHead>
                <TableHead className="text-right">推理 tokens</TableHead>
                <TableHead className="text-right">合计</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.items.map((item) => (
                <TableRow key={item.key}>
                  <TableCell className="font-medium">{item.key}</TableCell>
                  <TableCell className="text-right">{formatNumber(item.requests)}</TableCell>
                  <TableCell className="text-right">{formatNumber(item.input_tokens)}</TableCell>
                  <TableCell className="text-right">{formatNumber(item.output_tokens)}</TableCell>
                  <TableCell className="text-right">{formatNumber(item.cache_read_tokens)}</TableCell>
                  <TableCell className="text-right">{formatNumber(item.reasoning_tokens)}</TableCell>
                  <TableCell className="text-right font-medium">{formatNumber(item.total_tokens)}</TableCell>
                </TableRow>
              ))}
              <TableRow className="bg-muted/50 font-medium">
                <TableCell>合计</TableCell>
                <TableCell className="text-right">{formatNumber(data.totals.requests)}</TableCell>
                <TableCell className="text-right">{formatNumber(data.totals.input_tokens)}</TableCell>
                <TableCell className="text-right">{formatNumber(data.totals.output_tokens)}</TableCell>
                <TableCell className="text-right">{formatNumber(data.totals.cache_read_tokens)}</TableCell>
                <TableCell className="text-right">{formatNumber(data.totals.reasoning_tokens)}</TableCell>
                <TableCell className="text-right">{formatNumber(data.totals.total_tokens)}</TableCell>
              </TableRow>
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}
