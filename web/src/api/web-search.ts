import { api } from "./client"
import type { List } from "@/lib/types"

export type SearchProviderType = "deepseek" | "tavily" | "bocha" | "go_http"
export interface SearchProviderOption { id: string; name: string; type: SearchProviderType; status: "active" | "disabled" }
export interface SearchProvider extends SearchProviderOption { base_url: string; masked_key: string; timeout_seconds: number }
export interface SearchProviderBody { name: string; type: SearchProviderType; base_url: string; api_key?: string; timeout_seconds?: number; status: "active" | "disabled" }
export interface SearchResult { title: string; url: string; snippet: string; content: string; published_at?: string }
export interface SearchResponse { query: string; provider_id: string; provider_name: string; results: SearchResult[]; duration_ms: number; truncated: boolean; credits?: number }
export const SEARCH_TYPES: Record<SearchProviderType, { label: string; url: string }> = {
  go_http: { label: "内置", url: "" },
  deepseek: { label: "DeepSeek", url: "https://api.deepseek.com" },
  tavily: { label: "Tavily", url: "https://api.tavily.com" },
  bocha: { label: "博查", url: "https://api.bocha.cn" },
}

export const webSearchApi = {
  list: () => api<List<SearchProvider>>("/platform/web-search/providers"),
  config: () => api<{ default_provider_id: string | null; default_fetch_provider_id: string | null }>("/platform/web-search/config"),
  setFetchDefault: (id: string | null) => api("/platform/web-search/fetch-config", { method: "PUT", body: { default_fetch_provider_id: id } }),
  fetchCheck: (id: string, url: string) => api<FetchResponse>(`/platform/web-search/providers/${id}/fetch-check`, { method: "POST", body: { url } }),
  fetchOptions: (orgId: string) => api<List<SearchProviderOption> & { default_provider_id: string | null }>(`/orgs/${orgId}/admin/search-providers`, { params: { capability: "fetch" } }),
  setDefault: (id: string | null) => api("/platform/web-search/config", { method: "PUT", body: { default_provider_id: id } }),
  create: (body: SearchProviderBody) => api<SearchProvider>("/platform/web-search/providers", { method: "POST", body }),
  update: (id: string, body: SearchProviderBody) => api<SearchProvider>(`/platform/web-search/providers/${id}`, { method: "PUT", body }),
  remove: (id: string) => api(`/platform/web-search/providers/${id}`, { method: "DELETE" }),
  check: (id: string, query: string) => api<SearchResponse>(`/platform/web-search/providers/${id}/check`, { method: "POST", body: { query } }),
  options: (orgId: string) => api<List<SearchProviderOption> & { default_provider_id: string | null }>(`/orgs/${orgId}/admin/search-providers`),
}

export interface FetchResponse {
 url: string; final_url?: string; title: string; content: string; format: "markdown" | "text"
 provider_id: string; provider_name: string; fetched_at: string; duration_ms: number; truncated: boolean
}
export function supportsWebCapability(type: SearchProviderType, fetch: boolean) {
 return fetch ? type === "go_http" || type === "deepseek" : type !== "go_http"
}
