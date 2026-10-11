import { useCallback, useEffect, useState } from "react"
import { Plus, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/api/client"
import { settingsApi } from "@/api/settings"
import { PageHeader, TableSkeleton } from "@/components/common"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
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
import { SETTING_KEYS, type EgressAction, type EgressPolicy, type EgressRule, type ResourceLimits } from "@/lib/types"

const DEFAULT_EGRESS: EgressPolicy = { defaultAction: "allow", rules: [] }
const DEFAULT_RESOURCES: ResourceLimits = { cpu: "1", memory: "1Gi" }

export default function SettingsPage() {
  const [loaded, setLoaded] = useState(false)
  const [defaultImage, setDefaultImage] = useState("python:3.12")
  const [images, setImages] = useState<string[]>(["python:3.12"])
  const [egress, setEgress] = useState<EgressPolicy>(DEFAULT_EGRESS)
  const [resources, setResources] = useState<ResourceLimits>(DEFAULT_RESOURCES)
  const [idleTtl, setIdleTtl] = useState("28800")
  const [ttl, setTtl] = useState("3600")
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    try {
      const map = await settingsApi.get()
      if (typeof map[SETTING_KEYS.defaultImage] === "string") {
        setDefaultImage(map[SETTING_KEYS.defaultImage] as string)
      }
      if (Array.isArray(map[SETTING_KEYS.images])) {
        setImages((map[SETTING_KEYS.images] as unknown[]).map(String))
      }
      if (map[SETTING_KEYS.egressPolicy] && typeof map[SETTING_KEYS.egressPolicy] === "object") {
        const v = map[SETTING_KEYS.egressPolicy] as EgressPolicy
        setEgress({ defaultAction: v.defaultAction ?? "allow", rules: Array.isArray(v.rules) ? v.rules : [] })
      }
      if (map[SETTING_KEYS.resourceLimits] && typeof map[SETTING_KEYS.resourceLimits] === "object") {
        const v = map[SETTING_KEYS.resourceLimits] as ResourceLimits
        setResources({ cpu: v.cpu ?? "1", memory: v.memory ?? "1Gi" })
      }
      if (map[SETTING_KEYS.idleTtl] !== undefined) setIdleTtl(String(map[SETTING_KEYS.idleTtl]))
      if (map[SETTING_KEYS.ttl] !== undefined) setTtl(String(map[SETTING_KEYS.ttl]))
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setLoaded(true)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  function updateRule(index: number, patch: Partial<EgressRule>) {
    setEgress((prev) => ({
      ...prev,
      rules: prev.rules.map((r, i) => (i === index ? { ...r, ...patch } : r)),
    }))
  }

  async function save() {
    const idle = Number(idleTtl)
    const hard = Number(ttl)
    if (!Number.isInteger(idle) || idle < 0 || !Number.isInteger(hard) || hard <= 0) {
      toast.error("TTL 必须是非负整数（秒）")
      return
    }
    const cleanImages = images.map((s) => s.trim()).filter(Boolean)
    if (cleanImages.length === 0) {
      toast.error("镜像列表不能为空")
      return
    }
    if (!defaultImage.trim()) {
      toast.error("默认镜像不能为空")
      return
    }
    const cleanRules = egress.rules.filter((r) => r.target.trim())
    setSaving(true)
    try {
      await Promise.all([
        settingsApi.put(SETTING_KEYS.defaultImage, defaultImage.trim()),
        settingsApi.put(SETTING_KEYS.images, cleanImages),
        settingsApi.put(SETTING_KEYS.egressPolicy, { defaultAction: egress.defaultAction, rules: cleanRules }),
        settingsApi.put(SETTING_KEYS.resourceLimits, resources),
        settingsApi.put(SETTING_KEYS.idleTtl, idle),
        settingsApi.put(SETTING_KEYS.ttl, hard),
      ])
      toast.success("设置已保存")
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setSaving(false)
    }
  }

  if (!loaded) {
    return (
      <div>
        <PageHeader title="设置" description="平台云电脑全局配置" />
        <TableSkeleton rows={4} />
      </div>
    )
  }

  return (
    <div className="max-w-3xl">
      <PageHeader title="设置" description="平台云电脑全局配置">
        <Button size="sm" onClick={save} disabled={saving}>
          {saving ? "保存中…" : "保存"}
        </Button>
      </PageHeader>

      <div className="space-y-4">
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-base">云电脑镜像</CardTitle>
            <CardDescription>默认镜像与可选镜像列表（智能体编辑器下拉来源）</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="st-image">默认镜像</Label>
              <Input id="st-image" className="font-mono" value={defaultImage} onChange={(e) => setDefaultImage(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label>镜像列表</Label>
              <div className="space-y-1.5">
                {images.map((img, i) => (
                  <div key={i} className="flex gap-1.5">
                    <Input
                      className="font-mono"
                      value={img}
                      onChange={(e) => setImages((prev) => prev.map((v, j) => (j === i ? e.target.value : v)))}
                    />
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label="删除"
                      onClick={() => setImages((prev) => prev.filter((_, j) => j !== i))}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                ))}
                <Button variant="outline" size="sm" onClick={() => setImages((prev) => [...prev, ""])}>
                  <Plus className="size-3.5" />
                  添加镜像
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-base">出站策略（egress）</CardTitle>
            <CardDescription>云电脑网络访问控制：默认动作 + 规则列表</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center gap-3">
              <Switch
                checked={egress.defaultAction === "allow"}
                onCheckedChange={(v) => setEgress((prev) => ({ ...prev, defaultAction: v ? "allow" : "deny" }))}
              />
              <span className="text-sm">默认动作：{egress.defaultAction === "allow" ? "允许（allow）" : "拒绝（deny）"}</span>
            </div>
            <div className="rounded-md border">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-32">动作</TableHead>
                    <TableHead>目标（域名 / CIDR）</TableHead>
                    <TableHead className="w-14"></TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {egress.rules.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={3} className="text-center text-muted-foreground">
                        暂无规则
                      </TableCell>
                    </TableRow>
                  ) : (
                    egress.rules.map((rule, i) => (
                      <TableRow key={i}>
                        <TableCell>
                          <Select value={rule.action} onValueChange={(v) => updateRule(i, { action: v as EgressAction })}>
                            <SelectTrigger className="h-8 w-28">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value="allow">allow</SelectItem>
                              <SelectItem value="deny">deny</SelectItem>
                            </SelectContent>
                          </Select>
                        </TableCell>
                        <TableCell>
                          <Input
                            className="h-8 font-mono"
                            value={rule.target}
                            onChange={(e) => updateRule(i, { target: e.target.value })}
                          />
                        </TableCell>
                        <TableCell>
                          <Button
                            variant="ghost"
                            size="icon"
                            aria-label="删除规则"
                            onClick={() => setEgress((prev) => ({ ...prev, rules: prev.rules.filter((_, j) => j !== i) }))}
                          >
                            <Trash2 className="size-4" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setEgress((prev) => ({ ...prev, rules: [...prev.rules, { action: "allow", target: "" }] }))}
            >
              <Plus className="size-3.5" />
              添加规则
            </Button>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-base">资源默认</CardTitle>
            <CardDescription>云电脑默认 CPU / 内存配额</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="st-cpu">CPU</Label>
                <Input
                  id="st-cpu"
                  value={resources.cpu}
                  onChange={(e) => setResources((prev) => ({ ...prev, cpu: e.target.value }))}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="st-mem">内存</Label>
                <Input
                  id="st-mem"
                  value={resources.memory}
                  onChange={(e) => setResources((prev) => ({ ...prev, memory: e.target.value }))}
                />
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-base">回收策略</CardTitle>
            <CardDescription>空闲回收与硬 TTL（秒）</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="st-idle">空闲回收（idle_ttl_seconds）</Label>
                <Input id="st-idle" type="number" min={0} value={idleTtl} onChange={(e) => setIdleTtl(e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="st-ttl">硬 TTL（ttl_seconds）</Label>
                <Input id="st-ttl" type="number" min={1} value={ttl} onChange={(e) => setTtl(e.target.value)} />
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
