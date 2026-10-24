import type { ApiErrorBody, MuxClientFrame, MuxServerFrame } from "@/lib/types"
import { uuid } from "@/lib/utils"
import { ApiError } from "@/api/client"
import { requestWSTicket } from "@/api/ws-ticket"
import { useIdentityStore } from "@/stores/identity"

export interface StreamHandlers {
  onItem: (value: unknown) => void
  onError: (error: ApiErrorBody) => void
  onEnd: () => void
}

export type ConnectionStatus = "idle" | "connecting" | "open" | "reconnecting" | "closed"

interface StreamRegistration {
  streamId: string
  endpoint: string
  getPayload: () => unknown
  handlers: StreamHandlers
  opened: boolean
}

const BACKOFF_MIN_MS = 1000
const BACKOFF_MAX_MS = 30000

export class MuxConnection {
  private ws: WebSocket | null = null
  private token: string | null = null
  private streams = new Map<string, StreamRegistration>()
  private statusListeners = new Set<(status: ConnectionStatus) => void>()
  private status: ConnectionStatus = "idle"
  private attempt = 0
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private closedByUser = false
  private ticketRequest: AbortController | null = null

  connect(token: string) {
    if (this.token === token && (this.status === "open" || this.status === "connecting" || this.status === "reconnecting")) return
    this.teardownSocket()
    this.token = token
    this.closedByUser = false
    this.attempt = 0
    this.openSocket()
  }

  disconnect() {
    this.closedByUser = true
    this.token = null
    this.teardownSocket()
    this.setStatus("closed")
  }

  onStatusChange(listener: (status: ConnectionStatus) => void): () => void {
    this.statusListeners.add(listener)
    return () => this.statusListeners.delete(listener)
  }

  getStatus(): ConnectionStatus {
    return this.status
  }

  openStream(endpoint: string, getPayload: () => unknown, handlers: StreamHandlers): string {
    const streamId = uuid()
    const reg: StreamRegistration = { streamId, endpoint, getPayload, handlers, opened: false }
    this.streams.set(streamId, reg)
    if (this.status === "open") this.sendOpen(reg)
    return streamId
  }

  sendItem(streamId: string, value: unknown) {
    this.send({ type: "item", streamId, value })
  }

  closeStream(streamId: string, abort = false) {
    const reg = this.streams.get(streamId)
    if (!reg) return
    this.streams.delete(streamId)
    if (reg.opened && this.status === "open") {
      this.send({ type: abort ? "cancel" : "end", streamId })
    }
  }

  private async openSocket() {
    if (!this.token) return
    const token = this.token
    const request = new AbortController()
    this.ticketRequest = request
    const isCurrent = () => this.ticketRequest === request && this.token === token && !this.closedByUser
    const timeout = setTimeout(() => request.abort(), 10000)
    this.setStatus(this.attempt === 0 ? "connecting" : "reconnecting")
    let ws: WebSocket
    try {
      const { ticket } = await requestWSTicket(token, request.signal)
      if (!isCurrent()) return
      if (request.signal.aborted) throw new Error("获取连接票据超时")
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:"
      const url = `${proto}//${window.location.host}/api/ws?ticket=${encodeURIComponent(ticket)}`
      ws = new WebSocket(url)
    } catch (err) {
      if (!isCurrent()) return
      if (err instanceof ApiError && err.status === 401) {
        this.disconnect()
        // The store can change before React propagates the new login to mux.
        if (useIdentityStore.getState().token !== token) return
        if (err.code === "auth/password_change_required") {
          if (window.location.pathname !== "/change-password") window.location.href = "/change-password"
        } else {
          useIdentityStore.getState().clear()
          if (window.location.pathname !== "/login") window.location.href = "/login"
        }
      } else {
        this.scheduleReconnect()
      }
      return
    } finally {
      clearTimeout(timeout)
      if (this.ticketRequest === request) this.ticketRequest = null
    }
    this.ws = ws

    ws.onopen = () => {
      if (this.ws !== ws) return
      this.attempt = 0
      this.setStatus("open")
      for (const reg of this.streams.values()) this.sendOpen(reg)
    }
    ws.onmessage = (ev) => {
      if (this.ws !== ws) return
      this.handleMessage(ev.data)
    }
    ws.onclose = () => {
      if (this.ws !== ws) return
      this.ws = null
      for (const reg of this.streams.values()) reg.opened = false
      if (!this.closedByUser && this.token) this.scheduleReconnect()
      else this.setStatus("closed")
    }
    ws.onerror = () => {
      ws.close()
    }
  }

  private sendOpen(reg: StreamRegistration) {
    const sent = this.send({ type: "open", streamId: reg.streamId, endpoint: reg.endpoint, payload: reg.getPayload() })
    if (sent) reg.opened = true
  }

  private send(frame: MuxClientFrame): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false
    this.ws.send(JSON.stringify(frame))
    return true
  }

  private handleMessage(raw: unknown) {
    if (typeof raw !== "string") return
    let frame: MuxServerFrame
    try {
      frame = JSON.parse(raw) as MuxServerFrame
    } catch {
      return
    }
    const reg = this.streams.get(frame.streamId)
    if (!reg) return
    if (frame.type === "item") {
      reg.handlers.onItem(frame.value)
    } else if (frame.type === "error") {
      reg.handlers.onError(frame.error)
    } else if (frame.type === "end") {
      this.streams.delete(frame.streamId)
      reg.opened = false
      reg.handlers.onEnd()
    }
  }

  private scheduleReconnect() {
    this.setStatus("reconnecting")
    const base = Math.min(BACKOFF_MAX_MS, BACKOFF_MIN_MS * 2 ** this.attempt)
    this.attempt += 1
    const jitter = base * 0.2 * (Math.random() * 2 - 1)
    const delay = Math.max(200, Math.round(base + jitter))
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      if (!this.closedByUser && this.token) this.openSocket()
    }, delay)
  }

  private teardownSocket() {
    this.ticketRequest?.abort()
    this.ticketRequest = null
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    if (this.ws) {
      const ws = this.ws
      this.ws = null
      ws.onopen = null
      ws.onmessage = null
      ws.onclose = null
      ws.onerror = null
      try {
        ws.close()
      } catch {
        // ignore
      }
    }
    for (const reg of this.streams.values()) reg.opened = false
  }

  private setStatus(status: ConnectionStatus) {
    if (this.status === status) return
    this.status = status
    for (const listener of this.statusListeners) listener(status)
  }
}

export const mux = new MuxConnection()
