<script lang="ts" setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useMessagesStore } from '@/stores/snackbar'
import TaskLogViewer from '@/components/TaskLogViewer.vue'
import draggable from 'vuedraggable'
import {
    GetAllTickets,
    AddTicket,
    RemoveTicket,
    AddTicketTask,
    RemoveTask,
    ForceStartTask,
    GetTaskStatuses,
    ReorderTickets,
    GetChainTrigger,
    SetChainTrigger,
} from '../../wailsjs/go/scheduler/SchedulerService'
import type { FrontendTicket, FrontendTaskStatus } from '@/composables/schedulerTypes'
import { statColor, statLabel, StatWaiting, StatPending } from '@/composables/schedulerTypes'
import { DEFAULT_EXPIRE_DAYS, SECONDS_PER_DAY } from '@/composables/defaults'
import { useDebug } from '@/composables/useDebug'

const auth = useAuthStore()
const { debugLog } = useDebug();
const { t } = useI18n()
const messages = useMessagesStore()

// ── State ──────────────────────────────────────────────
const tickets = ref<FrontendTicket[]>([])
const taskStatuses = ref<FrontendTaskStatus[]>([])
const selectedHash = ref('')
const loading = ref(false)
const showAddDialog = ref(false)
const pollInterval = 2000

// ── Chain switching ───────────────────────────────────
const chainTrigger = ref<'success' | 'any'>('success')
const chainTriggerLoading = ref(false)

// ── Add ticket form ────────────────────────────────────
const form = ref({
    projectId: 0,
    projectName: '',
    screenId: 0,
    screenName: '',
    skuId: 0,
    skuName: '',
    start: 0,      // unix timestamp
    expire: 0,     // unix timestamp
    buyerName: '',
    buyerTel: '',
    buyerId: 0,
})
const formDate = ref('')
const formTime = ref('')

// ── Computed ───────────────────────────────────────────
const selectedTicket = computed(() =>
    tickets.value.find(t => t.hash === selectedHash.value)
)

const selectedStatus = computed(() => {
    const live = taskStatuses.value.find(s => s.taskID === selectedHash.value)
    if (live) return live
    // Fall back to persisted stat when task is no longer live
    const ticket = tickets.value.find(t => t.hash === selectedHash.value)
    if (ticket) {
        return {
            taskID: ticket.hash,
            targetTime: '',
            adjustedTime: '',
            remainingMs: 0,
            stat: ticket.stat ?? 0,
            statName: statLabel(ticket.stat ?? 0),
            error: '',
            projectName: ticket.projectName,
            screenName: ticket.screenName,
            skuName: ticket.skuName,
            buyerName: ticket.buyerName,
        } as FrontendTaskStatus
    }
    return undefined
})

// Merge tickets with task statuses for the list.
// Falls back to ticket.stat (persisted) when the task is no longer live.
const mergedList = computed(() => {
    const statusMap = new Map(taskStatuses.value.map(s => [s.taskID, s]))
    return tickets.value.map(t => {
        const liveStatus = statusMap.get(t.hash)
        // When the task is done, the scheduler removes it; fall back to persisted stat
        const stat = liveStatus ? liveStatus.stat : (t.stat ?? 0)
        return {
            ...t,
            status: liveStatus,
            // Synthetic status-like object for templates that use .stat / .statName / .error
            displayStat: liveStatus ? liveStatus.stat : (t.stat ?? 0),
            displayError: liveStatus?.error || null,
        }
    })
})

function isTaskActive(hash: string): boolean {
    const s = taskStatuses.value.find(x => x.taskID === hash)
    return s ? (s.stat === StatWaiting || s.stat === StatPending) : false
}

// ── Chain grouping & ordering ──────────────────────────
// Chain group key mirrors the Go TicketEntry.ChainGroupKey():
//   real-name + same project → "r:" + buyerId + ":p:" + projectId
//   ordinary (non-real-name) → "" (never chained, shown individually)
function chainGroupKey(t: FrontendTicket): string {
    return (t.buyerId ?? 0) > 0 ? `r:${t.buyerId}:p:${t.projectId}` : ''
}

// Tickets grouped into chains. Real-name tickets sharing the same buyer +
// project form a draggable chain group; ordinary tickets are placed in
// single-item groups (no drag, no chain).
interface GroupedTickets {
    key: string
    buyerName: string
    projectName: string
    chainable: boolean
    items: Array<FrontendTicket & { status?: FrontendTaskStatus; displayStat: number; displayError: string | null }>
}

