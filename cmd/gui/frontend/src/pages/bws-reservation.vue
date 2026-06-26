<script lang="ts" setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useMessagesStore } from '@/stores/snackbar'
import TaskLogViewer from '@/components/TaskLogViewer.vue'
import {
    GetBWSEntries,
    AddBWSEntry,
    RemoveBWSEntry,
    AddBWSTask,
    GetTaskStatuses,
} from '../../bindings/bilibili-ticket-golang/lib/biliutils/scheduler/schedulerservice'
import {
    GetBWSReservationInfo,
    BindBWSTicket,
} from '../../bindings/bilibili-ticket-golang/lib/biliutils/biliclient'
import type { FrontendBWSEntry, FrontendTaskStatus } from '@/composables/schedulerTypes'
import { statColor, statLabel, StatWaiting, StatPending } from '@/composables/schedulerTypes'
import { useDebug } from '@/composables/useDebug'

const auth = useAuthStore()
const { debugLog } = useDebug()
const { t } = useI18n()
const messages = useMessagesStore()

// BWS 仅在 7月8日 00:00 ~ 7月11日 24:00 期间可用；dev 环境始终可用
const bwsAvailable = computed(() => {
    if (import.meta.env.DEV) return true
    const now = new Date()
    const year = now.getFullYear()
    const start = new Date(year, 6, 8, 0, 0, 0)
    const end = new Date(year, 6, 12, 0, 0, 0)
    return now >= start && now < end
})

// ── State ──────────────────────────────────────────────
const entries = ref<FrontendBWSEntry[]>([])
const taskStatuses = ref<FrontendTaskStatus[]>([])
const selectedHash = ref('')
const loading = ref(false)
const showFetchDialog = ref(false)
const showAddDialog = ref(false)
const pollInterval = 3000

// ── Fetch BWS info form ─────────────────────────────────
const reserveDates = ref('')
const fetchingInfo = ref(false)
const bwsData = ref<any>(null) // raw BWSReservationData

// ── Bind ticket form ────────────────────────────────────
const showBindDialog = ref(false)
const bindBid = ref(202501)
const bindIdType = ref(0)
const bindPersonalId = ref('')
const bindTicketNo = ref('')
const bindUserName = ref('')
const bindingTicket = ref(false)
const bindNeeded = ref(false)

const idTypeOptions = computed(() => [
    { title: t('bwsReservation.idTypeIdcard'), value: 0 },
    { title: t('bwsReservation.idTypePassport'), value: 1 },
    { title: t('bwsReservation.idTypeHkMacau'), value: 2 },
    { title: t('bwsReservation.idTypeTaiwan'), value: 3 },
])

// ── Add entry form (from selected activity) ─────────────
const selectedActivity = ref<any>(null)
const formStartDelayMs = ref(0)
const formLoopDelayMs = ref(50)

// ── Computed ───────────────────────────────────────────
const selectedEntry = computed(() =>
    entries.value.find(e => e.hash === selectedHash.value)
)

const selectedStatus = computed(() => {
    const live = taskStatuses.value.find(s => s.taskID === selectedHash.value)
    if (live) return live
    const entry = entries.value.find(e => e.hash === selectedHash.value)
    if (entry) {
        return {
            taskID: entry.hash,
            targetTime: '',
            adjustedTime: '',
            remainingMs: 0,
            stat: entry.stat ?? 0,
            statName: statLabel(entry.stat ?? 0),
            error: '',
            projectName: entry.activityTitle,
            screenName: entry.reserveDate,
            skuName: t('bwsReservation.ticketNoLabel') + ' ' + entry.ticketNo,
            buyerName: '',
        } as FrontendTaskStatus
    }
    return undefined
})

// Merge entries with task statuses
const mergedList = computed(() => {
    const statusMap = new Map(taskStatuses.value.map(s => [s.taskID, s]))
    return entries.value.map(e => {
        const liveStatus = statusMap.get(e.hash)
        const stat = liveStatus ? liveStatus.stat : (e.stat ?? 0)
        return {
            ...e,
            displayStat: stat,
            displayError: liveStatus?.error || null,
            remainingMs: liveStatus?.remainingMs ?? 0,
        }
    })
})

function isTaskActive(hash: string): boolean {
    const s = taskStatuses.value.find(x => x.taskID === hash)
    return s ? (s.stat === StatWaiting || s.stat === StatPending) : false
}

