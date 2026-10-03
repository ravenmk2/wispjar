import {
  Bot,
  ChevronDown,
  ChevronRight,
  FilePen,
  FileText,
  Image,
  Loader2,
  MessageCircleQuestion,
  Puzzle,
  Search,
  SquareTerminal,
  Wrench,
  XCircle,
} from "lucide-react"
import { useState, type ReactNode } from "react"
import { Badge } from "@/components/ui/badge"
import { CopyIconButton } from "@/components/CopyIconButton"
import { DiffBlock } from "@/components/Markdown"
import { NodeList } from "@/features/chat/NodeList"
import { MessageImages } from "@/features/chat/MessageImages"
import { QuestionAnswerList } from "@/features/chat/QuestionCard"
import { safeParseJson } from "@/lib/format"
import { hasPendingApproval } from "@/lib/turn-grouping"
import { cn } from "@/lib/utils"
import type { ToolCallNode } from "@/lib/conversation"
import type { ToolRender } from "@/lib/types"

const TOOL_ICONS: Record<string, ReactNode> = {
  exec: <SquareTerminal className="size-3.5" />,
  read: <FileText className="size-3.5" />,
  write: <FilePen className="size-3.5" />,
  edit: <FilePen className="size-3.5" />,
  glob: <Search className="size-3.5" />,
  grep: <Search className="size-3.5" />,
  task: <Bot className="size-3.5" />,
  activate_skill: <Puzzle className="size-3.5" />,
  ask_user_question: <MessageCircleQuestion className="size-3.5" />,
  read_media: <Image className="size-3.5" />,
}

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
  if (node.result?.error) {
    return (
      <Badge variant="destructive" className="gap-1 text-[11px] font-normal">
        <XCircle className="size-3" />
        失败
      </Badge>
    )
  }
  if (!node.result) {
    return <Loader2 className="size-3.5 shrink-0 animate-spin text-fg-tertiary" />
  }
  const duration = node.result.meta?.duration_ms
  if (duration !== undefined) {
    return <span className="shrink-0 text-[11px] text-fg-caption">{(duration / 1000).toFixed(1)}s</span>
  }
  return null
}

const TRUNCATE_AT = 2000

function TerminalBody({ node, liveOutput }: { node: ToolCallNode; liveOutput?: string }) {
  const [showAll, setShowAll] = useState(false)
  const args = safeParseJson(node.arguments)
  const command = typeof args?.command === "string" ? args.command : node.arguments
  let stdout = ""
  let stderr = ""
  let exitCode: number | null = null
  if (node.result) {
    const parsed = safeParseJson(node.result.content)
    if (parsed && ("stdout" in parsed || "exit_code" in parsed)) {
      stdout = typeof parsed.stdout === "string" ? parsed.stdout : ""
      stderr = typeof parsed.stderr === "string" ? parsed.stderr : ""
      exitCode = typeof parsed.exit_code === "number" ? parsed.exit_code : null
    } else {
      stdout = node.result.error ?? node.result.content
    }
  } else if (liveOutput) {
    stdout = liveOutput
  }
  const truncated = !showAll && stdout.length > TRUNCATE_AT
  const visibleStdout = truncated ? stdout.slice(0, TRUNCATE_AT) : stdout
  return (
    <div className="overflow-hidden rounded-lg bg-muted/50">
      <div className="flex items-center gap-2 px-3 py-2">
        <span
          className={cn(
            "size-1.5 shrink-0 rounded-full",
            exitCode === null ? "bg-fg-caption" : exitCode === 0 ? "bg-success" : "bg-destructive",
          )}
        />
        <span className="shrink-0 font-mono text-[13px] text-fg-caption">$</span>
        <code className="min-w-0 flex-1 whitespace-pre-wrap break-all font-mono text-[13px]">{command}</code>
        {exitCode !== null && (
          <Badge
            variant={exitCode === 0 ? "secondary" : "destructive"}
            className="shrink-0 font-mono text-[11px] font-normal"
          >
            exit {exitCode}
          </Badge>
        )}
        <CopyIconButton text={command} />
      </div>
      {(stdout || stderr || !node.result) && (
        <div className="border-t px-3 py-2">
          <pre
            className={cn(
              "whitespace-pre-wrap break-all font-mono text-[13px] leading-relaxed",
              showAll ? "max-h-96 overflow-auto" : "max-h-60 overflow-hidden",
            )}
          >
            {visibleStdout}
            {stderr && <span className="text-destructive">{stderr}</span>}
            {!node.result && (
              <span className="ml-0.5 inline-block h-3 w-1.5 animate-pulse bg-fg-tertiary align-text-bottom" />
            )}
          </pre>
          {stdout.length > TRUNCATE_AT && (
            <button
              type="button"
              onClick={() => setShowAll((v) => !v)}
              className="mt-1 rounded-full bg-muted px-2.5 py-0.5 text-[11px] text-fg-secondary hover:text-foreground"
            >
              {showAll ? "收起" : "查看全部"}
            </button>
          )}
        </div>
      )}
    </div>
  )
}

