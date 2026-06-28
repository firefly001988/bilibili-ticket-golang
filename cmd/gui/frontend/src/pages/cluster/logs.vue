<script lang="ts" setup>
import { ref, computed, onMounted, onUnmounted, shallowRef, triggerRef, nextTick, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import { Snapshot, AttemptLogs, DeleteTerminalAttempts } from '../../../bindings/bilibili-ticket-golang/cmd/gui/cluster_service/clusterservice'

const { t } = useI18n(); const messages = useMessagesStore()

interface AttemptBrief { id: string; intentId: string; accountId: string; workerId: string; state: string; orderId?: string; paymentUrl?: string; reason?: string }
interface LogEntry { sequence: number; time: string; stage: string; message: string; code?: number; retryable?: boolean }

const attempts = ref<AttemptBrief[]>([])
const logs = ref<LogEntry[]>([])
const selectedAttemptId = ref('')
const selectedIds = ref<string[]>([])
const loadingAttempts = ref(true)
const loadingLogs = ref(false)
const deleting = ref(false)
const stateFilter = ref('all')
const autoRefresh = ref(true)
let timer: ReturnType<typeof setInterval> | null = null

async function loadAttempts() {
    try { const snap = await Snapshot(); attempts.value = (snap.attempts || []) as AttemptBrief[] } catch { }
    loadingAttempts.value = false
}

async function loadLogs() {
    if (!selectedAttemptId.value) { logs.value = []; return }
    loadingLogs.value = true
    try { const result = await AttemptLogs(selectedAttemptId.value); logs.value = (result || []) as LogEntry[] }
    catch (e: any) { logs.value = []; if (!String(e).includes('not found')) messages.add({ text: t('logs.loadFailed', { error: String(e) }), color: 'error' }) }
    loadingLogs.value = false
}

function selectAttempt(id: string) { selectedAttemptId.value = id; loadLogs() }

function toggleAutoRefresh() {
    autoRefresh.value = !autoRefresh.value
    if (autoRefresh.value) { timer = setInterval(loadAttempts, 5000) }
    else if (timer) { clearInterval(timer); timer = null }
}

async function deleteSelected() {
    const ids = selectedIds.value.filter(id => {
        const a = attempts.value.find(x => x.id === id)
        return a && ['succeeded', 'failed', 'stopped'].includes(a.state)
    })
    if (ids.length === 0) {
        messages.add({ text: t('logs.selectTerminalFirst'), color: 'warning' })
        return
    }
    deleting.value = true
    try {
        await DeleteTerminalAttempts(ids)
        selectedIds.value = []
        messages.add({ text: t('logs.deleted', { count: ids.length }), color: 'success' })
        await loadAttempts()
    } catch (e: any) {
        messages.add({ text: t('logs.deleteFailed', { error: String(e) }), color: 'error' })
    }
    deleting.value = false
}

const filteredAttempts = computed(() => stateFilter.value === 'all' ? attempts.value : attempts.value.filter(a => a.state === stateFilter.value))

const selectedAttempt = computed(() => attempts.value.find(a => a.id === selectedAttemptId.value))

const stateColor = (s: string): string => ({ running: 'info', waiting: 'warning', queued: 'grey', pending: 'warning', success: 'success', succeeded: 'success', completed: 'success', failed: 'error', stopped: 'grey', stopping: 'orange', cancelled: 'grey' } as any)[s] || 'grey'
const stateIcon = (s: string): string => ({ running: 'mdi-play-circle-outline', waiting: 'mdi-clock-outline', queued: 'mdi-timer-sand', pending: 'mdi-clock-outline', success: 'mdi-check-circle-outline', succeeded: 'mdi-check-circle-outline', completed: 'mdi-check-circle-outline', failed: 'mdi-close-circle-outline', stopped: 'mdi-stop-circle-outline', stopping: 'mdi-stop-circle-outline', cancelled: 'mdi-cancel' } as any)[s] || 'mdi-help-circle-outline'
const stateLabel = (s: string): string => {
    const m: Record<string, string> = { running: t('logs.stateRunning'), waiting: t('logs.stateWaiting'), queued: t('logs.stateQueued'), pending: t('logs.statePending'), success: t('logs.stateSuccess'), succeeded: t('logs.stateCompleted'), completed: t('logs.stateCompleted'), failed: t('logs.stateFailed'), stopped: t('logs.stateCancelled'), stopping: t('logs.stateCancelled'), cancelled: t('logs.stateCancelled') }
    return m[s] || s || '—'
}

const stateOptions = [
    { title: t('logs.allStates'), value: 'all' },
    { title: t('logs.stateRunning'), value: 'running' },
    { title: t('logs.stateWaiting'), value: 'waiting' },
    { title: t('logs.statePending'), value: 'pending' },
    { title: t('logs.stateSuccess'), value: 'success' },
    { title: t('logs.stateFailed'), value: 'failed' },
    { title: t('logs.stateCancelled'), value: 'stopped' },
]

const tableHeaders = [
    { title: t('logs.colId'), key: 'id', width: '60px', sortable: false },
    { title: t('logs.colState'), key: 'state', width: '80px', sortable: false },
    { title: t('logs.colAccount'), key: 'accountId', width: '90px', sortable: false },
    { title: t('logs.colWorker'), key: 'workerId', width: '90px', sortable: false },
    { title: t('logs.colOrder'), key: 'orderId', width: '80px', sortable: false },
]

const summaryStats = computed(() => {
    const t = attempts.value
    return { total: t.length, running: t.filter(a => a.state === 'running').length, success: t.filter(a => a.state === 'success' || a.state === 'succeeded' || a.state === 'completed').length, failed: t.filter(a => a.state === 'failed').length }
})

onMounted(async () => { await loadAttempts(); timer = setInterval(loadAttempts, 5000) })
onUnmounted(() => { if (timer) { clearInterval(timer); timer = null } })

// ── Compact log display (monospace, like TaskLogViewer) ──
const ITEM_HEIGHT = 20

/** Whether the user is at the bottom (follow-tail mode). */
const isFollowing = ref(true)

interface DisplayEntry { id: number; time: string; stage: string; message: string; code: number; lineColor: string }
const MAX_DISPLAY = 1000
let idCounter = 0
const displayLogs = shallowRef<DisplayEntry[]>([])
let lastProcessedRaw: LogEntry | null = null

function toDisplay(raw: LogEntry): DisplayEntry {
    return {
        id: ++idCounter,
        time: fmtTime(raw.time),
        stage: raw.stage,
        message: raw.message,
        code: raw.code ?? 0,
        lineColor: raw.code && raw.code !== 0 ? 'error' : /fail|error/i.test(raw.stage) ? 'error' : /complete|success|done/i.test(raw.stage) ? 'success' : /submit|order/i.test(raw.stage) ? 'warning' : 'grey',
    }
}

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

watch(
    logs,
    (arr) => {
        const prev = displayLogs.value
        if (arr.length === 0) { displayLogs.value = []; idCounter = 0; lastProcessedRaw = null; triggerRef(displayLogs); return }
        let startIdx = 0
        if (lastProcessedRaw !== null) {
            const lastIdx = arr.lastIndexOf(lastProcessedRaw)
            if (lastIdx >= 0) { startIdx = lastIdx + 1 }
            else { displayLogs.value = arr.map(toDisplay); lastProcessedRaw = arr[arr.length - 1]; triggerRef(displayLogs); return }
        }
        const newCount = arr.length - startIdx
        if (newCount <= 0) return
        const fresh = arr.slice(startIdx).map(toDisplay)
        const combined = prev.concat(fresh)
        displayLogs.value = combined.length > MAX_DISPLAY ? combined.slice(-MAX_DISPLAY) : combined
        lastProcessedRaw = arr[arr.length - 1]
        triggerRef(displayLogs)
    },
    { immediate: true },
)

let scrollPending = false
watch(displayLogs, () => {
    if (!isFollowing.value || scrollPending) return
    scrollPending = true
    nextTick(() => { scrollPending = false; scrollToBottom() })
}, { flush: 'post' })

function scrollToBottom() {
    const el = document.querySelector('.log-virtual') as HTMLElement | null
    if (el) el.scrollTop = el.scrollHeight
}
function onScroll() {
    const el = document.querySelector('.log-virtual') as HTMLElement | null
    if (!el) return
    isFollowing.value = el.scrollTop + el.clientHeight >= el.scrollHeight - 30
}
function jumpToBottom() { isFollowing.value = true; scrollToBottom() }
</script>

<template>
    <v-container>
        <div class="d-flex align-center mb-4">
            <h1 class="text-h5">{{ t('logs.title') }}</h1>
            <v-spacer />
            <v-btn :variant="autoRefresh ? 'tonal' : 'text'" :color="autoRefresh ? 'primary' : undefined" size="small"
                :prepend-icon="autoRefresh ? 'mdi-pause' : 'mdi-play'" @click="toggleAutoRefresh">
                {{ autoRefresh ? t('logs.autoRefreshOn') : t('logs.autoRefreshOff') }}
            </v-btn>
        </div>

        <!-- Stats bar -->
        <v-row dense class="mb-4">
            <v-col cols="6" sm="3">
                <v-card variant="outlined" class="pa-3 text-center">
                    <div class="text-caption text-medium-emphasis">{{ t('logs.stateTotal') }}</div>
                    <div class="text-h6 mt-1">{{ summaryStats.total }}</div>
                </v-card>
            </v-col>
            <v-col cols="6" sm="3">
                <v-card variant="outlined" class="pa-3 text-center" color="info">
                    <div class="text-caption text-medium-emphasis">{{ t('logs.stateRunning') }}</div>
                    <div class="text-h6 mt-1 text-info">{{ summaryStats.running }}</div>
                </v-card>
            </v-col>
            <v-col cols="6" sm="3">
                <v-card variant="outlined" class="pa-3 text-center" color="success">
                    <div class="text-caption text-medium-emphasis">{{ t('logs.stateSuccess') }}</div>
                    <div class="text-h6 mt-1 text-success">{{ summaryStats.success }}</div>
                </v-card>
            </v-col>
            <v-col cols="6" sm="3">
                <v-card variant="outlined" class="pa-3 text-center" color="error">
                    <div class="text-caption text-medium-emphasis">{{ t('logs.stateFailed') }}</div>
                    <div class="text-h6 mt-1 text-error">{{ summaryStats.failed }}</div>
                </v-card>
            </v-col>
        </v-row>

        <v-row>
            <v-col cols="12">
                <v-card elevation="2">
                    <v-card-item class="py-2 px-4">
                        <template #title><span class="text-subtitle-2">{{ t('logs.attempts') }}</span></template>
                        <template #append>
                            <v-btn v-if="selectedIds.length > 0" size="x-small" color="error" variant="tonal"
                                :loading="deleting" @click="deleteSelected" class="mr-2">
                                {{ t('logs.deleteSelected', { count: selectedIds.length }) }}
                            </v-btn>
                            <v-select v-model="stateFilter" :items="stateOptions" density="compact" variant="outlined"
                                hide-details style="max-width:110px" />
                        </template>
                    </v-card-item>
                    <v-data-table v-if="filteredAttempts.length > 0" :headers="tableHeaders" :items="filteredAttempts"
                        :items-per-page="20" :items-per-page-options="[10, 20, 50]" density="compact" item-value="id"
                        fixed-header height="320" hover show-select v-model="selectedIds"
                        :row-props="(row: any) => ({ class: selectedAttemptId === row.item.id ? 'bg-primary-lighten-4' : '', style: 'cursor:pointer' })"
                        @click:row="(_: any, row: any) => selectAttempt(row.item.id)">
                        <template #item.id="{ item }">
                            <span class="text-caption font-weight-bold">#{{ item.id?.slice(-8) }}</span>
                        </template>
                        <template #item.state="{ item }">
                            <v-chip :color="stateColor(item.state)" size="x-small" variant="flat"
                                :prepend-icon="stateIcon(item.state)">
                                {{ stateLabel(item.state) }}
                            </v-chip>
                        </template>
                        <template #item.accountId="{ item }">
                            <span class="text-caption">{{ item.accountId?.slice(0, 18) || '—' }}</span>
                        </template>
                        <template #item.workerId="{ item }">
                            <span class="text-caption">{{ item.workerId?.slice(0, 14) || '—' }}</span>
                        </template>
                        <template #item.orderId="{ item }">
                            <span class="text-caption text-primary" v-if="item.orderId">#{{ item.orderId }}</span>
                            <span class="text-caption text-medium-emphasis" v-else>—</span>
                        </template>
                        <template #bottom />
                    </v-data-table>
                    <v-card-text v-else class="text-medium-emphasis text-center py-10">
                        <v-icon size="36" class="mb-2">mdi-inbox-outline</v-icon>
                        <p class="text-caption">{{ t('logs.emptyAttempts') }}</p>
                    </v-card-text>
                </v-card>

                <!-- Log viewer (below attempt table, same column) -->
                <v-card v-if="selectedAttemptId" elevation="2" class="mt-4">
                    <v-card-item class="py-2 px-4 bg-surface-variant">
                        <template #prepend>
                            <v-icon :color="stateColor(selectedAttempt?.state || '')" size="20">{{
                                stateIcon(selectedAttempt?.state || '') }}</v-icon>
                        </template>
                        <template #title>
                            <span class="text-body-2 font-weight-bold">#{{ selectedAttemptId.slice(-8) }}</span>
                            <v-chip v-if="selectedAttempt" :color="stateColor(selectedAttempt.state)" size="x-small"
                                variant="flat" class="ml-2">
                                {{ stateLabel(selectedAttempt.state) }}
                            </v-chip>
                        </template>
                        <template #subtitle>
                            <span class="text-caption">
                                {{ selectedAttempt?.accountId?.slice(0, 16) || '—' }}
                                ·
                                {{ selectedAttempt?.workerId?.slice(0, 14) || '—' }}
                                <template v-if="selectedAttempt?.orderId">
                                    · <span class="text-primary">#{{ selectedAttempt.orderId }}</span>
                                </template>
                                <template v-if="selectedAttempt?.reason">
                                    · <span class="text-error">{{ selectedAttempt.reason }}</span>
                                </template>
                            </span>
                        </template>
                        <template #append>
                            <v-btn size="x-small" variant="text" :loading="loadingLogs" icon="mdi-refresh"
                                @click="loadLogs" />
                        </template>
                    </v-card-item>

                    <div v-if="selectedAttemptId" class="log-container">
                        <template v-if="loadingLogs">
                            <div class="text-center py-6">
                                <v-progress-circular indeterminate color="primary" size="28" />
                                <p class="text-caption text-medium-emphasis mt-2">{{ t('logs.loading') }}</p>
                            </div>
                        </template>
                        <template v-else-if="displayLogs.length > 0">
                            <div class="log-virtual" @scroll="onScroll">
                                <div v-for="entry in displayLogs" :key="entry.id"
                                    class="log-line d-flex align-center text-caption py-0">
                                    <span class="text-grey-darken-1 text-no-wrap mr-2">{{ entry.time }}</span>
                                    <span class="text-no-wrap mr-2"
                                        style="min-width:52px;max-width:52px;overflow:hidden;text-overflow:ellipsis"
                                        :class="'text-' + entry.lineColor">{{ entry.stage }}</span>
                                    <span class="text-truncate flex-grow-1" :class="'text-' + entry.lineColor">{{
                                        entry.message }}</span>
                                </div>
                            </div>
                            <div v-if="!isFollowing" class="log-follow-btn">
                                <v-btn icon="mdi-arrow-down" size="x-small" variant="tonal" color="primary"
                                    @click="jumpToBottom" />
                            </div>
                        </template>
                        <div v-else class="text-center py-6">
                            <v-icon size="36" class="mb-2" color="medium-emphasis">mdi-text-box-outline</v-icon>
                            <p class="text-caption text-medium-emphasis">{{ t('logs.emptyLogs') }}</p>
                        </div>
                    </div>
                </v-card>
            </v-col>
        </v-row>
    </v-container>
</template>

<style scoped>
.log-container {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
    position: relative;
}

.log-virtual {
    font-family: 'Cascadia Code', 'Fira Code', 'Consolas', monospace;
    font-size: 11px;
    line-height: 1.6;
    background: rgb(var(--v-theme-surface));
    overflow-y: auto;
    height: 360px;
    position: relative;
}

.log-line {
    height: v-bind(ITEM_HEIGHT + 'px');
    overflow: hidden;
    border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.04);
    padding: 0 8px;
}

.log-line:hover {
    background: rgba(var(--v-theme-on-surface), 0.03);
}

.log-follow-btn {
    position: absolute;
    bottom: 8px;
    right: 12px;
    z-index: 1;
}
</style>
