<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue'
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

const headers = computed(() => [
    { title: t('orders.colTime'), key: 'createdAt', width: 150, sortable: false },
    { title: t('orders.colOrder'), key: 'orderId', width: 150, sortable: false },
    { title: t('orders.colItem'), key: 'item', sortable: false },
    { title: t('orders.colBuyers'), key: 'buyers', width: 180, sortable: false },
    { title: t('orders.colAccountWorker'), key: 'accountWorker', width: 210, sortable: false },
    { title: t('orders.colExpire'), key: 'paymentExpire', width: 150, sortable: false },
    { title: t('orders.colActions'), key: 'actions', width: 170, sortable: false },
])

async function load() {
    loading.value = true
    try {
        const resp = await ListOrderRecords()
        records.value = ((resp.records || []) as OrderRecord[]).slice().sort((a, b) => {
            return new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime()
        })
    } catch (e: any) {
        messages.add({ text: t('orders.loadFailed', { error: String(e) }), color: 'error' })
    }
    loading.value = false
}

async function openPayment(record: OrderRecord) {
    opening.value[record.id] = true
    try {
        await OpenOrderPayment(record.id)
    } catch (e: any) {
        messages.add({ text: t('orders.openFailed', { error: String(e) }), color: 'error' })
    }
    opening.value[record.id] = false
}

async function copyPaymentURL(record: OrderRecord) {
    if (!record.paymentUrl) return
    try {
        await navigator.clipboard.writeText(record.paymentUrl)
        messages.add({ text: t('orders.copySuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('orders.copyFailed', { error: String(e) }), color: 'error' })
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

function itemTitle(record: OrderRecord): string {
    return [record.projectName || record.projectId, record.screenName || record.screenId, record.skuName || record.skuId]
        .filter(v => v !== undefined && v !== null && String(v) !== '')
        .map(String)
        .join(' / ') || '—'
}

function buyerText(record: OrderRecord): string {
    const names = record.buyerNames || []
    if (names.length === 0) return '—'
    return names.join('、')
}

function compactID(id?: string, max = 18): string {
    if (!id) return '—'
    return id.length > max ? `${id.slice(0, max)}…` : id
}

onMounted(load)
</script>

<template>
    <v-container>
        <div class="d-flex align-center mb-4">
            <h1 class="text-h5">{{ t('orders.title') }}</h1>
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
                :items-per-page="20" :items-per-page-options="[10, 20, 50, 100]" density="compact">
                <template #item.createdAt="{ item }">
                    <span class="text-caption text-no-wrap">{{ fmtDate(item.createdAt) }}</span>
                </template>
                <template #item.orderId="{ item }">
                    <span class="font-monospace text-caption text-primary">#{{ item.orderId || '—' }}</span>
                </template>
                <template #item.item="{ item }">
                    <div class="text-caption">{{ itemTitle(item) }}</div>
                    <div class="text-caption text-medium-emphasis">
                        PID {{ item.projectId || '—' }} · Screen {{ item.screenId || '—' }} · SKU {{ item.skuId || '—' }}
                    </div>
                </template>
                <template #item.buyers="{ item }">
                    <span class="text-caption">{{ buyerText(item) }}</span>
                </template>
                <template #item.accountWorker="{ item }">
                    <div class="text-caption">A: {{ compactID(item.accountId) }}</div>
                    <div class="text-caption text-medium-emphasis">W: {{ compactID(item.workerId) }}</div>
                </template>
                <template #item.paymentExpire="{ item }">
                    <span class="text-caption text-no-wrap">{{ fmtExpire(item.paymentExpire) }}</span>
                </template>
                <template #item.actions="{ item }">
                    <v-btn size="x-small" color="primary" variant="tonal" :disabled="!item.paymentUrl"
                        :loading="opening[item.id]" @click="openPayment(item)">
                        {{ t('orders.openPayment') }}
                    </v-btn>
                    <v-btn size="x-small" icon="mdi-content-copy" variant="text" :disabled="!item.paymentUrl"
                        class="ml-1" @click="copyPaymentURL(item)" />
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
