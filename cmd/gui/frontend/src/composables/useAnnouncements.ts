import { ref, computed } from 'vue'
import type { Announcement } from '@/utils/announcementParser'
import { parse } from '@/utils/announcementParser'
import { MIRRORS, resolveMirrorUrl, mirrorSelectOptions, MIRROR_KEYS } from '@/composables/mirrors'

/** Remote primary URL for announcements. */
const PRIMARY_URL = 'https://raw.githubusercontent.com/firefly001988/btg-announcements/refs/heads/main/announcement.txt'

/** Key for localStorage raw-text cache. */
const CACHE_KEY = 'announcement.cachedRaw'

/**
 * Composable that manages announcement state.
 * Fetches from remote GitHub repo, parses via announcementParser,
 * falls back to localStorage cache on errors.
 */
export function useAnnouncements() {
    const announcements = ref<Announcement[]>(loadCached())
    const loading = ref(false)
    const error = ref<string | null>(null)
    const activeMirrorIndex = ref(loadMirrorIndex())
    const lastFetchOk = ref(false)

    const mirrorOptions = computed(() => mirrorSelectOptions())

    async function fetchFromUrl(url: string): Promise<Announcement[]> {
        const response = await fetch(url, { cache: 'no-cache' })
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`)
        }
        const raw = await response.text()
        if (!raw.trim()) {
            throw new Error('empty response')
        }
        const parsed = parse(raw)
        if (parsed.length === 0) {
            throw new Error('no announcements parsed')
        }
        parsed.sort((a, b) => b.timestamp - a.timestamp)
        return parsed
    }

    /** Try active mirror, then fall through all mirrors. */
    async function refresh(tryAll = false) {
        loading.value = true
        error.value = null
        const startIndex = tryAll ? 0 : activeMirrorIndex.value
        for (let i = startIndex; i < MIRRORS.length; i++) {
            const url = resolveMirrorUrl(MIRRORS[i].prefix, PRIMARY_URL)
            try {
                const parsed = await fetchFromUrl(url)
                announcements.value = parsed
                const resp = await fetch(url, { cache: 'no-cache' })
                localStorage.setItem(CACHE_KEY, await resp.text())
                activeMirrorIndex.value = i
                persistMirrorIndex(i)
                lastFetchOk.value = true
                error.value = null
                loading.value = false
                return
            } catch (e: any) {
                console.warn(`[announcements] mirror ${MIRRORS[i].title} failed:`, e?.message ?? String(e))
            }
        }
        error.value = 'all mirrors failed'
        lastFetchOk.value = false
        loading.value = false
    }

    /** Switch to a specific mirror and retry. */
    async function switchMirror(index: number) {
        activeMirrorIndex.value = index
        persistMirrorIndex(index)
        loading.value = true
        error.value = null
        const url = resolveMirrorUrl(MIRRORS[index].prefix, PRIMARY_URL)
        try {
            const parsed = await fetchFromUrl(url)
            announcements.value = parsed
            const resp = await fetch(url, { cache: 'no-cache' })
            localStorage.setItem(CACHE_KEY, await resp.text())
            lastFetchOk.value = true
            error.value = null
        } catch (e: any) {
            error.value = e?.message ?? String(e)
            lastFetchOk.value = false
        } finally {
            loading.value = false
        }
    }

    function loadMirrorIndex(): number {
        const raw = localStorage.getItem(MIRROR_KEYS.announcement)
        if (raw !== null) {
            const n = parseInt(raw, 10)
            if (n >= 0 && n < MIRRORS.length) return n
        }
        return 0
    }

    function persistMirrorIndex(index: number) {
        localStorage.setItem(MIRROR_KEYS.announcement, String(index))
    }

    function loadCached(): Announcement[] {
        try {
            const raw = localStorage.getItem(CACHE_KEY)
            if (raw) {
                const parsed = parse(raw)
                parsed.sort((a, b) => b.timestamp - a.timestamp)
                return parsed
            }
        } catch { /* ignore */ }
        return []
    }

    return { announcements, refresh, loading, error, mirrorOptions, activeMirrorIndex, switchMirror }
}
