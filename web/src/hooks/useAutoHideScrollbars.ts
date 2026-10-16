import { useEffect } from "react"

const SCROLLABLE = ".overflow-auto, .overflow-y-auto, .overflow-x-auto"
const HIDE_DELAY = 500

/** Reveal native scrollbars without changing their gutter or scroll behavior. */
export function useAutoHideScrollbars() {
  useEffect(() => {
    const timers = new Map<HTMLElement, ReturnType<typeof setTimeout>>()
    let hovered: HTMLElement | null = null
    let dragging: HTMLElement | null = null

    const reveal = (el: HTMLElement) => {
      clearTimeout(timers.get(el))
      el.dataset.scrollbarVisible = ""
      timers.set(el, setTimeout(() => {
        if (el === hovered || el === dragging) return
        delete el.dataset.scrollbarVisible
        timers.delete(el)
      }, HIDE_DELAY))
    }
    const atScrollbar = (event: PointerEvent): HTMLElement | null => {
      let el = event.target instanceof Element ? event.target.closest<HTMLElement>(SCROLLABLE) : null
      while (el) {
        const rect = el.getBoundingClientRect()
        const nearRight = event.clientX >= rect.right - 12 && event.clientX <= rect.right
        const nearBottom = event.clientY >= rect.bottom - 12 && event.clientY <= rect.bottom
        if ((el.scrollHeight > el.clientHeight && nearRight) || (el.scrollWidth > el.clientWidth && nearBottom)) return el
        el = el.parentElement?.closest<HTMLElement>(SCROLLABLE) ?? null
      }
      return null
    }
    const onScroll = (event: Event) => {
      if (event.target instanceof HTMLElement && event.target.matches(SCROLLABLE)) reveal(event.target)
    }
    const onMove = (event: PointerEvent) => {
      const previous = hovered
      hovered = atScrollbar(event)
      if (previous && previous !== hovered) reveal(previous)
      if (hovered) reveal(hovered)
    }
    const onDown = (event: PointerEvent) => {
      dragging = atScrollbar(event)
      if (dragging) reveal(dragging)
    }
    const onUp = () => {
      const previous = dragging
      dragging = null
      if (previous) reveal(previous)
    }
    const onLeave = () => {
      const previous = hovered
      hovered = null
      if (previous) reveal(previous)
    }
    const onBlur = () => { onLeave(); onUp() }
    document.addEventListener("scroll", onScroll, true)
    document.addEventListener("pointermove", onMove, { passive: true })
    document.addEventListener("pointerdown", onDown, true)
    document.addEventListener("pointerup", onUp, true)
    document.addEventListener("pointercancel", onUp, true)
    document.documentElement.addEventListener("pointerleave", onLeave)
    window.addEventListener("blur", onBlur)
    return () => {
      document.removeEventListener("scroll", onScroll, true)
      document.removeEventListener("pointermove", onMove)
      document.removeEventListener("pointerdown", onDown, true)
      document.removeEventListener("pointerup", onUp, true)
      document.removeEventListener("pointercancel", onUp, true)
      document.documentElement.removeEventListener("pointerleave", onLeave)
      window.removeEventListener("blur", onBlur)
      timers.forEach((timer, el) => { clearTimeout(timer); delete el.dataset.scrollbarVisible })
    }
  }, [])
}
