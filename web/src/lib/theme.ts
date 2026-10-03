import { useSyncExternalStore } from "react"

export type ThemeMode = "light" | "dark" | "system"

const STORAGE_KEY = "wispjar-theme"
const MEDIA_QUERY = "(prefers-color-scheme: dark)"

const listeners = new Set<() => void>()
let mediaListenerAttached = false

export function getThemeMode(): ThemeMode {
  const stored = localStorage.getItem(STORAGE_KEY)
  return stored === "light" || stored === "dark" || stored === "system" ? stored : "system"
}

export function isDark(mode: ThemeMode = getThemeMode()): boolean {
  if (mode === "system") return window.matchMedia(MEDIA_QUERY).matches
  return mode === "dark"
}

function apply() {
  document.documentElement.classList.toggle("dark", isDark())
}

function onMediaChange() {
  if (getThemeMode() === "system") apply()
}

function attachMediaListener() {
  if (mediaListenerAttached) return
  window.matchMedia(MEDIA_QUERY).addEventListener("change", onMediaChange)
  mediaListenerAttached = true
}

export function setThemeMode(mode: ThemeMode) {
  localStorage.setItem(STORAGE_KEY, mode)
  apply()
  for (const listener of listeners) listener()
}

/** 启动时尽早调用：应用持久化主题并挂 system 监听（index.html 内联脚本负责首屏防闪烁） */
export function initTheme() {
  attachMediaListener()
  apply()
}

function subscribe(listener: () => void): () => void {
  attachMediaListener()
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function useThemeMode(): ThemeMode {
  return useSyncExternalStore(subscribe, getThemeMode)
}
