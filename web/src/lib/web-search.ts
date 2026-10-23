import type { SearchResponse, SearchResult } from "@/api/web-search"
import { safeParseJson } from "./format"

// History can outlive a renderer version. Validate before displaying saved data.
export function searchResultURL(raw: string): URL | null {
  try {
    const url = new URL(raw)
    return (url.protocol === "https:" || url.protocol === "http:") && !url.username && !url.password ? url : null
  } catch { return null }
}

export function parseSearchResponse(content: string): SearchResponse | null {
  const parsed = safeParseJson(content)
  if (!parsed || !Array.isArray(parsed.results)) return null
  const results: SearchResult[] = []
  for (const item of parsed.results) {
    if (!item || typeof item !== "object" || typeof item.url !== "string" || !searchResultURL(item.url)) continue
    results.push({
      url: item.url,
      title: typeof item.title === "string" ? item.title : "",
      snippet: typeof item.snippet === "string" ? item.snippet : "",
      content: typeof item.content === "string" ? item.content : "",
      published_at: typeof item.published_at === "string" ? item.published_at : undefined,
    })
  }
  return {
    query: typeof parsed.query === "string" ? parsed.query : "",
    provider_id: typeof parsed.provider_id === "string" ? parsed.provider_id : "",
    provider_name: typeof parsed.provider_name === "string" ? parsed.provider_name : "网络搜索",
    results,
    duration_ms: typeof parsed.duration_ms === "number" && Number.isFinite(parsed.duration_ms) ? parsed.duration_ms : 0,
    truncated: parsed.truncated === true,
  }
}
