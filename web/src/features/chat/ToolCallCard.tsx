import {
  ChevronDown,
  ChevronRight,
  FileDiff,
  FileText,
  Loader2,
  Puzzle,
  Search,
  SquareTerminal,
  Users,
  Wrench,
  XCircle,
} from "lucide-react"
import { useState, type ReactNode } from "react"
import { Badge } from "@/components/ui/badge"
import { DiffBlock } from "@/components/Markdown"
import { NodeList } from "@/features/chat/NodeList"
import { safeParseJson } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { ToolCallNode } from "@/lib/conversation"
import type { ToolRender } from "@/lib/types"

const TOOL_ICONS: Record<string, ReactNode> = {
  exec: <SquareTerminal className="size-3.5" />,
  read: <FileText className="size-3.5" />,
  write: <FileDiff className="size-3.5" />,
  edit: <FileDiff className="size-3.5" />,
  glob: <Search className="size-3.5" />,
  grep: <Search className="size-3.5" />,
  task: <Users className="size-3.5" />,
  activate_skill: <Puzzle className="size-3.5" />,
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
    return (
      <Badge variant="secondary" className="gap-1 text-[11px] font-normal">
        <Loader2 className="size-3 animate-spin" />
        执行中
      </Badge>
    )
  }
  const duration = node.result.meta?.duration_ms
  if (duration !== undefined) {
    return <span className="text-[11px] text-muted-foreground">{(duration / 1000).toFixed(1)}s</span>
  }
  return null
}

function TerminalBody({ node, liveOutput }: { node: ToolCallNode; liveOutput?: string }) {
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
  return (
    <div className="overflow-hidden rounded-md border bg-zinc-950 text-zinc-100">
      <div className="flex items-center gap-2 border-b border-zinc-800 px-3 py-1.5">
        <span className="text-zinc-500">$</span>
        <code className="flex-1 truncate font-mono text-xs">{command}</code>
        {exitCode !== null && (
          <Badge
            variant={exitCode === 0 ? "secondary" : "destructive"}
            className="font-mono text-[11px] font-normal"
          >
            exit {exitCode}
          </Badge>
        )}
      </div>
      {(stdout || stderr || !node.result) && (
        <pre className="max-h-72 overflow-auto whitespace-pre-wrap px-3 py-2 font-mono text-xs leading-relaxed">
          {stdout}
          {stderr && <span className="text-red-400">{stderr}</span>}
          {!node.result && <span className="ml-0.5 inline-block h-3 w-1.5 animate-pulse bg-zinc-400 align-text-bottom" />}
        </pre>
      )}
    </div>
  )
}

function DiffBody({ node }: { node: ToolCallNode }) {
  const args = safeParseJson(node.arguments)
  const path = typeof args?.path === "string" ? args.path : undefined
  const diff = node.result?.meta?.diff
  return (
    <div>
      {path && <div className="mb-1 font-mono text-xs text-muted-foreground">{path}</div>}
      {diff ? (
        <DiffBlock diff={diff} className="max-h-80 overflow-y-auto" />
      ) : node.result ? (
        <pre className="max-h-60 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/40 p-3 font-mono text-xs">
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
    <div>
      {query && <div className="mb-1 font-mono text-xs text-muted-foreground">/{query}/</div>}
      {node.result ? (
        lines.length > 0 ? (
          <div className="max-h-72 overflow-y-auto rounded-md border">
            {lines.map((line, i) => (
              <div key={i} className="border-b px-3 py-1 font-mono text-xs last:border-b-0 hover:bg-accent">
                {line}
              </div>
            ))}
          </div>
        ) : (
          <div className="text-xs text-muted-foreground">无匹配结果</div>
        )
      ) : null}
    </div>
  )
}

function ReadBody({ node }: { node: ToolCallNode }) {
  const args = safeParseJson(node.arguments)
  const path = typeof args?.path === "string" ? args.path : undefined
  return (
    <div>
      {path && <div className="mb-1 font-mono text-xs text-muted-foreground">{path}</div>}
      {node.result && (
        <pre className="max-h-80 overflow-auto whitespace-pre rounded-md border bg-muted/40 p-3 font-mono text-xs leading-relaxed">
          {node.result.error ?? node.result.content}
        </pre>
      )}
    </div>
  )
}

function GenericBody({ node }: { node: ToolCallNode }) {
  return (
    <div className="space-y-2">
      {node.arguments && (
        <pre className="max-h-40 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/40 p-3 font-mono text-xs">
          {node.arguments}
        </pre>
      )}
      {node.result && (
        <pre className="max-h-60 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/40 p-3 font-mono text-xs">
          {node.result.error ?? node.result.content}
        </pre>
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
  const render = node.result?.meta?.render ?? defaultRender(node.name)
  const [open, setOpen] = useState(!isTask && render !== "generic")
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
    return (args?.path ?? args?.pattern ?? "") as string
  })()

  return (
    <div className="rounded-md border">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left hover:bg-accent/50"
      >
        <span className="text-muted-foreground">{TOOL_ICONS[node.name] ?? <Wrench className="size-3.5" />}</span>
        <span className="text-xs font-medium">{node.name}</span>
        {summary && <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground">{summary}</span>}
        {!summary && <span className="flex-1" />}
        <StatusBadge node={node} />
        {open ? (
          <ChevronDown className="size-3.5 text-muted-foreground" />
        ) : (
          <ChevronRight className="size-3.5 text-muted-foreground" />
        )}
      </button>
      {open && (
        <div className="border-t px-2.5 py-2">
          {render === "terminal" && <TerminalBody node={node} liveOutput={liveOutput} />}
          {render === "diff" && <DiffBody node={node} />}
          {render === "search" && <SearchBody node={node} />}
          {render === "read" && <ReadBody node={node} />}
          {render === "generic" && <GenericBody node={node} />}
          {node.children.length > 0 && (
            <div className={cn("mt-2 border-l-2 pl-3", depth > 3 && "border-l")}>
              <NodeList nodes={node.children} sessionId={sessionId} depth={depth + 1} />
            </div>
          )}
        </div>
      )}
      {!open && node.children.length > 0 && (
        <div className="border-t px-2.5 py-1 text-[11px] text-muted-foreground">
          {node.children.length} 条嵌套轨迹
        </div>
      )}
    </div>
  )
}
