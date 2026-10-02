import {
  ArrowDown,
  ArrowUp,
  Check,
  ChevronRight,
  Maximize2,
  Minimize2,
  Plus,
  X,
} from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { useNavigate, useParams } from "react-router"
import { toast } from "sonner"
import { adminAgentsApi, normalizeAgentDetail } from "@/api/agents"
import { ApiError, errorMessage } from "@/api/client"
import { adminSkillsApi } from "@/api/skills"
import { settingsApi } from "@/api/settings"
import { workspaceApi } from "@/api/workspace"
import { TableSkeleton } from "@/components/common"
import { Button } from "@/components/ui/button"
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
import { Textarea } from "@/components/ui/textarea"
import { AgentStatusBadge } from "@/features/admin/AgentsPage"
import { AgentAvatar, AVATAR_ICONS } from "@/features/agents/AgentAvatar"
import { cn } from "@/lib/utils"
import {
  SETTING_KEYS,
  type AdminAgentDetail,
  type AdminAgentSummary,
  type AgentBody,
  type AgentStatus,
  type AgentToolsEnabled,
  type OrgModel,
  type ResourceLimits,
  type SkillSummary,
} from "@/lib/types"
import { useCurrentOrg } from "@/stores/identity"

const DEFAULT_FORM: AgentBody = {
  name: "",
  description: "",
  avatar: "bot",
  system_prompt: "",
  model_id: "",
  model_params: { temperature: 0.7 },
  max_iterations: 20,
  tools_enabled: { exec: true, read: true, write: true, edit: true, glob: true, grep: true },
  skill_ids: [],
  sub_agent_ids: [],
  sandbox_config: { image: "", resources: null },
}

const TOOL_LABELS: Array<{ key: keyof AgentToolsEnabled; label: string; description: string }> = [
  { key: "exec", label: "exec", description: "在沙箱中执行命令" },
  { key: "read", label: "read", description: "读取沙箱文件" },
  { key: "write", label: "write", description: "写入沙箱文件" },
  { key: "edit", label: "edit", description: "编辑沙箱文件" },
  { key: "glob", label: "glob", description: "按模式匹配文件" },
  { key: "grep", label: "grep", description: "搜索文件内容" },
]

const SECTIONS = [
  { id: "basic", label: "基本信息" },
  { id: "model", label: "模型" },
  { id: "prompt", label: "提示词" },
  { id: "tools", label: "工具" },
  { id: "skills", label: "Skills" },
  { id: "subagents", label: "子智能体" },
  { id: "sandbox", label: "沙箱" },
] as const

type SectionId = (typeof SECTIONS)[number]["id"]

function formatApiError(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.details && Object.keys(e.details).length > 0) {
      return `${e.message}：${JSON.stringify(e.details)}`
    }
    return e.message
  }
  return errorMessage(e)
}

