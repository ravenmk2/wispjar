export interface Computer {
  id: string
  org_id: string
  name: string
  status: string
  generation: number
  config: { image: string; resources: Record<string, string>; ttl_seconds: number; has_env: boolean }
  last_error: string
  error_detail?: ComputerErrorDetail | null
  last_active_at: string | null
  created_at: string
}

export interface ComputerConfig {
  image?: string
  resources?: Record<string, string>
  env?: Record<string, string>
  ttl_seconds?: number
}
export interface ComputerErrorDetail { code: string; message: string; suggestion: string; operation_id?: string }
export interface ComputerOperation { id: string; resource_id: string; action: string; status: string; error: string; error_detail?: ComputerErrorDetail | null }
export interface OperationAccepted { operation_id: string; resource_id: string; status: string }
export interface ComputerRun { id: string; session_id: string; agent_id: string; parent_run_id: string | null; status: string; lease_until: string | null }
export interface AgentBinding { agent_id: string; computer_id: string }
export interface UserVolume { id: string; kind: string; status: string; last_error: string; policy: Record<string, unknown>; usage: Record<string, unknown> }

const names: Record<string, string> = {
  stopped: "未启动", starting: "正在启动", running: "运行中", pausing: "正在暂停",
  paused: "已暂停", rebuilding: "正在重建", recovering: "正在强制停止", deleting: "正在删除", deleted: "已删除", failed: "失败",
  pending: "等待处理", completed: "已完成", ready: "可用", provisioning: "正在准备", missing: "卷缺失", error: "异常",
}
export function computerStatus(status: string): string { return names[status] ?? status }
export function computerTransitioning(status: string): boolean { return ["starting", "pausing", "rebuilding", "deleting", "recovering"].includes(status) }
export function selectableComputer(computer: Computer): boolean { return !["deleted", "deleting", "recovering"].includes(computer.status) }
export function resolveComputerSelection(computers: Computer[], bindings: AgentBinding[], agentId: string): string {
  const bound = bindings.find((b) => b.agent_id === agentId)?.computer_id
  return computers.some((c) => c.id === bound && selectableComputer(c)) ? bound! : ""
}
export function canMutateComputer(computer: Computer, runs: ComputerRun[]): boolean {
  return !computerTransitioning(computer.status) && computer.status !== "deleted" && runs.length === 0
}
export function workspaceRoot(sessionId: string): string { return `/sessions/${sessionId}` }

export function computerFailure(computer: Pick<Computer, "last_error" | "error_detail">): ComputerErrorDetail | null {
  if (computer.error_detail) return computer.error_detail
  if (!computer.last_error) return null
  return {
    code: "computer/backend_failed",
    message: "云电脑上次操作失败，旧记录未保存具体原因。",
    suggestion: "请联系管理员，提供电脑名称和失败时间排查；已有持久文件继续保留。",
  }
}

export function computerOperationError(operation: ComputerOperation): string {
  const detail = computerFailure({ last_error: operation.error, error_detail: operation.error_detail })
  return detail ? `${detail.message} ${detail.suggestion}` : "电脑操作失败，请联系管理员检查。"
}

export const computerErrorMessages: Record<string, string> = {
  "computer/required": "请在「个人中心 / 智能体」配置默认云电脑，也可从侧边栏智能体的「云电脑设置」快捷配置。",
  "computer/rebuild_required": "这台电脑需要重建以启用执行端认证，持久文件将保留。",
  "computer/busy": "电脑仍有活动任务或正在维护，请在任务完成后重试。",
  "computer/in_use": "电脑仍被会话或智能体默认设置引用，请先切换这些绑定。",
  "computer/unavailable": "云电脑后端未配置或暂时不可用，请联系管理员。",
  "computer/not_found": "电脑不存在或不属于当前账号。",
  "computer/invalid": "配置无效，请检查名称、可用镜像、资源和环境变量。",
  "volume/missing": "持久卷缺失，已停止执行以保护已有数据，请联系管理员恢复。",
  "operation/conflict": "此请求标识已用于其他操作，请刷新后重试。",
  "skill/update_pending": "技能有待更新，正在等待已有任务结束。",
  "skill/sync_failed": "技能同步失败，暂时无法开始任务，请重试或联系管理员。",
}
