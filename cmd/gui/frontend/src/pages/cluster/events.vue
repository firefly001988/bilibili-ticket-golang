<script lang="ts" setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import { GetClusterEventLog, ClearClusterEventLog } from '../../../bindings/bilibili-ticket-golang/cmd/gui/cluster_service/clusterservice'

const { t } = useI18n()
const messages = useMessagesStore()

interface ClusterEvent {
    time: string
    kind: string
    workerId: string
    stage: string
    message: string
    orderId?: string
    attemptId?: string
    code: number
    retryable: boolean
}

const events = ref<ClusterEvent[]>([])
const loading = ref(true)
const clearing = ref(false)
const clearDialog = ref(false)
let timer: ReturnType<typeof setInterval> | null = null

async function load() {
    try {
        const resp = await GetClusterEventLog()
        events.value = ((resp.events || []) as ClusterEvent[]).slice().sort((a, b) => {
            return new Date(b.time).getTime() - new Date(a.time).getTime()
        })
    } catch { /* silent */ }
    loading.value = false
}

async function clearEvents() {
    clearDialog.value = false
    clearing.value = true
    try {
        const deleted = await ClearClusterEventLog()
        events.value = []
        await load()
        messages.add({ text: t('events.clearSuccess', { count: deleted ?? 0 }), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('events.clearFailed', { error: String(e) }), color: 'error' })
    }
    clearing.value = false
}

onMounted(async () => {
    await load()
    timer = setInterval(load, 3000)
})
onUnmounted(() => { if (timer) { clearInterval(timer); timer = null } })

function fmtTime(ts: any): string {
    try {
        const d = ts instanceof Date ? ts : new Date(ts)
        if (isNaN(d.getTime())) return String(ts ?? '')
        const hh = String(d.getHours()).padStart(2, '0')
        const mi = String(d.getMinutes()).padStart(2, '0')
        const ss = String(d.getSeconds()).padStart(2, '0')
        const ms = String(d.getMilliseconds()).padStart(3, '0')
        return `${hh}:${mi}:${ss}.${ms}`
    } catch { return String(ts ?? '') }
}

function kindLabel(k: string): string {
    const m: Record<string, string> = {
        worker_connected: t('events.kindWorkerConnected'),
        worker_disconnected: t('events.kindWorkerDisconnected'),
        worker_healthy: t('events.kindWorkerHealthy'),
        worker_unhealthy: t('events.kindWorkerUnhealthy'),
        task_completed: t('events.kindTaskCompleted'),
        task_failed: t('events.kindTaskFailed'),
        task_superseded: t('events.kindTaskSuperseded'),
        task_stopped: t('events.kindTaskStopped'),
        heartbeat_timeout: t('events.kindHeartbeatTimeout'),
        heartbeat_latency: t('events.kindHeartbeatLatency'),
        worker_info: t('events.kindWorkerInfo'),
        dispatch_info: t('events.kindDispatchInfo'),
        dispatch_warning: t('events.kindDispatchWarning'),
    }
    return m[k] || k
}

function kindColor(k: string): string {
    switch (k) {
        case 'worker_connected':
        case 'task_completed':
        case 'worker_healthy': return 'success'
        case 'worker_disconnected':
        case 'task_failed':
        case 'worker_unhealthy': return 'error'
        case 'task_superseded': return 'info'
        case 'task_stopped': return 'grey'
        case 'heartbeat_timeout':
        case 'heartbeat_latency':
        case 'dispatch_warning': return 'warning'
        case 'dispatch_info': return 'info'
        default: return ''
    }
}

// ── Table ────────────────────────────────────────────────────
const searchText = ref('')

const tableHeaders = computed(() => [
    { title: t('events.colTime'), key: 'time', width: 90, sortable: false },
    { title: t('events.colWorker'), key: 'workerId', width: 80, sortable: false },
    { title: t('events.colStage'), key: 'stage', width: 70, sortable: false },
    { title: t('events.colType'), key: 'kind', width: 100, sortable: false },
    { title: t('events.colMessage'), key: 'message', sortable: false },
])
</script>

<template>
    <v-container>
        <div class="d-flex align-center mb-4">
            <h1 class="text-h5">{{ t('events.title') }}</h1>
        </div>

        <!-- Event log table -->
        <v-card elevation="2">
            <v-card-item class="py-2 px-4">
                <template #title>
                    <span class="text-subtitle-2">{{ t('events.feedTitle') }}</span>
                    <span class="text-caption text-medium-emphasis ml-2">({{ events.length }} {{ t('events.entries')
                        }})</span>
                </template>
                <template #append>
                    <v-btn size="x-small" variant="text" color="error" :loading="clearing" prepend-icon="mdi-delete"
                        class="mr-2" :disabled="events.length === 0" @click="clearDialog = true">
                        {{ t('events.clear') }}
                    </v-btn>
                    <v-btn size="x-small" variant="text" :loading="loading" icon="mdi-refresh" @click="load" />
                </template>
            </v-card-item>

            <v-data-table v-if="events.length > 0" :headers="tableHeaders" :items="events" :items-per-page="-1"
                hide-default-footer density="compact" hover class="events-table" :search="searchText">
                <template #top>
                    <v-text-field v-model="searchText" density="compact" variant="outlined" hide-details
                        :placeholder="t('events.searchPlaceholder')" prepend-inner-icon="mdi-magnify" clearable
                        class="mx-4 mt-2" />
                </template>
                <template #item.time="{ item }">
                    <span class="font-monospace text-caption text-no-wrap">{{ fmtTime(item.time) }}</span>
                </template>
                <template #item.workerId="{ item }">
                    <span class="font-monospace text-caption text-no-wrap">{{ item.workerId?.slice(0, 10) || '—'
                    }}</span>
                </template>
                <template #item.stage="{ item }">
                    <span class="font-monospace text-caption font-weight-bold"
                        :class="kindColor(item.kind) ? 'text-' + kindColor(item.kind) : ''">{{ item.stage }}</span>
                </template>
                <template #item.kind="{ item }">
                    <span class="text-caption" :class="kindColor(item.kind) ? 'text-' + kindColor(item.kind) : ''">{{
                        kindLabel(item.kind) }}</span>
                </template>
                <template #item.message="{ item }">
                    <span class="text-caption" :class="kindColor(item.kind) ? 'text-' + kindColor(item.kind) : ''">{{
                        item.message }}</span>
                </template>
            </v-data-table>

            <div v-else-if="!loading" class="text-center py-6">
                <v-icon size="36" class="mb-2" color="medium-emphasis">mdi-text-box-outline</v-icon>
                <p class="text-caption text-medium-emphasis">{{ t('events.empty') }}</p>
            </div>

            <div v-if="loading" class="text-center py-6">
                <v-progress-circular indeterminate color="primary" size="28" />
                <p class="text-caption text-medium-emphasis mt-2">{{ t('events.loading') }}</p>
            </div>

        </v-card>
        <v-dialog v-model="clearDialog" max-width="420">
            <v-card>
                <v-card-title>{{ t('events.clear') }}</v-card-title>
                <v-card-text>{{ t('events.clearConfirm') }}</v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="clearDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="error" :loading="clearing" @click="clearEvents">{{ t('events.clear') }}</v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </v-container>
</template>

<style scoped>
.events-table :deep(td) {
    font-family: 'Cascadia Code', 'Fira Code', 'Consolas', monospace;
    font-size: 11px;
}
</style>
