<script lang="ts" setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { GetClusterEventLog } from '../../../bindings/bilibili-ticket-golang/cmd/gui/cluster_service/clusterservice'

const { t } = useI18n()

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
let timer: ReturnType<typeof setInterval> | null = null

async function load() {
    try {
        const resp = await GetClusterEventLog()
        events.value = (resp.events || []) as ClusterEvent[]
    } catch { /* silent */ }
    loading.value = false
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
        heartbeat_timeout: t('events.kindHeartbeatTimeout'),
        heartbeat_latency: t('events.kindHeartbeatLatency'),
        worker_info: t('events.kindWorkerInfo'),
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
        case 'heartbeat_timeout':
        case 'heartbeat_latency': return 'warning'
        default: return ''
    }
}

// ── Table ────────────────────────────────────────────────────
const searchText = ref('')
const logViewHeight = ref(400)

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
                    <v-btn size="x-small" variant="text" :loading="loading" icon="mdi-refresh" @click="load" />
                </template>
            </v-card-item>

            <v-data-table v-if="events.length > 0" :headers="tableHeaders" :items="events" :items-per-page="10"
                :items-per-page-options="[10, 20, 30, 50]" density="compact" fixed-header :height="logViewHeight" hover
                class="events-table" :search="searchText">
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
    </v-container>
</template>

<style scoped>
.events-table :deep(td) {
    font-family: 'Cascadia Code', 'Fira Code', 'Consolas', monospace;
    font-size: 11px;
}
</style>
