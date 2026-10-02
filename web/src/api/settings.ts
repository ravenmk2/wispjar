import { api } from "./client"
import type { SettingKey, SettingsMap } from "@/lib/types"

function normalizeSettings(data: unknown): SettingsMap {
  const map: SettingsMap = {}
  if (data && typeof data === "object") {
    const obj = data as Record<string, unknown>
    if (Array.isArray(obj.items)) {
      for (const item of obj.items as Array<{ key: string; value: unknown }>) {
        map[item.key as SettingKey] = item.value
      }
    } else {
      for (const [k, v] of Object.entries(obj)) {
        map[k as SettingKey] = v
      }
    }
  }
  return map
}

export const settingsApi = {
  async get(): Promise<SettingsMap> {
    const data = await api<unknown>("/platform/settings")
    return normalizeSettings(data)
  },
  async getOrgSandbox(orgId: string): Promise<SettingsMap> {
    const data = await api<unknown>(`/orgs/${orgId}/admin/sandbox-settings`)
    return normalizeSettings(data)
  },
  put(key: SettingKey, value: unknown) {
    return api<{ ok: boolean }>(`/platform/settings/${encodeURIComponent(key)}`, {
      method: "PUT",
      body: value,
    })
  },
}