// ── Actions ────────────────────────────────────────────
async function refresh() {
    try {
        const [ents, sts] = await Promise.all([
            GetBWSEntries(),
            GetTaskStatuses(),
        ])
        debugLog('[BWS refresh] GetBWSEntries:', ents)
        debugLog('[BWS refresh] GetTaskStatuses:', sts)
        entries.value = ents || []
        taskStatuses.value = sts || []
    } catch (e: any) {
        console.error('BWS refresh failed:', e)
    }
}

// ── Fetch BWS info ─────────────────────────────────────
async function fetchBWSInfo() {
    if (!reserveDates.value.trim()) {
        messages.add({ text: t('bwsReservation.enterDates'), color: 'warning', timeout: 2000 })
        return
    }
    bindNeeded.value = false
    fetchingInfo.value = true
    try {
        const data = await GetBWSReservationInfo(reserveDates.value.trim())
        debugLog('[BWS] parsed data:', data)
        bwsData.value = data
        messages.add({ text: t('bwsReservation.infoLoaded'), color: 'success', timeout: 2000 })
    } catch (e: any) {
        const errMsg = String(e)
        if (errMsg.includes('75638')) {
            bindNeeded.value = true
            messages.add({ text: t('bwsReservation.bindRequired'), color: 'warning', timeout: 6000 })
            showBindDialog.value = true
        } else {
            messages.add({ text: t('bwsReservation.fetchFailed', { error: errMsg }), color: 'error', timeout: 4000 })
        }
    } finally {
        fetchingInfo.value = false
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
        await BindBWSTicket(
            bindBid.value,
            bindIdType.value,
            bindPersonalId.value.trim(),
            bindTicketNo.value.trim(),
            bindUserName.value.trim(),
        )
        messages.add({ text: t('bwsReservation.bindSuccess'), color: 'success', timeout: 4000 })
        showBindDialog.value = false
        bindNeeded.value = false
        await fetchBWSInfo()
    } catch (e: any) {
        messages.add({ text: t('bwsReservation.bindFailed', { message: String(e) }), color: 'error', timeout: 4000 })
    } finally {
        bindingTicket.value = false
    }
}

// ── Add entry ──────────────────────────────────────────
function openAddDialog(activity: any) {
    selectedActivity.value = activity
    formStartDelayMs.value = 0
    formLoopDelayMs.value = 50
    showAddDialog.value = true
}

