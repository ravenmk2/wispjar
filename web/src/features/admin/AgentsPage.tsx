import { Bot, Plus } from "lucide-react"
import { useCallback, useEffect, useState } from "react"
import { Link, useNavigate } from "react-router"
import { toast } from "sonner"
import { adminAgentsApi } from "@/api/agents"
import { ApiError, errorMessage } from "@/api/client"
import { ConfirmDialog, EmptyState, PageHeader, TableSkeleton } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { AgentAvatar, AVATAR_ICONS } from "@/features/agents/AgentAvatar"
import { formatDateTime } from "@/lib/format"
import type { AdminAgentSummary, AgentStatus } from "@/lib/types"
import { useIdentityStore } from "@/stores/identity"

export const AGENT_STATUS_LABEL: Record<AgentStatus, string> = {
  draft: "草稿",
  published: "已发布",
  archived: "已下线",
}

export function AgentStatusBadge({ status }: { status: AgentStatus }) {
  if (status === "published") return <Badge>{AGENT_STATUS_LABEL[status]}</Badge>
  if (status === "archived") return <Badge variant="outline">{AGENT_STATUS_LABEL[status]}</Badge>
  return <Badge variant="secondary">{AGENT_STATUS_LABEL[status]}</Badge>
}

export default function AgentsPage() {
  const currentOrgId = useIdentityStore((s) => s.currentOrgId)
  const navigate = useNavigate()
  const [agents, setAgents] = useState<AdminAgentSummary[] | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<AdminAgentSummary | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [actingId, setActingId] = useState<string | null>(null)

  const load = useCallback(async () => {
    if (!currentOrgId) return
    try {
      const res = await adminAgentsApi.list(currentOrgId, { limit: 100 })
      setAgents(res.items)
    } catch (e) {
      toast.error(errorMessage(e))
      setAgents([])
    }
  }, [currentOrgId])

  useEffect(() => {
    setAgents(null)
    load()
  }, [load])

  async function publish(agent: AdminAgentSummary) {
    if (!currentOrgId || actingId) return
    setActingId(agent.id)
    try {
      await adminAgentsApi.publish(currentOrgId, agent.id)
      toast.success(`「${agent.name}」已发布`)
      load()
    } catch (e) {
      toast.error(e instanceof ApiError && e.details ? `${e.message}：${JSON.stringify(e.details)}` : errorMessage(e))
    } finally {
      setActingId(null)
    }
  }

  async function archive(agent: AdminAgentSummary) {
    if (!currentOrgId || actingId) return
    setActingId(agent.id)
    try {
      await adminAgentsApi.archive(currentOrgId, agent.id)
      toast.success(`「${agent.name}」已下线`)
      load()
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setActingId(null)
    }
  }

  async function remove() {
    if (!currentOrgId || !deleteTarget) return
    setDeleting(true)
    try {
      await adminAgentsApi.remove(currentOrgId, deleteTarget.id)
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
      <PageHeader title="智能体" description="本机构全部智能体（含草稿与已下线）">
        <Button size="sm" onClick={() => navigate("/admin/agents/new")}>
          <Plus className="size-4" />
          新建智能体
        </Button>
      </PageHeader>

      {agents === null ? (
        <TableSkeleton />
      ) : agents.length === 0 ? (
        <EmptyState
          icon={<Bot className="size-10" />}
          title="暂无智能体"
          description="创建一个智能体，配置模型、提示词、工具与 Skills"
          action={
            <Button asChild size="sm">
              <Link to="/admin/agents/new">新建智能体</Link>
            </Button>
          }
        />
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>名称</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>模型</TableHead>
                <TableHead>Skills</TableHead>
                <TableHead>子智能体</TableHead>
                <TableHead>更新时间</TableHead>
                <TableHead className="w-52">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {agents.map((agent) => (
                <TableRow key={agent.id}>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      <span className="flex size-7 shrink-0 items-center justify-center rounded-md border bg-muted/40">
                        {agent.avatar && AVATAR_ICONS[agent.avatar] ? (
                          <AgentAvatar avatar={agent.avatar} className="size-4" />
                        ) : (
                          <span className="text-xs">{agent.avatar || <Bot className="size-4" />}</span>
                        )}
                      </span>
                      <div className="min-w-0">
                        <Link
                          to={`/admin/agents/${agent.id}`}
                          className="block truncate font-medium hover:underline"
                        >
                          {agent.name}
                        </Link>
                        {agent.description && (
                          <div className="max-w-64 truncate text-xs text-muted-foreground">{agent.description}</div>
                        )}
                      </div>
                    </div>
                  </TableCell>
                  <TableCell>
                    <AgentStatusBadge status={agent.status} />
                  </TableCell>
                  <TableCell className="text-muted-foreground">{agent.model_display_name || agent.model_id || "-"}</TableCell>
                  <TableCell className="text-muted-foreground">{agent.skill_count ?? "-"}</TableCell>
                  <TableCell className="text-muted-foreground">{agent.sub_agent_count ?? "-"}</TableCell>
                  <TableCell className="text-muted-foreground">{formatDateTime(agent.updated_at)}</TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1">
                      <Button variant="ghost" size="sm" asChild>
                        <Link to={`/admin/agents/${agent.id}`}>编辑</Link>
                      </Button>
                      {agent.status !== "published" ? (
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={actingId === agent.id}
                          onClick={() => void publish(agent)}
                        >
                          发布
                        </Button>
                      ) : (
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={actingId === agent.id}
                          onClick={() => void archive(agent)}
                        >
                          下线
                        </Button>
                      )}
                      {agent.status !== "published" && (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-destructive hover:text-destructive"
                          onClick={() => setDeleteTarget(agent)}
                        >
                          删除
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

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="删除智能体"
        description={`确定删除「${deleteTarget?.name}」？有会话引用时将自动转为下线。`}
        confirmText="删除"
        destructive
        loading={deleting}
        onConfirm={remove}
      />
    </div>
  )
}
