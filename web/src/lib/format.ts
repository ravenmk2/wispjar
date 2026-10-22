export function formatDateTime(s?: string | null): string {
  if (!s) return "-"
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  return d.toLocaleString("zh-CN", { hour12: false })
}

export function formatNumber(n: number): string {
  return n.toLocaleString("zh-CN")
}

export function formatCompactNumber(n: number): string {
  const trim = (v: number) => v.toFixed(1).replace(/\.0$/, "")
  if (n < 1000) return `${n}`
  if (n < 1_000_000) return `${trim(n / 1000)}k`
  return `${trim(n / 1_000_000)}M`
}

export function formatMessageTime(t?: number | null): string {
  if (!t) return ""
  const d = new Date(t)
  if (Number.isNaN(d.getTime())) return ""
  const hh = String(d.getHours()).padStart(2, "0")
  const mm = String(d.getMinutes()).padStart(2, "0")
  return `${d.getMonth() + 1}月${d.getDate()}日 ${hh}:${mm}`
}

export function passwordValid(pw: string): boolean {
  return pw.length >= 8 && /[A-Za-z]/.test(pw) && /\d/.test(pw)
}

export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall through to the legacy path
  }
  try {
    const ta = document.createElement("textarea")
    ta.value = text
    ta.style.position = "fixed"
    ta.style.opacity = "0"
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand("copy")
    ta.remove()
    return ok
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

export function formatCompactTime(s?: string | null): string {
  if (!s) return "-"
  const d = new Date(s)
  if (Number.isNaN(d.getTime())) return s
  const diff = Date.now() - d.getTime()
  if (diff < 60_000) return "刚刚"
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}分`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}时`
  if (diff < 7 * 86_400_000) return `${Math.floor(diff / 86_400_000)}天`
  return `${d.getMonth() + 1}月${d.getDate()}日`
}

export function formatBytes(n?: number | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return "-"
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`
  if (n < 1024 ** 4) return `${(n / 1024 ** 3).toFixed(1)} GB`
  return `${(n / 1024 ** 4).toFixed(1)} TB`
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
