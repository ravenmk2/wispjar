import { useIdentityStore } from "@/stores/identity"
import type { ErrorEnvelope } from "@/lib/types"

const BASE_URL = "/api/v1"

export class ApiError extends Error {
  status: number
  code: string
  details?: Record<string, unknown>

  constructor(status: number, code: string, message: string, details?: Record<string, unknown>) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.code = code
    this.details = details
  }
}

interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE"
  body?: unknown
  params?: Record<string, string | number | undefined>
}

async function parseError(res: Response): Promise<ApiError> {
  try {
    const data = (await res.json()) as ErrorEnvelope
    if (data?.error?.code) {
      return new ApiError(res.status, data.error.code, data.error.message || "请求失败", data.error.details)
    }
  } catch {
    // ignore
  }
  return new ApiError(res.status, "internal", `请求失败（HTTP ${res.status}）`)
}

async function ensureOk(res: Response): Promise<Response> {
  if (res.status === 401) {
    const err = await parseError(res)
    if (err.code === "auth/password_change_required") {
      if (window.location.pathname !== "/change-password") {
        window.location.href = "/change-password"
      }
      throw err
    }
    useIdentityStore.getState().clear()
    if (window.location.pathname !== "/login") {
      window.location.href = "/login"
    }
    throw err
  }
  if (!res.ok) {
    throw await parseError(res)
  }
  return res
}

// 鉴权字节流入口：multipart 上传与 blob 下载不能走 JSON api()
export async function apiRaw(path: string, init: RequestInit = {}): Promise<Response> {
  const { token, currentOrgId } = useIdentityStore.getState()
  const headers = new Headers(init.headers)
  if (token) headers.set("Authorization", `Bearer ${token}`)
  if (currentOrgId) headers.set("X-Org-Id", currentOrgId)
  const res = await fetch(`${BASE_URL}${path}`, { ...init, headers })
  return ensureOk(res)
}

export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = "GET", body, params } = options
  const url = new URL(BASE_URL + path, window.location.origin)
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined && v !== "") url.searchParams.set(k, String(v))
    }
  }

  const { token, currentOrgId } = useIdentityStore.getState()
  const headers: Record<string, string> = {}
  if (token) headers["Authorization"] = `Bearer ${token}`
  if (currentOrgId) headers["X-Org-Id"] = currentOrgId
  if (body !== undefined) headers["Content-Type"] = "application/json"

  const res = await fetch(url.toString(), {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })

  await ensureOk(res)

  if (res.status === 204) {
    return undefined as T
  }
  return (await res.json()) as T
}

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return "未知错误"
}