const buyerGroups = computed<GroupedTickets[]>(() => {
    const map = new Map<string, GroupedTickets>()
    const statusMap = new Map(taskStatuses.value.map(s => [s.taskID, s]))
    for (const t of tickets.value) {
        const key = chainGroupKey(t)
        const liveStatus = statusMap.get(t.hash)
        const item = {
            ...t,
            status: liveStatus,
            displayStat: liveStatus ? liveStatus.stat : (t.stat ?? 0),
            displayError: liveStatus?.error || null,
        }
        // Real-name: group by chain key; ordinary: unique key per ticket.
        const groupKey = key || `single:${t.hash}`
        let g = map.get(groupKey)
        if (!g) {
            g = {
                key: groupKey,
                buyerName: t.buyerName,
                projectName: t.projectName,
                chainable: key !== '',
                items: [],
            }
            map.set(groupKey, g)
        }
        g.items.push(item)
    }
    const groups = Array.from(map.values())
    for (const g of groups) {
        g.items.sort((a, b) => (a.sortOrder ?? 0) - (b.sortOrder ?? 0))
    }
    // Sort groups by buyer name then project name for stable display
    groups.sort((a, b) =>
        a.buyerName.localeCompare(b.buyerName) || a.projectName.localeCompare(b.projectName))
    return groups
})

async function onDragEnd(group: GroupedTickets) {
    const orderedHashes = group.items.map(i => i.hash)
    try {
        await ReorderTickets(orderedHashes)
        messages.add({ text: t('scheduler.orderSaved'), color: 'success', timeout: 2000 })
        await refresh()
    } catch (e: any) {
        messages.add({ text: t('scheduler.orderSaveFailed', { error: String(e) }), color: 'error', timeout: 4000 })
        await refresh() // revert to persisted order on failure
    }
}

async function loadChainTrigger() {
    try {
        const mode = await GetChainTrigger()
        chainTrigger.value = mode === 'any' ? 'any' : 'success'
    } catch { /* ignore */ }
}

