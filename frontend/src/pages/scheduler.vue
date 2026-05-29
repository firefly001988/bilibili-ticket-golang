<script lang="ts" setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useMessagesStore } from '@/stores/snackbar'
import TaskLogViewer from '@/components/TaskLogViewer.vue'
import {
    GetAllTickets,
    AddTicket,
    RemoveTicket,
    AddTicketTask,
    RemoveTask,
    ForceStartTask,
    GetTaskStatuses,
    GetRetryInterval,
} from '../../wailsjs/go/scheduler/SchedulerService'
import type { FrontendTicket, FrontendTaskStatus } from '@/composables/schedulerTypes'
import { statColor, statLabel, StatWaiting, StatPending } from '@/composables/schedulerTypes'
import { DEFAULT_EXPIRE_DAYS, SECONDS_PER_DAY } from '@/composables/defaults'
import { useDebug } from '@/composables/useDebug'

const auth = useAuthStore()
const { debugLog } = useDebug();
const messages = useMessagesStore()

// ── State ──────────────────────────────────────────────
const tickets = ref<FrontendTicket[]>([])
const taskStatuses = ref<FrontendTaskStatus[]>([])
const selectedHash = ref('')
const loading = ref(false)
const showAddDialog = ref(false)
const pollInterval = 2000

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
        const intervalMs = await GetRetryInterval()
        debugLog('[startTask] calling AddTicketTask with hash:', hash, 'intervalMs:', intervalMs)
        await AddTicketTask(hash, intervalMs)
        debugLog('[startTask] AddTicketTask returned successfully for hash:', hash)
        messages.add({ text: '任务已启动', color: 'success', timeout: 2000 })
        await refresh()
        selectedHash.value = hash
    } catch (e: any) {
        messages.add({ text: `启动失败: ${e}`, color: 'error', timeout: 4000 })
    } finally {
        loading.value = false
    }
}

async function stopTask(hash: string) {
    try {
        debugLog('[stopTask] calling RemoveTask for hash:', hash)
        await RemoveTask(hash)
        debugLog('[stopTask] RemoveTask returned successfully')
        messages.add({ text: '任务已停止', color: 'info', timeout: 2000 })
        await refresh()
    } catch (e: any) {
        messages.add({ text: `停止失败: ${e}`, color: 'error', timeout: 4000 })
    }
}

async function forceStart(hash: string) {
    try {
        debugLog('[forceStart] calling ForceStartTask for hash:', hash)
        await ForceStartTask(hash)
        debugLog('[forceStart] ForceStartTask returned successfully')
        messages.add({ text: '已强制启动任务', color: 'warning', timeout: 2000 })
        await refresh()
    } catch (e: any) {
        messages.add({ text: `强制启动失败: ${e}`, color: 'error', timeout: 4000 })
    }
}

async function deleteTicket(hash: string) {
    try {
        debugLog('[deleteTicket] calling RemoveTicket for hash:', hash)
        await RemoveTicket(hash)
        debugLog('[deleteTicket] RemoveTicket returned successfully')
        if (selectedHash.value === hash) selectedHash.value = ''
        messages.add({ text: '票据已删除', color: 'info', timeout: 2000 })
        await refresh()
    } catch (e: any) {
        messages.add({ text: `删除失败: ${e}`, color: 'error', timeout: 4000 })
    }
}

const formErrors = ref<string[]>([])

function validateForm(): boolean {
    formErrors.value = []
    const f = form.value
    if (!f.projectId) formErrors.value.push('请填写项目 ID')
    if (!f.projectName) formErrors.value.push('请填写项目名称')
    if (!f.screenId) formErrors.value.push('请填写场次 ID')
    if (!f.screenName) formErrors.value.push('请填写场次名称')
    if (!f.skuId) formErrors.value.push('请填写票种 ID')
    if (!f.skuName) formErrors.value.push('请填写票种名称')
    if (!formDate.value) formErrors.value.push('请选择开售日期')
    if (!formTime.value) formErrors.value.push('请选择开售时间')
    if (!f.buyerName.trim()) formErrors.value.push('请填写购票人姓名')
    if (!f.buyerTel.trim()) formErrors.value.push('请填写购票人电话')
    if (f.buyerId < 0) formErrors.value.push('实名 ID 不能为负数')
    return formErrors.value.length === 0
}

