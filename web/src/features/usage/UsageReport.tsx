import { useCallback, useEffect, useState } from "react"
import { BarChart3 } from "lucide-react"
import { toast } from "sonner"
import type { UsageQueryParams } from "@/api/reports"
import { errorMessage } from "@/api/client"
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
import type { UsageGroupBy, UsageItem, UsageResponse } from "@/lib/types"

function localDate(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

function today(): string {
  return localDate(new Date())
}

function daysAgo(n: number): string {
  return localDate(new Date(Date.now() - n * 86400_000))
}

export interface UsageGroupOption {
  value: UsageGroupBy
  label: string
}

function cachedTokens(item: UsageItem): number {
  return item.cache_read_tokens + item.cache_write_tokens
}

function KeyLabel({ item, names }: { item: UsageItem; names?: Record<string, string> }) {
  const name = names?.[item.key]
  if (!name) return <span className="font-medium">{item.key}</span>
  return (
    <div>
      <div className="font-medium">{name}</div>
      <div className="font-mono text-[11px] text-muted-foreground">{item.key.slice(0, 8)}…</div>
    </div>
  )
}

const BAR_SEGMENTS = [
  { label: "输入", className: "bg-sky-500", value: (i: UsageItem) => i.input_tokens },
  { label: "缓存", className: "bg-amber-500", value: cachedTokens },
  { label: "输出", className: "bg-emerald-500", value: (i: UsageItem) => i.output_tokens },
]

function UsageBars({ items, names }: { items: UsageItem[]; names?: Record<string, string> }) {
  const maxTotal = Math.max(...items.map((i) => i.total_tokens), 1)
  return (
    <div className="mb-4 rounded-md border p-4">
      <div className="mb-3 flex items-center gap-4 text-xs text-muted-foreground">
        {BAR_SEGMENTS.map((s) => (
          <span key={s.label} className="flex items-center gap-1.5">
            <span className={`size-2 rounded-sm ${s.className}`} />
            {s.label}
          </span>
        ))}
      </div>
      <div className="space-y-2">
        {items.map((item) => (
          <div key={item.key} className="flex items-center gap-3">
            <div className="w-36 shrink-0 truncate text-xs text-muted-foreground">
              {names?.[item.key] ?? item.key}
            </div>
            <div className="h-3 min-w-0 flex-1 overflow-hidden rounded-sm bg-muted">
              <div
                className="flex h-full"
                style={{ width: `${Math.max((item.total_tokens / maxTotal) * 100, item.total_tokens > 0 ? 0.5 : 0)}%` }}
              >
                {BAR_SEGMENTS.map((s) => {
                  const v = s.value(item)
                  if (v <= 0 || item.total_tokens <= 0) return null
                  return (
                    <div
                      key={s.label}
                      className={s.className}
                      style={{ width: `${(v / item.total_tokens) * 100}%` }}
                    />
                  )
                })}
              </div>
            </div>
            <div className="w-24 shrink-0 text-right font-mono text-xs">{formatNumber(item.total_tokens)}</div>
          </div>
        ))}
      </div>
    </div>
  )
}

export function UsageReport({
  title,
  description,
  groupOptions,
  defaultGroupBy = "day",
  query,
}: {
  title: string
  description: string
  groupOptions: UsageGroupOption[]
  defaultGroupBy?: UsageGroupBy
  query: (params: UsageQueryParams) => Promise<UsageResponse>
}) {
  const [from, setFrom] = useState(daysAgo(6))
  const [to, setTo] = useState(today())
  const [groupBy, setGroupBy] = useState<UsageGroupBy>(defaultGroupBy)
  const [data, setData] = useState<UsageResponse | null>(null)
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setData(await query({ from, to, group_by: groupBy }))
    } catch (e) {
      toast.error(errorMessage(e))
      setData(null)
    } finally {
      setLoading(false)
    }
  }, [query, from, to, groupBy])

  useEffect(() => {
    load()
  }, [load])

  const groupLabel = groupOptions.find((g) => g.value === groupBy)?.label ?? "维度"

  return (
    <div>
      <PageHeader title={title} description={description} />

      <div className="mb-4 flex flex-wrap items-end gap-3">
        <div className="space-y-1.5">
          <Label htmlFor="usage-from">开始日期</Label>
          <Input id="usage-from" type="date" className="w-40" value={from} onChange={(e) => setFrom(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="usage-to">结束日期</Label>
          <Input id="usage-to" type="date" className="w-40" value={to} onChange={(e) => setTo(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label>聚合维度</Label>
          <Select value={groupBy} onValueChange={(v) => setGroupBy(v as UsageGroupBy)}>
            <SelectTrigger className="w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {groupOptions.map((g) => (
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
      ) : !data || data.items.length === 0 ? (
        <EmptyState
          icon={<BarChart3 className="size-8" />}
          title="所选范围内没有用量数据"
          description="调整日期范围或聚合维度后重试"
        />
      ) : (
        <>
          <UsageBars items={data.items} names={data.names} />
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{groupLabel.replace(/^按/, "")}</TableHead>
                  <TableHead className="text-right">请求数</TableHead>
                  <TableHead className="text-right">输入 tokens</TableHead>
                  <TableHead className="text-right">输出 tokens</TableHead>
                  <TableHead className="text-right">缓存 tokens</TableHead>
                  <TableHead className="text-right">推理 tokens</TableHead>
                  <TableHead className="text-right">合计</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.items.map((item) => (
                  <TableRow key={item.key}>
                    <TableCell>
                      <KeyLabel item={item} names={data.names} />
                    </TableCell>
                    <TableCell className="text-right">{formatNumber(item.requests)}</TableCell>
                    <TableCell className="text-right">{formatNumber(item.input_tokens)}</TableCell>
                    <TableCell className="text-right">{formatNumber(item.output_tokens)}</TableCell>
                    <TableCell className="text-right">{formatNumber(cachedTokens(item))}</TableCell>
                    <TableCell className="text-right">{formatNumber(item.reasoning_tokens)}</TableCell>
                    <TableCell className="text-right font-medium">{formatNumber(item.total_tokens)}</TableCell>
                  </TableRow>
                ))}
                <TableRow className="bg-muted/50 font-medium">
                  <TableCell>合计</TableCell>
                  <TableCell className="text-right">{formatNumber(data.totals.requests)}</TableCell>
                  <TableCell className="text-right">{formatNumber(data.totals.input_tokens)}</TableCell>
                  <TableCell className="text-right">{formatNumber(data.totals.output_tokens)}</TableCell>
                  <TableCell className="text-right">
                    {formatNumber(data.totals.cache_read_tokens + data.totals.cache_write_tokens)}
                  </TableCell>
                  <TableCell className="text-right">{formatNumber(data.totals.reasoning_tokens)}</TableCell>
                  <TableCell className="text-right">{formatNumber(data.totals.total_tokens)}</TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        </>
      )}
    </div>
  )
}
