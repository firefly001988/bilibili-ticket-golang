<script lang="ts" setup>
import { ref, onMounted, computed } from 'vue'
import { GetAvailablePlugins, FetchPluginList } from '../../wailsjs/go/biliutils/BiliClient'
import type { plugins } from '../../wailsjs/go/models'
import { mirrorSelectOptionsByPrefix, MIRROR_KEYS } from '@/composables/mirrors'

// Re-use Wails-generated types instead of redefining them.
type PluginAsset = plugins.PluginAsset
type PluginInfo = plugins.PluginInfo
type PluginDefinition = plugins.PluginDefinition
type PluginListResult = plugins.PluginListResult

// =============================================================================
// State
// =============================================================================

const result = ref<PluginListResult | null>(null)
const definitions = ref<PluginDefinition[]>([])
const loading = ref(false)
const error = ref('')

// ── Mirror sources (from shared config) ─────────────────
const mirrorOptions = mirrorSelectOptionsByPrefix()
const selectedMirror = ref(mirrorOptions[0].value)

function loadMirror() {
    const saved = localStorage.getItem(MIRROR_KEYS.plugin)
    if (saved && mirrorOptions.some(m => m.value === saved)) {
        selectedMirror.value = saved
    }
}
function saveMirror(v: string) {
    selectedMirror.value = v
    localStorage.setItem(MIRROR_KEYS.plugin, v)
}

const mirrorPrefix = computed(() => selectedMirror.value || '')

/** Rewrite a GitHub URL through the selected mirror (if any). */
function mirrorUrl(url: string): string {
    if (!mirrorPrefix.value || !url.startsWith('https://')) return url
    return mirrorPrefix.value + url
}

// =============================================================================
// Data loading
// =============================================================================

async function fetchList() {
    loading.value = true
    error.value = ''
    try {
        const [defs, releases] = await Promise.all([
            GetAvailablePlugins(),
            FetchPluginList(),
        ])
        console.log('Fetched plugin definitions:', defs)
        console.log('Fetched plugin releases:', releases)
        definitions.value = defs ?? []
        result.value = releases
    } catch (e: any) {
        error.value = String(e)
    } finally {
        loading.value = false
    }
}

// =============================================================================
// Formatting
// =============================================================================

function formatSize(bytes: number): string {
    if (!bytes || bytes < 0) return '未知'
    if (bytes < 1024) return `${bytes} B`
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function formatDate(rfc3339: string): string {
    if (!rfc3339) return ''
    return new Date(rfc3339).toLocaleString('zh-CN')
}

/** Total download size of all assets in a release. */
function totalSize(assets: PluginAsset[]): string {
    const sum = assets.reduce((acc, a) => acc + (a.size || 0), 0)
    return formatSize(sum)
}

// =============================================================================
// Lifecycle
// =============================================================================

onMounted(() => {
    loadMirror()
    fetchList()
})
</script>

<template>
    <div>
        <!-- Header -->
        <div class="d-flex align-center mb-2">
            <h1 class="text-h5">插件下载</h1>
            <v-spacer />
            <v-select v-model="selectedMirror" :items="mirrorOptions" item-title="title" item-value="value"
                label="下载加速源" variant="outlined" density="compact" hide-details style="max-width: 200px" class="mr-2"
                @update:model-value="(v: string) => saveMirror(v as string)" />
            <v-btn prepend-icon="mdi-refresh" variant="tonal" size="default" :loading="loading" @click="fetchList">
                刷新列表
            </v-btn>
        </div>
        <v-divider thickness="3" class="mb-4" />

        <!-- Available plugin definitions -->
        <v-card v-if="definitions && definitions.length > 0" variant="outlined" class="pa-3 mb-4">
            <div class="text-body-2 font-weight-bold mb-2">可用插件 ({{ definitions.length }})</div>
            <div class="d-flex flex-wrap ga-2">
                <v-chip v-for="def in definitions" :key="def.name" variant="tonal" size="small">
                    <v-icon start size="16">mdi-puzzle</v-icon>
                    {{ def.name }}
                    <span class="ml-1 text-grey text-caption">{{ def.source }}</span>
                </v-chip>
            </div>
        </v-card>

        <!-- Error -->
        <v-card v-if="error" color="error" variant="tonal" class="pa-4 mb-4">
            <v-card-text>{{ error }}</v-card-text>
        </v-card>

        <!-- Loading -->
        <v-card v-if="loading && !result" variant="outlined" class="pa-6 text-center">
            <v-progress-circular indeterminate color="primary" class="mb-2" />
            <div class="text-body-2 text-grey">正在获取插件列表...</div>
        </v-card>

        <!-- Empty -->
        <v-card v-if="result && (!result.plugins || result.plugins.length === 0) && !result.error" variant="outlined"
            class="pa-6 text-center">
            <v-icon size="48" color="grey" class="mb-2">mdi-package-variant-closed</v-icon>
            <div class="text-body-1 text-grey">暂无可用插件</div>
        </v-card>

        <!-- Plugin list -->
        <div v-if="result?.plugins?.length">
            <v-card v-for="(plugin, i) in result.plugins" :key="plugin.version" variant="outlined"
                :class="['mb-4', i > 0 ? 'mt-2' : '']">
                <v-card-item>
                    <template #title>
                        <span class="font-weight-bold">{{ plugin.name }}</span>
                        <v-chip size="x-small" variant="tonal" color="primary" class="ml-2">
                            {{ plugin.version }}
                        </v-chip>
                        <v-chip size="x-small" class="ml-1">
                            {{ plugin.source }}
                        </v-chip>
                    </template>
                    <template #subtitle>
                        发布于 {{ formatDate(plugin.publishedAt) }}
                        <span class="ml-2 text-grey">· 共 {{ plugin.assets?.length ?? 0 }} 个资源</span>
                    </template>
                </v-card-item>

                <v-card-text>
                    <!-- Description -->
                    <div v-if="plugin.description" class="text-body-2 text-grey-darken-1 mb-3"
                        style="white-space: pre-wrap; max-height: 120px; overflow-y: auto">
                        {{ plugin.description }}
                    </div>

                    <!-- Download buttons per platform -->
                    <div class="text-body-2 font-weight-bold mb-2">下载:</div>
                    <div class="d-flex flex-wrap ga-2">
                        <v-btn v-for="asset in plugin.assets" :key="asset.name" :href="mirrorUrl(asset.downloadUrl)"
                            target="_blank" variant="tonal" size="small"
                            :prepend-icon="asset.platform === 'windows' ? 'mdi-microsoft-windows' : asset.platform === 'darwin' ? 'mdi-apple' : 'mdi-linux'">
                            {{ asset.platformLabel }}
                            <span class="ml-1 text-grey text-caption">{{ formatSize(asset.size) }}</span>
                            <v-tooltip v-if="asset.checksum" activator="parent" location="top">
                                SHA256: {{ asset.checksum }}
                            </v-tooltip>
                        </v-btn>
                    </div>
                </v-card-text>
            </v-card>
        </div>
    </div>
</template>
