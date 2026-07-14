<script lang="ts" setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import { ListOrderRecords, OpenOrderPayment } from '../../../bindings/bilibili-ticket-golang/cmd/gui/cluster_service/clusterservice'

const { t } = useI18n()
const messages = useMessagesStore()

interface OrderRecord {
    id: string
    orderId: string
    attemptId: string
    intentId: string
    macroTaskId: string
    taskGroupId?: string
    accountId?: string
    accountName?: string
    workerId?: string
    projectId?: number
    projectName?: string
    screenId?: number
    screenName?: string
    skuId?: number
    skuName?: string
    buyerNames?: string[]
    buyerIndex?: number
    buyerId?: number
    status?: 'pending' | 'succeeded' | 'failed'
    paymentUrl: string
    paymentExpire?: number
    orderTime?: number
    createdAt: string
}

const records = ref<OrderRecord[]>([])
const loading = ref(false)
const opening = ref<Record<string, boolean>>({})
const search = ref('')
const nowSec = ref(Math.floor(Date.now() / 1000))
let statusTimer: ReturnType<typeof setInterval> | null = null
let refreshTimer: ReturnType<typeof setInterval> | null = null
let loadInFlight = false

interface OrderTreeItem {
    id: string
    title: string
    kind: 'root' | 'child'
    record: OrderRecord
    children?: OrderTreeItem[]
}

const orderTrees = computed<OrderTreeItem[]>(() => {
    const query = search.value.trim().toLocaleLowerCase()
    const filtered = records.value.filter(record => {
        if (!query) return true
        return JSON.stringify(record).toLocaleLowerCase().includes(query)
    })
    const groups = new Map<string, OrderRecord[]>()
    for (const record of filtered) {
        const key = record.attemptId || record.intentId || record.id
        const group = groups.get(key) || []
        group.push(record)
        groups.set(key, group)
    }
    return [...groups.entries()].map(([key, group]) => {
        group.sort((a, b) => (a.buyerIndex ?? 0) - (b.buyerIndex ?? 0))
        const main = group[0]
        return {
            id: `root:${key}`,
            title: mainOrderName(main),
            kind: 'root',
            record: main,
            children: group.map(record => ({
                id: record.id,
                title: childOrderName(record),
                kind: 'child',
                record,
            })),
        }
    })
})

async function load(silent = false) {
    if (loadInFlight) return
    loadInFlight = true
    if (!silent) loading.value = true
    try {
        const resp = await ListOrderRecords()
        records.value = ((resp.records || []) as OrderRecord[]).slice().sort((a, b) => {
            return new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime()
        })
    } catch (e: any) {
        if (!silent) messages.addError(e, t('orders.loadFailed', { error: String(e) }))
    }
    if (!silent) loading.value = false
    loadInFlight = false
}

async function openPayment(record: OrderRecord) {
    if (!canPay(record)) return
    opening.value[record.id] = true
    try {
        await OpenOrderPayment(record.id)
    } catch (e: any) {
        messages.addError(e, t('orders.openFailed', { error: String(e) }))
    }
    opening.value[record.id] = false
}

async function copyPaymentURL(record: OrderRecord) {
    if (!canPay(record)) return
    try {
        await navigator.clipboard.writeText(record.paymentUrl)
        messages.add({ text: t('orders.copySuccess'), color: 'success' })
    } catch (e: any) {
        messages.addError(e, t('orders.copyFailed', { error: String(e) }))
    }
}

function fmtDate(value: any): string {
    if (!value) return '—'
    const d = typeof value === 'number' ? new Date(value * 1000) : new Date(value)
    if (isNaN(d.getTime())) return String(value)
    return d.toLocaleString()
}

function fmtExpire(sec?: number): string {
    if (!sec) return '—'
    return fmtDate(sec)
}

function isExpired(record: OrderRecord): boolean {
    return !!record.paymentExpire && nowSec.value >= record.paymentExpire
}

function canPay(record: OrderRecord): boolean {
    return !!record.paymentUrl && !isExpired(record)
}

function subOrderStatusText(status?: OrderRecord['status']): string {
    if (status === 'succeeded') return t('orders.statusSucceeded')
    if (status === 'failed') return t('orders.statusFailed')
    if (status === 'pending') return t('orders.statusPending')
    return ''
}

function subOrderStatusColor(status?: OrderRecord['status']): string {
    if (status === 'succeeded') return 'success'
    if (status === 'failed') return 'error'
    return 'warning'
}

function displayValue(value?: string | number): string {
    if (value === undefined || value === null || String(value) === '') return '—'
    return String(value)
}

function buyerText(record: OrderRecord): string {
    const names = record.buyerNames || []
    if (names.length === 0) return '—'
    return names.join('、')
}

function accountText(record: OrderRecord): string {
    if (record.accountName && record.accountId) return `${record.accountName} (${record.accountId})`
    return record.accountName || record.accountId || '—'
}

function mainOrderName(record: OrderRecord): string {
    const project = displayValue(record.projectName || record.projectId)
    const screen = displayValue(record.screenName || record.screenId)
    const sku = displayValue(record.skuName || record.skuId)
    return `${project} · ${screen} · ${sku}`
}

function childOrderName(record: OrderRecord): string {
    const buyer = buyerText(record)
    return `${t('orders.subOrder')} ${Number(record.buyerIndex ?? 0) + 1} · ${buyer}`
}

onMounted(() => {
    load()
    statusTimer = setInterval(() => {
        nowSec.value = Math.floor(Date.now() / 1000)
    }, 1000)
    refreshTimer = setInterval(() => load(true), 3000)
})

