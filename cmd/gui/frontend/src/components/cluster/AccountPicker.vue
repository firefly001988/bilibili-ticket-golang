<script lang="ts" setup>
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

interface AccountOption {
    id: string
    name?: string
    enabled?: boolean
    vipStatus?: number
    tags?: string[]
}

const props = withDefaults(defineProps<{
    modelValue: string[]
    accounts: AccountOption[]
    label?: string
    hint?: string
    disabled?: boolean
}>(), {
    modelValue: () => [],
    accounts: () => [],
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

const selected = computed({
    get: () => props.modelValue || [],
    set: (value: string[]) => emit('update:modelValue', dedupe(value)),
})

const selectedSet = computed(() => new Set(selected.value))
const accountByID = computed(() => new Map(props.accounts.map(account => [account.id, account])))

const filteredAccounts = computed(() => {
    const kw = keyword.value.trim().toLowerCase()
    if (!kw) return props.accounts
    return props.accounts.filter(account => {
        const haystack = [
            account.id,
            account.name || '',
            ...(accountTags(account)),
        ].join(' ').toLowerCase()
        return haystack.includes(kw)
    })
})

const allTags = computed(() => {
    const tags = new Set<string>()
    for (const account of props.accounts) {
        for (const tag of accountTags(account)) tags.add(tag)
    }
    return Array.from(tags).sort((a, b) => a.localeCompare(b))
})

const summary = computed(() => {
    const picked = selected.value.map(id => accountByID.value.get(id)).filter(Boolean) as AccountOption[]
    if (picked.length === 0) return t('accountPicker.none')
    const first = picked[0]
    const firstName = first.name || first.id
    if (picked.length === 1) return firstName
    return t('accountPicker.summary', { name: firstName, count: picked.length })
})

const selectedPreview = computed(() => {
    return selected.value.map(id => accountByID.value.get(id)).filter(Boolean).slice(0, 4) as AccountOption[]
})

const allSelected = computed(() => props.accounts.length > 0 && props.accounts.every(account => selectedSet.value.has(account.id)))

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

function accountTags(account: AccountOption) {
    return dedupe((account.tags || []).map(tag => String(tag).trim()).filter(Boolean))
}

function setSelected(values: string[]) {
    selected.value = values
}

function toggleAccount(accountID: string, checked?: boolean) {
    const next = new Set(selected.value)
    const shouldSelect = checked ?? !next.has(accountID)
    if (shouldSelect) next.add(accountID)
    else next.delete(accountID)
    setSelected([...next])
}

function selectAll() {
    setSelected(props.accounts.map(account => account.id))
}

function clearAll() {
    setSelected([])
}

function accountsForTag(tag: string) {
    return props.accounts.filter(account => accountTags(account).includes(tag))
}

function tagAllSelected(tag: string) {
    const tagged = accountsForTag(tag)
    return tagged.length > 0 && tagged.every(account => selectedSet.value.has(account.id))
}

function toggleTag(tag: string) {
    const next = new Set(selected.value)
    const tagged = accountsForTag(tag)
    if (tagAllSelected(tag)) {
        for (const account of tagged) next.delete(account.id)
    } else {
        for (const account of tagged) next.add(account.id)
    }
    setSelected([...next])
}
</script>

<template>
    <div>
        <v-card variant="outlined" class="pa-3 account-picker-card" :class="{ 'account-picker-card--disabled': disabled }"
            @click="!disabled && (dialog = true)">
            <div class="d-flex align-center" style="gap:8px">
                <div style="min-width:0;flex:1">
                    <div v-if="label" class="text-caption text-medium-emphasis mb-1">{{ label }}</div>
                    <div class="text-body-2 text-truncate">{{ summary }}</div>
                    <div v-if="hint" class="text-caption text-medium-emphasis mt-1">{{ hint }}</div>
                </div>
                <div class="d-flex align-center flex-wrap justify-end" style="gap:4px;max-width:50%">
                    <v-chip v-for="account in selectedPreview" :key="account.id" size="x-small" variant="tonal">
                        {{ account.name || account.id }}
                    </v-chip>
                    <v-chip v-if="selected.length > selectedPreview.length" size="x-small" variant="tonal">
                        +{{ selected.length - selectedPreview.length }}
                    </v-chip>
                </div>
                <v-btn icon="mdi-account-multiple" size="small" variant="text" :disabled="disabled" />
            </div>
        </v-card>

        <v-dialog v-model="dialog" max-width="760" scrollable>
            <v-card class="pa-4">
                <v-card-title class="d-flex align-center">
                    <v-icon start>mdi-account-multiple</v-icon>
                    {{ label || t('accountPicker.title') }}
                    <v-spacer />
                    <v-chip size="small" variant="tonal">{{ t('accountPicker.selected', { count: selected.length }) }}</v-chip>
                </v-card-title>
                <v-card-text>
                    <v-text-field v-model="keyword" prepend-inner-icon="mdi-magnify"
                        :label="t('accountPicker.search')" variant="outlined" density="compact" hide-details
                        clearable class="mb-3" />

                    <div class="d-flex align-center flex-wrap mb-3" style="gap:8px">
                        <v-btn size="small" variant="tonal" prepend-icon="mdi-select-all"
                            :disabled="allSelected || accounts.length === 0" @click="selectAll">
                            {{ t('accountPicker.selectAll') }}
                        </v-btn>
                        <v-btn size="small" variant="text" prepend-icon="mdi-close-box-multiple-outline"
                            :disabled="selected.length === 0" @click="clearAll">
                            {{ t('accountPicker.clearAll') }}
                        </v-btn>
                    </div>

                    <div v-if="allTags.length > 0" class="mb-3">
                        <div class="text-caption text-medium-emphasis mb-1">{{ t('accountPicker.tags') }}</div>
                        <div class="d-flex flex-wrap" style="gap:6px">
                            <v-chip v-for="tag in allTags" :key="tag" size="small"
                                :color="tagAllSelected(tag) ? 'primary' : undefined"
                                :variant="tagAllSelected(tag) ? 'flat' : 'tonal'" style="cursor:pointer"
                                @click="toggleTag(tag)">
                                <v-icon start size="x-small">{{ tagAllSelected(tag) ? 'mdi-check' : 'mdi-tag-outline' }}</v-icon>
                                {{ tag }}
                                <span class="ml-1">({{ accountsForTag(tag).length }})</span>
                            </v-chip>
                        </div>
                    </div>

                    <v-list density="compact" lines="two" class="border rounded">
                        <v-list-item v-for="account in filteredAccounts" :key="account.id" @click="toggleAccount(account.id)">
                            <template #prepend>
                                <v-checkbox-btn :model-value="selectedSet.has(account.id)" density="compact"
                                    @click.stop @update:model-value="toggleAccount(account.id, Boolean($event))" />
                            </template>
                            <template #title>
                                <span class="font-weight-medium">{{ account.name || account.id }}</span>
                                <v-chip v-for="tag in accountTags(account)" :key="tag" size="x-small" variant="tonal"
                                    class="ml-1">{{ tag }}</v-chip>
                            </template>
                            <template #subtitle>
                                <span class="font-monospace">{{ account.id }}</span>
                            </template>
                            <template #append>
                                <v-chip size="x-small" :color="account.enabled ? 'success' : 'grey'" variant="tonal">
                                    {{ account.enabled ? t('account.enabled') : t('account.disabled') }}
                                </v-chip>
                            </template>
                        </v-list-item>
                    </v-list>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn color="primary" @click="dialog = false">{{ t('common.done') }}</v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </div>
</template>

<style scoped>
.account-picker-card {
    cursor: pointer;
}

.account-picker-card:hover {
    border-color: rgb(var(--v-theme-primary));
}

.account-picker-card--disabled {
    cursor: default;
    opacity: 0.65;
}
</style>
