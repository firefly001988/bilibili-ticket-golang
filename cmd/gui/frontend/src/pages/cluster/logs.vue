<script lang="ts" setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import { Snapshot, AttemptLogs } from '../../../bindings/bilibili-ticket-golang/cmd/gui/clusterservice'

const { t } = useI18n(); const messages = useMessagesStore()

interface AttemptBrief { id: string; intentId: string; accountId: string; workerId: string; state: string; orderId?: string; paymentUrl?: string; reason?: string }
interface LogEntry { sequence: number; time: string; stage: string; message: string; code?: number; retryable?: boolean }

const attempts = ref<AttemptBrief[]>([])
const logs = ref<LogEntry[]>([])
const selectedAttemptId = ref('')
const loadingAttempts = ref(true)
const loadingLogs = ref(false)
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

const filteredAttempts = computed(() => stateFilter.value === 'all' ? attempts.value : attempts.value.filter(a => a.state === stateFilter.value))

const selectedAttempt = computed(() => attempts.value.find(a => a.id === selectedAttemptId.value))

const stateColor = (s: string): string => ({ running: 'info', pending: 'warning', success: 'success', completed: 'success', failed: 'error', cancelled: 'grey' } as any)[s] || 'grey'
const stateIcon = (s: string): string => ({ running: 'mdi-play-circle-outline', pending: 'mdi-clock-outline', success: 'mdi-check-circle-outline', completed: 'mdi-check-circle-outline', failed: 'mdi-close-circle-outline', cancelled: 'mdi-cancel' } as any)[s] || 'mdi-help-circle-outline'
const stateLabel = (s: string): string => {
    const m: Record<string, string> = { running: t('logs.stateRunning'), pending: t('logs.statePending'), success: t('logs.stateSuccess'), completed: t('logs.stateCompleted'), failed: t('logs.stateFailed'), cancelled: t('logs.stateCancelled') }
    return m[s] || s || '—'
}
const stageIcon = (s: string): string => { if (/fail|error/i.test(s)) return 'mdi-alert-circle'; if (/complete|success|done/i.test(s)) return 'mdi-check'; if (/prepare|token/i.test(s)) return 'mdi-key-variant'; if (/confirm/i.test(s)) return 'mdi-account-check'; if (/submit|order/i.test(s)) return 'mdi-cart-arrow-right'; return 'mdi-circle-small' }
const stageColorFn = (s: string): string => { if (/fail|error/i.test(s)) return 'error'; if (/complete|success|done/i.test(s)) return 'success'; if (/prepare|token|confirm/i.test(s)) return 'primary'; if (/submit|order/i.test(s)) return 'warning'; return 'grey' }

const stateOptions = [
    { title: t('logs.allStates'), value: 'all' },
    { title: t('logs.stateRunning'), value: 'running' },
    { title: t('logs.statePending'), value: 'pending' },
    { title: t('logs.stateSuccess'), value: 'success' },
    { title: t('logs.stateCompleted'), value: 'completed' },
    { title: t('logs.stateFailed'), value: 'failed' },
    { title: t('logs.stateCancelled'), value: 'cancelled' },
]

const tableHeaders = [
    { title: t('logs.colId'), key: 'id', width: '10%', sortable: false },
    { title: t('logs.colState'), key: 'state', width: '15%', sortable: false },
    { title: t('logs.colAccount'), key: 'accountId', width: '22%', sortable: false },
    { title: t('logs.colWorker'), key: 'workerId', width: '22%', sortable: false },
    { title: t('logs.colOrder'), key: 'orderId', width: '15%', sortable: false },
]

const summaryStats = computed(() => {
    const t = attempts.value
    return { total: t.length, running: t.filter(a => a.state === 'running').length, success: t.filter(a => a.state === 'success' || a.state === 'completed').length, failed: t.filter(a => a.state === 'failed').length }
})

