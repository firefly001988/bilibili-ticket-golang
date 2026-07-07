<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
    buyerAccountCount,
    buyerIdTail,
    filterBuyersBySearch,
    type SearchableBuyer,
} from '@/composables/buyerSearch'

const RECENT_BUYERS_KEY = 'btgo.recentBuyers'

const props = withDefaults(defineProps<{
    modelValue: string[]
    buyers: SearchableBuyer[]
    max?: number
    label?: string
    hint?: string
    disabled?: boolean
}>(), {
    modelValue: () => [],
    buyers: () => [],
    max: 1,
    label: '',
    hint: '',
    disabled: false,
})

const emit = defineEmits<{
    (e: 'update:modelValue', value: string[]): void
}>()

const { t } = useI18n()
const dialog = ref(false)
const keyword = ref('')
const recentIds = ref<string[]>(loadRecentBuyerIds())
const buyerIDSet = computed(() => new Set(props.buyers.map(buyer => buyer.logicalId)))
const buyerByID = computed(() => new Map(props.buyers.map(buyer => [buyer.logicalId, buyer])))

const selected = computed({
    get: () => pruneExisting(props.modelValue || []),
    set: (value: string[]) => emit('update:modelValue', pruneExisting(value).slice(0, normalizedMax.value)),
})

const selectedSet = computed(() => new Set(selected.value))
const normalizedMax = computed(() => Math.max(1, Number(props.max) || 1))
const selectedBuyers = computed(() => selected.value.map(id => buyerByID.value.get(id)).filter(Boolean) as SearchableBuyer[])
const selectedPreview = computed(() => selectedBuyers.value.slice(0, 4))

const summary = computed(() => {
    if (selectedBuyers.value.length === 0) return t('buyerPicker.none')
    const first = buyerDisplayName(selectedBuyers.value[0])
    if (selectedBuyers.value.length === 1) return first
    return t('buyerPicker.summary', { name: first, count: selectedBuyers.value.length })
})

const recentBuyers = computed(() => recentIds.value
    .map(id => buyerByID.value.get(id))
    .filter(Boolean) as SearchableBuyer[])

const filteredBuyers = computed(() => filterBuyersBySearch(props.buyers, keyword.value))

function dedupe(values: string[]) {
    const seen = new Set<string>()
    const result: string[] = []
    for (const value of values) {
        if (!value || seen.has(value)) continue
        seen.add(value)
        result.push(value)
    }
    return result
}

function pruneExisting(values: string[]) {
    return dedupe(values).filter(id => buyerIDSet.value.has(id))
}

function sameValues(a: string[], b: string[]) {
    return a.length === b.length && a.every((value, index) => value === b[index])
}

function loadRecentBuyerIds() {
    try {
        const parsed = JSON.parse(localStorage.getItem(RECENT_BUYERS_KEY) || '[]')
        return Array.isArray(parsed) ? dedupe(parsed.map(String)).slice(0, 30) : []
    } catch {
        return []
    }
}

function persistRecentBuyerIds(ids: string[]) {
    const normalized = dedupe(ids).slice(0, 30)
    recentIds.value = normalized
    try {
        localStorage.setItem(RECENT_BUYERS_KEY, JSON.stringify(normalized))
    } catch {
        // Ignore storage quota or private-mode failures.
    }
}

function rememberSelectedBuyers() {
    if (selected.value.length === 0) return
    persistRecentBuyerIds([...selected.value, ...recentIds.value])
}

function done() {
    rememberSelectedBuyers()
    dialog.value = false
}

function clearAll() {
    selected.value = []
}

function toggleBuyer(buyerID: string, checked?: boolean) {
    const next = new Set(selected.value)
    const shouldSelect = checked ?? !next.has(buyerID)
    if (shouldSelect) {
        if (!next.has(buyerID) && next.size >= normalizedMax.value) return
        next.add(buyerID)
    } else {
        next.delete(buyerID)
    }
    selected.value = [...next]
}

function buyerDisplayName(buyer: SearchableBuyer) {
    const name = buyer.name || buyer.logicalId || '—'
    const tail = buyerIdTail(buyer)
    return tail ? `${name} · ${tail}` : name
}

function buyerSubtitle(buyer: SearchableBuyer) {
    const parts: string[] = []
    if (buyer.idCard) parts.push(buyer.idCard)
    if (buyer.tel) parts.push(buyer.tel)
    const count = buyerAccountCount(buyer)
    parts.push(count > 0 ? t('buyerPicker.accountCount', { count }) : t('buyerPicker.noAccountMapping'))
    return parts.join(' · ')
}

watch(
    () => [props.modelValue, props.buyers] as const,
    () => {
        const pruned = pruneExisting(props.modelValue || []).slice(0, normalizedMax.value)
        if (!sameValues(pruned, props.modelValue || [])) emit('update:modelValue', pruned)
        recentIds.value = recentIds.value.filter(id => buyerIDSet.value.has(id))
    },
    { deep: true }
)
</script>

