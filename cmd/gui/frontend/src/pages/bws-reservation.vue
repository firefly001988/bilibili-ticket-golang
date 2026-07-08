<script lang="ts" setup>
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import {
    Snapshot,
    CheckBWSBind,
    GetBWSReservationInfo,
    BindBWSTicket,
    SubmitBWS,
    GetBWSTaskStatus,
    GetBWSTaskLogs,
    StopBWSTask,
    ListBWSEntries,
    DeleteBWSEntry,
} from '../../bindings/bilibili-ticket-golang/cmd/gui/cluster_service/clusterservice'

const { t } = useI18n()
const messages = useMessagesStore()

// ── Account & Worker ─────────────────────────────────────
const accounts = ref<Array<{ id: string; name: string; enabled: boolean; vipStatus: number; tags?: string[] }>>([])
const workers = ref<Array<{ id: string; name: string; address: string; type: string; healthy: boolean; tags?: string[] }>>([])
const selectedAccount = ref('')
const selectedWorkerID = ref('')

const accountOptions = computed(() =>
    accounts.value.filter(a => a.enabled).map(a => ({ title: `${a.name || a.id} (${a.id})`, value: a.id }))
)
const workerOptions = computed(() =>
    workers.value.filter(w => w.healthy).map(w => ({ title: `${w.name || w.id} (${w.id})`, value: w.id }))
)

// ── Bind check ───────────────────────────────────────────
const bindChecked = ref(false)
const isBound = ref(false)
const checkingBind = ref(false)

// ── Fetch BWS info ───────────────────────────────────────

const reserveDates = ref('20260710,20260711,20260712')
const reserveType = ref(1)
const fetchingInfo = ref(false)
const bwsResult = ref<any>(null)
const showResults = ref(true)
const filterActivity = ref('')

// ── Bind ticket form ─────────────────────────────────────
const showBindDialog = ref(false)
const bindBid = ref(202601)
const bindIdType = ref(0)
const bindPersonalId = ref('')
const bindTicketNo = ref('')
const bindUserName = ref('')
const bindNeeded = ref(false)
const bindingTicket = ref(false)

const idTypeOptions = computed(() => [
    { title: t('bwsReservation.idTypeIdcard'), value: 0 },
    { title: t('bwsReservation.idTypePassport'), value: 1 },
    { title: t('bwsReservation.idTypeHkMacau'), value: 2 },
    { title: t('bwsReservation.idTypeTaiwan'), value: 3 },
])

// ── Entries (local list) ─────────────────────────────────
interface BWSEntry {
    id: string          // attemptID from SubmitBWS
    accountId: string
    workerId: string
    activityId: number
    activityTitle: string
    ticketNo: string
    reserveTime: number
    reserveDate: string
    startDelayMs: number
    loopDelayMs: number
    status: string      // 'waiting' | 'running' | 'succeeded' | 'failed' | 'stopped'
    message: string
}
const entries = ref<BWSEntry[]>([])
const selectedEntry = ref<BWSEntry | null>(null)
const submitting = ref(false)

// ── Add entry dialog ─────────────────────────────────────
const showAddDialog = ref(false)
const selectedActivity = ref<any>(null)
const formStartDelayMs = ref(0)
const formLoopDelayMs = ref(50)
const formTicketNo = ref('')

// ── Logs ─────────────────────────────────────────────────
interface BWSLogEntry { sequence: number; time: string; stage: string; message: string; code: number }
const taskLogsMap = ref<Record<string, BWSLogEntry[]>>({})
const loadingLogs = ref(false)

const taskLogs = computed(() => {
    if (!selectedEntry.value) return []
    return taskLogsMap.value[selectedEntry.value.id] || []
})

// ── Load snapshot ────────────────────────────────────────
async function loadSnapshot() {
    try {
        const snap = await Snapshot()
        accounts.value = (snap.accounts || []) as any[]
        workers.value = (snap.workers || []) as any[]
    } catch (e: any) {
        console.error('BWS: load snapshot failed:', e)
    }
}

