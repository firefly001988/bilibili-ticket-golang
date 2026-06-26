import { ref, onUnmounted } from 'vue'
import { Events } from '@wailsio/runtime'
import { GetHistory, ClearHistory } from '../../bindings/bilibili-ticket-golang/lib/biliutils/scheduler/logbroker'
import type * as scheduler from '../../bindings/bilibili-ticket-golang/lib/biliutils/scheduler/models'

export function useTaskLogs(taskId: string) {
    const logs = ref<scheduler.LogEntry[]>([])
    const maxEntries = 1000

    let unsubscribe: (() => void) | null = null

    // --- RAF batching: prevent UI freeze from high-frequency log emissions ---
    let pendingLogs: scheduler.LogEntry[] = []
    let rafId = 0

    function flushPending() {
        if (pendingLogs.length === 0) return
        // Apply all accumulated entries in one single reactive update
        const merged = logs.value.concat(pendingLogs)
        logs.value = merged.length > maxEntries ? merged.slice(-maxEntries) : merged
        pendingLogs = []
        rafId = 0
    }

    function onLog(entry: scheduler.LogEntry) {
        if (entry.taskID !== taskId) return
        pendingLogs.push(entry)
        if (!rafId) {
            rafId = requestAnimationFrame(flushPending)
        }
    }

    async function loadHistory() {
        try {
            const history = await GetHistory(taskId)
            if (history) {
                logs.value = (history as scheduler.LogEntry[]).slice(-maxEntries)
            }
        } catch {
            // binding may not be ready yet
        }
    }

    async function subscribe() {
        await loadHistory()
        unsubscribe = Events.On('ticket:log', (ev: any) => { onLog(ev.data ?? ev) })
    }

    function doUnsubscribe() {
        if (unsubscribe) {
            unsubscribe()
            unsubscribe = null
        }
    }

    async function clear() {
        logs.value = []
        pendingLogs = []
        if (rafId) {
            cancelAnimationFrame(rafId)
            rafId = 0
        }
        try {
            await ClearHistory(taskId)
        } catch {
            // ignore
        }
    }

    onUnmounted(() => {
        doUnsubscribe()
        if (rafId) {
            cancelAnimationFrame(rafId)
            rafId = 0
        }
    })

    return {
        logs,
        subscribe,
        doUnsubscribe,
        clear,
        loadHistory,
    }
}
