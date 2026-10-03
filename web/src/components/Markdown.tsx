import { memo, useEffect, useState } from "react"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"
import { CopyIconButton } from "@/components/CopyIconButton"
import { highlightCode } from "@/lib/highlight"
import { cn } from "@/lib/utils"

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
      <div className="flex items-center border-b bg-muted/60 py-0.5 pl-3 pr-1">
        <span className="text-[11px] text-muted-foreground">{lang || "text"}</span>
        <CopyIconButton text={code} className="ml-auto size-6" />
      </div>
      {html ? (
        <div
          className="overflow-x-auto p-3 text-[13px] leading-relaxed [&_pre]:!bg-transparent [&_code]:!bg-transparent"
          dangerouslySetInnerHTML={{ __html: html }}
        />
      ) : (
        <pre className="overflow-x-auto p-3 font-mono text-[13px] leading-relaxed">
          <code>{code}</code>
        </pre>
      )}
    </div>
  )
}

export const Markdown = memo(function Markdown({ content, className }: { content: string; className?: string }) {
  return (
    <div className={cn("markdown-body text-sm leading-relaxed", className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          pre: ({ children }) => <>{children}</>,
          code: ({ className: codeClassName, children }) => {
            const text = String(children).replace(/\n$/, "")
            const match = /language-([\w-]+)/.exec(codeClassName ?? "")
            if (match || text.includes("\n")) {
              return <CodeBlock lang={match?.[1]} code={text} />
            }
            return (
              <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">{text}</code>
            )
          },
          a: ({ href, children }) => (
            <a href={href} target="_blank" rel="noreferrer" className="text-primary underline underline-offset-2">
              {children}
            </a>
          ),
          table: ({ children }) => (
            <div className="my-2 overflow-x-auto rounded-lg border">
              <table>{children}</table>
            </div>
          ),
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  )
})

export function DiffBlock({ diff, className }: { diff: string; className?: string }) {
  const lines = diff.split("\n")
  return (
    <div className={cn("overflow-x-auto rounded-md border bg-muted/40 font-mono text-xs leading-relaxed", className)}>
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
          <div key={i} className={cn("whitespace-pre px-3", lineClass)}>
            {line || " "}
          </div>
        )
      })}
    </div>
  )
}