// ── Check bind ───────────────────────────────────────────
async function checkBind() {
    if (!selectedAccount.value || !selectedWorkerID.value) return
    checkingBind.value = true
    try {
        isBound.value = await CheckBWSBind(JSON.stringify({ accountId: selectedAccount.value, workerId: selectedWorkerID.value }))
        bindChecked.value = true
        if (!isBound.value) {
            bindNeeded.value = true
            showBindDialog.value = true
        } else {
            messages.add({ text: '✅ ' + t('bwsReservation.bindCheckOk'), color: 'success', timeout: 2000 })
        }
    } catch (e: any) {
        messages.add({ text: t('bwsReservation.bindCheckFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    } finally {
        checkingBind.value = false
    }
}
// ── Submit bind ticket ──────────────────────────────────
async function submitBindTicket() {
    if (!bindTicketNo.value.trim() || !bindPersonalId.value.trim() || !bindUserName.value.trim()) {
        messages.add({ text: t('bwsReservation.bindIncomplete'), color: 'warning', timeout: 2000 })
        return
    }
    bindingTicket.value = true
    try {
        const result = await BindBWSTicket(JSON.stringify({
            accountId: selectedAccount.value,
            workerId: selectedWorkerID.value,
            bid: bindBid.value,
            idType: bindIdType.value,
            personalId: bindPersonalId.value.trim(),
            ticketNo: bindTicketNo.value.trim(),
            userName: bindUserName.value.trim(),
        }))
        // result is [code, message]
        const code = Array.isArray(result) ? result[0] : (result as any).code ?? -1
        const msg = Array.isArray(result) ? result[1] : (result as any).message ?? ''
        if (code === 0) {
            messages.add({ text: t('bwsReservation.bindSuccess'), color: 'success', timeout: 4000 })
            showBindDialog.value = false
            // Re-check bind status
            await checkBind()
        } else {
            messages.add({ text: t('bwsReservation.bindFailed', { message: msg || String(code) }), color: 'error', timeout: 4000 })
        }
    } catch (e: any) {
        messages.add({ text: t('bwsReservation.bindFailed', { message: String(e) }), color: 'error', timeout: 4000 })
    } finally {
        bindingTicket.value = false
    }
}
// ── Fetch BWS info ───────────────────────────────────────
async function fetchBWSInfo() {
    if (!reserveDates.value.trim()) {
        messages.add({ text: t('bwsReservation.enterDates'), color: 'warning', timeout: 2000 })
        return
    }
    if (!selectedAccount.value || !selectedWorkerID.value) {
        messages.add({ text: t('bwsReservation.selectAccountWorker'), color: 'warning', timeout: 2000 })
        return
    }
    bindNeeded.value = false
    fetchingInfo.value = true
    try {
        const data = await GetBWSReservationInfo(JSON.stringify({ accountId: selectedAccount.value, workerId: selectedWorkerID.value, reserveDates: reserveDates.value.trim(), reserveType: reserveType.value }))
        bwsResult.value = data
        messages.add({ text: t('bwsReservation.infoLoaded'), color: 'success', timeout: 2000 })
    } catch (e: any) {
        const errMsg = String(e)
        if (errMsg.includes('75638')) {
            bindNeeded.value = true
            messages.add({ text: t('bwsReservation.bindRequired'), color: 'warning', timeout: 6000 })
        } else {
            messages.add({ text: t('bwsReservation.fetchFailed', { error: errMsg }), color: 'error', timeout: 4000 })
        }
    } finally {
        fetchingInfo.value = false
    }
}

// ── Add entry ───────────────────────────────────────────
function openAddDialog(activity: any) {
    selectedActivity.value = activity
    formStartDelayMs.value = 0
    formLoopDelayMs.value = 50
    // Ticket only needs last 4 digits
    formTicketNo.value = getTicketForDate(activity.reserveDate)
    if (formTicketNo.value.length > 4) {
        formTicketNo.value = formTicketNo.value.slice(-4)
    }
    showAddDialog.value = true
}

// ── Submit BWS task ─────────────────────────────────────
async function submitAddEntry() {
    if (!selectedActivity.value || !selectedAccount.value || !selectedWorkerID.value) return
    const act = selectedActivity.value
    const ticket = act.ticket || getTicketForDate(act.reserveDate)

    submitting.value = true
    try {
        const input = {
            accountId: selectedAccount.value,
            workerId: selectedWorkerID.value,
            activityId: act.reserveId,
            ticketNo: formTicketNo.value || ticket,
            activityTitle: act.actTitle,
            reserveTime: act.reserveBeginTime,
            reserveDate: act.reserveDate,
            startDelayMs: formStartDelayMs.value,
            loopDelayMs: formLoopDelayMs.value,
        }
        const attemptID = await SubmitBWS(JSON.stringify(input))
        // Reload entries from backend so the new task appears immediately.
        await loadEntries()
        messages.add({ text: t('bwsReservation.taskStarted'), color: 'success', timeout: 2000 })
        showAddDialog.value = false
    } catch (e: any) {
        // Entry may have been saved to backend despite worker rejection — refresh list.
        await loadEntries()
        messages.add({ text: t('bwsReservation.startFailed', { error: String(e) }), color: 'warning', timeout: 6000 })
    } finally {
        submitting.value = false
    }
}

// ── Stop task ───────────────────────────────────────────
async function stopTask(entry: BWSEntry) {
    if (!entry.workerId) return
    try {
        await StopBWSTask(entry.workerId, entry.id)
        await loadEntries()
        messages.add({ text: t('bwsReservation.taskStopped'), color: 'info', timeout: 2000 })
    } catch (e: any) {
        messages.add({ text: t('bwsReservation.stopFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    }
}

// ── Force start task ────────────────────────────────────
async function forceStartTask(entry: BWSEntry) {
    if (!entry.workerId) return
    try {
        await StopBWSTask(entry.workerId, entry.id)
        // Re-submit with reserveTime set to now so the worker starts immediately
        // (Engine.Run skips the scheduled-wait block when StartAt is in the past)
        const input = {
            accountId: selectedAccount.value,
            workerId: entry.workerId,
            activityId: entry.activityId,
            ticketNo: entry.ticketNo,
            activityTitle: entry.activityTitle,
            reserveTime: Math.floor(Date.now() / 1000) - 10, // 10s ago → immediate
            reserveDate: entry.reserveDate,
            startDelayMs: 0,
            loopDelayMs: entry.loopDelayMs,
        }
        await SubmitBWS(JSON.stringify(input))
        await loadEntries()
        messages.add({ text: t('bwsReservation.forceStarted'), color: 'success', timeout: 2000 })
    } catch (e: any) {
        messages.add({ text: t('bwsReservation.forceStartFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    }
}

// ── Delete entry ────────────────────────────────────────
async function deleteEntry(entry: BWSEntry) {
    // Stop the task first if still running.
    if (entry.status === 'waiting' || entry.status === 'running') {
        try { await StopBWSTask(entry.workerId, entry.id) } catch { /* ignore */ }
    }
    // Remove from backend metadata + DB.
    try { await DeleteBWSEntry(entry.id) } catch { /* ignore */ }
    // Refresh from backend.
    await loadEntries()
    messages.add({ text: t('bwsReservation.entryDeleted'), color: 'info', timeout: 2000 })
}

// ── Load entries from backend ───────────────────────────
async function loadEntries() {
    try {
        const list = await ListBWSEntries()
        console.debug('[BWS] loadEntries: got', list?.length ?? 0, 'entries', list)
        entries.value = (list || []) as BWSEntry[]
    } catch (e: any) {
        console.debug('[BWS] loadEntries: ListBWSEntries failed', String(e))
    }

    // Refresh state for non-terminal entries from their respective workers.
    for (const entry of entries.value) {
        if (entry.status === 'succeeded' || entry.status === 'failed' || entry.status === 'stopped') continue
        if (!entry.workerId) continue
        try {
            const st = await GetBWSTaskStatus(entry.workerId, entry.id)
            if (st) {
                if (st.state === 'succeeded') entry.status = 'succeeded'
                else if (st.state === 'failed' || st.state === 'stopped') entry.status = 'failed'
                else if (st.state === 'running') entry.status = 'running'
                entry.message = st.message || ''
            }
        } catch (e: any) { console.debug('[BWS] loadEntries: GetBWSTaskStatus failed for', entry.id, String(e)) }
    }
    console.debug('[BWS] loadEntries: done, entries count=', entries.value.length)
}

// ── Load logs for selected entry ────────────────────────
async function loadLogs() {
    if (!selectedEntry.value || !selectedWorkerID.value) return
    loadingLogs.value = true
    try {
        const logs = await GetBWSTaskLogs(selectedWorkerID.value, selectedEntry.value.id)
        taskLogsMap.value[selectedEntry.value.id] = logs || []
    } catch { /* silent */ }
    loadingLogs.value = false
}

// ── Helpers ─────────────────────────────────────────────
function formatTime(ts: number): string {
    if (!ts) return '—'
    const d = new Date(ts * 1000)
    if (isNaN(d.getTime())) return String(ts)
    return d.toLocaleString('zh-CN')
}

function getTicketForDate(date: string): string {
    if (!bwsResult.value?.ticketInfos) return ''
    const ti = bwsResult.value.ticketInfos.find((t: any) => t.date === date)
    return ti?.ticket || ''
}

function statusColor(s: string): string {
    switch (s) {
        case 'waiting': return 'blue'
        case 'running': return 'orange'
        case 'succeeded': return 'green'
        case 'failed':
        case 'stopped': return 'grey'
        default: return 'grey'
    }
}

function statusIcon(s: string): string {
    switch (s) {
        case 'waiting': return 'mdi-clock-outline'
        case 'running': return 'mdi-progress-clock'
        case 'succeeded': return 'mdi-check-circle'
        case 'failed':
        case 'stopped': return 'mdi-close-circle'
        default: return 'mdi-help-circle'
    }
}

function stateLabel(s: number): string {
    switch (s) {
        case 1: return t('bwsReservation.stateNotOpen')
        case 2: return t('bwsReservation.stateAvailable')
        case 3: return t('bwsReservation.stateEnded')
        case 4: return t('bwsReservation.stateReserved')
        case 5: return t('bwsReservation.stateSoldOut')
        case 6: return t('bwsReservation.stateNotOpen')
        default: return ''
    }
}
function canAdd(act: any): boolean { return act.state === 1 || act.state === 2 }

const reservedIDs = computed(() => {
    if (!bwsResult.value?.reservedIds) return new Set<number>()
    return new Set<number>(bwsResult.value.reservedIds)
})

const sortedActivities = computed(() => {
    if (!bwsResult.value?.activities) return []
    return [...bwsResult.value.activities].sort((a: any, b: any) => {
        const d = a.reserveDate.localeCompare(b.reserveDate)
        return d !== 0 ? d : a.reserveId - b.reserveId
    })
})

const filteredActivities = computed(() => {
    const kw = filterActivity.value.trim().toLowerCase()
    if (!kw) return sortedActivities.value
    return sortedActivities.value.filter((a: any) => a.actTitle.toLowerCase().includes(kw))
})

// ── Lifecycle ───────────────────────────────────────────
let pollTimer: ReturnType<typeof setInterval> | null = null

// Reload entries immediately when worker changes.
watch(selectedWorkerID, (newVal) => {
    console.debug('[BWS] watch: selectedWorkerID changed to', newVal)
    if (newVal) loadEntries()
})

onMounted(async () => {
    console.debug('[BWS] onMounted: loading snapshot...')
    await loadSnapshot()
    console.debug('[BWS] onMounted: snapshot loaded, workers=', workers.value.length, 'accounts=', accounts.value.length)
    // Load entries immediately (no worker filter).
    await loadEntries()
    // Poll entries from backend.
    pollTimer = setInterval(() => loadEntries(), 3000)
    console.debug('[BWS] onMounted: polling started (3s interval)')
})

onUnmounted(() => {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
})
</script>

<template>
    <v-container>
        <!-- Header -->
        <div class="d-flex align-center mb-4">
            <h1 class="text-h5">{{ t('bwsReservation.title') }}</h1>
        </div>
        <v-divider thickness="3" class="mb-4" />

        <!-- Account & Worker selection -->
        <v-card variant="outlined" class="mb-4 pa-3">
            <v-row dense align="center">
                <v-col cols="12" sm="5">
                    <v-select v-model="selectedAccount" :items="accountOptions"
                        :label="t('bwsReservation.selectAccount')" variant="outlined" density="compact" hide-details
                        @update:model-value="bindChecked = false; bwsResult = null" />
                </v-col>
                <v-col cols="12" sm="5">
                    <v-select v-model="selectedWorkerID" :items="workerOptions"
                        :label="t('bwsReservation.selectWorker')" variant="outlined" density="compact" hide-details
                        class="worker-select-full" @update:model-value="bindChecked = false; bwsResult = null">
                        <template #selection="{ item }">
                            <span class="worker-id-full">{{ item.title }}</span>
                        </template>
                        <template #item="{ props, item }">
                            <v-list-item v-bind="props">
                                <template #title>
                                    <span class="worker-id-full">{{ item.title }}</span>
                                </template>
                            </v-list-item>
                        </template>
                    </v-select>
                </v-col>
                <v-col cols="12" sm="2">
                    <v-btn block color="info" variant="tonal" :disabled="!selectedAccount || !selectedWorkerID"
                        :loading="checkingBind" @click="checkBind">
                        {{ t('bwsReservation.checkBind') }}
                    </v-btn>
                </v-col>
            </v-row>

            <!-- Bind status -->
            <v-alert v-if="bindChecked && !isBound" density="compact" variant="tonal" color="warning" class="mt-3 mb-0"
                closable>
                {{ t('bwsReservation.bindRequired') }}
            </v-alert>
            <v-alert v-if="bindChecked && isBound" density="compact" variant="tonal" color="success" class="mt-3 mb-0"
                closable>
                ✅ {{ t('bwsReservation.bindCheckOk') }}
            </v-alert>
        </v-card>

        <!-- Action: Fetch activity -->
        <div v-if="isBound" class="mb-4">
            <v-card variant="outlined" class="pa-3">
                <div class="d-flex ga-2 align-center flex-wrap">
                    <v-text-field v-model="reserveDates" :label="t('bwsReservation.datesLabel')"
                        placeholder="20260711,20260712" variant="outlined" density="compact" hide-details
                        style="max-width: 240px;" @keyup.enter="fetchBWSInfo" />
                    <v-select v-model="reserveType" :label="t('bwsReservation.reserveType')"
                        :items="[{ title: t('bwsReservation.typeActivity'), value: 0 }, { title: t('bwsReservation.typeGoods'), value: 1 }]"
                        variant="outlined" density="compact" hide-details style="max-width: 120px;" />
                    <v-btn :loading="fetchingInfo" color="primary" @click="fetchBWSInfo">
                        {{ t('bwsReservation.query') }}
                    </v-btn>
                </div>
            </v-card>
        </div>

        <!-- Ticket info summary -->
        <v-card v-if="bwsResult?.ticketInfos?.length" variant="outlined" class="mb-4 pa-3">
            <div class="text-subtitle-2 mb-2">{{ t('bwsReservation.myTicketInfo') }}</div>
            <div class="d-flex flex-wrap ga-3">
                <v-chip v-for="ti in bwsResult.ticketInfos" :key="ti.date" color="info" variant="tonal" size="small">
                    {{ ti.date }}: {{ ti.screenName }} — {{ ti.skuName }}
                </v-chip>
            </div>
        </v-card>

        <!-- Inline results (Activity list) -->
        <v-card v-if="bwsResult?.activities?.length" variant="outlined" class="mb-4">
            <v-card-title class="d-flex align-center py-2 px-3" style="cursor: pointer"
                @click="showResults = !showResults">
                <v-icon :icon="showResults ? 'mdi-chevron-down' : 'mdi-chevron-right'" size="small" class="mr-1" />
                <span class="text-body-medium">{{ t('bwsReservation.activityList', { count: filteredActivities.length })
                }}</span>
                <v-spacer />
                <v-text-field v-if="showResults" v-model="filterActivity" :label="t('bwsReservation.filterName')"
                    variant="outlined" density="compact" hide-details clearable prepend-inner-icon="mdi-magnify"
                    size="small" style="max-width: 200px;" @click.stop />
            </v-card-title>
            <v-divider />
            <v-card-text class="pa-0">
                <v-expand-transition>
                    <div v-if="showResults" style="max-height: 500px; overflow-y: auto;">
                        <v-table density="compact">
                            <thead>
                                <tr>
                                    <th>{{ t('bwsReservation.colId') }}</th>
                                    <th>{{ t('bwsReservation.colActivity') }}</th>
                                    <th>{{ t('bwsReservation.colDate') }}</th>
                                    <th>{{ t('bwsReservation.colTicketNo') }}</th>
                                    <th>{{ t('bwsReservation.colGrabTime') }}</th>
                                    <th></th>
                                </tr>
                            </thead>
                            <tbody>
                                <tr v-for="act in filteredActivities" :key="act.reserveId"
                                    :class="{ 'text-grey': !canAdd(act) }">
                                    <td>{{ act.reserveId }}</td>
                                    <td class="text-no-wrap">
                                        {{ act.actTitle }}
                                        <v-chip :color="canAdd(act) ? 'green' : act.state === 4 ? 'blue' : 'grey'"
                                            size="x-small" variant="tonal">{{ stateLabel(act.state) }}</v-chip>
                                    </td>
                                    <td>{{ act.reserveDate }}</td>
                                    <td><code>{{ (getTicketForDate(act.reserveDate) || '—').slice(-4) }}</code></td>
                                    <td>{{ formatTime(act.reserveBeginTime) }}</td>
                                    <td>
                                        <v-btn v-if="canAdd(act)" icon="mdi-plus-circle-outline" size="x-small"
                                            variant="text" color="primary"
                                            @click="openAddDialog({ ...act, ticket: getTicketForDate(act.reserveDate) })" />
                                    </td>
                                </tr>
                            </tbody>
                        </v-table>
                    </div>
                </v-expand-transition>
            </v-card-text>
        </v-card>
        <v-card v-else-if="fetchingInfo" variant="outlined" class="mb-4 pa-6 text-center">
            <v-progress-circular indeterminate size="24" class="mb-2" />
            <p class="text-grey mb-0">{{ t('common.loading') }}</p>
        </v-card>

        <!-- Entry list -->
        <v-card variant="outlined" class="mb-4">
            <v-card-title class="d-flex align-center py-2 px-3">
                <span class="text-body-medium">{{ t('bwsReservation.entryList', { count: entries.length }) }}</span>
            </v-card-title>
            <v-divider />
            <v-card-text class="pa-0" style="max-height: 400px; overflow-y: auto;">
                <div v-if="entries.length === 0" class="text-body-2 text-grey pa-6 text-center">
                    {{ t('bwsReservation.emptyEntries') }}
                </div>
                <v-list v-else density="compact" lines="two">
                    <v-list-item v-for="e in entries" :key="e.id">
                        <template #prepend>
                            <v-icon :color="statusColor(e.status)" size="18">
                                {{ statusIcon(e.status) }}
                            </v-icon>
                        </template>
                        <template #title>
                            <span class="text-body-2">{{ e.activityTitle || e.id }}</span>
                            <v-chip :color="statusColor(e.status)" size="x-small" variant="tonal" class="ml-1">
                                {{ e.status }}
                            </v-chip>
                        </template>
                        <template #subtitle>
                            <span class="text-caption text-grey">
                                {{ e.accountId || '—' }} @ <span class="worker-id-full">{{ e.workerId || '—' }}</span> · ID: {{ e.activityId || '—' }} · {{
                                    e.reserveDate ||
                                    '—' }} ·
                                {{ t('bwsReservation.ticketNoLabel') }} {{ e.ticketNo || '—' }}
                            </span>
                        </template>
                        <template #append>
                            <div class="d-flex ga-0">
                                <v-btn v-if="e.status === 'waiting'" icon="mdi-fast-forward" size="x-small"
                                    variant="text" color="warning" @click.stop="forceStartTask(e)" />
                                <v-btn v-if="e.status === 'running'" icon="mdi-stop" size="x-small" variant="text"
                                    color="error" @click.stop="stopTask(e)" />
                                <v-btn icon="mdi-delete-outline" size="x-small" variant="text" color="grey"
                                    @click.stop="deleteEntry(e)" />
                            </div>
                        </template>
                    </v-list-item>
                </v-list>
            </v-card-text>
        </v-card>

        <!-- ── Add Entry Dialog ────────────────────────────────── -->
        <v-dialog v-model="showAddDialog" max-width="480">
            <v-card :title="t('bwsReservation.addBwsEntry')">
                <v-card-text v-if="selectedActivity">
                    <v-alert density="compact" variant="tonal" color="info" class="mb-3">
                        <strong>{{ selectedActivity.actTitle }}</strong>
                        <br />
                        {{ t('bwsReservation.date') }} {{ selectedActivity.reserveDate }} | ID: {{
                            selectedActivity.reserveId }}
                        <br />
                        {{ t('bwsReservation.grabTime') }} {{ formatTime(selectedActivity.reserveBeginTime) }}
                    </v-alert>

                    <v-row dense>
                        <v-col cols="6">
                            <v-text-field v-model="formTicketNo" :label="t('bwsReservation.bindTicketNo')"
                                variant="outlined" density="compact" hide-details
                                :hint="t('bwsReservation.ticketNoHint')" persistent-hint maxlength="4" />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="formStartDelayMs" :label="t('bwsReservation.startDelayLabel')"
                                type="number" variant="outlined" density="compact" hide-details
                                :hint="t('bwsReservation.startDelayHint')" persistent-hint />
                        </v-col>
                    </v-row>
                    <v-row dense class="mt-2">
                        <v-col cols="6">
                            <v-text-field v-model="formLoopDelayMs" :label="t('bwsReservation.loopDelayLabel')"
                                type="number" variant="outlined" density="compact" hide-details
                                :hint="t('bwsReservation.loopDelayHint')" persistent-hint />
                        </v-col>
                    </v-row>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showAddDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" variant="tonal" :loading="submitting" @click="submitAddEntry"
                        :disabled="!formTicketNo">
                        {{ t('bwsReservation.addBtn') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ── Bind Ticket Dialog ───────────────────────────────── -->
        <v-dialog v-model="showBindDialog" max-width="500" persistent>
            <v-card :title="t('bwsReservation.bindTicket')">
                <v-card-text>
                    <v-alert density="compact" variant="tonal" color="warning" class="mb-3" closable>
                        {{ t('bwsReservation.bindRequiredDesc') }}
                    </v-alert>

                    <v-row dense>
                        <v-col cols="6">
                            <v-text-field v-model="bindBid" :label="t('bwsReservation.bindBid')" type="number"
                                variant="outlined" density="compact" hide-details />
                        </v-col>
                        <v-col cols="6">
                            <v-select v-model="bindIdType" :label="t('bwsReservation.bindIdType')"
                                :items="idTypeOptions" item-title="title" item-value="value" variant="outlined"
                                density="compact" hide-details />
                        </v-col>
                    </v-row>
                    <v-row dense class="mt-1">
                        <v-col cols="6">
                            <v-text-field v-model="bindTicketNo" :label="t('bwsReservation.bindTicketNo')"
                                variant="outlined" density="compact" hide-details placeholder="1234" />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="bindUserName" :label="t('bwsReservation.bindUserName')"
                                variant="outlined" density="compact" hide-details />
                        </v-col>
                    </v-row>
                    <v-text-field v-model="bindPersonalId" :label="t('bwsReservation.bindPersonalId')"
                        variant="outlined" density="compact" hide-details class="mt-1" />
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showBindDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="warning" variant="tonal" :loading="bindingTicket" @click="submitBindTicket">
                        {{ t('bwsReservation.bindSubmit') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </v-container>
</template>

<style scoped>
code {
    font-size: 0.8em;
    background: rgba(0, 0, 0, 0.05);
    padding: 1px 4px;
    border-radius: 3px;
}

.worker-id-full {
    white-space: normal;
    overflow-wrap: anywhere;
    word-break: break-word;
}

.worker-select-full :deep(.v-field__input),
.worker-select-full :deep(.v-select__selection),
.worker-select-full :deep(.v-select__selection-text) {
    min-width: 0;
    white-space: normal;
    overflow: visible;
    text-overflow: clip;
}
</style>
