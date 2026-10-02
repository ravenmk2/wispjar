export function formatDateTime(s?: string | null): string {
  if (!s) return "-"
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  return d.toLocaleString("zh-CN", { hour12: false })
}

export function formatNumber(n: number): string {
  return n.toLocaleString("zh-CN")
}

export function passwordValid(pw: string): boolean {
  return pw.length >= 8 && /[A-Za-z]/.test(pw) && /\d/.test(pw)
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

export function formatRelativeTime(s?: string | null): string {
  if (!s) return "-"
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const diff = Date.now() - d.getTime()
  if (diff < 60_000) return "刚刚"
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`
  if (diff < 7 * 86_400_000) return `${Math.floor(diff / 86_400_000)} 天前`
  return d.toLocaleDateString("zh-CN")
}

export function formatBytes(n?: number | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return "-"
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

export function safeParseJson(text: string): Record<string, unknown> | null {
  try {
    const v = JSON.parse(text) as unknown
    if (v && typeof v === "object" && !Array.isArray(v)) return v as Record<string, unknown>
    return null
  } catch {
    return null
  }
}
