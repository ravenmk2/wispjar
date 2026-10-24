import type { Computer } from "./computers"
import type { SandboxFileItem } from "./types"

export function fileParent(path: string): string { return path.includes("/") ? path.slice(0, path.lastIndexOf("/")) : "." }
export function fileAncestors(path: string): string[] {
  if (path === ".") return ["."]
  const parts = path.split("/")
  return [".", ...parts.map((_, i) => parts.slice(0, i + 1).join("/"))]
}
export function sortFiles(items: SandboxFileItem[], ascending = true): SandboxFileItem[] {
  return [...items].sort((a, b) => a.type !== b.type ? (a.type === "dir" ? -1 : 1) : (ascending ? 1 : -1) * a.name.localeCompare(b.name, "zh-CN", { numeric: true }))
}
export function accessComputers(computers: Computer[]): Computer[] {
  const rank: Record<string, number> = { running: 0, starting: 1, paused: 2, stopped: 3 }
  return computers.filter(c => c.status in rank).sort((a, b) => rank[a.status] - rank[b.status] || a.name.localeCompare(b.name))
}
export function validFolderName(name: string): boolean { return !!name.trim() && name !== "." && name !== ".." && !/[\\/\x00-\x1f]/.test(name) }
