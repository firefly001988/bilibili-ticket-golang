/**
 * Shared GitHub mirror configuration used across the frontend.
 * Keep this single source of truth; both announcement fetcher and update page
 * consume the same mirror list.
 */

export interface MirrorOption {
    /** Display label in dropdowns. */
    title: string
    /** URL prefix to prepend to raw github URLs (empty = direct). */
    prefix: string
}

export const MIRRORS: MirrorOption[] = [
    { title: 'GitHub 直连', prefix: '' },
    { title: 'gh-proxy.com', prefix: 'https://gh-proxy.com/' },
    { title: 'gh.ddlc.top', prefix: 'https://gh.ddlc.top/' },
    { title: 'ghproxy.net', prefix: 'https://ghproxy.net/' },
]

/**
 * Build a select-friendly options array with {title, value} shape
 * (value = index into MIRRORS).
 */
export function mirrorSelectOptions() {
    return MIRRORS.map((m, i) => ({ title: m.title, value: i }))
}

/**
 * Build a v-select compatible options array with {title, value} shape
 * where value = the prefix string (for update.vue compatibility).
 */
export function mirrorSelectOptionsByPrefix() {
    return MIRRORS.map(m => ({ title: m.title, value: m.prefix }))
}

/**
 * Resolve a mirror prefix + raw GitHub URL to a full proxied URL.
 * If prefix is empty, returns the raw URL as-is.
 */
export function resolveMirrorUrl(prefix: string, rawUrl: string): string {
    if (!prefix) return rawUrl
    return prefix + rawUrl
}

/** localStorage keys for persisting per-feature mirror selection. */
export const MIRROR_KEYS = {
    /** Currently active mirror for announcements. */
    announcement: 'announcement.mirrorIndex',
    /** Currently active mirror for update download page. */
    update: 'update.mirrorSource',
    /** Currently active mirror for plugin download page. */
    plugin: 'plugin.mirrorSource',
} as const
