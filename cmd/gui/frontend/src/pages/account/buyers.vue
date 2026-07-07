<script lang="ts" setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import AccountPicker from '@/components/cluster/AccountPicker.vue'
import {
    Snapshot,
    ProvisionBuyer,
    UpdateBuyerPhone,
    RemoveBuyerFromAccount,
    RemoveBuyerFromAllAccounts,
    SyncAllAccountBuyers,
    SyncAllAccountBuyersFast,
    StartBuyerSync,
    GetBuyerSyncBatch,
} from '../../../bindings/bilibili-ticket-golang/cmd/gui/cluster_service/clusterservice'

const { t } = useI18n()
const messages = useMessagesStore()

// ── Types (mirror Go types) ─────────────────────────────────
interface BuyerAccountBadge {
    accountId: string
    accountName: string
    uid: string
}

interface BuyerWithAccounts {
    logicalId: string
    buyerId: number
    name: string
    tel: string
    tels: string[]
    idCard: string
    type: number
    accounts: BuyerAccountBadge[]
}

interface AccountSummary {
    id: string
    name: string
    tags?: string[]
    enabled?: boolean
    vipStatus?: number
}

interface BuyerSyncJob {
    id: string
    buyerId: string
    buyerName: string
    accountId: string
    accountName: string
    state: string
    message?: string
}

interface BuyerSyncLogItem {
    time: string
    level: string
    jobId?: string
    buyerId?: string
    accountId?: string
    state?: string
    message: string
}

interface BuyerSyncBatch {
    id: string
    state: string
    total: number
    running: number
    succeeded: number
    skipped: number
    failed: number
    message?: string
    jobs: BuyerSyncJob[]
    logs: BuyerSyncLogItem[]
}

// ── State ───────────────────────────────────────────────────
const buyers = ref<BuyerWithAccounts[]>([])
const accounts = ref<AccountSummary[]>([])
const loading = ref(true)

// Add buyer dialog
const showAddDialog = ref(false)
const newBuyer = ref<{ name: string; tel: string; idCard: string; idType: number; accountId: string }>({
    name: '', tel: '', idCard: '', idType: 0, accountId: ''
})
const adding = ref(false)

// Sync to account dialog
const showSyncDialog = ref(false)
const syncBuyer = ref<BuyerWithAccounts | null>(null)
const syncTargetAccounts = ref<string[]>([])
const syncing = ref<Record<string, boolean>>({})
const showSyncProgressDialog = ref(false)
const activeSyncBatch = ref<BuyerSyncBatch | null>(null)
let syncPollTimer: ReturnType<typeof setTimeout> | null = null

// Sync all accounts
const syncingAll = ref(false)
const syncingAllFast = ref(false)

// Edit phone dialog
const showPhoneDialog = ref(false)
const phoneBuyer = ref<BuyerWithAccounts | null>(null)
const phoneValue = ref('')
const phoneSaving = ref(false)

// Delete buyer dialog
const showDeleteDialog = ref(false)
const deleteBuyer = ref<BuyerWithAccounts | null>(null)
const deleteTargetAccounts = ref<string[]>([])
const deleting = ref(false)

// Multi-select
const selected = ref<Set<string>>(new Set())
const batchSyncing = ref(false)

const allSelected = computed({
    get: () => buyers.value.length > 0 && selected.value.size === buyers.value.length,
    set: (val: boolean) => {
        if (val) {
            selected.value = new Set(buyers.value.map(b => b.logicalId))
        } else {
            selected.value = new Set()
        }
    }
})

function toggleSelect(id: string) {
    const next = new Set(selected.value)
    if (next.has(id)) { next.delete(id) } else { next.add(id) }
    selected.value = next
}

const batchSelectedBuyers = computed(() =>
    buyers.value.filter(b => selected.value.has(b.logicalId))
)

