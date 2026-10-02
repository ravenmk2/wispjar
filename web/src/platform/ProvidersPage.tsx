import { useCallback, useEffect, useState } from "react"
import { Loader2, Pencil, PlugZap, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { providersApi, type ProviderBody } from "@/api/providers"
import { ConfirmDialog, EmptyState, PageHeader, TableSkeleton } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
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
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { formatDateTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { Provider, ProviderType } from "@/lib/types"

const TYPE_LABELS: Record<ProviderType, string> = {
  deepseek: "DeepSeek",
  ark: "火山 Ark",
  qwen: "千问",
  openai_compat: "OpenAI 兼容",
}

const TYPE_DEFAULT_BASE_URL: Record<ProviderType, string> = {
  deepseek: "https://api.deepseek.com/v1",
  ark: "https://ark.cn-beijing.volces.com/api/v3",
  qwen: "https://dashscope.aliyuncs.com/compatible-mode/v1",
  openai_compat: "",
}

export default function ProvidersPage() {
  const [providers, setProviders] = useState<Provider[] | null>(null)
  const [editTarget, setEditTarget] = useState<Provider | "new" | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Provider | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [checkingId, setCheckingId] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      const res = await providersApi.list()
      setProviders(res.items)
    } catch (e) {
      toast.error(errorMessage(e))
      setProviders([])
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  async function check(p: Provider) {
    setCheckingId(p.id)
    try {
      const res = await providersApi.check(p.id)
      toast.success(`连通正常（${res.latency_ms}ms）`)
      load()
    } catch (e) {
      toast.error(errorMessage(e))
      load()
    } finally {
      setCheckingId(null)
    }
  }

  async function remove() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await providersApi.remove(deleteTarget.id)
      toast.success("已删除")
      setDeleteTarget(null)
      load()
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div>
      <PageHeader title="供应商" description="模型供应商配置，密钥加密存储且不回显明文">
        <Button size="sm" onClick={() => setEditTarget("new")}>
          新建供应商
        </Button>
      </PageHeader>

      {providers === null ? (
        <TableSkeleton rows={3} />
      ) : providers.length === 0 ? (
        <EmptyState title="暂无供应商" description="创建供应商后即可上架模型" />
      ) : (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {providers.map((p) => {
            const lastCheck = p.extra?.last_check
            return (
              <Card key={p.id}>
                <CardHeader className="pb-3">
                  <div className="flex items-center justify-between gap-2">
                    <CardTitle className="flex items-center gap-2 text-base">
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span
                            className={cn(
                              "inline-block size-2 rounded-full",
                              lastCheck === undefined
                                ? "bg-muted-foreground/40"
                                : lastCheck.ok
                                  ? "bg-green-500"
                                  : "bg-red-500",
                            )}
                          />
                        </TooltipTrigger>
                        <TooltipContent>
                          {lastCheck === undefined
                            ? "尚未检查连通性"
                            : lastCheck.ok
                              ? `连通正常 · ${lastCheck.latency_ms}ms`
                              : "连通失败"}
                        </TooltipContent>
                      </Tooltip>
                      <span className="truncate">{p.name}</span>
                    </CardTitle>
                    <Badge variant="secondary">{TYPE_LABELS[p.type] ?? p.type}</Badge>
                  </div>
                </CardHeader>
                <CardContent className="space-y-1.5 pb-3 text-sm">
                  <div className="truncate text-muted-foreground" title={p.base_url}>
                    {p.base_url}
                  </div>
                  <div className="font-mono text-xs text-muted-foreground">{p.masked_key || "sk-****"}</div>
                  <div className="text-xs text-muted-foreground">
                    最近检查：{lastCheck ? formatDateTime(lastCheck.at) : "-"}
                  </div>
                  {p.extra?.remark && <div className="text-xs text-muted-foreground">备注：{p.extra.remark}</div>}
                </CardContent>
                <CardFooter className="gap-1">
                  <Button variant="outline" size="sm" disabled={checkingId === p.id} onClick={() => check(p)}>
                    {checkingId === p.id ? (
                      <Loader2 className="size-3.5 animate-spin" />
                    ) : (
                      <PlugZap className="size-3.5" />
                    )}
                    测试连通
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => setEditTarget(p)}>
                    <Pencil className="size-3.5" />
                    编辑
                  </Button>
                  <Button variant="ghost" size="sm" onClick={() => setDeleteTarget(p)}>
                    <Trash2 className="size-3.5" />
                    删除
                  </Button>
                </CardFooter>
              </Card>
            )
          })}
        </div>
      )}

      {editTarget && (
        <ProviderDialog
          provider={editTarget === "new" ? null : editTarget}
          onClose={(saved) => {
            setEditTarget(null)
            if (saved) load()
          }}
        />
      )}

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="删除供应商"
        description={`确定删除「${deleteTarget?.name}」？存在启用中的模型时无法删除。`}
        confirmText="删除"
        destructive
        loading={deleting}
        onConfirm={remove}
      />
    </div>
  )
}

function ProviderDialog({
  provider,
  onClose,
}: {
  provider: Provider | null
  onClose: (saved: boolean) => void
}) {
  const isEdit = provider !== null
  const [name, setName] = useState(provider?.name ?? "")
  const [type, setType] = useState<ProviderType>(provider?.type ?? "deepseek")
  const [baseUrl, setBaseUrl] = useState(provider?.base_url ?? TYPE_DEFAULT_BASE_URL.deepseek)
  const [apiKey, setApiKey] = useState("")
  const [remark, setRemark] = useState(provider?.extra?.remark ?? "")
  const [loading, setLoading] = useState(false)

  function onTypeChange(t: ProviderType) {
    setType(t)
    if (!isEdit || baseUrl === TYPE_DEFAULT_BASE_URL[type]) {
      setBaseUrl(TYPE_DEFAULT_BASE_URL[t])
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim() || !baseUrl.trim() || (!isEdit && !apiKey.trim())) return
    setLoading(true)
    const body: ProviderBody = {
      name: name.trim(),
      type,
      base_url: baseUrl.trim(),
      remark: remark.trim() || undefined,
    }
    if (apiKey.trim()) body.api_key = apiKey.trim()
    try {
      if (isEdit) {
        await providersApi.update(provider.id, body)
        toast.success("已保存")
      } else {
        await providersApi.create(body)
        toast.success("供应商已创建")
      }
      onClose(true)
    } catch (err) {
      toast.error(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose(false)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{isEdit ? "编辑供应商" : "新建供应商"}</DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="pv-name">名称</Label>
              <Input id="pv-name" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
            </div>
            <div className="space-y-1.5">
              <Label>类型</Label>
              <Select value={type} onValueChange={(v) => onTypeChange(v as ProviderType)}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(Object.keys(TYPE_LABELS) as ProviderType[]).map((t) => (
                    <SelectItem key={t} value={t}>
                      {TYPE_LABELS[t]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="pv-url">Base URL</Label>
            <Input id="pv-url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="pv-key">API Key</Label>
            <Input
              id="pv-key"
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              placeholder={isEdit ? "留空表示不更换" : "sk-..."}
              autoComplete="off"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="pv-remark">备注</Label>
            <Textarea id="pv-remark" rows={2} value={remark} onChange={(e) => setRemark(e.target.value)} />
          </div>
          <DialogFooter>
            <Button type="submit" disabled={loading || !name.trim() || !baseUrl.trim() || (!isEdit && !apiKey.trim())}>
              {loading ? "保存中…" : "保存"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