onMounted(async () => { await loadAttempts(); timer = setInterval(loadAttempts, 5000) })
onUnmounted(() => { if (timer) { clearInterval(timer); timer = null } })
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
                    <div class="text-caption text-medium-emphasis">{{ t('logs.statTotal') }}</div>
                    <div class="text-h6 mt-1">{{ summaryStats.total }}</div>
                </v-card>
            </v-col>
            <v-col cols="6" sm="3">
                <v-card variant="outlined" class="pa-3 text-center" color="info">
                    <div class="text-caption text-medium-emphasis">{{ t('logs.statRunning') }}</div>
                    <div class="text-h6 mt-1 text-info">{{ summaryStats.running }}</div>
                </v-card>
            </v-col>
            <v-col cols="6" sm="3">
                <v-card variant="outlined" class="pa-3 text-center" color="success">
                    <div class="text-caption text-medium-emphasis">{{ t('logs.statSuccess') }}</div>
                    <div class="text-h6 mt-1 text-success">{{ summaryStats.success }}</div>
                </v-card>
            </v-col>
            <v-col cols="6" sm="3">
                <v-card variant="outlined" class="pa-3 text-center" color="error">
                    <div class="text-caption text-medium-emphasis">{{ t('logs.statFailed') }}</div>
                    <div class="text-h6 mt-1 text-error">{{ summaryStats.failed }}</div>
                </v-card>
            </v-col>
        </v-row>

        <v-row>
            <v-col cols="12" lg="5">
                <v-card elevation="2">
                    <v-card-item class="py-2 px-4">
                        <template #title><span class="text-subtitle-2">{{ t('logs.attempts') }}</span></template>
                        <template #append>
                            <v-select v-model="stateFilter" :items="stateOptions" density="compact" variant="outlined"
                                hide-details style="max-width:110px" />
                        </template>
                    </v-card-item>
                    <v-data-table v-if="filteredAttempts.length > 0" :headers="tableHeaders" :items="filteredAttempts"
                        :items-per-page="20" :items-per-page-options="[10, 20, 50]" density="compact" item-value="id"
                        fixed-header height="480" hover
                        :row-props="(row: any) => ({ class: selectedAttemptId === row.item.id ? 'bg-primary-lighten-4' : '', style: 'cursor:pointer' })"
                        @click:row="(_: any, row: any) => selectAttempt(row.item.id)">
                        <template #item.id="{ item }">
                            <span class="text-caption font-weight-bold">#{{ item.id?.slice(-8) }}</span>
                        </template>
                        <template #item.state="{ item }">
                            <v-chip :color="stateColor(item.state)" size="x-small" variant="flat"
                                :prepend-icon="stateIcon(item.state)" density="compact">
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
            </v-col>

            <v-col cols="12" lg="7">
                <v-card elevation="2" style="min-height:528px">
                    <v-card-item v-if="selectedAttemptId" class="py-2 px-4 bg-surface-variant">
                        <template #prepend>
                            <v-icon :color="stateColor(selectedAttempt?.state || '')" size="20">{{
                                stateIcon(selectedAttempt?.state || '') }}</v-icon>
                        </template>
                        <template #title>
                            <span class="text-body-2 font-weight-bold">#{{ selectedAttemptId.slice(-8) }}</span>
                            <v-chip v-if="selectedAttempt" :color="stateColor(selectedAttempt.state)" size="x-small"
                                variant="flat" class="ml-2" density="compact">
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

                    <div v-if="selectedAttemptId" class="pa-3" style="max-height:456px;overflow-y:auto">
                        <template v-if="loadingLogs">
                            <div class="text-center py-6">
                                <v-progress-circular indeterminate color="primary" size="28" />
                                <p class="text-caption text-medium-emphasis mt-2">{{ t('logs.loading') }}</p>
                            </div>
                        </template>
                        <template v-else-if="logs.length > 0">
                            <div v-for="entry in logs" :key="entry.sequence" class="log-entry pa-3 mb-1"
                                :class="{ 'log-entry--error': entry.code && entry.code !== 0 }">
                                <div class="d-flex align-center gap-2 mb-1 flex-wrap">
                                    <v-icon :color="stageColorFn(entry.stage)" size="14" class="mr-1">{{
                                        stageIcon(entry.stage) }}</v-icon>
                                    <span class="text-caption font-weight-bold text-medium-emphasis">#{{ entry.sequence
                                        }}</span>
                                    <span class="text-caption text-medium-emphasis">@ {{ entry.time?.slice(11, 19)
                                        }}</span>
                                    <v-chip :color="stageColorFn(entry.stage)" size="x-small" variant="tonal"
                                        density="compact" class="ml-auto">{{ entry.stage }}</v-chip>
                                    <v-chip v-if="entry.code != null" :color="entry.code === 0 ? 'success' : 'error'"
                                        size="x-small" variant="flat" density="compact">{{ entry.code }}</v-chip>
                                </div>
                                <div class="text-caption log-message">{{ entry.message }}</div>
                                <div v-if="entry.retryable" class="mt-1">
                                    <v-chip color="orange" size="x-small" variant="flat" density="compact">{{
                                        t('logs.retryable') }}</v-chip>
                                </div>
                            </div>
                        </template>
                        <div v-else class="text-center py-6">
                            <v-icon size="36" class="mb-2" color="medium-emphasis">mdi-text-box-outline</v-icon>
                            <p class="text-caption text-medium-emphasis">{{ t('logs.emptyLogs') }}</p>
                        </div>
                    </div>

                    <v-card-text v-else
                        class="d-flex flex-column align-center justify-center h-100 text-medium-emphasis"
                        style="min-height:528px">
                        <v-icon size="64" class="mb-3">mdi-text-box-search-outline</v-icon>
                        <p class="text-body-2">{{ t('logs.selectHint') }}</p>
                    </v-card-text>
                </v-card>
            </v-col>
        </v-row>
    </v-container>
</template>

<style scoped>
.h-100 {
    height: 100%;
}

.log-entry {
    background: rgba(var(--v-theme-surface-variant), 0.4);
    border-radius: 8px;
    border: 1px solid rgba(var(--v-theme-surface-variant), 0.5);
    transition: background 0.15s;
}

.log-entry:hover {
    background: rgba(var(--v-theme-surface-variant), 0.65);
}

.log-entry--error {
    border-left: 3px solid rgb(var(--v-theme-error));
    background: rgba(var(--v-theme-error), 0.04);
}

.log-message {
    white-space: pre-wrap;
    word-break: break-all;
    line-height: 1.6;
    color: rgba(var(--v-theme-on-surface), 0.8);
}

.gap-2 {
    gap: 6px;
}
</style>