onUnmounted(() => {
    if (statusTimer) clearInterval(statusTimer)
    if (refreshTimer) clearInterval(refreshTimer)
})
</script>

<template>
    <v-container class="orders-page">
        <div class="page-title-bar">
            <h1 class="page-title">{{ t('orders.title') }}</h1>
            <v-spacer />
            <v-btn size="small" variant="text" :loading="loading" prepend-icon="mdi-refresh" @click="load()">
                {{ t('common.refresh') }}
            </v-btn>
        </div>

        <v-card elevation="2">
            <v-card-item class="py-2 px-4">
                <template #title>
                    <span class="text-subtitle-2">{{ t('orders.records') }}</span>
                    <span class="text-caption text-medium-emphasis ml-2">({{ records.length }})</span>
                </template>
            </v-card-item>
            <v-text-field v-model="search" density="compact" variant="outlined" hide-details
                :placeholder="t('orders.searchPlaceholder')" prepend-inner-icon="mdi-magnify" clearable
                class="mx-4 mb-2" />
            <v-treeview v-if="orderTrees.length > 0" :items="orderTrees" item-title="title" item-value="id"
                density="compact" class="orders-tree">
                <template #title="{ item }">
                    <div v-if="item.kind === 'root'" class="tree-root py-1">
                        <div class="order-title font-weight-bold wrap-anywhere">{{ item.title }}</div>
                        <div class="tree-id-grid order-meta text-medium-emphasis">
                            <span>TaskGroup ID: <b>{{ displayValue(item.record.taskGroupId) }}</b></span>
                            <span>Macro ID: <b>{{ displayValue(item.record.macroTaskId) }}</b></span>
                            <span>Intent ID: <b>{{ displayValue(item.record.intentId) }}</b></span>
                            <span>Attempt ID: <b>{{ displayValue(item.record.attemptId) }}</b></span>
                            <span>Account ID: <b>{{ accountText(item.record) }}</b></span>
                            <span>Worker ID: <b>{{ displayValue(item.record.workerId) }}</b></span>
                        </div>
                    </div>
                    <div v-else class="tree-child py-1">
                        <div class="d-flex align-center flex-wrap ga-1">
                            <span class="order-child-title font-weight-medium">{{ item.title }}</span>
                            <v-chip v-if="item.record.status" size="x-small"
                                :color="subOrderStatusColor(item.record.status)" variant="tonal">
                                {{ subOrderStatusText(item.record.status) }}
                            </v-chip>
                        </div>
                        <div class="tree-id-grid order-meta">
                            <span>Order ID: <b class="text-primary">{{ displayValue(item.record.orderId) }}</b></span>
                            <span>Buyer ID: <b>{{ displayValue(item.record.buyerId) }}</b></span>
                            <span>{{ t('orders.recordedAt') }}: {{ fmtDate(item.record.createdAt) }}</span>
                            <span>{{ t('orders.expireAt') }}: {{ fmtExpire(item.record.paymentExpire) }}</span>
                        </div>
                    </div>
                </template>
                <template #append="{ item }">
                    <div v-if="item.kind === 'child'" class="d-flex align-center ga-1 mr-2">
                        <template v-if="canPay(item.record)">
                            <v-btn size="x-small" color="primary" variant="tonal"
                                :loading="opening[item.record.id]" @click.stop="openPayment(item.record)">
                                {{ t('orders.openPayment') }}
                            </v-btn>
                            <v-btn size="x-small" icon="mdi-content-copy" variant="text"
                                @click.stop="copyPaymentURL(item.record)" />
                        </template>
                        <v-chip v-else-if="isExpired(item.record)" size="x-small" color="error" variant="tonal">
                            {{ t('orders.statusExpired') }}
                        </v-chip>
                    </div>
                </template>
            </v-treeview>
            <div v-else-if="!loading" class="text-center py-10">
                <v-icon size="40" color="medium-emphasis" class="mb-2">mdi-receipt-text-outline</v-icon>
                <p class="text-caption text-medium-emphasis">{{ t('orders.empty') }}</p>
            </div>
            <div v-if="loading" class="text-center py-6">
                <v-progress-circular indeterminate color="primary" size="28" />
                <p class="text-caption text-medium-emphasis mt-2">{{ t('common.loading') }}</p>
            </div>
        </v-card>
    </v-container>
</template>

<style scoped>
.orders-tree :deep(.v-list-item) {
    min-height: 36px;
    padding-top: 1px;
    padding-bottom: 1px;
    align-items: flex-start;
}

.orders-tree :deep(.v-list-item-title) {
    white-space: normal;
    overflow: visible;
}

.orders-tree :deep(.v-list-item__prepend) {
    padding-top: 1px;
}

.orders-tree :deep(.v-list-item__append) {
    align-self: center;
}

.tree-root,
.tree-child {
    width: 100%;
    min-width: 0;
}

.tree-id-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0 12px;
    line-height: 1.35;
}

.order-title {
    font-size: 14px;
    line-height: 1.4;
}

.order-child-title {
    font-size: 12px;
    line-height: 1.35;
}

.order-meta {
    font-size: 10px;
}

.tree-id-grid span,
.tree-id-grid b,
.wrap-anywhere {
    white-space: normal;
    overflow-wrap: anywhere;
    word-break: break-word;
}

@media (max-width: 800px) {
    .tree-id-grid {
        grid-template-columns: 1fr;
    }
}
</style>
