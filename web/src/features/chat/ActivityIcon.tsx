import { Brain, Bot, FilePen, FileText, Image, MessageCircleQuestion, Search, SquareTerminal, Wrench } from "lucide-react"
import { cn } from "@/lib/utils"

const icons = {
  reasoning: Brain,
  exec: SquareTerminal,
  read: FileText,
  write: FilePen,
  edit: FilePen,
  glob: Search,
  grep: Search,
  task: Bot,
  question: MessageCircleQuestion,
  ask_user_question: MessageCircleQuestion,
  read_media: Image,
}

export function ActivityIcon({ name, active = false }: { name: string; active?: boolean }) {
  const skill = name === "skill" || name === "activate_skill"
  const className = cn("chat-activity-icon", active && "is-running", skill && "chat-activity-skill")
  if (skill) {
    return (
      <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={className}>
        <path d="m21.64 3.64-1.28-1.28a1.21 1.21 0 0 0-1.72 0L2.36 18.64a1.21 1.21 0 0 0 0 1.72l1.28 1.28a1.2 1.2 0 0 0 1.72 0L21.64 5.36a1.2 1.2 0 0 0 0-1.72Z" />
        <path d="m14 7 3 3" />
        <g className="activity-star"><path d="M5 6v4M7 8H3" /></g>
        <g className="activity-star"><path d="M19 14v4M21 16h-4" /></g>
        <g className="activity-star"><path d="M10 2v2M11 3H9" /></g>
      </svg>
    )
  }
  const Icon = icons[name as keyof typeof icons] ?? Wrench
  return <Icon aria-hidden="true" className={className} />
}
