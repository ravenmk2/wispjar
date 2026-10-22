import { useCallback, useEffect, useMemo, useState } from "react"
import { errorMessage } from "@/api/client"
import { sessionsApi } from "@/api/sessions"
import type { SessionSummary } from "@/lib/types"

export const SESSION_PAGE_SIZE = 10

interface SearchState {
  items: SessionSummary[]
  nextCursor?: string
  loading: boolean
  error: string | null
}

const EMPTY: SearchState = { items: [], loading: false, error: null }

export function useSessionSearch(orgId: string | undefined, query: string) {
  // Each query owns its requests, so responses from a previous query/org cannot append here.
  const context = useMemo(() => ({
    orgId, query, cancelled: false, loading: false, cursor: undefined as string | undefined,
  }), [orgId, query])
  const [state, setState] = useState<{ context: typeof context; page: SearchState }>({ context, page: EMPTY })

  const loadPage = useCallback(async (cursor?: string) => {
    if (!context.orgId || !context.query || context.cancelled || context.loading) return
    context.loading = true
    setState((previous) => ({
      context,
      page: { ...(previous.context === context ? previous.page : EMPTY), loading: true, error: null },
    }))
    try {
      const res = await sessionsApi.list(context.orgId, { q: context.query, cursor, limit: SESSION_PAGE_SIZE })
      if (context.cancelled) return
      const nextCursor = res.next_cursor || undefined
      context.cursor = nextCursor
      setState((previous) => {
        const items = cursor && previous.context === context ? previous.page.items : []
        const merged = new Map(items.map((item) => [item.id, item]))
        for (const item of res.items) merged.set(item.id, item)
        return {
          context,
          page: { items: [...merged.values()], nextCursor, loading: false, error: null },
        }
      })
    } catch (error) {
      if (!context.cancelled) {
        setState((previous) => ({
          context,
          page: { ...(previous.context === context ? previous.page : EMPTY), loading: false, error: errorMessage(error) },
        }))
      }
    } finally {
      context.loading = false
    }
  }, [context])

  useEffect(() => {
    context.cancelled = false
    setState({ context, page: { ...EMPTY, loading: !!orgId && !!query } })
    const timer = setTimeout(() => void loadPage(), 300)
    return () => {
      context.cancelled = true
      clearTimeout(timer)
    }
  }, [context, loadPage, orgId, query])

  const page = state.context === context ? state.page : { ...EMPTY, loading: !!query }
  const loadMore = useCallback(() => loadPage(context.cursor), [context, loadPage])
  return { ...page, loadMore }
}
