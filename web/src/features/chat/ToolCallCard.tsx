import { X } from "lucide-react"
import { ActivityIcon } from "@/features/chat/ActivityIcon"
import { useLayoutEffect, useRef, useState } from "react"
import { Badge } from "@/components/ui/badge"
import { CopyIconButton } from "@/components/CopyIconButton"
import { DisclosureChevron, DisclosureContent, useDisclosureMotion } from "@/components/DisclosureMotion"
import { DiffBlock } from "@/components/Markdown"
import { NodeList } from "@/features/chat/NodeList"
import { MessageImages } from "@/features/chat/MessageImages"
import { QuestionAnswerList } from "@/features/chat/QuestionCard"
import { safeParseJson } from "@/lib/format"
import { isQuestionTool, toolDisplayName } from "@/lib/tool-names"
import { hasPendingApproval } from "@/lib/turn-grouping"
import { execOutput, toolCancelled, toolFailure, type ToolOutput } from "@/lib/tool-output"
import { cn } from "@/lib/utils"
import { useChatDisclosure } from "@/stores/chatPresentation"
import type { ToolCallNode } from "@/lib/conversation"
import type { ToolRender } from "@/lib/types"

function defaultRender(name: string): ToolRender {
  switch (name) {
    case "exec":
      return "terminal"
    case "write":
    case "edit":
      return "diff"
    case "glob":
    case "grep":
      return "search"
    case "read":
      return "read"
    default:
      return "generic"
  }
}

function diffStats(diff?: string): { added: number; removed: number } | null {
  if (!diff) return null
  let added = 0
  let removed = 0
  for (const line of diff.split("\n")) {
    if (line.startsWith("+") && !line.startsWith("+++")) added++
    else if (line.startsWith("-") && !line.startsWith("---")) removed++
  }
  if (added === 0 && removed === 0) return null
  return { added, removed }
}

function StatusBadge({ node }: { node: ToolCallNode }) {
  if (toolCancelled(node) || (!node.result && node.endReason)) {
    return <span className="shrink-0 text-meta text-fg-process-muted">已中止</span>
  }
  const failure = toolFailure(node)
  if (failure) {
    return <span className="shrink-0" title={`调用失败 · ${failure}`}><X className="size-3.5 text-destructive" strokeWidth={2.5} aria-label={`调用失败 · ${failure}`} /></span>
  }
  if (!node.result) {
    return null
  }
  const duration = node.result.meta?.duration_ms
  if (duration !== undefined) {
    return <span className="shrink-0 text-meta text-fg-process-muted">{(duration / 1000).toFixed(1)}s</span>
  }
  return null
}

