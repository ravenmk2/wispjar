import { createContext, memo, useContext, useEffect, useState, type ComponentProps } from "react"
import ReactMarkdown, { defaultUrlTransform } from "react-markdown"
import remarkGfm from "remark-gfm"
import { CopyIconButton } from "@/components/CopyIconButton"
import { MarkdownImage } from "@/components/MarkdownImage"
import { highlightCode } from "@/lib/highlight"
import { cn } from "@/lib/utils"

const TableCopyContext = createContext<{ source: string; headerEnd?: number } | null>(null)

function MarkdownImageRenderer({ node: _node, ...props }: ComponentProps<"img"> & { node?: unknown }) {
  return <MarkdownImage {...props} />
}

function TableHeaderCell({ sourceEnd, children, ...props }: ComponentProps<"th"> & { sourceEnd?: number }) {
  const table = useContext(TableCopyContext)
  const hasCopy = sourceEnd !== undefined && sourceEnd === table?.headerEnd
  return (
    <th {...props}>
      {hasCopy ? (
        <div className="flex min-w-0 items-center gap-2">
          <span className="flex-1">{children}</span>
          <CopyIconButton text={table!.source} tooltip="复制表格" className="size-8" />
        </div>
      ) : children}
    </th>
  )
}

function BlockHeader({ label, text, tooltip }: { label: string; text: string; tooltip: string }) {
  return (
    <div className="flex min-h-10 items-center gap-3 border-b bg-muted/60 py-0.5 pl-3 pr-1">
      <span className="text-meta text-muted-foreground">{label}</span>
      <CopyIconButton text={text} tooltip={tooltip} className="ml-auto size-8" />
    </div>
  )
}

export function CodeBlock({ lang, code, className }: { lang?: string; code: string; className?: string }) {
  const [html, setHtml] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    void highlightCode(code, lang).then((result) => {
      if (!cancelled) setHtml(result)
    })
    return () => {
      cancelled = true
    }
  }, [code, lang])

  return (
    <div className={cn("codeblock group relative my-2 overflow-hidden rounded-md border bg-muted/40", className)}>
      <BlockHeader label={lang || "text"} text={code} tooltip="复制代码" />
      {html ? (
        <div
          className="overflow-x-auto p-3 text-chat [&_pre]:!bg-transparent [&_code]:!bg-transparent"
          dangerouslySetInnerHTML={{ __html: html }}
        />
      ) : (
        <pre className="overflow-x-auto p-3 font-mono text-chat">
          <code>{code}</code>
        </pre>
      )}
    </div>
  )
}

export const Markdown = memo(function Markdown({ content, className, externalContent = false, resolveUrl, onLink }: {
  content: string; className?: string; externalContent?: boolean
  resolveUrl?: (url: string) => string | undefined
  onLink?: (url: string) => boolean
}) {
  return (
    <div className={cn("markdown-body min-w-0 break-words text-chat", className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        urlTransform={(url) => {
          const safe = defaultUrlTransform(url)
          return safe && resolveUrl ? resolveUrl(safe) : safe
        }}
        components={{
          img: externalContent ? ({ alt }) => <span className="text-muted-foreground">[图片：{alt || "外部图片"}]</span> : MarkdownImageRenderer,
          pre: ({ children }) => <>{children}</>,
          code: ({ className: codeClassName, children }) => {
            const text = String(children).replace(/\n$/, "")
            const match = /language-([\w-]+)/.exec(codeClassName ?? "")
            if (match || text.includes("\n")) {
              return <CodeBlock lang={match?.[1]} code={text} />
            }
            return (
              <code className="mx-1 rounded-lg bg-muted px-1.5 py-0.5 font-sans text-[1em]">{text}</code>
            )
          },
          a: ({ href, children }) => (
            <a href={href} target="_blank" rel="noreferrer" className="text-link underline underline-offset-2" onClick={(event) => {
              if (href && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey && onLink?.(href)) event.preventDefault()
            }}>
              {children}
            </a>
          ),
          table: ({ children, node }) => {
            const start = node?.position?.start.offset
            const end = node?.position?.end.offset
            const headerEnd = node?.children.find((child) => child.type === "element" && child.tagName === "thead")?.position?.end.offset
            // Strip container prefixes so tables inside quotes/lists paste independently.
            const source = start !== undefined && end !== undefined
              ? content.slice(start, end)
                .split(/\r?\n/)
                .map((line) => line.replace(/^[ \t]*(?:>[ \t]*)+/, "").trimStart())
                .join("\n")
              : ""

            return (
              <TableCopyContext.Provider value={{ source, headerEnd }}>
                <div className="markdown-table my-2 overflow-x-auto rounded-lg border">
                  <table>{children}</table>
                </div>
              </TableCopyContext.Provider>
            )
          },
          th: ({ node, ...props }) => <TableHeaderCell {...props} sourceEnd={node?.position?.end.offset} />,
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  )
})

export function DiffBlock({ diff, className, wrapped = false }: { diff: string; className?: string; wrapped?: boolean }) {
  const lines = diff.split("\n")
  return (
    <div className={cn("overflow-x-auto rounded-md border bg-muted/40 font-mono text-chat", className)}>
      {lines.map((line, i) => {
        let lineClass = "text-muted-foreground"
        if (line.startsWith("+") && !line.startsWith("+++")) {
          lineClass = "bg-green-500/10 text-green-700 dark:text-green-400"
        } else if (line.startsWith("-") && !line.startsWith("---")) {
          lineClass = "bg-red-500/10 text-red-700 dark:text-red-400"
        } else if (line.startsWith("@@")) {
          lineClass = "bg-blue-500/10 text-blue-700 dark:text-blue-400"
        }
        return (
          <div key={i} className={cn("px-3", wrapped ? "whitespace-pre-wrap [overflow-wrap:anywhere]" : "whitespace-pre", lineClass)}>
            {line || " "}
          </div>
        )
      })}
    </div>
  )
}
