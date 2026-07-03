import { defineStore } from "pinia"
import { ref } from "vue"
import type { SnackbarMessage } from "vuetify/lib/components/VSnackbarQueue/VSnackbarQueue.mjs"

/** Structured error info extracted from Wails RuntimeError.cause (produced by global.Fault.MarshalJSON). */
export interface FaultInfo {
    op?: string
    file?: string
    line?: number
    error?: string
    hint?: string
}

/**
 * Try to extract structured Fault information from a Wails RuntimeError.
 *
 * Wails v3 wraps the Go CallError inside BindingCallFailedError on its way
 * to the frontend.  Depending on the transport layer the `cause` field may
 * or may not survive.  This function tries multiple strategies:
 *   1. err.cause as a structured object (ideal path)
 *   2. Parse the [file:line] prefix from err.message (fallback)
 *   3. err.error?.cause (nested RuntimeError wrappers)
 *
 * Returns null if the error does not contain Fault data.
 */
export function extractFault(err: any): FaultInfo | null {
    // Strategy 1: direct cause field
    let jsonObj = JSON.parse(err?.message ?? '{}')
    let cause: any = jsonObj?.cause ?? jsonObj?.error?.cause
    if (cause && typeof cause === 'object' && cause.file && cause.line) {
        return {
            op: cause.op || '',
            file: cause.file,
            line: cause.line,
            error: cause.error || '',
            hint: cause.hint || '',
        }
    }

    // Strategy 2: Wails sometimes nests the RuntimeError inside another error
    cause = err?.error?.cause
    if (cause && typeof cause === 'object' && cause.file && cause.line) {
        return {
            op: cause.op || '',
            file: cause.file,
            line: cause.line,
            error: cause.error || '',
            hint: cause.hint || '',
        }
    }

    // Strategy 3: parse the [file:line] prefix from the error message.
    // Our Fault.Error() produces: "[file:line] op: err — 建议: hint"
    const msg: string = err?.message ?? err?.error?.message ?? ''
    const match = msg.match(/^\[([^\]:]+):(\d+)\]\s+(.+?)(?::\s+(.+?))?\s*(?:—\s*建议:\s*(.+))?$/)
    if (match) {
        const [, file, lineStr, op, error, hint] = match
        return {
            op: (op || '').trim(),
            file: file.trim(),
            line: parseInt(lineStr, 10),
            error: (error || '').trim(),
            hint: (hint || '').trim(),
        }
    }

    return null
}

/**
 * Format a Fault error into a human-readable string for display.
 */
export function formatFault(fault: FaultInfo): string {
    let result = `[${fault.file}:${fault.line}]`
    if (fault.op) result += ` ${fault.op}`
    if (fault.error) result += `: ${fault.error}`
    return result
}

export const useMessagesStore = defineStore('messages', () => {
    const queue = ref<SnackbarMessage[]>([])
    let zIndexCounter = 1000
    function add(message: SnackbarMessage) {
        if (typeof message === 'string') {
            message = { text: message, color: 'info' }
        }
        queue.value.push({
            ...message,
            zIndex: zIndexCounter++,
        })
    }

    /**
     * Add an error message with automatic Fault parsing.
     * If the error contains a Wails Fault cause, it is rendered as a
     * structured multi-line message with clear visual hierarchy:
     *   line 1: file:line + operation (bold, error color)
     *   line 2: separator
     *   line 3: underlying error message
     *   line 4: separator
     *   line 5: human-readable suggestion (if present)
     */
    function addError(err: any, fallbackText?: string) {
        const fault = extractFault(err)
        const text = err?.message ?? err?.toString() ?? fallbackText ?? 'Unknown error'

        if (fault) {
            const parts: string[] = []

            // Header: location + operation
            const header = `📍 <${fault.file}:${fault.line}> ${fault.op}`
            parts.push(header)

            // Underlying error
            if (fault.error) {
                parts.push(`⚠️ ${fault.error}`)
            }

            // Hint / suggestion
            if (fault.hint) {
                parts.push(`💡 ${fault.hint}`)
            }

            add({
                text: parts.join('\n'),
                color: 'error',
                timeout: fault.hint ? 10000 : 6000,
                variant: 'tonal',
            } as any)
        } else {
            add({ text, color: 'error' })
        }
    }

    return { queue, add, addError }
})