// ── Data loading ────────────────────────────────────────────
async function load() {
    loading.value = true
    try {
        const snap = await Snapshot()
        buyers.value = (snap.buyers || []) as BuyerWithAccounts[]
        accounts.value = (snap.accounts || []) as AccountSummary[]
        console.log('[buyers] loaded', buyers.value.length, 'buyers,', accounts.value.length, 'accounts')
        for (const b of buyers.value) {
            console.log('[buyers] name:', b.name, 'type:', b.type, 'idTypeLabel:', idTypeLabel(b.type))
        }
    } catch (e: any) {
        messages.add({ text: t('buyer.loadFailed', { error: String(e) }), color: 'error' })
    }
    loading.value = false
}

onMounted(load)
onUnmounted(() => {
    if (syncPollTimer) clearTimeout(syncPollTimer)
})

// ── Add buyer ──────────────────────────────────────────────
async function addBuyer() {
    if (!newBuyer.value.name.trim() || !newBuyer.value.accountId) {
        messages.add({ text: t('buyer.formIncomplete'), color: 'warning' })
        return
    }
    adding.value = true
    try {
        await ProvisionBuyer(JSON.stringify({
            accountId: newBuyer.value.accountId,
            buyer: {
                logicalId: '',
                name: newBuyer.value.name.trim(),
                tel: newBuyer.value.tel.trim(),
                idCard: newBuyer.value.idCard.trim(),
                type: newBuyer.value.idType,
            }
        }), true)
        showAddDialog.value = false
        newBuyer.value = { name: '', tel: '', idCard: '', idType: 0, accountId: '' }
        await load()
        messages.add({ text: t('buyer.addSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('buyer.addFailed', { error: String(e) }), color: 'error' })
    }
    adding.value = false
}

// ── Sync buyer to a specific account ────────────────────────
function openSync(ba: BuyerWithAccounts) {
    syncBuyer.value = ba
    syncTargetAccounts.value = []
    showSyncDialog.value = true
}

async function doSyncToAccount() {
    if (!syncBuyer.value || syncTargetAccounts.value.length === 0) return
    const key = syncBuyer.value.logicalId
    syncing.value[key] = true
    showSyncDialog.value = false
    syncBuyer.value = null
    await startBuyerSyncBatch([key], syncTargetAccounts.value, () => {
        syncing.value[key] = false
    })
}

async function syncToAllAccounts(ba: BuyerWithAccounts) {
    const key = ba.logicalId
    syncing.value[key] = true
    await startBuyerSyncBatch([key], [], () => {
        syncing.value[key] = false
    })
}

// ── Batch operations ───────────────────────────────────────
async function batchSyncToAllAccounts() {
    batchSyncing.value = true
    const buyerIds = batchSelectedBuyers.value.map(b => b.logicalId)
    await startBuyerSyncBatch(buyerIds, [], () => {
        selected.value = new Set()
        batchSyncing.value = false
    })
}

async function batchSyncToAccount() {
    showBatchSyncDialog.value = true
}
async function refreshAllBuyers() {
    syncingAll.value = true
    try {
        const result = await SyncAllAccountBuyers()
        await load()
        messages.add({ text: t('buyer.refreshAllSuccess', { count: (result || []).length }), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('buyer.refreshAllFailed', { error: String(e) }), color: 'error' })
    }
    syncingAll.value = false
}

async function fastRefreshAllBuyers() {
    syncingAllFast.value = true
    try {
        const result = await SyncAllAccountBuyersFast()
        await load()
        messages.add({ text: t('buyer.fastRefreshAllSuccess', { count: (result || []).length }), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('buyer.fastRefreshAllFailed', { error: String(e) }), color: 'error' })
    }
    syncingAllFast.value = false
}

function openPhoneDialog(b: BuyerWithAccounts) {
    phoneBuyer.value = b
    phoneValue.value = b.tel || ''
    showPhoneDialog.value = true
}
async function doSetPhone() {
    if (!phoneBuyer.value || !phoneValue.value.trim()) return
    phoneSaving.value = true
    try {
        await UpdateBuyerPhone(phoneBuyer.value.logicalId, phoneValue.value.trim())
        showPhoneDialog.value = false
        phoneBuyer.value = null
        await load()
        messages.add({ text: t('buyer.phoneUpdated'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('buyer.phoneUpdateFailed', { error: String(e) }), color: 'error' })
    }
    phoneSaving.value = false
}

const deleteTargetAccountItems = computed(() => {
    if (!deleteBuyer.value) return []
    return (deleteBuyer.value.accounts || []).map(a => ({
        title: `${a.accountName || a.uid || a.accountId} (${a.accountId})`,
        value: a.accountId
    }))
})

function openDeleteDialog(b: BuyerWithAccounts) {
    deleteBuyer.value = b
    deleteTargetAccounts.value = []
    showDeleteDialog.value = true
}
async function doDeleteBuyer() {
    if (!deleteBuyer.value) return
    const key = deleteBuyer.value.logicalId
    deleting.value = true
    try {
        const accountIds = deleteTargetAccounts.value
        if (accountIds.length === 0) {
            // Delete from all accounts
            await RemoveBuyerFromAllAccounts(key)
        } else {
            for (const accountId of accountIds) {
                await RemoveBuyerFromAccount(key, accountId)
            }
        }
        showDeleteDialog.value = false
        deleteBuyer.value = null
        await load()
        messages.add({ text: t('buyer.deleteSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('buyer.deleteFailed', { error: String(e) }), color: 'error' })
    }
    deleting.value = false
}

// Batch sync to account dialog
const showBatchSyncDialog = ref(false)
const batchTargetAccounts = ref<string[]>([])

async function doBatchSyncToAccount() {
    if (batchTargetAccounts.value.length === 0) return
    batchSyncing.value = true
    showBatchSyncDialog.value = false
    const buyerIds = batchSelectedBuyers.value.map(b => b.logicalId)
    const accountIds = [...batchTargetAccounts.value]
    await startBuyerSyncBatch(buyerIds, accountIds, () => {
        batchTargetAccounts.value = []
        selected.value = new Set()
        batchSyncing.value = false
    })
}

async function startBuyerSyncBatch(buyerIds: string[], accountIds: string[], onDone?: () => void) {
    try {
        const batch = await StartBuyerSync(JSON.stringify({ buyerIds, accountIds })) as BuyerSyncBatch
        activeSyncBatch.value = batch
        showSyncProgressDialog.value = true
        pollBuyerSyncBatch(batch.id, onDone)
    } catch (e: any) {
        onDone?.()
        messages.add({ text: `启动同步失败: ${String(e)}`, color: 'error' })
    }
}

function pollBuyerSyncBatch(batchId: string, onDone?: () => void) {
    if (syncPollTimer) clearTimeout(syncPollTimer)
    syncPollTimer = setTimeout(async () => {
        try {
            const batch = await GetBuyerSyncBatch(batchId) as BuyerSyncBatch
            activeSyncBatch.value = batch
            if (isSyncBatchTerminal(batch)) {
                syncPollTimer = null
                await load()
                onDone?.()
                if (batch.failed > 0) {
                    messages.add({ text: `同步完成：成功 ${batch.succeeded}，跳过 ${batch.skipped}，失败 ${batch.failed}`, color: 'warning' })
                } else {
                    messages.add({ text: `同步完成：成功 ${batch.succeeded}，跳过 ${batch.skipped}`, color: 'success' })
                }
                return
            }
            pollBuyerSyncBatch(batchId, onDone)
        } catch (e: any) {
            syncPollTimer = null
            onDone?.()
            messages.add({ text: `查询同步进度失败: ${String(e)}`, color: 'error' })
        }
    }, 800)
}

function isSyncBatchTerminal(batch: BuyerSyncBatch) {
    return batch.state === 'success' || batch.state === 'failed'
}

const syncProgressPercent = computed(() => {
    const batch = activeSyncBatch.value
    if (!batch || batch.total <= 0) return 0
    return Math.round(((batch.succeeded + batch.skipped + batch.failed) / batch.total) * 100)
})
const activeSyncTerminal = computed(() => !!activeSyncBatch.value && isSyncBatchTerminal(activeSyncBatch.value))

function syncStateColor(state: string) {
    switch (state) {
        case 'success': return 'success'
        case 'skipped': return 'info'
        case 'failed': return 'error'
        case 'running': return 'warning'
        default: return 'default'
    }
}

function syncStateLabel(state: string) {
    switch (state) {
        case 'success': return '成功'
        case 'skipped': return '跳过'
        case 'failed': return '失败'
        case 'running': return '同步中'
        case 'pending': return '等待'
        default: return state || '未知'
    }
}

function formatSyncLogTime(value: string) {
    if (!value) return ''
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return date.toLocaleTimeString()
}

// ── Helpers ──────────────────────────────────────────────────
const idTypeLabel = (type: number) => {
    switch (type) {
        case 0: return t('buyer.idTypeIdcard')
        case 1: return t('buyer.idTypePassport')
        case 2: return t('buyer.idTypeHkMacau')
        case 3: return t('buyer.idTypeTaiwan')
        default: return t('buyer.idTypeUnknown', { type })
    }
}

const idTypeItems = [
    { title: t('buyer.idTypeIdcard'), value: 0 },
    { title: t('buyer.idTypePassport'), value: 1 },
    { title: t('buyer.idTypeHkMacau'), value: 2 },
    { title: t('buyer.idTypeTaiwan'), value: 3 },
]

const accountItems = computed(() =>
    accounts.value.map(a => ({ title: `${a.name || a.id} (${a.id})`, value: a.id }))
)

// ── Filters ──────────────────────────────────────────────────
const filterName = ref('')
const filterAccount = ref('')
const filterAccountMode = ref<'has' | 'hasNot'>('has')

const filterAccountModeItems = computed(() => [
    { title: t('buyer.filterHas'), value: 'has' },
    { title: t('buyer.filterHasNot'), value: 'hasNot' },
])

const filteredBuyers = computed(() => {
    let list = buyers.value
    const kw = filterName.value.trim().toLowerCase()
    if (kw) {
        list = list.filter(b => {
            if ((b.name || '').toLowerCase().includes(kw)) return true
            if ((b.tel || '').includes(kw)) return true
            if ((b.idCard || '').includes(kw)) return true
            if ((b.tels || []).some(t => t.includes(kw))) return true
            return false
        })
    }
    if (filterAccount.value) {
        const targetId = filterAccount.value
        const hasAccount = (b: BuyerWithAccounts) => (b.accounts || []).some(a => a.accountId === targetId)
        list = list.filter(b => filterAccountMode.value === 'has' ? hasAccount(b) : !hasAccount(b))
    }
    return list
})

const filterAccountItems = computed(() => {
    const items: { title: string; value: string }[] = [{ title: t('buyer.filterAccountAll'), value: '' }]
    for (const a of accounts.value) {
        items.push({ title: `${a.name || a.id}`, value: a.id })
    }
    return items
})

function accountDisplayName(acc: BuyerAccountBadge) {
    return acc.accountName || acc.uid || acc.accountId
}

function accountSummarySuffix(accounts: BuyerAccountBadge[]) {
    if (!accounts || accounts.length <= 1) return ''
    return t('buyer.accountSummarySuffix', { count: accounts.length })
}
</script>

<template>
    <v-container>
        <div class="page-title-bar" style="gap:12px;flex-wrap:wrap">
            <h1 class="page-title">{{ t('buyer.title') }}</h1>
            <v-spacer />
            <div style="display:flex;gap:.5rem;flex-wrap:wrap">
                <v-btn prepend-icon="mdi-refresh" variant="tonal" :loading="syncingAll" @click="refreshAllBuyers">
                    {{ t('buyer.refreshAllBuyers') }}
                </v-btn>
                <v-btn prepend-icon="mdi-lightning-bolt" variant="tonal" color="warning" :loading="syncingAllFast"
                    @click="fastRefreshAllBuyers">
                    {{ t('buyer.fastRefreshAllBuyers') }}
                </v-btn>
                <v-btn prepend-icon="mdi-plus" color="primary" @click="showAddDialog = true">
                    {{ t('buyer.addBuyer') }}
                </v-btn>
            </div>
        </div>

        <!-- Filter bar -->
        <div v-if="!loading && buyers.length > 0"
            style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin-bottom:8px">
            <v-text-field v-model="filterName" :label="t('buyer.filterName')" prepend-inner-icon="mdi-magnify"
                variant="outlined" density="compact" hide-details clearable style="flex:1;min-width:200px" />
            <v-select v-model="filterAccount" :items="filterAccountItems" :label="t('buyer.filterAccount')"
                variant="outlined" density="compact" hide-details style="flex:1;min-width:180px" />
            <v-select v-if="filterAccount" v-model="filterAccountMode" :items="filterAccountModeItems"
                :label="t('buyer.filterMode')" variant="outlined" density="compact" hide-details style="width:120px" />
            <v-chip v-if="filteredBuyers.length !== buyers.length" size="small" color="info" variant="tonal">
                {{ t('buyer.filterCount', { filtered: filteredBuyers.length, total: buyers.length }) }}
            </v-chip>
        </div>

        <!-- Batch action bar -->
        <v-slide-y-transition>
            <v-card v-if="selected.size > 0" color="primary" variant="tonal" class="mb-3 pa-3">
                <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
                    <v-icon>mdi-checkbox-multiple-marked</v-icon>
                    <span>{{ t('buyer.batchSelected', { count: selected.size }) }}</span>
                    <v-spacer />
                    <v-btn size="small" variant="flat" color="primary" :loading="batchSyncing"
                        @click="batchSyncToAllAccounts">
                        {{ t('buyer.batchSyncAll') }}
                    </v-btn>
                    <v-btn size="small" variant="flat" :loading="batchSyncing" @click="batchSyncToAccount">
                        {{ t('buyer.batchSyncToAccount') }}
                    </v-btn>
                    <v-btn size="small" variant="text" @click="selected = new Set()">
                        {{ t('buyer.clearSelection') }}
                    </v-btn>
                </div>
            </v-card>
        </v-slide-y-transition>

        <!-- Loading -->
        <v-row v-if="loading" justify="center" class="mt-6">
            <v-progress-circular indeterminate color="primary" />
        </v-row>

        <!-- Empty state (no buyers at all) -->
        <v-card v-else-if="buyers.length === 0" class="mt-4 pa-6 text-center" variant="outlined">
            <v-card-text class="text-medium-emphasis">
                <v-icon size="48" class="mb-3">mdi-account-details</v-icon>
                <p>{{ t('buyer.emptyHint') }}</p>
                <v-btn prepend-icon="mdi-plus" color="primary" class="mt-3" @click="showAddDialog = true">
                    {{ t('buyer.addBuyer') }}
                </v-btn>
            </v-card-text>
        </v-card>

        <!-- Empty state (filtered to zero) -->
        <v-card v-else-if="filteredBuyers.length === 0" class="mt-4 pa-6 text-center" variant="outlined">
            <v-card-text class="text-medium-emphasis">
                <v-icon size="48" class="mb-3">mdi-filter-off</v-icon>
                <p>{{ t('buyer.filterEmpty') }}</p>
                <v-btn size="small" variant="tonal" @click="filterName = ''; filterAccount = ''">
                    {{ t('buyer.filterClear') }}
                </v-btn>
            </v-card-text>
        </v-card>

        <!-- Buyer list -->
        <v-table v-else>
            <thead>
                <tr>
                    <th style="width:40px">
                        <v-checkbox-btn :model-value="allSelected" @update:model-value="allSelected = $event"
                            density="compact" hide-details />
                    </th>
                    <th>{{ t('buyer.colName') }}</th>
                    <th>{{ t('buyer.colIdType') }}</th>
                    <th>{{ t('buyer.colIdCard') }}</th>
                    <th>{{ t('buyer.colTel') }}</th>
                    <th>{{ t('buyer.colAccounts') }}</th>
                    <th>{{ t('buyer.colActions') }}</th>
                </tr>
            </thead>
            <tbody>
                <tr v-for="b in filteredBuyers" :key="b.logicalId"
                    :class="{ 'bg-primary-lighten-5': selected.has(b.logicalId) }" style="table-layout:fixed">
                    <td>
                        <v-checkbox-btn :model-value="selected.has(b.logicalId)" @click="toggleSelect(b.logicalId)"
                            density="compact" hide-details />
                    </td>
                    <td style="max-width:180px">
                        <div class="text-truncate" style="overflow:hidden;white-space:nowrap;text-overflow:ellipsis">
                            <v-icon start size="small" class="mr-1">mdi-account</v-icon>
                            <strong>{{ b.name || t('buyer.unnamed') }}</strong>
                        </div>
                    </td>
                    <td class="text-caption" style="max-width:100px">
                        <div class="text-truncate">
                            <template v-if="b.type != null">{{ idTypeLabel(b.type) }}</template>
                            <span v-else class="text-medium-emphasis">—</span>
                        </div>
                    </td>
                    <td class="text-caption font-monospace" style="max-width:180px">
                        <div class="text-truncate">
                            <template v-if="b.idCard">{{ b.idCard }}</template>
                            <span v-else class="text-medium-emphasis">—</span>
                        </div>
                    </td>
                    <td class="text-caption" style="max-width:140px">
                        <v-tooltip v-if="b.tels && b.tels.length > 1" location="bottom">
                            <template #activator="{ props }">
                                <v-chip v-bind="props" size="x-small" color="info" variant="tonal">
                                    {{ b.tels[0] }}
                                    <span class="ml-1" style="opacity:0.7">+{{ b.tels.length - 1 }}</span>
                                </v-chip>
                            </template>
                            <div class="d-flex flex-column" style="gap:2px">
                                <div v-for="(ph, i) in b.tels" :key="i" class="text-caption">
                                    {{ ph }}
                                </div>
                            </div>
                        </v-tooltip>
                        <div v-else class="text-truncate">
                            <template v-if="b.tel">{{ b.tel }}</template>
                            <span v-else class="text-medium-emphasis">—</span>
                        </div>
                    </td>
                    <td style="max-width:200px">
                        <v-tooltip v-if="b.accounts && b.accounts.length > 1" location="bottom">
                            <template #activator="{ props }">
                                <span v-bind="props" class="d-inline-flex align-center">
                                    <v-chip size="x-small" color="primary" variant="tonal">
                                        {{ accountDisplayName(b.accounts[0]) }}
                                    </v-chip>
                                    <span class="text-caption text-medium-emphasis ml-1">
                                        {{ accountSummarySuffix(b.accounts) }}
                                    </span>
                                </span>
                            </template>
                            <div class="d-flex flex-column" style="gap:4px">
                                <div v-for="acc in b.accounts" :key="acc.accountId" class="text-caption">
                                    {{ accountDisplayName(acc) }}
                                    <span class="text-medium-emphasis">({{ acc.accountId }})</span>
                                </div>
                            </div>
                        </v-tooltip>
                        <v-chip v-else-if="b.accounts && b.accounts.length === 1" size="x-small" color="primary"
                            variant="tonal">
                            {{ accountDisplayName(b.accounts[0]) }}
                        </v-chip>
                        <span v-else class="text-caption text-medium-emphasis">
                            {{ t('buyer.noAccounts') }}
                        </span>
                    </td>
                    <td class="text-no-wrap" style="white-space:nowrap">
                        <div style="display:flex;gap:4px">
                            <v-btn icon="mdi-cellphone-edit" size="small" variant="text" :title="t('buyer.editPhone')"
                                @click="openPhoneDialog(b)" />
                            <v-btn icon="mdi-delete-outline" size="small" variant="text" :title="t('buyer.deleteBuyer')"
                                @click="openDeleteDialog(b)" />
                            <v-btn icon="mdi-account-plus" size="small" variant="text" :loading="syncing[b.logicalId]"
                                :title="t('buyer.syncToAccount')" @click="openSync(b)" />
                            <v-btn icon="mdi-account-multiple-plus" size="small" variant="text"
                                :loading="syncing[b.logicalId]" :title="t('buyer.syncToAllAccounts')"
                                @click="syncToAllAccounts(b)" />
                        </div>
                    </td>
                </tr>
            </tbody>
        </v-table>

        <!-- ═══ Add Buyer Dialog ═══ -->
        <v-dialog v-model="showAddDialog" max-width="520" persistent>
            <v-card class="pa-4">
                <v-card-title>{{ t('buyer.addBuyerTitle') }}</v-card-title>
                <v-card-text>
                    <v-select v-model="newBuyer.accountId" :items="accountItems" :label="t('buyer.targetAccount')"
                        variant="outlined" density="compact" class="mb-3" />
                    <v-text-field v-model="newBuyer.name" :label="t('buyer.nameLabel')"
                        :placeholder="t('buyer.namePlaceholder')" variant="outlined" density="compact" class="mb-3" />
                    <v-text-field v-model="newBuyer.tel" :label="t('buyer.telLabel')"
                        :placeholder="t('buyer.telPlaceholder')" variant="outlined" density="compact" class="mb-3" />
                    <v-select v-model="newBuyer.idType" :items="idTypeItems" :label="t('buyer.idTypeLabel')"
                        variant="outlined" density="compact" class="mb-3" />
                    <v-text-field v-model="newBuyer.idCard" :label="t('buyer.idCardLabel')"
                        :placeholder="t('buyer.idCardPlaceholder')" variant="outlined" density="compact" />
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showAddDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" :loading="adding" :disabled="!newBuyer.name.trim() || !newBuyer.accountId"
                        @click="addBuyer">
                        {{ t('common.save') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ═══ Batch Sync to Account Dialog ═══ -->
        <v-dialog v-model="showBatchSyncDialog" max-width="820">
            <v-card class="pa-4">
                <v-card-title>{{ t('buyer.batchSyncTitle', { count: selected.size }) }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">
                        {{ t('buyer.batchSyncHint') }}
                    </p>
                    <AccountPicker v-model="batchTargetAccounts" :accounts="accounts"
                        :label="t('buyer.targetAccount')" :hint="t('buyer.targetAccountPickerHint')" />
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showBatchSyncDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" :disabled="batchTargetAccounts.length === 0" :loading="batchSyncing"
                        @click="doBatchSyncToAccount">
                        {{ t('buyer.syncBtn') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ═══ Sync to Account Dialog ═══ -->
        <v-dialog v-model="showSyncDialog" max-width="820">
            <v-card class="pa-4">
                <v-card-title>{{ t('buyer.syncToTitle', { name: syncBuyer?.name || syncBuyer?.logicalId })
                    }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">
                        {{ t('buyer.syncToHint') }}
                    </p>
                    <AccountPicker v-model="syncTargetAccounts" :accounts="accounts"
                        :label="t('buyer.targetAccount')" :hint="t('buyer.targetAccountPickerHint')" />
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showSyncDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" :disabled="syncTargetAccounts.length === 0"
                        :loading="!!syncBuyer && syncing[syncBuyer.logicalId]" @click="doSyncToAccount">
                        {{ t('buyer.syncBtn') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ═══ Buyer Sync Progress Dialog ═══ -->
        <v-dialog v-model="showSyncProgressDialog" max-width="860">
            <v-card class="pa-4">
                <v-card-title class="d-flex align-center" style="gap:8px">
                    <span>购票人同步进度</span>
                    <v-spacer />
                    <v-chip v-if="activeSyncBatch" size="small" :color="syncStateColor(activeSyncBatch.state)"
                        variant="tonal">
                        {{ syncStateLabel(activeSyncBatch.state) }}
                    </v-chip>
                </v-card-title>
                <v-card-text v-if="activeSyncBatch">
                    <div class="mb-3">
                        <div class="d-flex align-center mb-2" style="gap:8px;flex-wrap:wrap">
                            <span class="text-body-2">
                                共 {{ activeSyncBatch.total }} 项，
                                成功 {{ activeSyncBatch.succeeded }}，
                                跳过 {{ activeSyncBatch.skipped }}，
                                失败 {{ activeSyncBatch.failed }}，
                                运行中 {{ activeSyncBatch.running }}
                            </span>
                            <span v-if="activeSyncBatch.message" class="text-caption text-medium-emphasis">
                                {{ activeSyncBatch.message }}
                            </span>
                        </div>
                        <v-progress-linear :model-value="syncProgressPercent" height="8" rounded
                            :color="activeSyncBatch.failed > 0 ? 'warning' : 'primary'" />
                    </div>

                    <v-row dense>
                        <v-col cols="12" md="7">
                            <div class="text-subtitle-2 mb-2">任务</div>
                            <div class="sync-progress-panel">
                                <div v-for="job in activeSyncBatch.jobs" :key="job.id" class="sync-progress-row">
                                    <div style="min-width:0;flex:1">
                                        <div class="text-body-2 text-truncate">
                                            {{ job.buyerName || job.buyerId }}
                                            <span class="text-medium-emphasis">→</span>
                                            {{ job.accountName || job.accountId }}
                                        </div>
                                        <div v-if="job.message" class="text-caption text-medium-emphasis text-truncate">
                                            {{ job.message }}
                                        </div>
                                    </div>
                                    <v-chip size="x-small" :color="syncStateColor(job.state)" variant="tonal">
                                        {{ syncStateLabel(job.state) }}
                                    </v-chip>
                                </div>
                            </div>
                        </v-col>
                        <v-col cols="12" md="5">
                            <div class="text-subtitle-2 mb-2">日志</div>
                            <div class="sync-progress-panel">
                                <div v-for="(log, index) in [...(activeSyncBatch.logs || [])].reverse()" :key="index"
                                    class="sync-log-row">
                                    <div class="text-caption text-medium-emphasis">
                                        {{ formatSyncLogTime(log.time) }}
                                        <span v-if="log.accountId"> · {{ log.accountId }}</span>
                                    </div>
                                    <div class="text-body-2">{{ log.message }}</div>
                                </div>
                            </div>
                        </v-col>
                    </v-row>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showSyncProgressDialog = false">
                        {{ activeSyncTerminal ? '关闭' : '后台运行' }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ═══ Edit Phone Dialog ═══ -->
        <v-dialog v-model="showPhoneDialog" max-width="420">
            <v-card class="pa-4">
                <v-card-title>{{ t('buyer.editPhoneTitle', { name: phoneBuyer?.name || phoneBuyer?.logicalId })
                }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">
                        {{ t('buyer.editPhoneHint') }}
                    </p>
                    <v-text-field v-model="phoneValue" :label="t('buyer.telLabel')"
                        :placeholder="t('buyer.telPlaceholder')" variant="outlined" density="compact" />
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showPhoneDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" :disabled="!phoneValue.trim()" :loading="phoneSaving" @click="doSetPhone">
                        {{ t('common.save') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ═══ Delete Buyer Dialog ═══ -->
        <v-dialog v-model="showDeleteDialog" max-width="420">
            <v-card class="pa-4">
                <v-card-title>{{ t('buyer.deleteTitle', { name: deleteBuyer?.name || deleteBuyer?.logicalId })
                }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">
                        {{ t('buyer.deleteHint') }}
                    </p>
                    <v-select v-model="deleteTargetAccounts" :items="deleteTargetAccountItems"
                        :label="t('buyer.deleteTargetLabel')" variant="outlined" density="compact" multiple chips
                        :hint="t('buyer.deleteTargetHint')" persistent-hint />
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showDeleteDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="error" :loading="deleting" @click="doDeleteBuyer">
                        {{ t('buyer.deleteBtn') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </v-container>
</template>

<style scoped>
.sync-progress-panel {
    max-height: 360px;
    overflow: auto;
    border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
    border-radius: 10px;
}

.sync-progress-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 10px;
    border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}

.sync-progress-row:last-child,
.sync-log-row:last-child {
    border-bottom: 0;
}

.sync-log-row {
    padding: 8px 10px;
    border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}
</style>