async function onChainTriggerChange(mode: 'success' | 'any') {
    chainTriggerLoading.value = true
    try {
        await SetChainTrigger(mode)
        messages.add({ text: t('scheduler.chainTriggerSaved'), color: 'success', timeout: 2000 })
    } catch (e: any) {
        messages.add({ text: t('scheduler.chainTriggerSaveFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    } finally {
        chainTriggerLoading.value = false
    }
}

// ── Actions ────────────────────────────────────────────
async function refresh() {
    try {
        const [tks, sts] = await Promise.all([
            GetAllTickets(),
            GetTaskStatuses(),
        ])
        debugLog('[refresh] GetAllTickets:', tks);
        debugLog('[refresh] GetTaskStatuses:', sts);
        tickets.value = tks || []
        taskStatuses.value = sts || []
    } catch (e: any) {
        console.error('Refresh failed:', e)
    }
}

async function startTask(hash: string) {
    loading.value = true
    try {
        debugLog('[startTask] calling AddTicketTask for hash:', hash)
        await AddTicketTask(hash)
        debugLog('[startTask] AddTicketTask returned successfully for hash:', hash)
        messages.add({ text: t('scheduler.taskStarted'), color: 'success', timeout: 2000 })
        await refresh()
        selectedHash.value = hash
    } catch (e: any) {
        messages.add({ text: t('scheduler.startFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    } finally {
        loading.value = false
    }
}

async function stopTask(hash: string) {
    try {
        debugLog('[stopTask] calling RemoveTask for hash:', hash)
        await RemoveTask(hash)
        debugLog('[stopTask] RemoveTask returned successfully')
        messages.add({ text: t('scheduler.taskStopped'), color: 'info', timeout: 2000 })
        await refresh()
    } catch (e: any) {
        messages.add({ text: t('scheduler.stopFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    }
}

async function forceStart(hash: string) {
    try {
        debugLog('[forceStart] calling ForceStartTask for hash:', hash)
        await ForceStartTask(hash)
        debugLog('[forceStart] ForceStartTask returned successfully')
        messages.add({ text: t('scheduler.forceStarted'), color: 'warning', timeout: 2000 })
        await refresh()
    } catch (e: any) {
        messages.add({ text: t('scheduler.forceStartFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    }
}

async function deleteTicket(hash: string) {
    try {
        debugLog('[deleteTicket] calling RemoveTicket for hash:', hash)
        await RemoveTicket(hash)
        debugLog('[deleteTicket] RemoveTicket returned successfully')
        if (selectedHash.value === hash) selectedHash.value = ''
        messages.add({ text: t('scheduler.ticketDeleted'), color: 'info', timeout: 2000 })
        await refresh()
    } catch (e: any) {
        messages.add({ text: t('scheduler.deleteFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    }
}

const formErrors = ref<string[]>([])

function validateForm(): boolean {
    formErrors.value = []
    const f = form.value
    if (!f.projectId) formErrors.value.push(t('scheduler.formProjectId'))
    if (!f.projectName) formErrors.value.push(t('scheduler.formProjectName'))
    if (!f.screenId) formErrors.value.push(t('scheduler.formScreenId'))
    if (!f.screenName) formErrors.value.push(t('scheduler.formScreenName'))
    if (!f.skuId) formErrors.value.push(t('scheduler.formSkuId'))
    if (!f.skuName) formErrors.value.push(t('scheduler.formSkuName'))
    if (!formDate.value) formErrors.value.push(t('scheduler.formDate'))
    if (!formTime.value) formErrors.value.push(t('scheduler.formTime'))
    if (!f.buyerName.trim()) formErrors.value.push(t('scheduler.formBuyerName'))
    if (!f.buyerTel.trim()) formErrors.value.push(t('scheduler.formBuyerTel'))
    if (f.buyerId < 0) formErrors.value.push(t('scheduler.formBuyerId'))
    return formErrors.value.length === 0
}

async function submitAddTicket() {
    if (!validateForm()) {
        messages.add({ text: t('scheduler.formIncomplete', { fields: formErrors.value.join('、') }), color: 'warning', timeout: 4000 })
        return
    }
    const startDate = formDate.value ? new Date(formDate.value + 'T' + (formTime.value || '00:00:00')) : new Date()
    const startUnix = Math.floor(startDate.getTime() / 1000)
    // Expire 30 days from now, ensuring it's never in the past
    const expireUnix = Math.floor(Date.now() / 1000) + SECONDS_PER_DAY * DEFAULT_EXPIRE_DAYS

    try {
        const hash = await AddTicket({
            hash: '',
            projectId: Number(form.value.projectId),
            projectName: form.value.projectName,
            screenId: Number(form.value.screenId),
            screenName: form.value.screenName,
            skuId: Number(form.value.skuId),
            skuName: form.value.skuName,
            start: startUnix,
            expire: expireUnix,
            buyerName: form.value.buyerName,
            buyerTel: form.value.buyerTel,
            buyerId: Number(form.value.buyerId),
            stat: 0,
            sortOrder: 0,
        })

        debugLog('[submitAddTicket] AddTicket returned hash:', hash);

        messages.add({ text: t('scheduler.ticketAdded', { hash: hash.slice(0, 8) }), color: 'success', timeout: 2000 })
        showAddDialog.value = false
        await refresh()
    } catch (e: any) {
        messages.add({ text: t('scheduler.addFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    }
}

function formatTime(ts: string): string {
    if (!ts) return '—'
    const d = new Date(ts)
    if (isNaN(d.getTime())) return ts
    return d.toLocaleString('zh-CN')
}

function formatRemaining(ms: number): string {
    if (ms <= 0) return t('scheduler.expired')
    const abs = Math.abs(ms)
    const h = Math.floor(abs / 3600000)
    const m = Math.floor((abs % 3600000) / 60000)
    const s = Math.floor((abs % 60000) / 1000)
    if (h > 0) return `${h}h ${m}m ${s}s`
    if (m > 0) return `${m}m ${s}s`
    return `${s}s`
}

// ── Lifecycle ──────────────────────────────────────────
let timer: ReturnType<typeof setInterval> | null = null

onMounted(async () => {
    await auth.checkLoginStatus()
    if (auth.isLogin) {
        await Promise.all([refresh(), loadChainTrigger()])
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
            <h1 class="text-h5">{{ t('scheduler.title') }}</h1>
            <v-spacer />
            <v-btn v-if="auth.isLogin" prepend-icon="mdi-plus" color="primary" variant="tonal" size="small"
                @click="showAddDialog = true">
                {{ t('scheduler.addTicket') }}
            </v-btn>
        </div>
        <v-divider thickness="3" class="mb-4" />

        <v-card v-if="!auth.isLogin" color="warning" variant="tonal" class="pa-4">
            <v-card-text>
                {{ t('scheduler.loginRequired') }}
            </v-card-text>
        </v-card>

        <template v-else>
            <!-- Chain trigger toggle -->
            <v-card variant="outlined" class="mb-4 pa-3">
                <div class="d-flex align-center flex-wrap ga-3">
                    <v-icon icon="mdi-link-variant" size="small" />
                    <span class="text-body-2">{{ t('scheduler.chainTrigger') }}</span>
                    <v-btn-toggle v-model="chainTrigger" mandatory color="primary" density="compact"
                        :loading="chainTriggerLoading"
                        @update:model-value="(v: string) => onChainTriggerChange(v === 'any' ? 'any' : 'success')">
                        <v-btn value="success" size="small">{{ t('scheduler.chainTriggerSuccess') }}</v-btn>
                        <v-btn value="any" size="small">{{ t('scheduler.chainTriggerAny') }}</v-btn>
                    </v-btn-toggle>
                    <span class="text-caption text-grey">{{ t('scheduler.chainTriggerHint') }}</span>
                </div>
            </v-card>

            <!-- Main content: ticket list + log viewer (vertical) -->
            <!-- Ticket list grouped by buyer with drag-to-reorder -->
            <v-card variant="outlined" class="mb-4">
                <v-card-title class="d-flex align-center py-2 px-3">
                    <span class="text-body-medium">{{ t('scheduler.ticketList', { count: mergedList.length }) }}</span>
                    <v-spacer />
                    <v-btn icon="mdi-refresh" size="x-small" variant="text" :loading="loading" @click="refresh" />
                </v-card-title>
                <v-divider />
                <v-card-text class="pa-0" style="max-height: 600px; overflow-y: auto;">
                    <div v-if="mergedList.length === 0" class="text-label-medium text-grey pa-6 text-center">
                        {{ t('scheduler.emptyTickets') }}
                        <router-link to="/ticket-project">{{ t('nav.projectLookup') }}</router-link>
                    </div>
                    <div v-else>
                        <div v-for="group in buyerGroups" :key="group.key" class="border-b">
                            <!-- Group header -->
                            <div class="d-flex align-center px-3 py-2">
                                <v-icon icon="mdi-account" size="small" class="mr-2" />
                                <span class="text-body-2 font-weight-bold">{{ group.buyerName }}</span>
                                <v-icon v-if="group.chainable" icon="mdi-link-variant" size="x-small" class="mx-1"
                                    color="primary" />
                                <span v-if="group.chainable" class="text-caption text-grey ml-1">
                                    {{ group.projectName }}
                                </span>
                                <v-chip size="x-small" variant="tonal" class="ml-2">
                                    {{ group.items.length }}
                                </v-chip>
                                <span v-if="group.chainable" class="text-caption text-grey ml-2">
                                    {{ t('scheduler.dragHint') }}
                                </span>
                            </div>
                            <!-- Draggable ticket list within chain group (real-name only) -->
                            <!-- Each group uses a unique group name so items can only be dragged
                                 within the same buyer+project chain, never across chains. -->
                            <draggable v-if="group.chainable" :list="group.items" item-key="hash" handle=".drag-handle"
                                :animation="150" :group="group.key" @end="onDragEnd(group)">
                                <template #item="{ element: tk, index }">
                                    <v-list-item :key="tk.hash" :active="selectedHash === tk.hash" lines="two"
                                        @click="selectedHash = tk.hash">
                                        <template #prepend>
                                            <v-icon class="drag-handle" icon="mdi-drag-vertical" size="18"
                                                color="grey-lighten-1" />
                                            <v-chip size="x-small" variant="outlined" class="mr-2">{{ index + 1
                                            }}</v-chip>
                                            <v-icon :color="statColor(tk.displayStat)" size="18">
                                                {{ tk.displayStat === 0 ? 'mdi-clock-outline' :
                                                    tk.displayStat === 1 ? 'mdi-progress-clock' :
                                                        tk.displayStat === 2 ? 'mdi-check-circle' :
                                                            tk.displayStat === 3 ? 'mdi-close-circle' : 'mdi-alert-circle' }}
                                            </v-icon>
                                        </template>

                                        <template #title>
                                            <span class="text-body-2">{{ tk.projectName }}</span>
                                            <v-chip :color="statColor(tk.displayStat)" size="x-small" variant="tonal"
                                                class="ml-1">
                                                {{ statLabel(tk.displayStat) }}
                                            </v-chip>
                                        </template>

                                        <template #subtitle>
                                            <span class="text-caption text-grey">
                                                {{ tk.screenName }} · {{ tk.skuName }}
                                                <template v-if="tk.status && tk.status.stat === StatWaiting">
                                                    · {{ t('scheduler.remaining', {
                                                        time:
                                                            formatRemaining(tk.status.remainingMs)
                                                    }) }}
                                                </template>
                                            </span>
                                        </template>

                                        <template #append>
                                            <div class="d-flex ga-0">
                                                <v-btn v-if="!isTaskActive(tk.hash)" icon="mdi-play" size="x-small"
                                                    variant="text" color="success" @click.stop="startTask(tk.hash)" />
                                                <v-btn v-else icon="mdi-stop" size="x-small" variant="text"
                                                    color="error" @click.stop="stopTask(tk.hash)" />
                                                <v-btn icon="mdi-lightning-bolt" size="x-small" variant="text"
                                                    color="warning" @click.stop="forceStart(tk.hash)" />
                                                <v-btn icon="mdi-delete-outline" size="x-small" variant="text"
                                                    color="grey" @click.stop="deleteTicket(tk.hash)" />
                                            </div>
                                        </template>
                                    </v-list-item>
                                </template>
                            </draggable>
                            <!-- Non-chainable (ordinary) tickets: plain list, no drag -->
                            <v-list v-else density="compact" lines="two">
                                <v-list-item v-for="tk in group.items" :key="tk.hash" :active="selectedHash === tk.hash"
                                    @click="selectedHash = tk.hash">
                                    <template #prepend>
                                        <v-icon :color="statColor(tk.displayStat)" size="18">
                                            {{ tk.displayStat === 0 ? 'mdi-clock-outline' :
                                                tk.displayStat === 1 ? 'mdi-progress-clock' :
                                                    tk.displayStat === 2 ? 'mdi-check-circle' :
                                                        tk.displayStat === 3 ? 'mdi-close-circle' : 'mdi-alert-circle' }}
                                        </v-icon>
                                    </template>

                                    <template #title>
                                        <span class="text-body-2">{{ tk.projectName }}</span>
                                        <v-chip :color="statColor(tk.displayStat)" size="x-small" variant="tonal"
                                            class="ml-1">
                                            {{ statLabel(tk.displayStat) }}
                                        </v-chip>
                                    </template>

                                    <template #subtitle>
                                        <span class="text-caption text-grey">
                                            {{ tk.screenName }} · {{ tk.skuName }} · {{ tk.buyerName }}
                                            <template v-if="tk.status && tk.status.stat === StatWaiting">
                                                · {{ t('scheduler.remaining', {
                                                    time:
                                                        formatRemaining(tk.status.remainingMs)
                                                }) }}
                                            </template>
                                        </span>
                                    </template>

                                    <template #append>
                                        <div class="d-flex ga-0">
                                            <v-btn v-if="!isTaskActive(tk.hash)" icon="mdi-play" size="x-small"
                                                variant="text" color="success" @click.stop="startTask(tk.hash)" />
                                            <v-btn v-else icon="mdi-stop" size="x-small" variant="text" color="error"
                                                @click.stop="stopTask(tk.hash)" />
                                            <v-btn icon="mdi-lightning-bolt" size="x-small" variant="text"
                                                color="warning" @click.stop="forceStart(tk.hash)" />
                                            <v-btn icon="mdi-delete-outline" size="x-small" variant="text" color="grey"
                                                @click.stop="deleteTicket(tk.hash)" />
                                        </div>
                                    </template>
                                </v-list-item>
                            </v-list>
                        </div>
                    </div>
                </v-card-text>
            </v-card>

            <!-- Log viewer + details -->
            <template v-if="selectedHash && selectedTicket">
                <!-- Ticket details card -->
                <v-card variant="outlined" class="mb-2 pa-3">
                    <div class="d-flex flex-wrap ga-3 text-caption">
                        <div>
                            <span class="text-grey">{{ t('scheduler.projectId') }}</span>
                            <strong>{{ selectedTicket.projectName }}</strong>
                            ({{ selectedTicket.projectId }})
                        </div>
                        <div>
                            <span class="text-grey">{{ t('scheduler.screen') }}</span>
                            <strong>{{ selectedTicket.screenName }}</strong>
                            ({{ selectedTicket.screenId }})
                        </div>
                        <div>
                            <span class="text-grey">{{ t('scheduler.sku') }}</span>
                            <strong>{{ selectedTicket.skuName }}</strong>
                            ({{ selectedTicket.skuId }})
                        </div>
                        <div>
                            <span class="text-grey">{{ t('scheduler.buyer') }}</span>
                            <strong>{{ selectedTicket.buyerName }}</strong>
                        </div>
                        <div>
                            <span class="text-grey">{{ t('scheduler.status') }}</span>
                            <v-chip v-if="selectedStatus" :color="statColor(selectedStatus.stat)" size="x-small"
                                variant="tonal">
                                {{ statLabel(selectedStatus.stat) }}
                            </v-chip>
                        </div>
                        <div v-if="selectedTicket.start">
                            <span class="text-grey">{{ t('scheduler.saleTime') }}</span>
                            <strong>{{ formatTime(new Date(selectedTicket.start * 1000).toLocaleString('zh-CN'))
                            }}</strong>
                        </div>
                        <div v-if="selectedStatus && selectedStatus.error" class="w-100">
                            <span class="text-grey">{{ t('scheduler.errorLabel') }}</span>
                            <span class="text-red">{{ selectedStatus.error }}</span>
                        </div>
                        <div v-if="selectedStatus && selectedStatus.stat === StatWaiting && selectedStatus.remainingMs > 0"
                            class="w-100">
                            <span class="text-grey">{{ t('scheduler.remainingLabel') }}</span>
                            <strong>{{ formatRemaining(selectedStatus.remainingMs) }}</strong>
                        </div>
                    </div>
                </v-card>

                <!-- Log viewer -->
                <TaskLogViewer :task-id="selectedHash" :key="selectedHash" />
            </template>

            <v-card v-else variant="outlined" class="pa-6 text-center">
                <v-icon size="48" color="grey">mdi-console-line</v-icon>
                <p class="text-grey mt-2 mb-0">{{ t('scheduler.selectLog') }}</p>
            </v-card>
        </template>

        <!-- Add Ticket Dialog -->
        <v-dialog v-model="showAddDialog" max-width="560">
            <v-card :title="t('scheduler.addTicket')">
                <v-card-text>
                    <v-row dense>
                        <v-col cols="6">
                            <v-text-field v-model="form.projectId" :label="t('scheduler.formProjectIdLabel')"
                                type="number" variant="outlined" density="compact" required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="form.projectName" :label="t('scheduler.formProjectNameLabel')"
                                variant="outlined" density="compact" required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="form.screenId" :label="t('scheduler.formScreenIdLabel')"
                                type="number" variant="outlined" density="compact" required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="form.screenName" :label="t('scheduler.formScreenNameLabel')"
                                variant="outlined" density="compact" required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="form.skuId" :label="t('scheduler.formSkuIdLabel')" type="number"
                                variant="outlined" density="compact" required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="form.skuName" :label="t('scheduler.formSkuNameLabel')"
                                variant="outlined" density="compact" required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="formDate" :label="t('scheduler.formDateLabel')" type="date"
                                variant="outlined" density="compact" />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="formTime" :label="t('scheduler.formTimeLabel')" type="time"
                                variant="outlined" density="compact" />
                        </v-col>
                        <v-col cols="4">
                            <v-text-field v-model="form.buyerName" :label="t('scheduler.formBuyerNameLabel')"
                                variant="outlined" density="compact" required />
                        </v-col>
                        <v-col cols="4">
                            <v-text-field v-model="form.buyerTel" :label="t('scheduler.formBuyerTelLabel')"
                                variant="outlined" density="compact" required />
                        </v-col>
                        <v-col cols="4">
                            <v-text-field v-model="form.buyerId" :label="t('scheduler.formBuyerIdLabel')" type="number"
                                variant="outlined" density="compact" :hint="t('scheduler.formBuyerIdHint')"
                                persistent-hint required />
                        </v-col>
                    </v-row>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showAddDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" variant="tonal" @click="submitAddTicket">{{ t('scheduler.addBtn') }}</v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </div>
</template>

<style scoped>
.v-list-item {
    cursor: pointer;
}

.drag-handle {
    cursor: grab;
}

.drag-handle:active {
    cursor: grabbing;
}

.border-b {
    border-bottom: 1px solid rgba(0, 0, 0, 0.12);
}
</style>