function TerminalSection({ label, content, running = false }: { label: string; content: string; running?: boolean }) {
  const [expanded, setExpanded] = useState(false)
  const [overflowing, setOverflowing] = useState(false)
  const bodyRef = useRef<HTMLPreElement>(null)
  const followRef = useRef(true)
  useLayoutEffect(() => {
    const el = bodyRef.current
    if (!el) return
    const measure = () => {
      setOverflowing(el.scrollHeight > el.clientHeight)
      if (running && followRef.current) el.scrollTop = el.scrollHeight
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [content, expanded, running])
  return (
    <div className="border-t px-3 py-2">
      <div className="mb-1 flex items-center justify-between gap-2 text-meta text-fg-process-muted">
        <span>{label}</span>
        <CopyIconButton text={content} tooltip={`复制${label}`} className="size-6" />
      </div>
      <pre ref={bodyRef} onScroll={() => {
        const el = bodyRef.current
        if (el && expanded) followRef.current = el.scrollHeight - el.clientHeight - el.scrollTop < 24
      }} className={cn("overflow-x-auto whitespace-pre font-sans tabular-nums text-chat text-fg-process", label === "命令" && "font-mono", expanded ? "max-h-96 overflow-y-auto" : "max-h-[calc(8*var(--chat-line-height))] overflow-y-hidden")}>
        {content}
      </pre>
      {(overflowing || expanded) && <button type="button" aria-expanded={expanded} onClick={() => {
        setExpanded((value) => !value)
        followRef.current = true
      }} className="mt-2 rounded px-1 text-meta text-fg-process hover:bg-accent hover:text-foreground">
        {expanded ? "收起" : "展开全部"}
      </button>}
    </div>
  )
}

function TerminalBody({ node, liveOutput }: { node: ToolCallNode; liveOutput?: ToolOutput }) {
  const args = safeParseJson(node.arguments)
  const command = typeof args?.command === "string" ? args.command : node.arguments
  const cwd = typeof args?.cwd === "string" && args.cwd ? args.cwd : "/workspace"
  const output = execOutput(node.result, liveOutput)
  const running = !node.result && !node.endReason
  const cancelled = toolCancelled(node) || (!node.result && !!node.endReason)
  const failure = toolFailure(node)
  const duration = node.result?.meta?.duration_ms
  return (
    <div className="min-w-0 overflow-hidden rounded-lg bg-muted/50">
      <div className="flex min-w-0 items-center gap-2 px-3 py-2 text-meta text-fg-process-muted">
        <span className="min-w-0 flex-1 truncate font-sans tabular-nums" title={cwd}>{cwd}</span>
        {duration !== undefined && <span className="shrink-0 tabular-nums">{(duration / 1000).toFixed(1)}s</span>}
        {duration !== undefined && output.exitCode !== null && <span>·</span>}
        {output.exitCode !== null && <span className="shrink-0 font-sans tabular-nums">exit {output.exitCode}</span>}
        {running && <span className="shrink-0">运行中</span>}
        {cancelled && <span className="shrink-0">已中止</span>}
        {failure && <X className="size-3.5 shrink-0 text-destructive" strokeWidth={2.5} aria-label="执行失败" />}
      </div>
      <TerminalSection label="命令" content={command} />
      {output.stdout && <TerminalSection label="stdout" content={output.stdout} running={running} />}
      {output.stderr && <TerminalSection label="stderr" content={output.stderr} running={running} />}
      {output.error && <TerminalSection label={cancelled ? "中止信息" : "错误详情"} content={output.error} />}
      {!output.stdout && !output.stderr && !output.error && !cancelled && <div className="border-t px-3 py-2 text-meta text-fg-process-muted">{running ? "等待输出…" : "无输出"}</div>}
      {output.truncated && <div className="border-t px-3 py-2 text-meta text-fg-process-muted">输出已达到服务端保留上限，早期内容已截断</div>}
    </div>
  )
}

function DiffBody({ node }: { node: ToolCallNode }) {
  const args = safeParseJson(node.arguments)
  const path = typeof args?.path === "string" ? args.path : undefined
  const diff = node.result?.meta?.diff
  return (
    <div className="rounded-lg bg-muted/50 p-3">
      {path && <div className="mb-1 font-sans tabular-nums text-chat text-fg-process">{path}</div>}
      {diff ? (
        <DiffBlock diff={diff} className="max-h-80 overflow-y-auto bg-background" />
      ) : node.result ? (
        <pre className="max-h-60 overflow-auto whitespace-pre-wrap font-sans tabular-nums text-chat">
          {node.result.error ?? node.result.content}
        </pre>
      ) : null}
    </div>
  )
}

function SearchBody({ node }: { node: ToolCallNode }) {
  const args = safeParseJson(node.arguments)
  const query = (args?.pattern ?? args?.query ?? "") as string
  const lines = (node.result?.content ?? "").split("\n").filter(Boolean)
  return (
    <div className="rounded-lg bg-muted/50 p-3">
      {query && <div className="mb-1 font-sans tabular-nums text-chat text-fg-process">/{query}/</div>}
      {node.result ? (
        lines.length > 0 ? (
          <div className="max-h-72 overflow-y-auto rounded-md border bg-background">
            {lines.map((line, i) => (
              <div key={i} className="border-b px-3 py-1 font-sans tabular-nums text-chat last:border-b-0 hover:bg-accent">
                {line}
              </div>
            ))}
          </div>
        ) : (
          <div className="text-chat text-fg-process">无匹配结果</div>
        )
      ) : null}
    </div>
  )
}

function ReadBody({ node }: { node: ToolCallNode }) {
  const args = safeParseJson(node.arguments)
  const path = typeof args?.path === "string" ? args.path : undefined
  return (
    <div className="rounded-lg bg-muted/50 p-3">
      {path && <div className="mb-1 font-sans tabular-nums text-chat text-fg-process">{path}</div>}
      {node.result?.attachments && node.result.attachments.length > 0 && (
        <MessageImages attachments={node.result.attachments} className="mb-2" />
      )}
      {node.result && (
        <pre className="max-h-80 overflow-auto whitespace-pre font-sans tabular-nums text-chat">
          {node.result.error ?? node.result.content}
        </pre>
      )}
    </div>
  )
}

function GenericBody({ node }: { node: ToolCallNode }) {
  return (
    <div className="space-y-2 rounded-lg bg-muted/50 p-3">
      {node.arguments && (
        <pre className="max-h-40 overflow-auto whitespace-pre-wrap font-mono text-chat">{node.arguments}</pre>
      )}
      {node.result && (
        <pre className="max-h-60 overflow-auto whitespace-pre-wrap font-sans tabular-nums text-chat">
          {node.result.error ?? node.result.content}
        </pre>
      )}
    </div>
  )
}

// question（旧名 ask_user_question）：settled 展开为只读问答卡；超时转 continued 提示可补答
function QuestionBody({ node }: { node: ToolCallNode }) {
  const q = node.question!
  const continued = !q.answers && !!node.result && safeParseJson(node.result.content)?.pending === true
  return (
    <div className="rounded-lg bg-muted/50 p-3">
      <QuestionAnswerList questions={q.questions} answers={q.answers} />
      {continued && (
        <div className="mt-2 text-xs text-fg-process-muted">等待超时，问题保持可补答，可在下方提问卡继续作答</div>
      )}
    </div>
  )
}

export function ToolCallCard({
  node,
  sessionId,
  liveOutput,
  depth = 0,
}: {
  node: ToolCallNode
  sessionId: string
  liveOutput?: ToolOutput
  depth?: number
}) {
  const isTask = node.name === "task"
  const isQuestion = isQuestionTool(node.name) && !!node.question
  const render = node.result?.meta?.render ?? defaultRender(node.name)
  const [open, setOpen] = useChatDisclosure(sessionId, `tool-${node.callId}`)
  const pendingApproval = !node.endReason && hasPendingApproval(node.children)
  const pendingQuestion = !node.endReason && !!node.question && !node.question.answers
  const expanded = open || pendingApproval || pendingQuestion
  const motion = useDisclosureMotion()
  const running = !node.result && !node.endReason
  const args = safeParseJson(node.arguments)
  const taskTarget =
    isTask && args
      ? ((args.sub_agent_id ?? args.agent_id ?? args.agent ?? "") as string)
      : ""

  const summary = (() => {
    if (isTask) {
      const task = typeof args?.task === "string" ? args.task : typeof args?.description === "string" ? args.description : ""
      return taskTarget ? `委派给 ${taskTarget}${task ? `：${task}` : ""}` : task
    }
    if (node.name === "exec") return (args?.command as string) ?? ""
    if (node.name === "skill" || node.name === "activate_skill") return typeof args?.name === "string" ? args.name : ""
    if (isQuestion) return node.question?.questions[0]?.question ?? ""
    return (args?.path ?? args?.pattern ?? "") as string
  })()

  const stats = node.name === "write" || node.name === "edit" ? diffStats(node.result?.meta?.diff) : null

  return (
    <div className="text-fg-process">
      <button
        type="button"
        aria-expanded={expanded}
        title={summary || undefined}
        onClick={(event) => motion.toggle(event, () => setOpen(!open))}
        className="chat-process-trigger flex min-h-9 w-full items-center gap-1.5 rounded-md text-left hover:bg-accent/50"
      >
        <ActivityIcon name={node.name} active={running && !pendingApproval && !pendingQuestion} />
        <span className="shrink-0 text-chat text-fg-process" title={node.name}>
          {toolDisplayName(node.name)}
        </span>
        {summary ? (
          <span
            className={cn(
              "min-w-0 flex-1 truncate font-sans tabular-nums text-chat text-fg-process",
              running && !pendingApproval && !pendingQuestion && "text-shimmer",
            )}
          >
            · {summary}
          </span>
        ) : (
          <span className="flex-1" />
        )}
        {stats && (
          <span className="shrink-0 font-sans tabular-nums text-meta">
            {stats.added > 0 && <span className="text-success">+{stats.added}</span>}
            {stats.added > 0 && stats.removed > 0 && " "}
            {stats.removed > 0 && <span className="text-destructive">-{stats.removed}</span>}
          </span>
        )}
        {pendingApproval && (
          <Badge variant="outline" className="shrink-0 gap-1 border-amber-500/50 text-meta font-normal text-amber-600 dark:text-amber-400">
            待审批
          </Badge>
        )}
        {pendingQuestion && (
          <Badge variant="outline" className="shrink-0 gap-1 border-amber-500/50 text-meta font-normal text-amber-600 dark:text-amber-400">
            待回答
          </Badge>
        )}
        <StatusBadge node={node} />
        <DisclosureChevron open={expanded} revision={motion.revision} className="text-fg-process-muted" />
      </button>
      <DisclosureContent open={expanded} revision={motion.revision}>
        <div className="py-1">
          {isQuestion && <QuestionBody node={node} />}
          {!isQuestion && render === "terminal" && <TerminalBody node={node} liveOutput={liveOutput} />}
          {!isQuestion && render === "diff" && <DiffBody node={node} />}
          {!isQuestion && render === "search" && <SearchBody node={node} />}
          {!isQuestion && render === "read" && <ReadBody node={node} />}
          {!isQuestion && render === "generic" && <GenericBody node={node} />}
          {node.children.length > 0 && (
            <div className={cn("mt-2 border-l-2 pl-3", depth > 3 && "border-l")}>
              <NodeList nodes={node.children} sessionId={sessionId} depth={depth + 1} />
            </div>
          )}
        </div>
      </DisclosureContent>
      {!expanded && node.children.length > 0 && (
        <div className="py-0.5 text-meta text-fg-process-muted">{node.children.length} 条嵌套轨迹</div>
      )}
    </div>
  )
}
