import type { FetchResponse } from "../api/web-search"
import { safeParseJson } from "./format"
import { searchResultURL } from "./web-search"

export function parseFetchResponse(content: string): FetchResponse | null {
  const p = safeParseJson(content)
  if (!p || typeof p.url !== "string" || !searchResultURL(p.url) || typeof p.content !== "string" || (p.format !== "markdown" && p.format !== "text")) return null
  return {
    url: p.url,
    final_url: typeof p.final_url === "string" && searchResultURL(p.final_url) ? p.final_url : undefined,
    title: typeof p.title === "string" ? p.title : "",
    content: p.content,
    format: p.format,
    provider_id: typeof p.provider_id === "string" ? p.provider_id : "",
    provider_name: typeof p.provider_name === "string" ? p.provider_name : "网页读取",
    fetched_at: typeof p.fetched_at === "string" ? p.fetched_at : "",
    duration_ms: typeof p.duration_ms === "number" && Number.isFinite(p.duration_ms) ? p.duration_ms : 0,
    truncated: p.truncated === true,
  }
}
