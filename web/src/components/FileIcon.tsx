import { Database, File, FileArchive, FileCode2, FileCog, FileImage, FileSpreadsheet, FileText, FileType2, Film, Folder, Music2, Presentation } from "lucide-react"
import { cn } from "@/lib/utils"

export function FileIcon({ name, directory = false, className }: { name: string; directory?: boolean; className?: string }) {
  const ext = name.split(".").pop()?.toLowerCase() ?? ""
  let Icon = File
  let color = "text-muted-foreground"
  if (directory) { Icon = Folder; color = "text-amber-700 dark:text-amber-400" }
  else if (/^(png|jpe?g|gif|webp|svg|bmp|ico|avif|tiff?)$/.test(ext)) { Icon = FileImage; color = "text-violet-600 dark:text-violet-400" }
  else if (ext === "pdf") { Icon = FileType2; color = "text-red-600 dark:text-red-400" }
  else if (/^(xlsx?|csv|tsv|ods)$/.test(ext)) { Icon = FileSpreadsheet; color = "text-emerald-600 dark:text-emerald-400" }
  else if (/^(pptx?|odp)$/.test(ext)) { Icon = Presentation; color = "text-orange-600 dark:text-orange-400" }
  else if (/^(docx?|odt|rtf)$/.test(ext)) { Icon = FileText; color = "text-blue-600 dark:text-blue-400" }
  else if (/^(mp3|wav|ogg|m4a|flac|aac|opus)$/.test(ext)) { Icon = Music2; color = "text-violet-600 dark:text-violet-400" }
  else if (/^(mp4|mov|webm|mkv|avi|m4v)$/.test(ext)) { Icon = Film; color = "text-violet-600 dark:text-violet-400" }
  else if (/^(zip|gz|tgz|tar|7z|rar|bz2|xz)$/.test(ext)) { Icon = FileArchive; color = "text-amber-700 dark:text-amber-400" }
  else if (/^(json|ya?ml|toml|ini|env|conf|config)$/.test(ext) || name.startsWith(".env")) Icon = FileCog
  else if (/^(go|[cm]?[jt]sx?|py|rs|java|c|cpp|h|hpp|sh|bash|html?|css|scss|sql|rb|php|vue|svelte)$/.test(ext)) { Icon = FileCode2; color = "text-blue-600 dark:text-blue-400" }
  else if (/^(db|sqlite3?|safetensors|parquet)$/.test(ext)) Icon = Database
  else if (/^(md|markdown|txt|log)$/.test(ext)) Icon = FileText
  return <Icon aria-hidden="true" className={cn("size-4 shrink-0", color, className)} />
}