async function submitAddEntry() {
    if (!selectedActivity.value) return
    const act = selectedActivity.value

    // Find ticket for this activity's date
    const ticket = bwsData.value?.TicketMapping?.[act.ReserveDate]
    if (!ticket) {
        messages.add({ text: t('bwsReservation.ticketNotFound', { date: act.ReserveDate }), color: 'error', timeout: 4000 })
        return
    }

    try {
        const hash = await AddBWSEntry({
            hash: '',
            activityId: act.ReserveID,
            ticketNo: ticket,
            activityTitle: act.ActTitle,
            reserveTime: act.ReserveBeginTime,
            reserveDate: act.ReserveDate,
            expire: Math.floor(Date.now() / 1000) + 86400 * 30, // 30 days
            startDelayMs: formStartDelayMs.value,
            loopDelayMs: formLoopDelayMs.value,
            stat: 0,
        })

        debugLog('[BWS] AddBWSEntry hash:', hash)
        messages.add({ text: t('bwsReservation.entryAdded', { hash: hash.slice(0, 8) }), color: 'success', timeout: 2000 })
        showAddDialog.value = false
        await refresh()
    } catch (e: any) {
        messages.add({ text: t('bwsReservation.addFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    }
}

// ── Task management ────────────────────────────────────
async function startTask(hash: string) {
    loading.value = true
    try {
        await AddBWSTask(hash)
        messages.add({ text: t('bwsReservation.taskStarted'), color: 'success', timeout: 2000 })
        await refresh()
        selectedHash.value = hash
    } catch (e: any) {
        messages.add({ text: t('bwsReservation.startFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    } finally {
        loading.value = false
    }
}

async function deleteEntry(hash: string) {
    try {
        await RemoveBWSEntry(hash)
        if (selectedHash.value === hash) selectedHash.value = ''
        messages.add({ text: t('bwsReservation.entryDeleted'), color: 'info', timeout: 2000 })
        await refresh()
    } catch (e: any) {
        messages.add({ text: t('bwsReservation.deleteFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    }
}

// ── Helpers ────────────────────────────────────────────
function formatTime(ts: number): string {
    if (!ts) return '—'
    const d = new Date(ts * 1000)
    if (isNaN(d.getTime())) return String(ts)
    return d.toLocaleString('zh-CN')
}

function formatRemaining(ms: number): string {
    if (ms <= 0) return t('bwsReservation.expired')
    const h = Math.floor(ms / 3600000)
    const m = Math.floor((ms % 3600000) / 60000)
    const s = Math.floor((ms % 60000) / 1000)
    if (h > 0) return `${h}h ${m}m ${s}s`
    if (m > 0) return `${m}m ${s}s`
    return `${s}s`
}

function getTicketForDate(date: string): string {
    return bwsData.value?.TicketMapping?.[date] || '—'
}

function getTicketInfo(date: string): any {
    return bwsData.value?.TicketInfo?.[date]
}

// ── Lifecycle ──────────────────────────────────────────
let timer: ReturnType<typeof setInterval> | null = null

onMounted(async () => {
    await auth.checkLoginStatus()
    if (auth.isLogin) {
        await refresh()
        timer = setInterval(refresh, pollInterval)
    }
})

onUnmounted(() => {
    if (timer) { clearInterval(timer); timer = null }
})
</script>

<template>
    <div>
        <div class="d-flex align-center">
            <h1 class="text-h5">{{ t('bwsReservation.title') }}</h1>
            <v-spacer />
            <v-btn v-if="auth.isLogin" prepend-icon="mdi-cloud-download" color="info" variant="tonal" size="small"
                @click="showFetchDialog = true">
                {{ t('bwsReservation.fetchActivity') }}
            </v-btn>
        </div>
        <v-divider thickness="3" class="mb-4" />

        <v-card v-if="!auth.isLogin" color="warning" variant="tonal" class="pa-4">
            <v-card-text>
                {{ t('bwsReservation.loginRequired') }}
            </v-card-text>
        </v-card>

        <v-card v-else-if="!bwsAvailable" color="grey" variant="tonal" class="pa-4">
            <v-card-title class="text-h6">{{ t('bwsReservation.notStarted') }}</v-card-title>
            <v-card-text>
                {{ t('bwsReservation.notStartedDesc') }}
            </v-card-text>
        </v-card>

        <template v-else>
            <!-- Ticket info summary -->
            <v-card v-if="bwsData?.TicketInfo" variant="outlined" class="mb-4 pa-3">
                <div class="text-subtitle-2 mb-2">{{ t('bwsReservation.myTicketInfo') }}</div>
                <div class="d-flex flex-wrap ga-3">
                    <v-chip v-for="(info, date) in bwsData.TicketInfo" :key="date" color="info" variant="tonal"
                        size="small">
                        {{ date }}: {{ info.ScreenName }} — {{ info.SkuName }}
                        <v-tooltip activator="parent" location="top">{{ info.Ticket }}</v-tooltip>
                    </v-chip>
                </div>
            </v-card>

            <!-- Entry list -->
            <v-card variant="outlined" class="mb-4">
                <v-card-title class="d-flex align-center py-2 px-3">
                    <span class="text-body-medium">{{ t('bwsReservation.entryList', { count: mergedList.length })
                        }}</span>
                    <v-spacer />
                    <v-btn icon="mdi-refresh" size="x-small" variant="text" :loading="loading" @click="refresh" />
                </v-card-title>
                <v-divider />
                <v-card-text class="pa-0" style="max-height: 600px; overflow-y: auto;">
                    <div v-if="mergedList.length === 0" class="text-label-medium text-grey pa-6 text-center">
                        {{ t('bwsReservation.emptyEntries') }}
                    </div>
                    <v-list v-else density="compact" lines="two">
                        <v-list-item v-for="e in mergedList" :key="e.hash" :active="selectedHash === e.hash"
                            @click="selectedHash = e.hash">
                            <template #prepend>
                                <v-icon :color="statColor(e.displayStat)" size="18">
                                    {{ e.displayStat === 0 ? 'mdi-clock-outline' :
                                        e.displayStat === 1 ? 'mdi-progress-clock' :
                                            e.displayStat === 2 ? 'mdi-check-circle' :
                                                e.displayStat === 3 ? 'mdi-close-circle' : 'mdi-alert-circle' }}
                                </v-icon>
                            </template>
                            <template #title>
                                <span class="text-body-2">{{ e.activityTitle }}</span>
                                <v-chip :color="statColor(e.displayStat)" size="x-small" variant="tonal" class="ml-1">
                                    {{ statLabel(e.displayStat) }}
                                </v-chip>
                            </template>
                            <template #subtitle>
                                <span class="text-caption text-grey">
                                    ID: {{ e.activityId }} · {{ e.reserveDate }} · {{ t('bwsReservation.ticketNoLabel')
                                    }} {{ e.ticketNo.slice(0, 8) }}...
                                    <template v-if="e.displayStat === StatWaiting && e.remainingMs > 0">
                                        · {{ t('bwsReservation.remaining', { time: formatRemaining(e.remainingMs) }) }}
                                    </template>
                                </span>
                            </template>
                            <template #append>
                                <div class="d-flex ga-0">
                                    <v-btn v-if="!isTaskActive(e.hash)" icon="mdi-play" size="x-small" variant="text"
                                        color="success" @click.stop="startTask(e.hash)" />
                                    <v-btn v-else icon="mdi-stop" size="x-small" variant="text" color="error"
                                        @click.stop="deleteEntry(e.hash)" />
                                    <v-btn icon="mdi-delete-outline" size="x-small" variant="text" color="grey"
                                        @click.stop="deleteEntry(e.hash)" />
                                </div>
                            </template>
                        </v-list-item>
                    </v-list>
                </v-card-text>
            </v-card>

            <!-- Detail + log -->
            <template v-if="selectedHash && selectedEntry">
                <v-card variant="outlined" class="mb-2 pa-3">
                    <div class="d-flex flex-wrap ga-3 text-caption">
                        <div>
                            <span class="text-grey">{{ t('bwsReservation.activity') }}</span>
                            <strong>{{ selectedEntry.activityTitle }}</strong>
                            (ID: {{ selectedEntry.activityId }})
                        </div>
                        <div>
                            <span class="text-grey">{{ t('bwsReservation.date') }}</span>
                            <strong>{{ selectedEntry.reserveDate }}</strong>
                        </div>
                        <div>
                            <span class="text-grey">{{ t('bwsReservation.ticketNo') }}</span>
                            <strong>{{ selectedEntry.ticketNo }}</strong>
                        </div>
                        <div v-if="selectedEntry.reserveTime">
                            <span class="text-grey">{{ t('bwsReservation.grabTime') }}</span>
                            <strong>{{ formatTime(selectedEntry.reserveTime) }}</strong>
                        </div>
                        <div>
                            <span class="text-grey">{{ t('bwsReservation.delay') }}</span>
                            <strong>{{ selectedEntry.startDelayMs }}ms</strong>
                        </div>
                        <div>
                            <span class="text-grey">{{ t('bwsReservation.interval') }}</span>
                            <strong>{{ selectedEntry.loopDelayMs }}ms</strong>
                        </div>
                        <div v-if="selectedStatus && selectedStatus.error" class="w-100">
                            <span class="text-grey">{{ t('bwsReservation.errorLabel') }}</span>
                            <span class="text-red">{{ selectedStatus.error }}</span>
                        </div>
                    </div>
                </v-card>
                <TaskLogViewer :task-id="selectedHash" :key="selectedHash" />
            </template>

            <v-card v-else variant="outlined" class="pa-6 text-center">
                <v-icon size="48" color="grey">mdi-console-line</v-icon>
                <p class="text-grey mt-2 mb-0">{{ t('bwsReservation.selectLog') }}</p>
            </v-card>
        </template>

        <!-- ── Bind Ticket Dialog ─────────────────────────────── -->
        <v-dialog v-model="showBindDialog" max-width="500" persistent>
            <v-card :title="t('bwsReservation.bindTicket')">
                <v-card-text>
                    <v-alert density="compact" variant="tonal" color="warning" class="mb-3" closable>
                        {{ t('bwsReservation.bindRequiredDesc') }}
                    </v-alert>

                    <v-row dense>
                        <v-col cols="6">
                            <v-text-field v-model="bindBid" :label="t('bwsReservation.bindBid')" type="number"
                                variant="outlined" density="compact" hide-details="auto" />
                        </v-col>
                        <v-col cols="6">
                            <v-select v-model="bindIdType" :label="t('bwsReservation.bindIdType')"
                                :items="idTypeOptions" item-title="title" item-value="value"
                                variant="outlined" density="compact" hide-details="auto" />
                        </v-col>
                    </v-row>
                    <v-row dense class="mt-1">
                        <v-col cols="6">
                            <v-text-field v-model="bindTicketNo" :label="t('bwsReservation.bindTicketNo')"
                                variant="outlined" density="compact" hide-details="auto" placeholder="1234" />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="bindUserName" :label="t('bwsReservation.bindUserName')"
                                variant="outlined" density="compact" hide-details="auto" />
                        </v-col>
                    </v-row>
                    <v-text-field v-model="bindPersonalId" :label="t('bwsReservation.bindPersonalId')"
                        variant="outlined" density="compact" hide-details="auto" class="mt-1" />
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

        <!-- ── Fetch BWS Info Dialog ─────────────────────────── -->
        <v-dialog v-model="showFetchDialog" max-width="800" scrollable>
            <v-card :title="t('bwsReservation.fetchDialogTitle')">
                <v-card-text>
                    <div class="d-flex ga-2 mb-4 mt-2 align-center">
                        <v-text-field v-model="reserveDates" :label="t('bwsReservation.datesLabel')"
                            placeholder="20250711,20250712,20250713" variant="outlined" density="compact"
                            hide-details="auto" style="max-width: 360px;" @keyup.enter="fetchBWSInfo" />
                        <v-btn :loading="fetchingInfo" color="primary" @click="fetchBWSInfo">{{
                            t('bwsReservation.query')
                            }}</v-btn>
                    </div>

                    <div v-if="bwsData?.ActivityMapping">
                        <div class="text-subtitle-2 mb-2">
                            {{ t('bwsReservation.activityList', { count: Object.keys(bwsData.ActivityMapping).length })
                            }}
                        </div>
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
                                <tr v-for="(act, id) in bwsData.ActivityMapping" :key="id"
                                    :class="{ 'text-grey': bwsData.ReservedIDs?.[id] || act.State === 3 }">
                                    <td>{{ id }}</td>
                                    <td>
                                        {{ act.ActTitle }}
                                        <v-chip v-if="bwsData.ReservedIDs?.[id]" color="green" size="x-small"
                                            variant="tonal">{{
                                            t('bwsReservation.reserved') }}</v-chip>
                                        <v-chip v-else-if="act.State === 3" color="grey" size="x-small"
                                            variant="tonal">{{
                                            t('bwsReservation.ended') }}</v-chip>
                                    </td>
                                    <td>{{ act.ReserveDate }}</td>
                                    <td>
                                        <code>{{ (getTicketForDate(act.ReserveDate) || '—').slice(0, 8) }}...</code>
                                    </td>
                                    <td>{{ formatTime(act.ReserveBeginTime) }}</td>
                                    <td>
                                        <v-btn v-if="!bwsData.ReservedIDs?.[id] && act.State !== 3"
                                            icon="mdi-plus-circle-outline" size="x-small" variant="text" color="primary"
                                            :disabled="!getTicketForDate(act.ReserveDate)"
                                            :title="!getTicketForDate(act.ReserveDate) ? t('bwsReservation.noTicketForDate') : t('bwsReservation.addActivity')"
                                            @click="openAddDialog(act)" />
                                    </td>
                                </tr>
                            </tbody>
                        </v-table>
                    </div>
                    <div v-else-if="!fetchingInfo" class="text-grey text-center pa-4">
                        {{ t('bwsReservation.inputDatesHint') }}
                    </div>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showFetchDialog = false">{{ t('bwsReservation.close') }}</v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ── Add Entry Dialog ──────────────────────────────── -->
        <v-dialog v-model="showAddDialog" max-width="480">
            <v-card :title="t('bwsReservation.addBwsEntry')">
                <v-card-text v-if="selectedActivity">
                    <v-alert density="compact" variant="tonal" color="info" class="mb-3">
                        <strong>{{ selectedActivity.ActTitle }}</strong>
                        <br />
                        {{ t('bwsReservation.date') }} {{ selectedActivity.ReserveDate }} | ID: {{
                        selectedActivity.ReserveID }}
                        <br />
                        {{ t('bwsReservation.grabTime') }} {{ formatTime(selectedActivity.ReserveBeginTime) }}
                        <br />
                        {{ t('bwsReservation.ticketNo') }} {{ getTicketForDate(selectedActivity.ReserveDate) ||
                            t('bwsReservation.ticketNotFoundShort') }}
                    </v-alert>

                    <v-row dense>
                        <v-col cols="6">
                            <v-text-field v-model="formStartDelayMs" :label="t('bwsReservation.startDelayLabel')"
                                type="number" variant="outlined" density="compact" hide-details="auto"
                                :hint="t('bwsReservation.startDelayHint')" persistent-hint />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="formLoopDelayMs" :label="t('bwsReservation.loopDelayLabel')"
                                type="number" variant="outlined" density="compact" hide-details="auto"
                                :hint="t('bwsReservation.loopDelayHint')" persistent-hint />
                        </v-col>
                    </v-row>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showAddDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" variant="tonal" @click="submitAddEntry"
                        :disabled="!selectedActivity || !getTicketForDate(selectedActivity?.ReserveDate)">
                        {{ t('bwsReservation.addBtn') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </div>
</template>

<style scoped>
.v-list-item {
    cursor: pointer;
}

code {
    font-size: 0.8em;
    background: rgba(0, 0, 0, 0.05);
    padding: 1px 4px;
    border-radius: 3px;
}
</style>
