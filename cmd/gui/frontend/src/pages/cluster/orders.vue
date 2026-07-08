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

const headers = computed(() => [
    { title: t('orders.colOrder'), key: 'summary', minWidth: 420, sortable: false },
    { title: t('orders.colBuyers'), key: 'buyers', width: 150, sortable: false },
    { title: t('orders.colTime'), key: 'time', width: 180, sortable: false },
    { title: t('orders.colActions'), key: 'actions', width: 150, sortable: false },
])

async function load() {
    loading.value = true
    try {
        const resp = await ListOrderRecords()
        records.value = ((resp.records || []) as OrderRecord[]).slice().sort((a, b) => {
            return new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime()
        })
    } catch (e: any) {
        messages.addError(e, t('orders.loadFailed', { error: String(e) }))
    }
    loading.value = false
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
    if (record.accountName && record.accountId) return `${record.accountName} (${compactID(record.accountId, 14)})`
    return record.accountName || compactID(record.accountId, 14)
}

function compactID(id?: string, max = 18): string {
    if (!id) return '—'
    return id.length > max ? `${id.slice(0, max)}…` : id
}

onMounted(() => {
    load()
    statusTimer = setInterval(() => {
        nowSec.value = Math.floor(Date.now() / 1000)
    }, 1000)
})

onUnmounted(() => {
    if (statusTimer) clearInterval(statusTimer)
})
</script>

<template>
    <v-container>
        <div class="page-title-bar">
            <h1 class="page-title">{{ t('orders.title') }}</h1>
            <v-spacer />
            <v-btn size="small" variant="text" :loading="loading" prepend-icon="mdi-refresh" @click="load">
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
            <v-data-table v-if="records.length > 0" :headers="headers" :items="records" :search="search"
                :items-per-page="20" :items-per-page-options="[10, 20, 50, 100]" density="comfortable"
                class="orders-table">
                <template #item.summary="{ item }">
                    <div class="order-summary py-1">
                        <div class="d-flex align-center ga-2 min-w-0">
                            <span class="font-monospace text-caption text-primary text-no-wrap">#{{ item.orderId || '—'
                            }}</span>
                            <span class="text-caption text-medium-emphasis text-truncate">
                                {{ displayValue(item.projectName || item.projectId) }}
                            </span>
                        </div>
                        <div class="order-item-line text-caption mt-1">
                            <span class="order-label">场次</span>
                            <span class="text-truncate">{{ displayValue(item.screenName || item.screenId) }}</span>
                            <span class="order-sep">·</span>
                            <span class="order-label">SKU</span>
                            <span class="text-truncate">{{ displayValue(item.skuName || item.skuId) }}</span>
                        </div>
                        <div class="order-meta text-caption text-medium-emphasis mt-1">
                            <span>A {{ accountText(item) }}</span>
                            <span class="order-worker-id">W {{ displayValue(item.workerId) }}</span>
                        </div>
                    </div>
                </template>
                <template #item.buyers="{ item }">
                    <span class="text-caption buyer-cell">{{ buyerText(item) }}</span>
                </template>
                <template #item.time="{ item }">
                    <div class="text-caption text-no-wrap">{{ fmtDate(item.createdAt) }}</div>
                    <div class="text-caption text-medium-emphasis text-no-wrap">{{ t('orders.expireAt') }}: {{
                        fmtExpire(item.paymentExpire) }}</div>
                </template>
                <template #item.actions="{ item }">
                    <div class="d-flex align-center action-cell"
                        :class="isExpired(item) ? 'justify-start' : 'justify-end'">
                        <template v-if="canPay(item)">
                        <v-btn size="small" color="primary" variant="tonal"
                            :loading="opening[item.id]" @click="openPayment(item)">
                            {{ t('orders.openPayment') }}
                        </v-btn>
                        <v-btn size="small" icon="mdi-content-copy" variant="text"
                            class="ml-1" @click="copyPaymentURL(item)" />
                        </template>
                        <v-chip v-else-if="isExpired(item)" size="small" color="error" variant="tonal">
                            {{ t('orders.statusExpired') }}
                        </v-chip>
                    </div>
                </template>
            </v-data-table>
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
.orders-table :deep(td) {
    vertical-align: middle;
    white-space: nowrap;
}

.orders-table :deep(th) {
    white-space: nowrap;
}

.orders-table :deep(table) {
    min-width: 960px;
}

.order-summary {
    min-width: 0;
    max-width: 100%;
}

.order-item-line {
    display: grid;
    grid-template-columns: auto minmax(80px, 1fr) auto auto minmax(120px, 1.35fr);
    align-items: center;
    column-gap: 6px;
    min-width: 0;
}

.order-label {
    color: rgba(var(--v-theme-on-surface), 0.56);
    flex: none;
}

.order-sep {
    color: rgba(var(--v-theme-on-surface), 0.38);
}

.order-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 10px;
    line-height: 1.35;
}

.order-worker-id {
    white-space: normal;
    overflow-wrap: anywhere;
    word-break: break-word;
}

.buyer-cell {
    display: inline-block;
    max-width: 150px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.action-cell {
    min-width: 116px;
}
</style>
