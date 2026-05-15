/**
 * Global defaults shared across the frontend.
 * Keep in sync with global/global.go on the Go side.
 */

/** Default polling interval between submit attempts (ms). */
export const DEFAULT_INTERVAL_MS = 500

/** Default ticket expiry in days from now, if no API expiry is provided. */
export const DEFAULT_EXPIRE_DAYS = 30

/** Default seconds in a day. */
export const SECONDS_PER_DAY = 86400
