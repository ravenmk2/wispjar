import { safeParseJson } from "./format"
import type { ToolCallNode, ToolResultData } from "./conversation"

export interface ToolOutput {
  stdout: string
  stderr: string
}

export function execOutput(result?: ToolResultData, live?: ToolOutput) {
  const parsed = result ? safeParseJson(result.content) : null
  const structured = parsed && ("stdout" in parsed || "stderr" in parsed || "exit_code" in parsed || "error" in parsed)
  const error = result?.error || (typeof parsed?.error === "string" ? parsed.error : undefined)
  let stdout = live?.stdout ?? ""
  let stderr = live?.stderr ?? ""
  if (result) {
    // A cancelled command may return only an error; retain any streamed output.
    stdout = structured ? typeof parsed.stdout === "string" ? parsed.stdout : "" : result.content
    stderr = structured && typeof parsed.stderr === "string" ? parsed.stderr : ""
    if (error) {
      stdout ||= live?.stdout ?? ""
      stderr ||= live?.stderr ?? ""
    }
  }
  return {
    stdout,
    stderr,
    exitCode: typeof parsed?.exit_code === "number" ? parsed.exit_code : null,
    error,
    truncated: result?.meta?.truncated === true || parsed?.truncated === true,
  }
}

export function toolCancelled(node: ToolCallNode): boolean {
  if (node.endReason !== "interrupted") return false
  if (!node.result) return true
  const error = node.name === "exec" ? execOutput(node.result).error : node.result.error
  return !!error && /(?:context|operation) cancel(?:l)?ed|已取消|已中止/i.test(error)
}

export function toolFailure(node: ToolCallNode): string | undefined {
  if (toolCancelled(node)) return undefined
  if (node.name !== "exec") {
    const content = node.result?.content ?? ""
    return node.result?.error || (content.startsWith("Tool execution failed: ") ? content : undefined)
  }
  const output = execOutput(node.result)
  return output.error || (output.exitCode !== null && output.exitCode !== 0 ? `exit ${output.exitCode}` : undefined)
}

export function writeOutput(node: ToolCallNode) {
  const args = safeParseJson(node.arguments)
  const result = node.result ? safeParseJson(node.result.content) : null
  const error = toolFailure(node)
  const completed = !!node.result && !error && typeof result?.bytes === "number" && typeof result?.path === "string"
  const operation = completed ? node.result?.meta?.operation : undefined
  return {
    content: typeof args?.content === "string" ? args.content : undefined,
    diff: completed ? node.result?.meta?.diff : undefined,
    operation: operation === "create" || operation === "update" ? operation : undefined,
    completed,
    error,
  }
}
