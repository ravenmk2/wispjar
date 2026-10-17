import { ChevronRight } from "lucide-react"
import { useEffect, useLayoutEffect, useRef, useState, type MouseEvent, type ReactNode } from "react"
import { cn } from "@/lib/utils"

export const MANUAL_DISCLOSURE_EVENT = "chat:manual-disclosure"
const OPEN_MS = 200
const CLOSE_MS = 140
const EASING = "cubic-bezier(0, 0, 0.2, 1)"
const scrollAnchors = new WeakMap<HTMLElement, () => void>()

export function cancelDisclosureScrollAnchor(scroll: HTMLElement) {
  scrollAnchors.get(scroll)?.()
}

function anchorDisclosure(trigger: HTMLElement) {
  const scroll = trigger.closest<HTMLElement>("[data-chat-scroll]")
  if (!scroll) return () => {}
  cancelDisclosureScrollAnchor(scroll)
  const top = trigger.getBoundingClientRect().top
  const previousAnchor = scroll.style.overflowAnchor
  const start = performance.now()
  let frame = 0
  let stopped = false
  const stop = () => {
    if (stopped) return
    stopped = true
    cancelAnimationFrame(frame)
    scroll.style.overflowAnchor = previousAnchor
    delete scroll.dataset.disclosureMoving
    for (const type of ["wheel", "touchstart", "pointerdown", "keydown"]) scroll.removeEventListener(type, stop)
    scrollAnchors.delete(scroll)
  }
  scrollAnchors.set(scroll, stop)
  scroll.style.overflowAnchor = "none"
  scroll.dataset.disclosureMoving = "true"
  scroll.dispatchEvent(new Event(MANUAL_DISCLOSURE_EVENT))
  const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches
  const follow = (now: number) => {
    if (!trigger.isConnected) return stop()
    scroll.scrollTop += trigger.getBoundingClientRect().top - top
    if (reduced || now - start >= OPEN_MS + 40) stop()
    else frame = requestAnimationFrame(follow)
  }
  for (const type of ["wheel", "touchstart", "pointerdown", "keydown"]) scroll.addEventListener(type, stop, { passive: true })
  frame = requestAnimationFrame(follow)
  return stop
}

// A revision belongs to this trigger only; defaults, streaming and ancestor toggles do not animate it.
export function useDisclosureMotion() {
  const [revision, setRevision] = useState(0)
  const releaseRef = useRef<(() => void) | null>(null)
  useEffect(() => () => releaseRef.current?.(), [])
  return {
    revision,
    toggle(event: MouseEvent<HTMLElement>, change: () => void) {
      releaseRef.current?.()
      releaseRef.current = anchorDisclosure(event.currentTarget)
      setRevision((value) => value + 1)
      change()
    },
  }
}

function play(element: HTMLElement | SVGElement, frames: Keyframe[], open: boolean) {
  const animation = element.animate(frames, { duration: open ? OPEN_MS : CLOSE_MS, easing: EASING, fill: "both" })
  const media = window.matchMedia("(prefers-reduced-motion: reduce)")
  const reduce = () => { if (media.matches) animation.finish() }
  media.addEventListener("change", reduce)
  void animation.finished.then(
    () => media.removeEventListener("change", reduce),
    () => media.removeEventListener("change", reduce),
  )
  return animation
}

export function DisclosureContent({ open, revision, children }: {
  open: boolean; revision: number; children: ReactNode
}) {
  const [present, setPresent] = useState(open)
  const ref = useRef<HTMLDivElement>(null)
  const animationRef = useRef<Animation | null>(null)
  const previous = useRef({ open, revision })

  useLayoutEffect(() => {
    const manual = previous.current.open !== open && previous.current.revision !== revision
    previous.current = { open, revision }
    const element = ref.current
    const fromHeight = element?.getBoundingClientRect().height ?? 0
    const fromOpacity = element ? getComputedStyle(element).opacity : "1"
    const interrupted = animationRef.current !== null
    animationRef.current?.cancel()
    animationRef.current = null
    if (element) {
      element.style.overflow = ""
      element.inert = !open
      delete element.dataset.disclosureAnimating
    }
    if (!manual || !element || window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      setPresent(open)
      return
    }
    setPresent(true)
    const height = element.firstElementChild?.getBoundingClientRect().height ?? 0
    element.style.overflow = "hidden"
    element.dataset.disclosureAnimating = open ? "opening" : "closing"
    const animation = play(element, [
      { height: `${interrupted || !open ? fromHeight : 0}px`, opacity: interrupted || !open ? fromOpacity : 0 },
      { height: `${open ? height : 0}px`, opacity: open ? 1 : 0 },
    ], open)
    animationRef.current = animation
    void animation.finished.then(() => {
      if (animationRef.current !== animation || ref.current !== element) return
      animationRef.current = null
      animation.cancel()
      element.style.overflow = ""
      delete element.dataset.disclosureAnimating
      setPresent(open)
    }, () => {})
  }, [open, revision])

  useEffect(() => () => animationRef.current?.cancel(), [])
  if (!open && !present) return null
  return <div ref={ref} data-disclosure-content aria-hidden={!open} className="flow-root min-w-0">
    <div className="flow-root min-w-0">{children}</div>
  </div>
}

export function DisclosureChevron({ open, revision, className }: { open: boolean; revision: number; className?: string }) {
  const ref = useRef<SVGSVGElement>(null)
  const animationRef = useRef<Animation | null>(null)
  const previous = useRef({ open, revision })
  useLayoutEffect(() => {
    const before = previous.current
    previous.current = { open, revision }
    const element = ref.current
    const from = animationRef.current && element ? getComputedStyle(element).transform : `rotate(${before.open ? 90 : 0}deg)`
    animationRef.current?.cancel()
    animationRef.current = null
    if (!element || before.open === open || before.revision === revision || window.matchMedia("(prefers-reduced-motion: reduce)").matches) return
    const animation = play(element, [{ transform: from }, { transform: `rotate(${open ? 90 : 0}deg)` }], open)
    animationRef.current = animation
    void animation.finished.then(() => {
      if (animationRef.current !== animation) return
      animationRef.current = null
      animation.cancel()
    }, () => {})
  }, [open, revision])
  useEffect(() => () => animationRef.current?.cancel(), [])
  return <ChevronRight ref={ref} aria-hidden="true" className={cn("chat-disclosure-icon", className)} style={{ transform: `rotate(${open ? 90 : 0}deg)` }} />
}
