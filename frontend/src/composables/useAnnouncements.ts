import { ref, computed } from 'vue'
import type { Announcement } from '@/utils/announcementParser'
import { parse } from '@/utils/announcementParser'
import { MIRRORS, resolveMirrorUrl, mirrorSelectOptions, MIRROR_KEYS } from '@/composables/mirrors'

/** Remote primary URL for announcements. */
const PRIMARY_URL = 'https://raw.githubusercontent.com/firefly001988/btg-announcements/refs/heads/main/announcement.txt'

/** Key for localStorage raw-text cache. */
const CACHE_KEY = 'announcement.cachedRaw'

/**
 * Composable that manages announcement state across the app.
 * Fetches announcements from the remote GitHub repository and parses them
 * via announcementParser. Falls back to a localStorage cache on errors.
 */
export function useAnnouncements() {
    // ── State ────────────────────────────────────────────────
    const announcements = ref<Announcement[]>(loadCached())
    const loading = ref(false)
    const error = ref<string | null>(null)
    /** Currently active mirror index (0 = primary), persisted. */
    const activeMirrorIndex = ref(loadMirrorIndex())
    /** Whether the last fetch succeeded. */
    const lastFetchOk = ref(false)

    /** Mirror options for the UI select (shared from mirrors.ts). */
    const mirrorOptions = computed(() => mirrorSelectOptions())

    /**
     * Try fetching from a specific URL. Returns parsed announcements.
     * Throws on any error.
     */
    async function fetchFromUrl(url: string): Promise<Announcement[]> {
        const response = await fetch(url, { cache: 'no-cache' })
        if (!response.ok) {
            throw new Error(`HTTP ${response.status}: ${response.statusText}`)
        }
        const raw = await response.text()
        if (!raw.trim()) {
            throw new Error('响应内容为空')
        }
        const parsed = parse(raw)
        if (parsed.length === 0) {
            throw new Error('未解析到任何公告')
        }
        parsed.sort((a, b) => b.timestamp - a.timestamp)
        return parsed
    }

    /**
     * Try fetching from the active mirror (or fall through all mirrors).
     * @param tryAll If true, iterate through all mirrors on failure.
     */
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
                console.warn(`[useAnnouncements] 镜像 ${MIRRORS[i].title} 获取失败:`, e?.message ?? String(e))
            }
        }
        // All mirrors failed
        error.value = '所有镜像均无法访问'
        lastFetchOk.value = false
        loading.value = false
        console.warn('[useAnnouncements] 所有镜像失败，使用缓存数据')
    }

    /** Switch to a specific mirror and retry fetching. */
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

    // ── Mirror persistence ──────────────────────────────────
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


    /** Parse cached raw text on startup so we have content before the network call completes. */
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

    return {
        announcements,
        loading,
        error,
        refresh,
        switchMirror,
        mirrorOptions,
        activeMirrorIndex,
        lastFetchOk,
    }
}