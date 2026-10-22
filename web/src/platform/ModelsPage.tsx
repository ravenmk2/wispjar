import { useCallback, useEffect, useMemo, useState } from "react"
import { Pencil, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { modelsApi } from "@/api/models"
import { providersApi } from "@/api/providers"
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
import { Switch } from "@/components/ui/switch"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { formatNumber } from "@/lib/format"
import { REASONING_EFFORT_OPTIONS } from "@/lib/types"
import type { Model, ModelCapabilities, Provider } from "@/lib/types"

type CapabilityFlag = "tools" | "reasoning" | "vision" | "streaming" | "tool_images"

const CAPABILITY_LABELS: Array<{ key: CapabilityFlag; label: string }> = [
  { key: "tools", label: "工具调用" },
  { key: "reasoning", label: "推理" },
  { key: "vision", label: "视觉" },
  { key: "streaming", label: "流式" },
  { key: "tool_images", label: "工具图片（file_id）" },
]

export default function ModelsPage() {
  const [models, setModels] = useState<Model[] | null>(null)
  const [providers, setProviders] = useState<Provider[]>([])
  const [editTarget, setEditTarget] = useState<Model | "new" | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<Model | null>(null)
  const [deleting, setDeleting] = useState(false)

  const load = useCallback(async () => {
    try {
      const [m, p] = await Promise.all([modelsApi.list(), providersApi.list()])
      setModels(m.items)
      setProviders(p.items)
    } catch (e) {
      toast.error(errorMessage(e))
      setModels([])
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const providerName = useMemo(() => {
    const map = new Map(providers.map((p) => [p.id, p.name]))
    return (m: Model) => m.provider_name ?? map.get(m.provider_id) ?? m.provider_id
  }, [providers])

  const groups = useMemo(() => {
    if (!models) return []
    const map = new Map<string, { name: string; items: Model[] }>()
    for (const m of models) {
      const g = map.get(m.provider_id) ?? { name: providerName(m), items: [] }
      g.items.push(m)
      map.set(m.provider_id, g)
    }
    return [...map.values()]
  }, [models, providerName])

  async function toggleStatus(m: Model) {
    try {
      await modelsApi.update(m.id, {
        model_code: m.model_code,
        display_name: m.display_name,
        capabilities: m.capabilities,
        context_size: m.context_size,
        default_params: m.default_params,
        status: m.status === "active" ? "disabled" : "active",
      })
      load()
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  async function remove() {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await modelsApi.remove(deleteTarget.id)
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
      <PageHeader title="模型" description="平台上架模型全局共享，所有机构可用">
        <Button size="sm" onClick={() => setEditTarget("new")}>
          上架模型
        </Button>
      </PageHeader>

      {models === null ? (
        <TableSkeleton />
      ) : models.length === 0 ? (
        <EmptyState title="暂无模型" description="上架模型后机构智能体即可选用" />
      ) : (
        <div className="space-y-6">
          {groups.map((g) => (
            <div key={g.name}>
              <h2 className="mb-2 text-sm font-medium text-muted-foreground">{g.name}</h2>
              <div className="rounded-md border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>模型代码</TableHead>
                      <TableHead>显示名</TableHead>
                      <TableHead>能力</TableHead>
                      <TableHead className="text-right">上下文</TableHead>
                      <TableHead>上架</TableHead>
                      <TableHead className="w-24">操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {g.items.map((m) => (
                      <TableRow key={m.id}>
                        <TableCell className="font-mono text-sm">{m.model_code}</TableCell>
                        <TableCell className="font-medium">{m.display_name}</TableCell>
                        <TableCell>
                          <div className="flex flex-wrap gap-1">
                            {CAPABILITY_LABELS.filter((c) => m.capabilities?.[c.key]).map((c) => (
                              <Badge key={c.key} variant="secondary">
                                {c.label}
                              </Badge>
                            ))}
                          </div>
                          {m.capabilities?.reasoning_efforts && m.capabilities.reasoning_efforts.length > 0 && (
                            <div className="mt-1 text-xs text-muted-foreground">
                              档位 {m.capabilities.reasoning_efforts.join(" / ")}
                              {m.capabilities.reasoning_effort_default
                                ? `（默认 ${m.capabilities.reasoning_effort_default}）`
                                : "（默认）"}
                            </div>
                          )}
                        </TableCell>
                        <TableCell className="text-right text-muted-foreground">
                          {formatNumber(m.context_size)}
                        </TableCell>
                        <TableCell>
                          <Switch checked={m.status === "active"} onCheckedChange={() => toggleStatus(m)} />
                        </TableCell>
                        <TableCell>
                          <div className="flex gap-1">
                            <Button variant="ghost" size="icon" aria-label="编辑" onClick={() => setEditTarget(m)}>
                              <Pencil className="size-4" />
                            </Button>
                            <Button variant="ghost" size="icon" aria-label="删除" onClick={() => setDeleteTarget(m)}>
                              <Trash2 className="size-4" />
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </div>
          ))}
        </div>
      )}

      {editTarget && (
        <ModelDialog
          model={editTarget === "new" ? null : editTarget}
          providers={providers}
          onClose={(saved) => {
            setEditTarget(null)
            if (saved) load()
          }}
        />
      )}

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="删除模型"
        description={`确定删除「${deleteTarget?.display_name}」？被智能体引用时无法删除。`}
        confirmText="删除"
        destructive
        loading={deleting}
        onConfirm={remove}
      />
    </div>
  )
}

function ModelDialog({
  model,
  providers,
  onClose,
}: {
  model: Model | null
  providers: Provider[]
  onClose: (saved: boolean) => void
}) {
  const isEdit = model !== null
  const [providerId, setProviderId] = useState(model?.provider_id ?? providers[0]?.id ?? "")
  const [modelCode, setModelCode] = useState(model?.model_code ?? "")
  const [displayName, setDisplayName] = useState(model?.display_name ?? "")
  const [capabilities, setCapabilities] = useState<ModelCapabilities>(
    model?.capabilities ?? { tools: true, reasoning: false, vision: false, streaming: true },
  )
  const [contextSize, setContextSize] = useState(String(model?.context_size ?? 64000))
  const [paramsText, setParamsText] = useState(model?.default_params ? JSON.stringify(model.default_params, null, 2) : "")
  const [paramsError, setParamsError] = useState("")
  const [loading, setLoading] = useState(false)

  function parseParams(): Record<string, unknown> | null | undefined {
    const text = paramsText.trim()
    if (!text) return null
    try {
      const v = JSON.parse(text) as unknown
      if (v === null || typeof v !== "object" || Array.isArray(v)) {
        setParamsError("默认参数必须是 JSON 对象")
        return undefined
      }
      setParamsError("")
      return v as Record<string, unknown>
    } catch {
      setParamsError("JSON 格式不正确")
      return undefined
    }
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    const params = parseParams()
    if (params === undefined) return
    const size = Number(contextSize)
    if (!Number.isInteger(size) || size <= 0) {
      setParamsError("")
      toast.error("上下文长度必须是正整数")
      return
    }
    if (!modelCode.trim() || !displayName.trim() || (!isEdit && !providerId)) return
    setLoading(true)
    const body = {
      model_code: modelCode.trim(),
      display_name: displayName.trim(),
      capabilities,
      context_size: size,
      default_params: params,
    }
    try {
      if (isEdit) {
        await modelsApi.update(model.id, { ...body, status: model.status })
        toast.success("已保存")
      } else {
        await modelsApi.create({ ...body, provider_id: providerId })
        toast.success("模型已上架")
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
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{isEdit ? "编辑模型" : "上架模型"}</DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          {!isEdit && (
            <div className="space-y-1.5">
              <Label>供应商</Label>
              <Select value={providerId} onValueChange={setProviderId}>
                <SelectTrigger>
                  <SelectValue placeholder="选择供应商" />
                </SelectTrigger>
                <SelectContent>
                  {providers.map((p) => (
                    <SelectItem key={p.id} value={p.id}>
                      {p.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="md-code">模型代码</Label>
              <Input
                id="md-code"
                className="font-mono"
                placeholder="deepseek-chat"
                value={modelCode}
                onChange={(e) => setModelCode(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="md-name">显示名</Label>
              <Input id="md-name" value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
            </div>
          </div>
          <div className="space-y-1.5">
            <Label>API 协议</Label>
            <Select value={capabilities.api_protocol ?? "chat_completions"} onValueChange={(value: "chat_completions" | "responses") => setCapabilities((prev) => ({ ...prev, api_protocol: value, tool_images: value === "responses" && prev.tool_images }))}>
              <SelectTrigger aria-label="API 协议"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="chat_completions">Chat Completions（文本工具结果）</SelectItem>
                <SelectItem value="responses">Responses（支持原生工具图片）</SelectItem>
              </SelectContent>
            </Select>
            <Label>能力</Label>
            <div className="flex flex-wrap gap-4 rounded-md border px-3 py-2">
              {CAPABILITY_LABELS.map((c) => (
                <label key={c.key} className="flex cursor-pointer items-center gap-1.5 text-sm">
                  <input
                    type="checkbox"
                    className="size-3.5 accent-primary"
                    checked={capabilities[c.key] ?? false}
                    disabled={c.key === "tool_images" && (capabilities.api_protocol !== "responses" || !capabilities.tools || !capabilities.vision)}
                    onChange={(e) =>
                      setCapabilities((prev) => ({
                        ...prev,
                        [c.key]: e.target.checked,
                        ...((c.key === "vision" || c.key === "tools") && !e.target.checked ? { tool_images: false } : {}),
                        ...(c.key === "reasoning" && !e.target.checked
                          ? { reasoning_efforts: undefined, reasoning_effort_default: undefined }
                          : {}),
                      }))
                    }
                  />
                  {c.label}
                </label>
              ))}
            </div>
            {capabilities.reasoning && (
              <div className="space-y-2 rounded-md border px-3 py-2">
                <div className="flex flex-wrap gap-4">
                  {REASONING_EFFORT_OPTIONS.map((effort) => (
                    <label key={effort} className="flex cursor-pointer items-center gap-1.5 text-sm">
                      <input
                        type="checkbox"
                        className="size-3.5 accent-primary"
                        checked={capabilities.reasoning_efforts?.includes(effort) ?? false}
                        onChange={(e) =>
                          setCapabilities((prev) => {
                            const current = prev.reasoning_efforts ?? []
                            const efforts = e.target.checked
                              ? REASONING_EFFORT_OPTIONS.filter((v) => v === effort || current.includes(v))
                              : current.filter((v) => v !== effort)
                            return {
                              ...prev,
                              reasoning_efforts: efforts.length > 0 ? efforts : undefined,
                              reasoning_effort_default:
                                prev.reasoning_effort_default && efforts.includes(prev.reasoning_effort_default)
                                  ? prev.reasoning_effort_default
                                  : undefined,
                            }
                          })
                        }
                      />
                      {effort}
                    </label>
                  ))}
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-xs text-muted-foreground">默认档</span>
                  <Select
                    value={capabilities.reasoning_effort_default ?? "__provider__"}
                    onValueChange={(v) =>
                      setCapabilities((prev) => ({
                        ...prev,
                        reasoning_effort_default: v === "__provider__" ? undefined : v,
                      }))
                    }
                  >
                    <SelectTrigger className="h-8 w-40">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="__provider__">默认</SelectItem>
                      {REASONING_EFFORT_OPTIONS.filter((effort) => capabilities.reasoning_efforts?.includes(effort)).map((effort) => (
                        <SelectItem key={effort} value={effort}>
                          {effort}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <span className="text-xs text-muted-foreground">不勾选档位 = 不支持选档</span>
                </div>
              </div>
            )}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="md-ctx">上下文长度（tokens）</Label>
            <Input
              id="md-ctx"
              type="number"
              min={1}
              value={contextSize}
              onChange={(e) => setContextSize(e.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="md-params">默认参数（JSON，可留空）</Label>
            <Textarea
              id="md-params"
              rows={4}
              className="font-mono text-xs"
              placeholder='{"temperature": 0.7, "max_tokens": 4096}'
              value={paramsText}
              onChange={(e) => {
                setParamsText(e.target.value)
                setParamsError("")
              }}
            />
            {paramsError && <p className="text-sm text-destructive">{paramsError}</p>}
          </div>
          <DialogFooter>
            <Button type="submit" disabled={loading || !modelCode.trim() || !displayName.trim()}>
              {loading ? "保存中…" : "保存"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
