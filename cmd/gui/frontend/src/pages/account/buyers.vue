<script lang="ts" setup>
import { ref, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import {
    Snapshot,
    ProvisionBuyer,
    SyncAllAccountBuyers,
    SyncBuyerToAccount,
    SyncBuyerToAllAccounts,
} from '../../../bindings/bilibili-ticket-golang/cmd/gui/clusterservice'

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
    idCard: string
    type: number
    accounts: BuyerAccountBadge[]
}

interface AccountSummary {
    id: string
    name: string
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

// Sync all accounts
const syncingAll = ref(false)

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
    let ok = 0; let fail = 0
    for (const accountId of syncTargetAccounts.value) {
        try {
            await SyncBuyerToAccount(key, accountId)
            ok++
        } catch { fail++ }
    }
    showSyncDialog.value = false
    syncBuyer.value = null
    await load()
    syncing.value[key] = false
    if (fail === 0) {
        messages.add({ text: t('buyer.syncToAccountMultiSuccess', { count: ok }), color: 'success' })
    } else {
        messages.add({ text: t('buyer.syncToAccountPartial', { ok, fail }), color: 'warning' })
    }
}

async function syncToAllAccounts(ba: BuyerWithAccounts) {
    const key = ba.logicalId
    syncing.value[key] = true
    try {
        await SyncBuyerToAllAccounts(key)
        await load()
        messages.add({ text: t('buyer.syncAllSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('buyer.syncAllFailed', { error: String(e) }), color: 'error' })
    }
    syncing.value[key] = false
}

// ── Batch operations ───────────────────────────────────────
async function batchSyncToAllAccounts() {
    batchSyncing.value = true
    let ok = 0; let fail = 0
    for (const b of batchSelectedBuyers.value) {
        try {
            await SyncBuyerToAllAccounts(b.logicalId)
            ok++
        } catch { fail++ }
    }
    selected.value = new Set()
    await load()
    batchSyncing.value = false
    if (fail === 0) {
        messages.add({ text: t('buyer.batchSyncAllSuccess', { count: ok }), color: 'success' })
    } else {
        messages.add({ text: t('buyer.batchSyncPartial', { ok, fail }), color: 'warning' })
    }
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

// Batch sync to account dialog
const showBatchSyncDialog = ref(false)
const batchTargetAccounts = ref<string[]>([])

async function doBatchSyncToAccount() {
    if (batchTargetAccounts.value.length === 0) return
    batchSyncing.value = true
    let ok = 0; let fail = 0
    for (const b of batchSelectedBuyers.value) {
        for (const accountId of batchTargetAccounts.value) {
            try {
                await SyncBuyerToAccount(b.logicalId, accountId)
                ok++
            } catch { fail++ }
        }
    }
    showBatchSyncDialog.value = false
    batchTargetAccounts.value = []
    selected.value = new Set()
    await load()
    batchSyncing.value = false
    if (fail === 0) {
        messages.add({ text: t('buyer.batchSyncAccountSuccess', { count: ok }), color: 'success' })
    } else {
        messages.add({ text: t('buyer.batchSyncPartial', { ok, fail }), color: 'warning' })
    }
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
const filterAccountMode = ref<'has' | 'hasNot' | 'any'>('any')

const filterAccountModeItems = computed(() => [
    { title: t('buyer.filterAny'), value: 'any' },
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
            return false
        })
    }
    if (filterAccount.value && filterAccountMode.value !== 'any') {
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
</script>

<template>
    <v-container>
        <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">
            <h1>{{ t('buyer.title') }}</h1>
            <v-spacer />
            <v-btn prepend-icon="mdi-refresh" variant="tonal" :loading="syncingAll" @click="refreshAllBuyers">
                {{ t('buyer.refreshAllBuyers') }}
            </v-btn>
            <v-btn prepend-icon="mdi-plus" color="primary" @click="showAddDialog = true">
                {{ t('buyer.addBuyer') }}
            </v-btn>
        </div>

        <v-divider class="mt-2 mb-4" thickness="3" />

        <!-- Filter bar -->
        <v-row v-if="!loading && buyers.length > 0" class="mb-2">
            <v-col cols="12" sm="5">
                <v-text-field v-model="filterName" :label="t('buyer.filterName')" prepend-inner-icon="mdi-magnify"
                    variant="outlined" density="compact" hide-details clearable />
            </v-col>
            <v-col cols="12" sm="4">
                <v-select v-model="filterAccount" :items="filterAccountItems" :label="t('buyer.filterAccount')"
                    variant="outlined" density="compact" hide-details clearable />
            </v-col>
            <v-col cols="12" sm="3" class="d-flex align-center">
                <v-select v-model="filterAccountMode" :items="filterAccountModeItems" :label="t('buyer.filterMode')"
                    variant="outlined" density="compact" hide-details style="min-width:100px" />
                <v-chip v-if="filteredBuyers.length !== buyers.length" class="ml-2" size="small" color="info"
                    variant="tonal">
                    {{ t('buyer.filterCount', { filtered: filteredBuyers.length, total: buyers.length }) }}
                </v-chip>
            </v-col>
        </v-row>

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
                    :class="{ 'bg-primary-lighten-5': selected.has(b.logicalId) }">
                    <td>
                        <v-checkbox-btn :model-value="selected.has(b.logicalId)" @click="toggleSelect(b.logicalId)"
                            density="compact" hide-details />
                    </td>
                    <td>
                        <v-icon start size="small" class="mr-1">mdi-account</v-icon>
                        <strong>{{ b.name || t('buyer.unnamed') }}</strong>
                    </td>
                    <td class="text-caption">
                        <template v-if="b.type != null">{{ idTypeLabel(b.type) }}</template>
                        <span v-else class="text-medium-emphasis">—</span>
                    </td>
                    <td class="text-caption font-monospace">
                        <template v-if="b.idCard">{{ b.idCard }}</template>
                        <span v-else class="text-medium-emphasis">—</span>
                    </td>
                    <td class="text-caption">
                        <template v-if="b.tel">{{ b.tel }}</template>
                        <span v-else class="text-medium-emphasis">—</span>
                    </td>
                    <td>
                        <div v-if="b.accounts && b.accounts.length > 0" style="display:flex;gap:4px;flex-wrap:wrap">
                            <v-chip v-for="acc in b.accounts" :key="acc.accountId" size="x-small" color="primary"
                                variant="tonal">
                                {{ acc.accountName || acc.uid }}
                            </v-chip>
                        </div>
                        <span v-else class="text-caption text-medium-emphasis">
                            {{ t('buyer.noAccounts') }}
                        </span>
                    </td>
                    <td>
                        <div style="display:flex;gap:4px">
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
        <v-dialog v-model="showBatchSyncDialog" max-width="420">
            <v-card class="pa-4">
                <v-card-title>{{ t('buyer.batchSyncTitle', { count: selected.size }) }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">
                        {{ t('buyer.batchSyncHint') }}
                    </p>
                    <v-select v-model="batchTargetAccounts" :items="accountItems" :label="t('buyer.targetAccount')"
                        variant="outlined" density="compact" multiple chips />
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
        <v-dialog v-model="showSyncDialog" max-width="420">
            <v-card class="pa-4">
                <v-card-title>{{ t('buyer.syncToTitle', { name: syncBuyer?.name || syncBuyer?.logicalId })
                    }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">
                        {{ t('buyer.syncToHint') }}
                    </p>
                    <v-select v-model="syncTargetAccounts" :items="accountItems" :label="t('buyer.targetAccount')"
                        variant="outlined" density="compact" multiple chips />
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
    </v-container>
</template>
