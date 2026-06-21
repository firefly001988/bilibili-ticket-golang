import { ref, onMounted, onUnmounted } from 'vue'
import { clusterCall, type ClusterSnapshot, type WorkerLogEntry } from '@/composables/clusterTypes'

/** Shared reactive snapshot refreshed every 5s. */
const snapshot = ref<ClusterSnapshot>({ taskGroups: [], accounts: [], buyers: [], workers: [], macros: [], attempts: [] })
const loading = ref(false)
let timer: number | undefined
const listeners: Array<() => void> = []

function notify() { listeners.forEach(fn => fn()) }

export function useCluster() {
    async function refresh() {
        try {
            const next = await clusterCall<ClusterSnapshot>('Snapshot')
            next.taskGroups ||= []
            next.accounts ||= []
            next.buyers ||= []
            next.workers ||= []
            next.macros ||= []
            next.attempts ||= []
            snapshot.value = next
            notify()
        } catch (e) { /* errors surfaced per-call */ }
    }

    async function invoke(method: string, ...args: any[]) {
        loading.value = true
        try { await clusterCall(method, ...args); await refresh(); return true }
        catch (e) { throw e }
        finally { loading.value = false }
    }

    function onRefresh(fn: () => void) { listeners.push(fn) }

    async function loadAttemptLogs(attemptId: string): Promise<WorkerLogEntry[]> {
        return clusterCall<WorkerLogEntry[]>('AttemptLogs', attemptId)
    }

    onMounted(() => { refresh(); timer = window.setInterval(refresh, 5000) })
    onUnmounted(() => { if (timer) window.clearInterval(timer) })

    return { snapshot, loading, refresh, invoke, loadAttemptLogs, onRefresh }
}