export default function AgentEditorPage() {
  const { id } = useParams()
  const isNew = !id
  const currentOrg = useCurrentOrg()
  const navigate = useNavigate()

  const [form, setForm] = useState<AgentBody>(DEFAULT_FORM)
  const [status, setStatus] = useState<AgentStatus | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [section, setSection] = useState<SectionId>("basic")
  const [models, setModels] = useState<OrgModel[]>([])
  const [skills, setSkills] = useState<SkillSummary[]>([])
  const [agents, setAgents] = useState<AdminAgentSummary[]>([])
  const [sandboxImages, setSandboxImages] = useState<string[] | null>(null)
  const [defaultResources, setDefaultResources] = useState<ResourceLimits | null>(null)
  const [acting, setActing] = useState<"save" | "publish" | "archive" | null>(null)
  const [saveError, setSaveError] = useState<string | null>(null)

  const patch = useCallback((p: Partial<AgentBody>) => {
    setForm((f) => ({ ...f, ...p }))
    setSaveError(null)
  }, [])

  useEffect(() => {
    if (!currentOrg) return
    workspaceApi
      .models(currentOrg.id)
      .then((res) => setModels(res.items))
      .catch((e) => toast.error(errorMessage(e)))
    adminSkillsApi
      .list(currentOrg.id)
      .then(setSkills)
      .catch(() => setSkills([]))
    adminAgentsApi
      .list(currentOrg.id, { limit: 100 })
      .then((res) => setAgents(res.items))
      .catch(() => setAgents([]))
    settingsApi
      .getOrgSandbox(currentOrg.id)
      .then((map) => {
        const images = map[SETTING_KEYS.images]
        setSandboxImages(Array.isArray(images) ? images.map(String) : [])
        const limits = map[SETTING_KEYS.resourceLimits]
        if (limits && typeof limits === "object") setDefaultResources(limits as ResourceLimits)
      })
      .catch(() => {
        setSandboxImages(null)
      })
  }, [currentOrg])

  useEffect(() => {
    if (!currentOrg) return
    if (isNew) {
      setForm(DEFAULT_FORM)
      setStatus(null)
      setLoaded(true)
      return
    }
    setLoaded(false)
    adminAgentsApi
      .detail(currentOrg.id, id)
      .then((detail) => {
        const { id: _id, status: s, updated_at: _u, created_at: _c, ...body } = detail
        setForm({ ...DEFAULT_FORM, ...body })
        setStatus(s)
        setLoaded(true)
      })
      .catch((e) => {
        toast.error(errorMessage(e))
        navigate("/admin/agents", { replace: true })
      })
  }, [currentOrg, id, isNew, navigate])

  function buildBody(): AgentBody {
    const params = form.model_params ?? {}
    const modelParams: AgentBody["model_params"] = {}
    if (params.temperature !== undefined) modelParams.temperature = params.temperature
    if (params.max_tokens !== undefined && params.max_tokens > 0) modelParams.max_tokens = params.max_tokens
    const resources = form.sandbox_config.resources
    const hasResources = resources && ((resources.cpu ?? "") !== "" || (resources.memory ?? "") !== "")
    return {
      ...form,
      name: form.name.trim(),
      description: form.description.trim(),
      model_params: Object.keys(modelParams).length > 0 ? modelParams : null,
      sandbox_config: {
        image: form.sandbox_config.image,
        resources: hasResources ? resources : null,
      },
    }
  }

  async function save(): Promise<AdminAgentDetail | null> {
    if (!currentOrg || acting) return null
    if (!form.name.trim()) {
      setSaveError("名称不能为空")
      setSection("basic")
      return null
    }
    setActing("save")
    setSaveError(null)
    try {
      const detail = isNew
        ? normalizeAgentDetail(await adminAgentsApi.create(currentOrg.id, buildBody()))
        : normalizeAgentDetail(await adminAgentsApi.update(currentOrg.id, id, buildBody()))
      setStatus(detail.status)
      toast.success(isNew ? "草稿已创建" : "已保存")
      if (isNew) navigate(`/admin/agents/${detail.id}`, { replace: true })
      else if (status === "published" && detail.status === "draft") {
        toast.info("已发布智能体被修改，已自动转为草稿，需重新发布")
      }
      return detail
    } catch (e) {
      setSaveError(formatApiError(e))
      return null
    } finally {
      setActing(null)
    }
  }

  async function publish() {
    if (!currentOrg || acting) return
    setActing("publish")
    setSaveError(null)
    try {
      let agentId = id
      if (isNew) {
        if (!form.name.trim()) {
          setSaveError("名称不能为空")
          setSection("basic")
          return
        }
        const created = normalizeAgentDetail(await adminAgentsApi.create(currentOrg.id, buildBody()))
        agentId = created.id
        navigate(`/admin/agents/${created.id}`, { replace: true })
      }
      const detail = normalizeAgentDetail(await adminAgentsApi.publish(currentOrg.id, agentId!))
      setStatus(detail.status ?? "published")
      toast.success("已发布")
    } catch (e) {
      setSaveError(formatApiError(e))
    } finally {
      setActing(null)
    }
  }

  async function archive() {
    if (!currentOrg || !id || acting) return
    setActing("archive")
    setSaveError(null)
    try {
      const detail = normalizeAgentDetail(await adminAgentsApi.archive(currentOrg.id, id))
      setStatus(detail.status ?? "archived")
      toast.success("已下线")
    } catch (e) {
      setSaveError(formatApiError(e))
    } finally {
      setActing(null)
    }
  }

  if (!loaded) {
    return <TableSkeleton rows={6} />
  }

  const effectiveStatus: AgentStatus = status ?? "draft"
  const subAgentCandidates = agents.filter((a) => a.status === "published" && a.id !== id)

  return (
    <div className="flex h-full flex-col">
      <div className="mb-4 flex items-center gap-2 text-sm text-muted-foreground">
        <button type="button" className="hover:text-foreground" onClick={() => navigate("/admin/agents")}>
          智能体
        </button>
        <ChevronRight className="size-3.5" />
        <span className="text-foreground">{isNew ? "新建智能体" : form.name || "编辑智能体"}</span>
      </div>

      <div className="flex min-h-0 flex-1 gap-6">
        <nav className="w-36 shrink-0 space-y-0.5">
          {SECTIONS.map((s) => (
            <button
              key={s.id}
              type="button"
              onClick={() => setSection(s.id)}
              className={cn(
                "block w-full rounded-md px-3 py-1.5 text-left text-sm text-muted-foreground hover:bg-accent hover:text-foreground",
                section === s.id && "bg-accent font-medium text-foreground",
              )}
            >
              {s.label}
            </button>
          ))}
        </nav>

        <div className="min-w-0 flex-1 pb-20">
          <div className="max-w-2xl">
            {section === "basic" && <BasicSection form={form} patch={patch} />}
            {section === "model" && <ModelSection form={form} patch={patch} models={models} />}
            {section === "prompt" && <PromptSection form={form} patch={patch} />}
            {section === "tools" && <ToolsSection form={form} patch={patch} />}
            {section === "skills" && <SkillsSection form={form} patch={patch} skills={skills} />}
            {section === "subagents" && (
              <SubAgentsSection form={form} patch={patch} candidates={subAgentCandidates} />
            )}
            {section === "sandbox" && (
              <SandboxSection
                form={form}
                patch={patch}
                images={sandboxImages}
                defaultResources={defaultResources}
              />
            )}
          </div>
        </div>
      </div>

      <div className="sticky bottom-0 -mx-6 -mb-6 mt-4 border-t bg-background px-6 py-3">
        <div className="flex items-center gap-3">
          <AgentStatusBadge status={effectiveStatus} />
          {saveError ? (
            <span className="min-w-0 flex-1 truncate text-sm text-destructive" title={saveError}>
              {saveError}
            </span>
          ) : (
            <span className="flex-1 text-sm text-muted-foreground">
              {effectiveStatus === "published" ? "保存后将转为草稿，需重新发布" : ""}
            </span>
          )}
          {effectiveStatus === "published" && (
            <Button variant="outline" size="sm" disabled={acting !== null} onClick={() => void archive()}>
              {acting === "archive" ? "下线中…" : "下线"}
            </Button>
          )}
          <Button variant="outline" size="sm" disabled={acting !== null} onClick={() => void save()}>
            {acting === "save" ? "保存中…" : effectiveStatus === "draft" ? "保存草稿" : "保存"}
          </Button>
          {effectiveStatus !== "published" && (
            <Button size="sm" disabled={acting !== null} onClick={() => void publish()}>
              {acting === "publish" ? "发布中…" : "发布"}
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}

function SectionTitle({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="mb-4">
      <h2 className="text-base font-medium">{title}</h2>
      {hint && <p className="mt-0.5 text-sm text-muted-foreground">{hint}</p>}
    </div>
  )
}

function BasicSection({ form, patch }: { form: AgentBody; patch: (p: Partial<AgentBody>) => void }) {
  return (
    <div>
      <SectionTitle title="基本信息" hint="描述将作为 task 委派时的能力说明" />
      <div className="space-y-4">
        <div className="space-y-1.5">
          <Label htmlFor="agent-name">名称</Label>
          <Input
            id="agent-name"
            value={form.name}
            onChange={(e) => patch({ name: e.target.value })}
            placeholder="例如：代码助手"
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="agent-desc">描述</Label>
          <Textarea
            id="agent-desc"
            rows={3}
            value={form.description}
            onChange={(e) => patch({ description: e.target.value })}
            placeholder="简要说明该智能体的能力与适用场景"
          />
        </div>
        <div className="space-y-1.5">
          <Label>头像</Label>
          <div className="flex items-center gap-3">
            <span className="flex size-10 items-center justify-center rounded-md border bg-muted/40">
              <AgentAvatar avatar={form.avatar} className="size-5" />
            </span>
            <div className="grid max-w-md grid-cols-9 gap-1">
              {Object.entries(AVATAR_ICONS).map(([name, Icon]) => (
                <button
                  key={name}
                  type="button"
                  title={name}
                  onClick={() => patch({ avatar: name })}
                  className={cn(
                    "flex size-7 items-center justify-center rounded-md border text-muted-foreground hover:bg-accent hover:text-foreground",
                    form.avatar === name && "border-primary bg-accent text-foreground ring-1 ring-primary",
                  )}
                >
                  <Icon className="size-4" />
                </button>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

function ModelSection({
  form,
  patch,
  models,
}: {
  form: AgentBody
  patch: (p: Partial<AgentBody>) => void
  models: OrgModel[]
}) {
  const params = form.model_params ?? {}
  const temperature = params.temperature ?? 0.7
  return (
    <div>
      <SectionTitle title="模型" />
      <div className="space-y-4">
        <div className="space-y-1.5">
          <Label>模型</Label>
          <Select value={form.model_id} onValueChange={(v) => patch({ model_id: v })}>
            <SelectTrigger>
              <SelectValue placeholder="选择模型" />
            </SelectTrigger>
            <SelectContent>
              {models.map((m) => (
                <SelectItem key={m.id} value={m.id}>
                  {m.display_name} · {m.provider_name} · {Math.round(m.context_size / 1000)}k 上下文
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-1.5">
          <Label>temperature：{temperature.toFixed(1)}</Label>
          <div className="flex items-center gap-3">
            <input
              type="range"
              min={0}
              max={2}
              step={0.1}
              value={temperature}
              onChange={(e) =>
                patch({ model_params: { ...params, temperature: Number(e.target.value) } })
              }
              className="h-1.5 w-64 cursor-pointer accent-primary"
            />
            <Input
              type="number"
              min={0}
              max={2}
              step={0.1}
              value={temperature}
              onChange={(e) => {
                const v = Number(e.target.value)
                if (!Number.isNaN(v)) {
                  patch({ model_params: { ...params, temperature: Math.min(2, Math.max(0, v)) } })
                }
              }}
              className="h-8 w-24"
            />
          </div>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="agent-max-tokens">max_tokens</Label>
          <Input
            id="agent-max-tokens"
            type="number"
            min={1}
            value={params.max_tokens ?? ""}
            onChange={(e) => {
              const v = e.target.value === "" ? undefined : Number(e.target.value)
              patch({ model_params: { ...params, max_tokens: v } })
            }}
            placeholder="留空使用模型默认"
            className="w-48"
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="agent-max-iterations">max_iterations</Label>
          <Input
            id="agent-max-iterations"
            type="number"
            min={1}
            value={form.max_iterations}
            onChange={(e) => {
              const v = Number(e.target.value)
              if (!Number.isNaN(v) && v > 0) patch({ max_iterations: v })
            }}
            className="w-48"
          />
        </div>
      </div>
    </div>
  )
}

function PromptSection({ form, patch }: { form: AgentBody; patch: (p: Partial<AgentBody>) => void }) {
  const [fullscreen, setFullscreen] = useState(false)
  const ref = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    const el = ref.current
    if (!el || fullscreen) return
    el.style.height = "auto"
    el.style.height = `${Math.min(Math.max(el.scrollHeight, 160), 560)}px`
  }, [form.system_prompt, fullscreen])

  const editor = (className: string, textareaRef?: React.Ref<HTMLTextAreaElement>) => (
    <Textarea
      ref={textareaRef}
      value={form.system_prompt}
      onChange={(e) => patch({ system_prompt: e.target.value })}
      placeholder="系统提示词（发布时不能为空）"
      className={cn("font-mono text-[13px] leading-relaxed", className)}
    />
  )

  return (
    <div>
      <SectionTitle title="提示词" />
      <div className="space-y-2">
        <div className="flex justify-end">
          <Button variant="outline" size="sm" onClick={() => setFullscreen(true)}>
            <Maximize2 className="size-3.5" />
            全屏编辑
          </Button>
        </div>
        {editor("min-h-40 resize-none overflow-hidden", ref)}
      </div>
      {fullscreen && (
        <div className="fixed inset-0 z-50 flex flex-col bg-background">
          <div className="flex h-12 shrink-0 items-center justify-between border-b px-4">
            <span className="text-sm font-medium">提示词 · {form.name || "未命名智能体"}</span>
            <Button variant="outline" size="sm" onClick={() => setFullscreen(false)}>
              <Minimize2 className="size-3.5" />
              退出全屏
            </Button>
          </div>
          <div className="min-h-0 flex-1 p-4">{editor("h-full resize-none")}</div>
        </div>
      )}
    </div>
  )
}

function ToolsSection({ form, patch }: { form: AgentBody; patch: (p: Partial<AgentBody>) => void }) {
  return (
    <div>
      <SectionTitle title="工具" hint="沙箱工具开关，默认全开" />
      <div className="divide-y rounded-md border">
        {TOOL_LABELS.map((tool) => (
          <div key={tool.key} className="flex items-center gap-3 px-3 py-2.5">
            <div className="min-w-0 flex-1">
              <div className="font-mono text-sm">{tool.label}</div>
              <div className="text-xs text-muted-foreground">{tool.description}</div>
            </div>
            <Switch
              checked={form.tools_enabled[tool.key]}
              onCheckedChange={(checked) =>
                patch({ tools_enabled: { ...form.tools_enabled, [tool.key]: checked } })
              }
            />
          </div>
        ))}
      </div>
    </div>
  )
}

function SkillsSection({
  form,
  patch,
  skills,
}: {
  form: AgentBody
  patch: (p: Partial<AgentBody>) => void
  skills: SkillSummary[]
}) {
  const byId = new Map(skills.map((s) => [s.id, s]))
  const selected = form.skill_ids.map((sid) => byId.get(sid)).filter((s): s is SkillSummary => !!s)
  const unselected = skills.filter((s) => !form.skill_ids.includes(s.id))

  function move(index: number, delta: number) {
    const ids = [...form.skill_ids]
    const target = index + delta
    if (target < 0 || target >= ids.length) return
    ;[ids[index], ids[target]] = [ids[target], ids[index]]
    patch({ skill_ids: ids })
  }

  return (
    <div>
      <SectionTitle title="Skills" hint="选择并排序，顺序即激活优先级" />
      {skills.length === 0 ? (
        <p className="text-sm text-muted-foreground">本机构暂无 Skills，可先在 Skills 管理页上传。</p>
      ) : (
        <div className="space-y-4">
          {selected.length > 0 && (
            <div className="divide-y rounded-md border">
              {selected.map((skill, i) => (
                <div key={skill.id} className="flex items-center gap-2 px-3 py-2">
                  <span className="w-5 text-center text-xs text-muted-foreground">{i + 1}</span>
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-mono text-sm">{skill.name}</div>
                    {skill.description && (
                      <div className="truncate text-xs text-muted-foreground">{skill.description}</div>
                    )}
                  </div>
                  <Button variant="ghost" size="icon" className="size-7" disabled={i === 0} onClick={() => move(i, -1)}>
                    <ArrowUp className="size-3.5" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-7"
                    disabled={i === selected.length - 1}
                    onClick={() => move(i, 1)}
                  >
                    <ArrowDown className="size-3.5" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-7"
                    onClick={() => patch({ skill_ids: form.skill_ids.filter((sid) => sid !== skill.id) })}
                  >
                    <X className="size-3.5" />
                  </Button>
                </div>
              ))}
            </div>
          )}
          {unselected.length > 0 && (
            <div className="divide-y rounded-md border border-dashed">
              {unselected.map((skill) => (
                <div key={skill.id} className="flex items-center gap-2 px-3 py-2">
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-mono text-sm text-muted-foreground">{skill.name}</div>
                    {skill.description && (
                      <div className="truncate text-xs text-muted-foreground">{skill.description}</div>
                    )}
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => patch({ skill_ids: [...form.skill_ids, skill.id] })}
                  >
                    <Plus className="size-3.5" />
                    添加
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function SubAgentsSection({
  form,
  patch,
  candidates,
}: {
  form: AgentBody
  patch: (p: Partial<AgentBody>) => void
  candidates: AdminAgentSummary[]
}) {
  function toggle(agentId: string) {
    patch({
      sub_agent_ids: form.sub_agent_ids.includes(agentId)
        ? form.sub_agent_ids.filter((a) => a !== agentId)
        : [...form.sub_agent_ids, agentId],
    })
  }
  return (
    <div>
      <SectionTitle title="子智能体" hint="通过 task 工具委派；子智能体不能再有下级" />
      {candidates.length === 0 ? (
        <p className="text-sm text-muted-foreground">本机构暂无其他已发布的智能体可作为子智能体。</p>
      ) : (
        <div className="divide-y rounded-md border">
          {candidates.map((agent) => {
            const checked = form.sub_agent_ids.includes(agent.id)
            return (
              <button
                key={agent.id}
                type="button"
                onClick={() => toggle(agent.id)}
                className="flex w-full items-center gap-3 px-3 py-2 text-left hover:bg-accent/50"
              >
                <span
                  className={cn(
                    "flex size-4 shrink-0 items-center justify-center rounded-sm border",
                    checked && "border-primary bg-primary text-primary-foreground",
                  )}
                >
                  {checked && <Check className="size-3" />}
                </span>
                <AgentAvatar avatar={agent.avatar} className="size-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm">{agent.name}</div>
                  {agent.description && (
                    <div className="truncate text-xs text-muted-foreground">{agent.description}</div>
                  )}
                </div>
              </button>
            )
          })}
        </div>
      )}
    </div>
  )
}

function SandboxSection({
  form,
  patch,
  images,
  defaultResources,
}: {
  form: AgentBody
  patch: (p: Partial<AgentBody>) => void
  images: string[] | null
  defaultResources: ResourceLimits | null
}) {
  const resources = form.sandbox_config.resources ?? {}
  const setResources = (r: { cpu?: string; memory?: string }) =>
    patch({ sandbox_config: { ...form.sandbox_config, resources: r } })

  return (
    <div>
      <SectionTitle title="沙箱" hint="留空使用平台默认配置" />
      <div className="space-y-4">
        <div className="space-y-1.5">
          <Label>镜像</Label>
          {images !== null && images.length > 0 ? (
            <Select
              value={form.sandbox_config.image || "__default__"}
              onValueChange={(v) =>
                patch({ sandbox_config: { ...form.sandbox_config, image: v === "__default__" ? "" : v } })
              }
            >
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">平台默认</SelectItem>
                {images.map((img) => (
                  <SelectItem key={img} value={img}>
                    {img}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            <Input
              value={form.sandbox_config.image}
              onChange={(e) => patch({ sandbox_config: { ...form.sandbox_config, image: e.target.value } })}
              placeholder="留空使用平台默认镜像"
              className="font-mono"
            />
          )}
        </div>
        <div className="flex gap-4">
          <div className="space-y-1.5">
            <Label htmlFor="agent-cpu">CPU</Label>
            <Input
              id="agent-cpu"
              value={resources.cpu ?? ""}
              onChange={(e) => setResources({ ...resources, cpu: e.target.value })}
              placeholder={defaultResources?.cpu ? `默认 ${defaultResources.cpu}` : "如 1"}
              className="w-40 font-mono"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="agent-memory">内存</Label>
            <Input
              id="agent-memory"
              value={resources.memory ?? ""}
              onChange={(e) => setResources({ ...resources, memory: e.target.value })}
              placeholder={defaultResources?.memory ? `默认 ${defaultResources.memory}` : "如 1Gi"}
              className="w-40 font-mono"
            />
          </div>
        </div>
      </div>
    </div>
  )
}
