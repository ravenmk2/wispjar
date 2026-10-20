import { MessageCircleQuestion, Wrench } from "lucide-react"
import { cn } from "@/lib/utils"

// Split the SVG into semantic parts so the outline stays stable while its details animate.
const glyphs = {
  reasoning: <>
    <g transform="rotate(-45 12 12)">
      <ellipse className="activity-atom-track" cx="12" cy="12" rx="9" ry="5" />
      <circle className="activity-atom-electron" r="1.35" />
    </g>
    <g transform="rotate(45 12 12)">
      <ellipse className="activity-atom-track" cx="12" cy="12" rx="9" ry="5" />
      <circle className="activity-atom-electron activity-atom-electron-offset" r="1.35" />
    </g>
    <circle className="activity-atom-core" cx="12" cy="12" r="1.9" />
  </>,
  search: <><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 5 5" /></>,
  read: <>
    <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z" />
    <path d="M14 2v6h6" />
    <path className="activity-scan" d="M8 11h5" />
    <path className="activity-scan activity-scan-middle" d="M8 15h8" />
    <path className="activity-scan activity-scan-last" d="M8 19h6" />
  </>,
  write: <>
    <path d="M13 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h13" />
    <path className="activity-scan" d="M7 17h5" />
    <g className="activity-pen"><path d="m15 6 3 3M20 3a2.1 2.1 0 0 1 3 3L13 16l-4 1 1-4Z" /></g>
  </>,
  exec: <>
    <rect x="3" y="4" width="18" height="16" rx="2" />
    <path className="activity-prompt" d="m7 8 3 3-3 3" />
    <path className="activity-cursor" d="M13 15h4" />
  </>,
  task: <>
    <path d="M12 3v3M10 3h2M3 12H1M23 12h-2" />
    <rect x="3" y="6" width="18" height="14" rx="3" />
    <path d="M9 16h6" />
    <path className="activity-eye" d="M8 10v2" />
    <path className="activity-eye activity-eye-offset" d="M16 10v2" />
  </>,
  skill: <>
    <path d="m21.64 3.64-1.28-1.28a1.21 1.21 0 0 0-1.72 0L2.36 18.64a1.21 1.21 0 0 0 0 1.72l1.28 1.28a1.2 1.2 0 0 0 1.72 0L21.64 5.36a1.2 1.2 0 0 0 0-1.72Z" />
    <path d="m14 7 3 3" />
    <g className="activity-star"><path d="M5 6v4M7 8H3" /></g>
    <g className="activity-star activity-star-middle"><path d="M19 14v4M21 16h-4" /></g>
    <g className="activity-star activity-star-last"><path d="M10 2v2M11 3H9" /></g>
  </>,
  read_media: <>
    <rect x="3" y="3" width="18" height="18" rx="2" />
    <circle className="activity-star" cx="8.5" cy="8.5" r="1.5" />
    <path className="activity-signal" d="m21 15-5-5-7 8-3-3-3 3" />
  </>,
}

export function ActivityIcon({ name, active = false }: { name: string; active?: boolean }) {
  const kind = name === "glob" || name === "grep" ? "search"
    : name === "edit" ? "write"
    : name === "activate_skill" ? "skill" : name
  const glyph = Object.hasOwn(glyphs, kind) ? glyphs[kind as keyof typeof glyphs] : undefined
  const className = cn("chat-activity-icon", active && "is-running", `chat-activity-${glyph ? kind : "fallback"}`)
  if (glyph) {
    return <svg aria-hidden="true" viewBox={kind === "reasoning" ? "3 3 18 18" : "0 0 24 24"} fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" className={className}>{glyph}</svg>
  }
  const Icon = name === "question" || name === "ask_user_question" ? MessageCircleQuestion : Wrench
  return <Icon aria-hidden="true" className={className} />
}
