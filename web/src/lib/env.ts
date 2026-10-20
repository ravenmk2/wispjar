export interface EnvRow { key: string; value?: string }
export interface EnvPatch { set: Record<string, string>; unset: string[] }

export function envRowsError(rows: EnvRow[]): string | null {
  const keys = new Set<string>()
  for (const row of rows) {
    const key = row.key.trim()
    if (!key) { if (row.value) return "请填写环境变量名"; continue }
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) return `环境变量名 ${key} 不合法`
    if (["WISPJAR", "HOME"].includes(key) || key.toUpperCase().startsWith("EXECD_")) return `${key} 为平台保留变量名`
    if (keys.has(key)) return `环境变量名 ${key} 重复`
    keys.add(key)
    if (row.value?.includes("\0")) return `${key} 的值不能包含 NUL`
    if (new TextEncoder().encode(row.value ?? "").length > 4096) return `${key} 的值不能超过 4096 字节`
  }
  return keys.size > 64 ? "环境变量不能超过 64 项" : null
}

export function envPatch(rows: EnvRow[], originalKeys: string[]): EnvPatch {
  const set: Record<string, string> = {}
  const kept = new Set<string>()
  for (const row of rows) {
    const key = row.key.trim()
    if (!key) continue
    kept.add(key)
    if (row.value !== undefined) Object.defineProperty(set, key, { value: row.value, enumerable: true, configurable: true, writable: true })
  }
  return { set, unset: originalKeys.filter((key) => !kept.has(key)) }
}
