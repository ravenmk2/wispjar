import type { SearchResponse } from "@/api/web-search"
import { searchResultURL } from "@/lib/web-search"

export function WebSearchResults({ result }: { result: SearchResponse }) {
  return <div className="space-y-3">
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
      <span>{result.provider_name}</span><span>{result.results.length} 条结果</span>
      <span>{(result.duration_ms / 1000).toFixed(1)}s</span>
      {result.truncated && <span>内容已截断</span>}
    </div>
    {result.results.length === 0 ? <p className="text-sm text-muted-foreground">未找到相关网页，可调整关键词后重试。</p> :
      <div className="max-h-96 divide-y overflow-y-auto rounded-md border bg-background">
        {result.results.map((item, index) => {
          const url = searchResultURL(item.url)
          if (!url) return null
          return <article key={`${item.url}-${index}`} className="space-y-1 px-3 py-2.5">
            <a href={url.href} target="_blank" rel="noopener noreferrer" className="break-words text-sm font-medium text-link hover:underline">{item.title || url.hostname}</a>
            <div className="flex flex-wrap gap-x-2 text-xs text-muted-foreground"><span>{url.hostname}</span>{item.published_at && <span>{item.published_at}</span>}</div>
            <p className="whitespace-pre-wrap break-words text-sm text-muted-foreground">{item.snippet}</p>
          </article>
        })}
      </div>}
  </div>
}
