import { Waypoints } from "lucide-react"
import { DisclosureChevron, DisclosureContent, useDisclosureMotion } from "@/components/DisclosureMotion"
import { NodeView } from "@/features/chat/NodeList"
import { ActivityIcon } from "@/features/chat/ActivityIcon"
import { activeProcessNode, reasoningTail, type ProcessGroup } from "@/lib/turn-grouping"
import { safeParseJson } from "@/lib/format"
import { toolDisplayName } from "@/lib/tool-names"
import { useChatDisclosure } from "@/stores/chatPresentation"
import { cn } from "@/lib/utils"

export function TurnProcessGroup({ group, sessionId, latestTurn }: {
  group: ProcessGroup; sessionId: string; latestTurn: number
}) {
  const [open, setOpen] = useChatDisclosure(sessionId, group.id)
  const expanded = open || group.hasPending
  const motion = useDisclosureMotion()
  const tools = group.nodes.filter((n) => n.kind === "tool-call")
  const reasoning = group.nodes.filter((n) => n.kind === "assistant")
  if (group.nodes.length === 1 && reasoning.length === 1) {
    return <NodeView node={reasoning[0]} sessionId={sessionId} depth={0} latestTurn={latestTurn} />
  }

  const activity = activeProcessNode(group)
  const thinking = activity?.kind === "assistant" ? activity.content.find((b) => b.type === "reasoning" && b.streaming) : undefined
  const working = activity?.kind === "tool-call" ? activity : undefined
  let label = tools.length ? `已使用工具 ${tools.length} 次` : reasoning.length ? "思考" : "工具调用"
  let detail = ""
  if (!expanded && thinking?.type === "reasoning") {
    label = "思考中"
    detail = reasoningTail(thinking.text) || "正在思考…"
  } else if (!expanded && working) {
    label = working.name === "skill" || working.name === "activate_skill" ? "技能" : `正在${toolDisplayName(working.name)}`
    const args = safeParseJson(working.arguments)
    detail = String(args?.name ?? args?.command ?? args?.path ?? args?.pattern ?? "")
  }
  const active = !!thinking || !!working

  return (
    <div data-process-group={group.id}>
      <button type="button" onClick={(event) => motion.toggle(event, () => setOpen(!expanded))} disabled={group.hasPending}
        aria-expanded={expanded} aria-label={`${expanded ? "收起" : "展开"}${label}${detail ? ` · ${detail}` : ""}`}
        className="chat-process-trigger flex min-h-9 w-full items-center gap-1.5 rounded-md text-left text-chat text-fg-process hover:bg-accent/50 disabled:cursor-default">
        {!expanded && activity ? <ActivityIcon name={activity.kind === "assistant" ? "reasoning" : activity.name} active />
          : tools.length > 0 && <Waypoints aria-hidden="true" className="chat-activity-icon" strokeWidth={1.65} />}
        <span className={cn("shrink-0", active && "text-shimmer")}>{label}</span>
        {detail && <>
          <span className="shrink-0">·</span>
          <span className={cn("min-w-0 overflow-hidden whitespace-nowrap text-ellipsis", !expanded && thinking && "[direction:rtl] text-left")} title={detail}><bdi>{detail}</bdi></span>
        </>}
        <DisclosureChevron open={expanded} revision={motion.revision} />
      </button>
      <DisclosureContent open={expanded} revision={motion.revision}><div className="space-y-1 py-1">
        {group.nodes.map((node) => <NodeView key={node.id} node={node} sessionId={sessionId} depth={0} latestTurn={latestTurn} />)}
      </div></DisclosureContent>
    </div>
  )
}
