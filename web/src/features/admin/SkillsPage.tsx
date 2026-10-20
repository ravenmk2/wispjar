import { FileText, Folder, Puzzle, Upload, X } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import { ApiError, errorMessage } from "@/api/client"
import { adminSkillsApi } from "@/api/skills"
import { ConfirmDialog, EmptyState, PageHeader, TableSkeleton } from "@/components/common"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { formatBytes, formatDateTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { SkillDetail, SkillSummary } from "@/lib/types"
import { useIdentityStore } from "@/stores/identity"

function uploadErrorMessage(e: unknown): string {
  if (e instanceof ApiError && e.status === 422 && e.details) {
    const parts = Object.values(e.details).map((v) => (typeof v === "string" ? v : JSON.stringify(v)))
    return parts.length > 0 ? `${e.message}：${parts.join("；")}` : e.message
  }
  return errorMessage(e)
}

export default function SkillsPage() {
  const currentOrgId = useIdentityStore((s) => s.currentOrgId)
  const [skills, setSkills] = useState<SkillSummary[] | null>(null)
  const [uploading, setUploading] = useState<{ name: string; percent: number } | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const [detailId, setDetailId] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<SkillSummary | null>(null)
  const [deleting, setDeleting] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const replacementInput = useRef<HTMLInputElement>(null)
  const [replacement, setReplacement] = useState<SkillSummary | null>(null)
  const [replacing, setReplacing] = useState(false)

  const load = useCallback(async () => {
    if (!currentOrgId) return
    try {
      setSkills(await adminSkillsApi.list(currentOrgId))
    } catch (e) {
      toast.error(errorMessage(e))
      setSkills([])
    }
  }, [currentOrgId])

  useEffect(() => {
    setSkills(null)
    load()
  }, [load])

  async function replace(file?: File) {
    if (!currentOrgId || !replacement || !file || replacing) return
    if (!file.name.toLowerCase().endsWith(".zip") || file.size > 20 * 1024 * 1024) { toast.error("请选择不超过 20MB 的技能 zip 包"); return }
    setReplacing(true)
    try {
      const result = await adminSkillsApi.replace(currentOrgId, replacement.id, replacement.revision, file)
      toast.success(`「${result.name}」已更新为修订 ${result.revision}；已有任务结束后同步安装。`)
      setReplacement(null); setDetailId(null); await load()
    } catch (e) {
      toast.error(e instanceof ApiError && e.status === 409 ? "技能已被其他操作更新，请刷新列表后重新选择文件。" : uploadErrorMessage(e))
      await load()
    } finally { setReplacing(false); if (replacementInput.current) replacementInput.current.value = "" }
  }

  async function upload(file: File | undefined) {
    if (!currentOrgId || !file || uploading) return
    if (!file.name.toLowerCase().endsWith(".zip")) {
      toast.error("仅支持 .zip 格式的 skill 包")
      return
    }
    setUploading({ name: file.name, percent: 0 })
    try {
      const skill = await adminSkillsApi.upload(currentOrgId, file, (percent) =>
        setUploading({ name: file.name, percent }),
      )
      toast.success(`「${skill.name}」上传成功`)
      load()
    } catch (e) {
      toast.error(uploadErrorMessage(e))
    } finally {
      setUploading(null)
      if (fileInputRef.current) fileInputRef.current.value = ""
    }
  }

  async function remove() {
    if (!currentOrgId || !deleteTarget) return
    setDeleting(true)
    try {
      await adminSkillsApi.remove(currentOrgId, deleteTarget.id)
      toast.success("已删除")
      setDeleteTarget(null)
      load()
    } catch (e) {
      toast.error(uploadErrorMessage(e))
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div>
      <PageHeader title="技能" description="本机构技能包（agentskills.io 规范 zip）">
        <Button size="sm" disabled={uploading !== null} onClick={() => fileInputRef.current?.click()}>
          <Upload className="size-4" />
          上传技能
        </Button>
        <input
          ref={fileInputRef}
          type="file"
          accept=".zip,application/zip"
          className="hidden"
          onChange={(e) => void upload(e.target.files?.[0])}
        />
      </PageHeader>
      <input ref={replacementInput} type="file" accept=".zip,application/zip" className="hidden" onChange={(e) => void replace(e.target.files?.[0])} />
      {replacing && <p role="status" className="mb-3 text-sm text-muted-foreground">正在校验并替换「{replacement?.name}」技能包…</p>}

      <div
        onDragOver={(e) => {
          e.preventDefault()
          setDragOver(true)
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => {
          e.preventDefault()
          setDragOver(false)
          void upload(e.dataTransfer.files?.[0])
        }}
        className={cn(
          "mb-4 flex items-center justify-center rounded-md border border-dashed px-4 py-6 text-sm text-muted-foreground",
          dragOver && "border-primary bg-accent/50 text-foreground",
        )}
      >
        {uploading ? (
          <div className="w-full max-w-md">
            <div className="mb-1.5 flex items-center justify-between text-xs">
              <span className="truncate">{uploading.name}</span>
              <span>{uploading.percent}%</span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <div className="h-full bg-primary transition-all" style={{ width: `${uploading.percent}%` }} />
            </div>
          </div>
        ) : (
          <span>拖拽 zip 到此处上传，或点击右上角「上传技能」（≤20MB）</span>
        )}
      </div>

      {skills === null ? (
        <TableSkeleton />
      ) : skills.length === 0 ? (
        <EmptyState icon={<Puzzle className="size-10" />} title="暂无技能" description="上传技能 zip 包后可被智能体引用" />
      ) : (
        <TooltipProvider>
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead>描述</TableHead>
                  <TableHead>引用数</TableHead>
                  <TableHead>大小</TableHead>
                  <TableHead>上传者</TableHead>
                  <TableHead>上传时间</TableHead>
                  <TableHead className="w-32">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {skills.map((skill) => (
                  <TableRow key={skill.id}>
                    <TableCell className="font-mono text-sm font-medium">{skill.name}<span className="ml-2 text-xs text-muted-foreground">r{skill.revision}</span></TableCell>
                    <TableCell className="max-w-72">
                      <span className="block truncate text-muted-foreground">{skill.description || "-"}</span>
                    </TableCell>
                    <TableCell>
                      {skill.referenced_by.length === 0 ? (
                        <span className="text-muted-foreground">0</span>
                      ) : (
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <Badge variant="secondary" className="cursor-default">
                              {skill.referenced_by.length}
                            </Badge>
                          </TooltipTrigger>
                          <TooltipContent>
                            <div className="space-y-0.5">
                              {skill.referenced_by.map((ref) => (
                                <div key={ref.agent_id || ref.agent_name}>{ref.agent_name}</div>
                              ))}
                            </div>
                          </TooltipContent>
                        </Tooltip>
                      )}
                    </TableCell>
                    <TableCell className="text-muted-foreground">{formatBytes(skill.size_bytes)}</TableCell>
                    <TableCell className="text-muted-foreground">{skill.uploader_name || "-"}</TableCell>
                    <TableCell className="text-muted-foreground">{formatDateTime(skill.created_at)}</TableCell>
                    <TableCell>
                      <div className="flex items-center gap-1">
                        <Button variant="ghost" size="sm" disabled={replacing || !!uploading} onClick={() => { setReplacement(skill); replacementInput.current?.click() }}>更新包</Button>
                        <Button variant="ghost" size="sm" onClick={() => setDetailId(skill.id)}>
                          详情
                        </Button>
                        {skill.referenced_by.length > 0 ? (
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <span>
                                <Button variant="ghost" size="sm" disabled className="text-muted-foreground">
                                  删除
                                </Button>
                              </span>
                            </TooltipTrigger>
                            <TooltipContent>被 {skill.referenced_by.length} 个智能体引用，无法删除</TooltipContent>
                          </Tooltip>
                        ) : (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-destructive hover:text-destructive"
                            onClick={() => setDeleteTarget(skill)}
                          >
                            删除
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </TooltipProvider>
      )}

      {detailId && currentOrgId && (
        <SkillDetailDrawer orgId={currentOrgId} skillId={detailId} onClose={() => setDetailId(null)} />
      )}

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title="删除技能"
        description={`确定删除「${deleteTarget?.name}」？删除后不可恢复。`}
        confirmText="删除"
        destructive
        loading={deleting}
        onConfirm={remove}
      />
    </div>
  )
}

function SkillDetailDrawer({
  orgId,
  skillId,
  onClose,
}: {
  orgId: string
  skillId: string
  onClose: () => void
}) {
  const [detail, setDetail] = useState<SkillDetail | null>(null)
  const [filePath, setFilePath] = useState<string | null>(null)
  const [fileContent, setFileContent] = useState<{ content: string; truncated?: boolean } | null>(null)
  const [fileLoading, setFileLoading] = useState(false)

  useEffect(() => {
    setDetail(null)
    setFilePath(null)
    setFileContent(null)
    adminSkillsApi
      .detail(orgId, skillId)
      .then(setDetail)
      .catch((e) => {
        toast.error(errorMessage(e))
        onClose()
      })
  }, [orgId, skillId, onClose])

  async function viewFile(path: string) {
    if (fileLoading) return
    setFilePath(path)
    setFileContent(null)
    setFileLoading(true)
    try {
      const res = await adminSkillsApi.file(orgId, skillId, path)
      setFileContent({ content: res.content, truncated: res.truncated })
    } catch (e) {
      toast.error(errorMessage(e))
      setFilePath(null)
    } finally {
      setFileLoading(false)
    }
  }

  const metaEntries = detail ? Object.entries(detail.meta) : []

  return (
    <div className="fixed inset-0 z-50">
      <div className="absolute inset-0 bg-black/40" onClick={onClose} />
      <div className="absolute inset-y-0 right-0 flex w-full max-w-xl flex-col border-l bg-background shadow-xl">
        <div className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
          <Puzzle className="size-4 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate font-mono text-sm font-medium">
            {detail?.name ?? "加载中…"}
          </span>
          <Button variant="ghost" size="icon" className="size-8" onClick={onClose}>
            <X className="size-4" />
          </Button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-4">
          {!detail ? (
            <TableSkeleton rows={4} />
          ) : (
            <div className="space-y-5">
              {detail.description && <p className="text-sm text-muted-foreground">{detail.description}</p>}

              <section>
                <h3 className="mb-2 text-sm font-medium">frontmatter</h3>
                {metaEntries.length === 0 ? (
                  <p className="text-sm text-muted-foreground">无</p>
                ) : (
                  <div className="overflow-hidden rounded-md border">
                    <Table>
                      <TableBody>
                        {metaEntries.map(([key, value]) => (
                          <TableRow key={key}>
                            <TableCell className="w-36 font-mono text-xs text-muted-foreground">{key}</TableCell>
                            <TableCell className="whitespace-pre-wrap break-all font-mono text-xs">
                              {typeof value === "string" ? value : JSON.stringify(value)}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                )}
              </section>

              <section>
                <h3 className="mb-2 text-sm font-medium">文件（{detail.files.length}）</h3>
                <div className="divide-y rounded-md border">
                  {detail.files.map((f) => (
                    <button
                      key={f.path}
                      type="button"
                      disabled={f.type === "dir"}
                      onClick={() => void viewFile(f.path)}
                      className={cn(
                        "flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm",
                        f.type === "dir" ? "cursor-default text-muted-foreground" : "hover:bg-accent/50",
                        filePath === f.path && "bg-accent",
                      )}
                    >
                      {f.type === "dir" ? (
                        <Folder className="size-3.5 shrink-0 text-muted-foreground" />
                      ) : (
                        <FileText className="size-3.5 shrink-0 text-muted-foreground" />
                      )}
                      <span className="min-w-0 flex-1 truncate font-mono text-xs">{f.path}</span>
                      {f.type !== "dir" && f.size > 0 && (
                        <span className="text-xs text-muted-foreground">{formatBytes(f.size)}</span>
                      )}
                    </button>
                  ))}
                </div>
              </section>

              {filePath && (
                <section>
                  <h3 className="mb-2 flex items-center gap-2 text-sm font-medium">
                    <span className="min-w-0 truncate font-mono text-xs font-normal text-muted-foreground">
                      {filePath}
                    </span>
                    {fileContent?.truncated && <Badge variant="secondary">已截断</Badge>}
                  </h3>
                  {fileLoading || !fileContent ? (
                    <div className="h-24 animate-pulse rounded-md bg-muted" />
                  ) : (
                    <pre className="max-h-96 overflow-auto whitespace-pre-wrap rounded-md border bg-muted/40 p-3 font-mono text-xs leading-relaxed">
                      {fileContent.content}
                    </pre>
                  )}
                </section>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
