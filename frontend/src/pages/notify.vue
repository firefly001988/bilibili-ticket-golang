<script lang="ts" setup>
import { ref, onMounted, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import {
    GetNotifyChannels,
    AddNotifyChannel,
    RemoveNotifyChannel,
    UpdateNotifyChannel,
    TestNotifyChannel,
    GetNotifyChannelTypes,
} from '../../wailsjs/go/scheduler/SchedulerService'
import type { FrontendNotifyChannel, NotifyChannelTypeMeta, NotifyChannelFieldMeta } from '@/composables/schedulerTypes'

const { t } = useI18n()
const messages = useMessagesStore()

// ── State ──────────────────────────────────────────────
const channels = ref<FrontendNotifyChannel[]>([])
const channelTypes = ref<NotifyChannelTypeMeta[]>([])
const showDialog = ref(false)
const formType = ref('')
const formName = ref('')
/** key-value params whose keys are defined by NotifyChannelTypeMeta.Fields */
const formParams = ref<Record<string, string>>({})
const editingIndex = ref<number | null>(null)
const testingIndex = ref<number | null>(null)

// ── Computed ───────────────────────────────────────────
/** Field metadata for the currently selected channel type. */
const currentTypeMeta = computed<NotifyChannelTypeMeta | undefined>(() =>
    channelTypes.value.find(t => t.type === formType.value),
)

/** v-select items: [{ title: 'Gotify', value: 'gotify' }, ...] */
const typeItems = computed(() =>
    channelTypes.value.map(t => ({ title: t.label, value: t.type })),
)

// ── Data loading ───────────────────────────────────────
async function load() {
    try {
        channels.value = await GetNotifyChannels()
    } catch (e: any) {
        console.error('Load notify channels failed:', e)
    }
}

async function loadTypes() {
    try {
        channelTypes.value = await GetNotifyChannelTypes()
        if (!formType.value && channelTypes.value.length > 0) {
            formType.value = channelTypes.value[0]!.type
        }
    } catch (e: any) {
        console.error('Load notify channel types failed:', e)
    }
}

// ── Dialog helpers ─────────────────────────────────────
function resetForm() {
    editingIndex.value = null
    formType.value = channelTypes.value[0]?.type ?? ''
    formName.value = ''
    // formParams is populated by the watch on formType → applyDefaults()
}

function openAdd() {
    resetForm()
    showDialog.value = true
}

function openEdit(index: number) {
    const ch = channels.value[index]
    if (!ch) return
    formType.value = ch.type
    formName.value = ch.name
    formParams.value = { ...ch.params }
    editingIndex.value = index
    showDialog.value = true
}

async function toggleEnabled(index: number) {
    const ch = channels.value[index]
    if (!ch) return
    try {
        ch.enabled = !ch.enabled
        await UpdateNotifyChannel(index, { index, type: ch.type, name: ch.name, enabled: ch.enabled, params: ch.params })
        messages.add({ text: ch.enabled ? t('notify.enabled') : t('notify.disabled'), color: 'info', timeout: 1500 })
    } catch (e: any) {
        ch.enabled = !ch.enabled // revert
        messages.add({ text: `操作失败: ${e}`, color: 'error', timeout: 4000 })
    }
}

/** Fill formParams with default values from metadata for the selected channel type. */
function applyDefaults() {
    const meta = currentTypeMeta.value
    if (!meta) return
    const defaults: Record<string, string> = {}
    for (const field of meta.fields) {
        if (field.default) {
            defaults[field.key] = field.default
        }
    }
    formParams.value = defaults
}

// Reset params when channel type changes (only for new channels)
watch(formType, () => {
    if (editingIndex.value === null) {
        applyDefaults()
    }
})

// ── Actions ────────────────────────────────────────────
async function submit() {
    try {
        const ch: FrontendNotifyChannel = {
            index: editingIndex.value ?? 0,
            type: formType.value,
            name: formName.value,
            enabled: editingIndex.value !== null
                ? (channels.value[editingIndex.value]?.enabled ?? true)
                : true,
            params: { ...formParams.value },
        }
        if (editingIndex.value !== null) {
            await UpdateNotifyChannel(editingIndex.value, ch)
            messages.add({ text: '通知渠道已更新', color: 'success', timeout: 2000 })
        } else {
            await AddNotifyChannel(ch)
            messages.add({ text: '通知渠道已添加', color: 'success', timeout: 2000 })
        }
        showDialog.value = false
        await load()
    } catch (e: any) {
        messages.add({ text: `操作失败: ${e}`, color: 'error', timeout: 4000 })
    }
}

async function remove(index: number) {
    try {
        await RemoveNotifyChannel(index)
        messages.add({ text: '通知渠道已删除', color: 'info', timeout: 2000 })
        await load()
    } catch (e: any) {
        messages.add({ text: `删除失败: ${e}`, color: 'error', timeout: 4000 })
    }
}

async function test(index: number) {
    testingIndex.value = index
    try {
        await TestNotifyChannel(index)
        messages.add({ text: '测试消息发送成功', color: 'success', timeout: 3000 })
    } catch (e: any) {
        messages.add({ text: `测试失败: ${e}`, color: 'error', timeout: 4000 })
    } finally {
        testingIndex.value = null
    }
}

function typeLabel(t: string): string {
    const meta = channelTypes.value.find(m => m.type === t)
    return meta?.label ?? t
}

/** Show first non-empty param value as subtitle, or fallback text. */
function channelSubtitle(ch: FrontendNotifyChannel): string {
    const vals = Object.values(ch.params ?? {}).filter(Boolean)
    return vals.length > 0 ? vals.join(' · ') : '未配置参数'
}

// ── Lifecycle ──────────────────────────────────────────
onMounted(async () => {
    await loadTypes()
    await load()
})
</script>

<template>
    <div>
        <div class="d-flex align-center">
            <h1 class="text-h5">通知渠道</h1>
            <v-spacer />
            <v-btn prepend-icon="mdi-plus" color="primary" variant="tonal" size="small" @click="openAdd">
                添加渠道
            </v-btn>
        </div>
        <v-divider thickness="3" class="mb-4" />

        <v-card variant="outlined">
            <v-card-text class="pa-0">
                <div v-if="channels.length === 0" class="text-grey text-caption pa-6 text-center">
                    暂未配置通知渠道 — 添加渠道后，抢票成功时将向所有渠道发送通知
                </div>
                <v-list v-else density="compact" lines="one">
                    <v-list-item v-for="(ch, i) in channels" :key="i" :class="{ 'text-disabled': !ch.enabled }">
                        <template #prepend>
                            <v-icon size="18" :color="ch.enabled ? 'blue' : 'grey'">mdi-bell-ring</v-icon>
                        </template>
                        <template #title>
                            <span class="text-body-2">{{ ch.name || '未命名' }}</span>
                            <v-chip size="x-small" variant="tonal" class="ml-1">{{ typeLabel(ch.type) }}</v-chip>
                            <v-chip v-if="!ch.enabled" size="x-small" color="grey" variant="tonal"
                                class="ml-1">已关闭</v-chip>
                        </template>
                        <template #subtitle>
                            <span class="text-caption text-grey">{{ channelSubtitle(ch) }}</span>
                        </template>
                        <template #append>
                            <div class="d-flex ga-0 align-center">
                                <v-switch :model-value="ch.enabled" color="primary" density="compact" hide-details
                                    @click.stop @update:model-value="toggleEnabled(i)" class="mr-4" />
                                <v-btn icon="mdi-pencil" size="x-small" variant="text" color="grey"
                                    @click.stop="openEdit(i)" />
                                <v-btn icon="mdi-test-tube" size="x-small" variant="text" color="warning"
                                    :loading="testingIndex === i" @click.stop="test(i)" />
                                <v-btn icon="mdi-delete-outline" size="x-small" variant="text" color="grey"
                                    @click.stop="remove(i)" />
                            </div>
                        </template>
                    </v-list-item>
                </v-list>
            </v-card-text>
        </v-card>


        <!-- Dialog -->
        <v-dialog v-model="showDialog" max-width="480">
            <v-card :title="editingIndex !== null ? '编辑通知渠道' : '添加通知渠道'">
                <v-card-text>
                    <v-row dense>
                        <v-col cols="12">
                            <v-select v-model="formType" label="渠道类型" variant="outlined" density="compact"
                                :items="typeItems" required />
                        </v-col>
                        <v-col cols="12">
                            <v-text-field v-model="formName" label="名称 (选填)" variant="outlined" density="compact"
                                hint="方便识别，如「手机通知」" persistent-hint />
                        </v-col>
                        <!-- Dynamic fields from Go metadata -->
                        <template v-if="currentTypeMeta">
                            <v-col v-for="field in currentTypeMeta.fields" :key="field.key" cols="12">
                                <v-text-field v-model="formParams[field.key]" :label="field.label" :type="field.type"
                                    :placeholder="field.placeholder" :hint="field.hint" :persistent-hint="!!field.hint"
                                    :required="field.required" variant="outlined" density="compact" />
                            </v-col>
                        </template>
                    </v-row>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showDialog = false">取消</v-btn>
                    <v-btn color="primary" variant="tonal" @click="submit">
                        {{ editingIndex !== null ? '保存' : '添加' }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </div>
</template>