async function submitAddTicket() {
    if (!validateForm()) {
        messages.add({ text: `请完善以下信息: ${formErrors.value.join('、')}`, color: 'warning', timeout: 4000 })
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
        })

        debugLog('[submitAddTicket] AddTicket returned hash:', hash);

        messages.add({ text: `票据已添加 (${hash.slice(0, 8)}...)`, color: 'success', timeout: 2000 })
        showAddDialog.value = false
        await refresh()
    } catch (e: any) {
        messages.add({ text: `添加失败: ${e}`, color: 'error', timeout: 4000 })
    }
}

function formatTime(ts: string): string {
    if (!ts) return '—'
    const d = new Date(ts)
    if (isNaN(d.getTime())) return ts
    return d.toLocaleString('zh-CN')
}

function formatRemaining(ms: number): string {
    if (ms <= 0) return '已过期'
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
            <h1 class="text-h5">Scheduler</h1>
            <v-spacer />
            <v-btn v-if="auth.isLogin" prepend-icon="mdi-plus" color="primary" variant="tonal" size="small"
                @click="showAddDialog = true">
                添加票据
            </v-btn>
        </div>
        <v-divider thickness="3" class="mb-4" />

        <v-card v-if="!auth.isLogin" color="warning" variant="tonal" class="pa-4">
            <v-card-text>
                请先 <router-link to="/account">登录</router-link> 后才能管理任务。
            </v-card-text>
        </v-card>

        <template v-else>
            <!-- Main content: ticket list + log viewer (vertical) -->
            <!-- Ticket list -->
            <v-card variant="outlined" class="mb-4">
                <v-card-title class="d-flex align-center py-2 px-3">
                    <span class="text-body-medium">票据列表 ({{ mergedList.length }})</span>
                    <v-spacer />
                    <v-btn icon="mdi-refresh" size="x-small" variant="text" :loading="loading" @click="refresh" />
                </v-card-title>
                <v-divider />
                <v-card-text class="pa-0" style="max-height: 600px; overflow-y: auto;">
                    <div v-if="mergedList.length === 0" class="text-label-medium text-grey pa-6 text-center">
                        暂无票据 — 点击「添加票据」或前往
                        <router-link to="/ticket-project">项目查找</router-link>
                    </div>
                    <v-list v-else density="compact" lines="two">
                        <v-list-item v-for="t in mergedList" :key="t.hash" :active="selectedHash === t.hash"
                            @click="selectedHash = t.hash">
                            <template #prepend>
                                <v-icon :color="statColor(t.displayStat)" size="18">
                                    {{ t.displayStat === 0 ? 'mdi-clock-outline' :
                                        t.displayStat === 1 ? 'mdi-progress-clock' :
                                            t.displayStat === 2 ? 'mdi-check-circle' :
                                                t.displayStat === 3 ? 'mdi-close-circle' : 'mdi-alert-circle' }}
                                </v-icon>
                            </template>

                            <template #title>
                                <span class="text-body-2">{{ t.projectName }}</span>
                                <v-chip :color="statColor(t.displayStat)" size="x-small" variant="tonal" class="ml-1">
                                    {{ statLabel(t.displayStat) }}
                                </v-chip>
                            </template>

                            <template #subtitle>
                                <span class="text-caption text-grey">
                                    {{ t.screenName }} · {{ t.skuName }} · {{ t.buyerName }}
                                    <template v-if="t.status && t.status.stat === StatWaiting">
                                        · 剩余 {{ formatRemaining(t.status.remainingMs) }}
                                    </template>
                                </span>
                            </template>

                            <template #append>
                                <div class="d-flex ga-0">
                                    <v-btn v-if="!isTaskActive(t.hash)" icon="mdi-play" size="x-small" variant="text"
                                        color="success" @click.stop="startTask(t.hash)" />
                                    <v-btn v-else icon="mdi-stop" size="x-small" variant="text" color="error"
                                        @click.stop="stopTask(t.hash)" />
                                    <v-btn icon="mdi-lightning-bolt" size="x-small" variant="text" color="warning"
                                        @click.stop="forceStart(t.hash)" />
                                    <v-btn icon="mdi-delete-outline" size="x-small" variant="text" color="grey"
                                        @click.stop="deleteTicket(t.hash)" />
                                </div>
                            </template>
                        </v-list-item>
                    </v-list>
                </v-card-text>
            </v-card>

            <!-- Log viewer + details -->
            <template v-if="selectedHash && selectedTicket">
                <!-- Ticket details card -->
                <v-card variant="outlined" class="mb-2 pa-3">
                    <div class="d-flex flex-wrap ga-3 text-caption">
                        <div>
                            <span class="text-grey">项目:</span>
                            <strong>{{ selectedTicket.projectName }}</strong>
                            ({{ selectedTicket.projectId }})
                        </div>
                        <div>
                            <span class="text-grey">场次:</span>
                            <strong>{{ selectedTicket.screenName }}</strong>
                            ({{ selectedTicket.screenId }})
                        </div>
                        <div>
                            <span class="text-grey">票种:</span>
                            <strong>{{ selectedTicket.skuName }}</strong>
                            ({{ selectedTicket.skuId }})
                        </div>
                        <div>
                            <span class="text-grey">购票人:</span>
                            <strong>{{ selectedTicket.buyerName }}</strong>
                        </div>
                        <div>
                            <span class="text-grey">状态:</span>
                            <v-chip v-if="selectedStatus" :color="statColor(selectedStatus.stat)" size="x-small"
                                variant="tonal">
                                {{ statLabel(selectedStatus.stat) }}
                            </v-chip>
                        </div>
                        <div v-if="selectedTicket.start">
                            <span class="text-grey">开售:</span>
                            <strong>{{ formatTime(new Date(selectedTicket.start * 1000).toLocaleString('zh-CN'))
                                }}</strong>
                        </div>
                        <div v-if="selectedStatus && selectedStatus.error" class="w-100">
                            <span class="text-grey">错误:</span>
                            <span class="text-red">{{ selectedStatus.error }}</span>
                        </div>
                        <div v-if="selectedStatus && selectedStatus.stat === StatWaiting && selectedStatus.remainingMs > 0"
                            class="w-100">
                            <span class="text-grey">剩余:</span>
                            <strong>{{ formatRemaining(selectedStatus.remainingMs) }}</strong>
                        </div>
                    </div>
                </v-card>

                <!-- Log viewer -->
                <TaskLogViewer :task-id="selectedHash" :key="selectedHash" />
            </template>

            <v-card v-else variant="outlined" class="pa-6 text-center">
                <v-icon size="48" color="grey">mdi-console-line</v-icon>
                <p class="text-grey mt-2 mb-0">选择上方的票据查看实时日志</p>
            </v-card>
        </template>

        <!-- Add Ticket Dialog -->
        <v-dialog v-model="showAddDialog" max-width="560">
            <v-card title="添加票据">
                <v-card-text>
                    <v-row dense>
                        <v-col cols="6">
                            <v-text-field v-model="form.projectId" label="项目 ID" type="number" variant="outlined"
                                density="compact" required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="form.projectName" label="项目名称" variant="outlined" density="compact"
                                required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="form.screenId" label="场次 ID" type="number" variant="outlined"
                                density="compact" required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="form.screenName" label="场次名称" variant="outlined" density="compact"
                                required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="form.skuId" label="票种 ID" type="number" variant="outlined"
                                density="compact" required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="form.skuName" label="票种名称" variant="outlined" density="compact"
                                required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="formDate" label="开售日期" type="date" variant="outlined"
                                density="compact" />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="formTime" label="开售时间" type="time" variant="outlined"
                                density="compact" />
                        </v-col>
                        <v-col cols="4">
                            <v-text-field v-model="form.buyerName" label="购票人姓名" variant="outlined" density="compact"
                                required />
                        </v-col>
                        <v-col cols="4">
                            <v-text-field v-model="form.buyerTel" label="购票人电话" variant="outlined" density="compact"
                                required />
                        </v-col>
                        <v-col cols="4">
                            <v-text-field v-model="form.buyerId" label="实名 ID (0=普通)" type="number" variant="outlined"
                                density="compact" hint="0 为普通购票，>0 为强实名" persistent-hint required />
                        </v-col>
                    </v-row>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showAddDialog = false">取消</v-btn>
                    <v-btn color="primary" variant="tonal" @click="submitAddTicket">添加</v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </div>
</template>

<style scoped>
.v-list-item {
    cursor: pointer;
}
</style>
