<script lang="ts" setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

interface WorkerOption {
    id: string
    name?: string
    address?: string
    type?: string
    healthy?: boolean
    tags?: string[]
}

const props = withDefaults(defineProps<{
    modelValue: string[]
    workers: WorkerOption[]
    label?: string
    hint?: string
    disabled?: boolean
}>(), {
    modelValue: () => [],
    workers: () => [],
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
const workerIDSet = computed(() => new Set(props.workers.map(worker => worker.id)))

const selected = computed({
    get: () => pruneExisting(props.modelValue || []),
    set: (value: string[]) => emit('update:modelValue', pruneExisting(value)),
})

const selectedSet = computed(() => new Set(selected.value))
const workerByID = computed(() => new Map(props.workers.map(worker => [worker.id, worker])))

const filteredWorkers = computed(() => {
    const kw = keyword.value.trim().toLowerCase()
    if (!kw) return props.workers
    return props.workers.filter(worker => {
        const haystack = [
            worker.id,
            worker.name || '',
            worker.address || '',
            ...(workerTags(worker)),
        ].join(' ').toLowerCase()
        return haystack.includes(kw)
    })
})

const allTags = computed(() => {
    const tags = new Set<string>()
    for (const worker of props.workers) {
        for (const tag of workerTags(worker)) {
            tags.add(tag)
        }
    }
    return Array.from(tags).sort((a, b) => {
        if (a === 'local') return -1
        if (b === 'local') return 1
        if (a === 'remote') return -1
        if (b === 'remote') return 1
        return a.localeCompare(b)
    })
})

const summary = computed(() => {
    const picked = selected.value.map(id => workerByID.value.get(id)).filter(Boolean) as WorkerOption[]
    if (picked.length === 0) return t('workerPicker.none')
    const first = picked[0]
    const firstName = first.name || first.id
    if (picked.length === 1) return firstName
    return t('workerPicker.summary', { name: firstName, count: picked.length })
})

const selectedPreview = computed(() => {
    return selected.value.map(id => workerByID.value.get(id)).filter(Boolean).slice(0, 4) as WorkerOption[]
})

const allSelected = computed(() => props.workers.length > 0 && props.workers.every(worker => selectedSet.value.has(worker.id)))

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
    return dedupe(values).filter(id => workerIDSet.value.has(id))
}

function sameValues(a: string[], b: string[]) {
    return a.length === b.length && a.every((value, index) => value === b[index])
}

function workerTags(worker: WorkerOption) {
    const tags = worker.tags && worker.tags.length > 0 ? worker.tags : [worker.type || 'remote']
    return dedupe(tags.map(tag => String(tag).trim()).filter(Boolean))
}

function setSelected(values: string[]) {
    selected.value = values
}

function toggleWorker(workerID: string, checked?: boolean) {
    const next = new Set(selected.value)
    const shouldSelect = checked ?? !next.has(workerID)
    if (shouldSelect) next.add(workerID)
    else next.delete(workerID)
    setSelected([...next])
}

function selectAll() {
    setSelected(props.workers.map(worker => worker.id))
}

function clearAll() {
    setSelected([])
}

function workersForTag(tag: string) {
    return props.workers.filter(worker => workerTags(worker).includes(tag))
}

function tagAllSelected(tag: string) {
    const tagged = workersForTag(tag)
    return tagged.length > 0 && tagged.every(worker => selectedSet.value.has(worker.id))
}

function toggleTag(tag: string) {
    const next = new Set(selected.value)
    const tagged = workersForTag(tag)
    if (tagAllSelected(tag)) {
        for (const worker of tagged) next.delete(worker.id)
    } else {
        for (const worker of tagged) next.add(worker.id)
    }
    setSelected([...next])
}

watch(
    () => [props.modelValue, props.workers] as const,
    () => {
        const pruned = pruneExisting(props.modelValue || [])
        if (!sameValues(pruned, props.modelValue || [])) emit('update:modelValue', pruned)
    },
    { deep: true }
)
</script>

<template>
    <div>
        <v-card variant="outlined" class="pa-3 worker-picker-card" :class="{ 'worker-picker-card--disabled': disabled }"
            @click="!disabled && (dialog = true)">
            <div class="d-flex align-center" style="gap:8px">
                <div style="min-width:0;flex:1">
                    <div v-if="label" class="text-caption text-medium-emphasis mb-1">{{ label }}</div>
                    <div class="text-body-2 text-truncate">{{ summary }}</div>
                    <div v-if="hint" class="text-caption text-medium-emphasis mt-1">{{ hint }}</div>
                </div>
                <div class="d-flex align-center flex-wrap justify-end" style="gap:4px;max-width:50%">
                    <v-chip v-for="worker in selectedPreview" :key="worker.id" size="x-small" variant="tonal">
                        {{ worker.name || worker.id }}
                    </v-chip>
                    <v-chip v-if="selected.length > selectedPreview.length" size="x-small" variant="tonal">
                        +{{ selected.length - selectedPreview.length }}
                    </v-chip>
                </div>
                <v-btn icon="mdi-server-network" size="small" variant="text" :disabled="disabled" />
            </div>
        </v-card>

        <v-dialog v-model="dialog" max-width="760" scrollable>
            <v-card class="pa-4">
                <v-card-title class="d-flex align-center">
                    <v-icon start>mdi-server-network</v-icon>
                    {{ label || t('workerPicker.title') }}
                    <v-spacer />
                    <v-chip size="small" variant="tonal">{{ t('workerPicker.selected', { count: selected.length }) }}</v-chip>
                </v-card-title>
                <v-card-text>
                    <v-text-field v-model="keyword" prepend-inner-icon="mdi-magnify"
                        :label="t('workerPicker.search')" variant="outlined" density="compact" hide-details
                        clearable class="mb-3" />

                    <div class="d-flex align-center flex-wrap mb-3" style="gap:8px">
                        <v-btn size="small" variant="tonal" prepend-icon="mdi-select-all"
                            :disabled="allSelected || workers.length === 0" @click="selectAll">
                            {{ t('workerPicker.selectAll') }}
                        </v-btn>
                        <v-btn size="small" variant="text" prepend-icon="mdi-close-box-multiple-outline"
                            :disabled="selected.length === 0" @click="clearAll">
                            {{ t('workerPicker.clearAll') }}
                        </v-btn>
                    </div>

                    <div v-if="allTags.length > 0" class="mb-3">
                        <div class="text-caption text-medium-emphasis mb-1">{{ t('workerPicker.tags') }}</div>
                        <div class="d-flex flex-wrap" style="gap:6px">
                            <v-chip v-for="tag in allTags" :key="tag" size="small"
                                :color="tagAllSelected(tag) ? 'primary' : undefined"
                                :variant="tagAllSelected(tag) ? 'flat' : 'tonal'" style="cursor:pointer"
                                @click="toggleTag(tag)">
                                <v-icon start size="x-small">{{ tagAllSelected(tag) ? 'mdi-check' : 'mdi-tag-outline' }}</v-icon>
                                {{ tag }}
                                <span class="ml-1">({{ workersForTag(tag).length }})</span>
                            </v-chip>
                        </div>
                    </div>

                    <v-list density="compact" lines="two" class="border rounded">
                        <v-list-item v-for="worker in filteredWorkers" :key="worker.id" @click="toggleWorker(worker.id)">
                            <template #prepend>
                                <v-checkbox-btn :model-value="selectedSet.has(worker.id)" density="compact"
                                    @click.stop @update:model-value="toggleWorker(worker.id, Boolean($event))" />
                            </template>
                            <template #title>
                                <span class="font-weight-medium">{{ worker.name || worker.id }}</span>
                                <v-chip v-for="tag in workerTags(worker)" :key="tag" size="x-small" variant="tonal"
                                    class="ml-1">{{ tag }}</v-chip>
                            </template>
                            <template #subtitle>
                                <span class="font-monospace">{{ worker.address || worker.id }}</span>
                            </template>
                            <template #append>
                                <v-chip size="x-small" :color="worker.healthy ? 'success' : 'error'" variant="tonal">
                                    {{ worker.healthy ? t('worker.online') : t('worker.offline') }}
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
.worker-picker-card {
    cursor: pointer;
}

.worker-picker-card:hover {
    border-color: rgb(var(--v-theme-primary));
}

.worker-picker-card--disabled {
    cursor: default;
    opacity: 0.65;
}
</style>
