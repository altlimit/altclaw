import { ref, type Ref } from 'vue'

/**
 * Composable for cursor-based API pagination.
 * Fetches items in pages from a URL that returns { items_key: [...], cursor_key: "..." }.
 *
 * @param baseUrl - API endpoint (e.g. '/api/history')
 * @param pageSize - Number of items per page
 * @param itemsKey - Key in JSON response for the items array (default: 'entries')
 * @param cursorKey - Key in JSON response for the next cursor (default: 'cursor')
 * @param extraParams - Optional extra query params to include
 */
export function usePaginated<T>(
  baseUrl: string,
  pageSize = 20,
  itemsKey = 'entries',
  cursorKey = 'cursor',
  extraParams?: () => Record<string, string>
) {
  const items: Ref<T[]> = ref([])
  const loading = ref(false)
  const hasMore = ref(false)
  let cursor = ''

  async function load(append = false) {
    loading.value = true
    try {
      const url = new URL(baseUrl, window.location.origin)
      url.searchParams.set('limit', String(pageSize))
      if (cursor) url.searchParams.set(cursorKey, cursor)
      if (extraParams) {
        for (const [k, v] of Object.entries(extraParams())) {
          if (v) url.searchParams.set(k, v)
        }
      }
      const resp = await fetch(url.toString())
      const data = await resp.json()
      const newItems: T[] = data[itemsKey] || []
      const nextCursor: string = data[cursorKey] || ''

      if (append) {
        items.value = [...items.value, ...newItems]
      } else {
        items.value = newItems
      }
      cursor = nextCursor
      hasMore.value = nextCursor !== ''
    } catch {
      if (!append) items.value = []
      hasMore.value = false
    } finally {
      loading.value = false
    }
  }

  function loadMore() {
    if (hasMore.value && !loading.value) {
      load(true)
    }
  }

  function reset() {
    items.value = []
    cursor = ''
    hasMore.value = false
    load(false)
  }

  return { items, loading, hasMore, load, loadMore, reset }
}

/**
 * Simple frontend-only paginated view over a reactive array.
 * Shows items in increments of `pageSize`, with a "View More" to expand.
 */
export function useSlicePaginated<T>(source: Ref<T[]>, pageSize = 20) {
  const visibleCount = ref(pageSize)

  const visibleItems = ref<T[]>([]) as Ref<T[]>
  const hasMore = ref(false)

  function update() {
    visibleItems.value = source.value.slice(0, visibleCount.value) as T[]
    hasMore.value = source.value.length > visibleCount.value
  }

  function showMore() {
    visibleCount.value += pageSize
    update()
  }

  function resetPage() {
    visibleCount.value = pageSize
    update()
  }

  return { visibleItems, hasMore, showMore, resetPage, update }
}