function DiffBody({ node }: { node: ToolCallNode }) {
  const args = safeParseJson(node.arguments)
  const path = typeof args?.path === "string" ? args.path : undefined
  const diff = node.result?.meta?.diff
  return (
    <div className="rounded-lg bg-muted/50 p-3">
      {path && <div className="mb-1 font-mono text-xs text-fg-secondary">{path}</div>}
      {diff ? (
        <DiffBlock diff={diff} className="max-h-80 overflow-y-auto bg-background" />
      ) : node.result ? (
        <pre className="max-h-60 overflow-auto whitespace-pre-wrap font-mono text-xs">
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
      {query && <div className="mb-1 font-mono text-xs text-fg-secondary">/{query}/</div>}
      {node.result ? (
        lines.length > 0 ? (
          <div className="max-h-72 overflow-y-auto rounded-md border bg-background">
            {lines.map((line, i) => (
              <div key={i} className="border-b px-3 py-1 font-mono text-xs last:border-b-0 hover:bg-accent">
                {line}
              </div>
            ))}
          </div>
        ) : (
          <div className="text-xs text-fg-secondary">无匹配结果</div>
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
      {path && <div className="mb-1 font-mono text-xs text-fg-secondary">{path}</div>}
      {node.result?.attachments && node.result.attachments.length > 0 && (
        <MessageImages attachments={node.result.attachments} className="mb-2" />
      )}
      {node.result && (
        <pre className="max-h-80 overflow-auto whitespace-pre font-mono text-xs leading-relaxed">
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
        <pre className="max-h-40 overflow-auto whitespace-pre-wrap font-mono text-xs">{node.arguments}</pre>
      )}
      {node.result && (
        <pre className="max-h-60 overflow-auto whitespace-pre-wrap font-mono text-xs">
          {node.result.error ?? node.result.content}
        </pre>
      )}
    </div>
  )
}

// ask_user_question：settled 展开为只读问答卡；超时转 continued 提示可补答
function QuestionBody({ node }: { node: ToolCallNode }) {
  const q = node.question!
  const continued = !q.answers && !!node.result && safeParseJson(node.result.content)?.pending === true
  return (
    <div className="rounded-lg bg-muted/50 p-3">
      <QuestionAnswerList questions={q.questions} answers={q.answers} />
      {continued && (
        <div className="mt-2 text-xs text-fg-caption">等待超时，问题保持可补答，可在下方提问卡继续作答</div>
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
  liveOutput?: string
  depth?: number
}) {
  const isTask = node.name === "task"
  const isQuestion = node.name === "ask_user_question" && !!node.question
  const render = node.result?.meta?.render ?? defaultRender(node.name)
  const [open, setOpen] = useState(false)
  const pendingApproval = hasPendingApproval(node.children)
  const pendingQuestion = !!node.question && !node.question.answers
  const expanded = open || pendingApproval || pendingQuestion
  const running = !node.result
  const error = node.result?.error
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
    if (isQuestion) return node.question?.questions[0]?.question ?? ""
    return (args?.path ?? args?.pattern ?? "") as string
  })()

  const stats = node.name === "write" || node.name === "edit" ? diffStats(node.result?.meta?.diff) : null

  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex h-8 w-full items-center gap-1.5 rounded-md px-1 text-left hover:bg-accent/50"
      >
        <span className="shrink-0 text-fg-tertiary">{TOOL_ICONS[node.name] ?? <Wrench className="size-3.5" />}</span>
        <span className="shrink-0 text-[13px] font-medium text-fg-secondary">{node.name}</span>
        {error ? (
          <span className="min-w-0 flex-1 truncate font-mono text-[13px] text-destructive">
            · {error.split("\n")[0]}
          </span>
        ) : summary ? (
          <span
            className={cn(
              "min-w-0 flex-1 truncate font-mono text-[13px] text-fg-tertiary",
              running && "text-shimmer",
            )}
          >
            · {summary}
          </span>
        ) : (
          <span className="flex-1" />
        )}
        {stats && (
          <span className="shrink-0 font-mono text-[11px]">
            {stats.added > 0 && <span className="text-success">+{stats.added}</span>}
            {stats.added > 0 && stats.removed > 0 && " "}
            {stats.removed > 0 && <span className="text-destructive">-{stats.removed}</span>}
          </span>
        )}
        {pendingApproval && (
          <Badge variant="outline" className="shrink-0 gap-1 border-amber-500/50 text-[11px] font-normal text-amber-600 dark:text-amber-400">
            <Loader2 className="size-3 animate-spin" />
            待审批
          </Badge>
        )}
        {pendingQuestion && (
          <Badge variant="outline" className="shrink-0 gap-1 border-amber-500/50 text-[11px] font-normal text-amber-600 dark:text-amber-400">
            待回答
          </Badge>
        )}
        <StatusBadge node={node} />
        {expanded ? (
          <ChevronDown className="size-3.5 shrink-0 text-fg-tertiary" />
        ) : (
          <ChevronRight className="size-3.5 shrink-0 text-fg-tertiary" />
        )}
      </button>
      {expanded && (
        <div className="py-1 pl-1">
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
      )}
      {!expanded && node.children.length > 0 && (
        <div className="py-0.5 pl-7 text-[11px] text-fg-caption">{node.children.length} 条嵌套轨迹</div>
      )}
    </div>
  )
}
