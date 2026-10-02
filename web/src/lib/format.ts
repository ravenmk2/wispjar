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
