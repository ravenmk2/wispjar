import { ApiError } from "./client"
import type { ErrorEnvelope } from "@/lib/types"

export interface WSTicket {
  ticket: string
  expiresAt: string
}

// The connection handles auth errors only after checking this request still
// belongs to the current login. A late 401 must not clear a newer login.
export async function requestWSTicket(token: string, signal: AbortSignal): Promise<WSTicket> {
  const res = await fetch("/api/v1/auth/ws-ticket", {
    method: "POST",
    headers: { Authorization: `Bearer ${token}` },
    cache: "no-store",
    signal,
  })
  if (!res.ok) {
    let body: ErrorEnvelope | undefined
    try { body = await res.json() as ErrorEnvelope } catch { /* non-JSON gateway error */ }
    throw new ApiError(res.status, body?.error?.code ?? "internal", body?.error?.message ?? "获取连接票据失败")
  }
  return res.json() as Promise<WSTicket>
}