<template>
    <div>
        <v-card variant="outlined" class="pa-3 buyer-picker-card" :class="{ 'buyer-picker-card--disabled': disabled }"
            @click="!disabled && (dialog = true)">
            <div class="d-flex align-center" style="gap:8px">
                <div style="min-width:0;flex:1">
                    <div v-if="label" class="text-caption text-medium-emphasis mb-1">{{ label }}</div>
                    <div class="text-body-2 text-truncate">{{ summary }}</div>
                    <div class="text-caption text-medium-emphasis mt-1">
                        {{ hint || t('buyerPicker.maxHint', { max: normalizedMax }) }}
                    </div>
                </div>
                <div class="d-flex align-center flex-wrap justify-end" style="gap:4px;max-width:50%">
                    <v-chip v-for="buyer in selectedPreview" :key="buyer.logicalId" size="x-small" variant="tonal">
                        {{ buyerDisplayName(buyer) }}
                    </v-chip>
                    <v-chip v-if="selected.length > selectedPreview.length" size="x-small" variant="tonal">
                        +{{ selected.length - selectedPreview.length }}
                    </v-chip>
                    <v-chip size="x-small" variant="outlined" color="primary">
                        {{ selected.length }}/{{ normalizedMax }}
                    </v-chip>
                </div>
                <v-btn icon="mdi-account-multiple-plus" size="small" variant="text" :disabled="disabled" />
            </div>
        </v-card>

        <v-dialog v-model="dialog" max-width="860" scrollable>
            <v-card class="pa-4">
                <v-card-title class="d-flex align-center">
                    <v-icon start>mdi-account-multiple-plus</v-icon>
                    {{ label || t('buyerPicker.title') }}
                    <v-spacer />
                    <v-chip size="small" variant="tonal" color="primary">
                        {{ selected.length }}/{{ normalizedMax }}
                    </v-chip>
                </v-card-title>
                <v-card-text>
                    <v-text-field v-model="keyword" prepend-inner-icon="mdi-magnify"
                        :label="t('buyerPicker.search')" variant="outlined" density="compact" hide-details clearable
                        class="mb-3" />

                    <v-alert v-if="selected.length >= normalizedMax" type="info" variant="tonal" density="compact"
                        class="mb-3">
                        {{ t('buyerPicker.maxReached', { max: normalizedMax }) }}
                    </v-alert>

                    <div v-if="recentBuyers.length > 0 && !keyword.trim()" class="mb-4">
                        <div class="text-caption text-medium-emphasis mb-2">{{ t('buyerPicker.recent') }}</div>
                        <div class="d-flex flex-wrap" style="gap:6px">
                            <v-chip v-for="buyer in recentBuyers" :key="buyer.logicalId" size="small"
                                :color="selectedSet.has(buyer.logicalId) ? 'primary' : undefined"
                                :variant="selectedSet.has(buyer.logicalId) ? 'flat' : 'tonal'" style="cursor:pointer"
                                @click="toggleBuyer(buyer.logicalId)">
                                <v-icon start size="x-small">
                                    {{ selectedSet.has(buyer.logicalId) ? 'mdi-check' : 'mdi-account-outline' }}
                                </v-icon>
                                {{ buyerDisplayName(buyer) }}
                            </v-chip>
                        </div>
                    </div>

                    <v-list density="compact" lines="two" class="border rounded">
                        <v-list-item v-for="buyer in filteredBuyers" :key="buyer.logicalId"
                            @click="toggleBuyer(buyer.logicalId)">
                            <template #prepend>
                                <v-checkbox-btn :model-value="selectedSet.has(buyer.logicalId)" density="compact"
                                    @click.stop @update:model-value="toggleBuyer(buyer.logicalId, Boolean($event))" />
                            </template>
                            <template #title>
                                <span class="font-weight-medium">{{ buyerDisplayName(buyer) }}</span>
                                <v-chip v-if="buyerAccountCount(buyer) === 0" size="x-small" color="warning"
                                    variant="tonal" class="ml-2">
                                    {{ t('buyerPicker.noAccountMapping') }}
                                </v-chip>
                            </template>
                            <template #subtitle>
                                <span>{{ buyerSubtitle(buyer) }}</span>
                            </template>
                        </v-list-item>
                    </v-list>
                </v-card-text>
                <v-card-actions>
                    <v-btn variant="text" :disabled="selected.length === 0" @click="clearAll">
                        {{ t('buyerPicker.clearAll') }}
                    </v-btn>
                    <v-spacer />
                    <v-btn color="primary" @click="done">{{ t('common.done') }}</v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </div>
</template>

<style scoped>
.buyer-picker-card {
    cursor: pointer;
}

.buyer-picker-card:hover {
    border-color: rgb(var(--v-theme-primary));
}

.buyer-picker-card--disabled {
    cursor: default;
    opacity: 0.65;
}
</style>
