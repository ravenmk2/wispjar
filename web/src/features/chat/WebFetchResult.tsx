import type { FetchResponse } from "@/api/web-search"
import { Markdown } from "@/components/Markdown"
import { searchResultURL } from "@/lib/web-search"

export function WebFetchResult({ result }: { result: FetchResponse }) {
  const source = result.final_url || result.url
  return <div className="min-w-0 space-y-3">
    <div className="space-y-1">
      <div className="break-words text-sm font-medium">{result.title || "网页正文"}</div>
      {searchResultURL(source) && <a className="block break-all text-xs text-link underline" href={source} target="_blank" rel="noreferrer">{source}</a>}
      <p className="text-xs text-muted-foreground">{result.provider_name} · {result.duration_ms} ms · {result.format}{result.truncated ? " · 正文已截断" : ""}</p>
    </div>
    <div className="max-h-[60vh] overflow-auto rounded-md border bg-background p-3">
      {result.format === "markdown" ? <Markdown content={result.content} externalContent /> : <pre className="whitespace-pre-wrap break-words font-sans text-sm">{result.content}</pre>}
    </div>
  </div>
}
