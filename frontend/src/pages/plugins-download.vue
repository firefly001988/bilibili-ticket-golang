<script lang="ts" setup>
import { ref, onMounted, computed } from 'vue'
import { GetAvailablePlugins, FetchPluginListByName } from '../../wailsjs/go/biliutils/BiliClient'
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
const selectedPlugin = ref('')
const loadingDefs = ref(false)
const loadingReleases = ref(false)
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

async function fetchDefinitions() {
    loadingDefs.value = true
    error.value = ''
    try {
        const defs = await GetAvailablePlugins()
        console.log('Fetched plugin definitions:', defs)
        definitions.value = defs ?? []
    } catch (e: any) {
        error.value = String(e)
    } finally {
        loadingDefs.value = false
    }
}

async function fetchReleases(name: string) {
    if (!name) return
    loadingReleases.value = true
    error.value = ''
    result.value = null
    try {
        const releases = await FetchPluginListByName(name)
        console.log('Fetched plugin releases:', releases)
        result.value = releases
    } catch (e: any) {
        error.value = String(e)
    } finally {
        loadingReleases.value = false
    }
}

function selectPlugin(name: string) {
    if (selectedPlugin.value === name) {
        // deselect
        selectedPlugin.value = ''
        result.value = null
        return
    }
    selectedPlugin.value = name
    fetchReleases(name)
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

// =============================================================================
// Lifecycle
// =============================================================================

onMounted(() => {
    loadMirror()
    fetchDefinitions()
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
        </div>
        <v-divider thickness="3" class="mb-4" />

        <!-- Available plugin definitions – selectable -->
        <v-card variant="outlined" class="pa-3 mb-4">
            <div class="d-flex align-center mb-3">
                <v-icon start size="20" color="primary">mdi-puzzle</v-icon>
                <span class="text-body-2 font-weight-bold">选择要下载的插件 ({{ definitions.length }})</span>
                <v-spacer />
                <v-progress-circular v-if="loadingDefs" indeterminate size="16" width="2" color="primary" />
            </div>

            <v-row v-if="definitions.length > 0" dense>
                <v-col v-for="def in definitions" :key="def.name" cols="12" sm="6" md="4">
                    <v-card :variant="selectedPlugin === def.name ? 'elevated' : 'outlined'"
                        :color="selectedPlugin === def.name ? 'primary' : undefined" class="cursor-pointer plugin-card"
                        :class="{ 'selected': selectedPlugin === def.name }" @click="selectPlugin(def.name)">
                        <v-card-item>
                            <template #title>
                                <div style="align-items: center; display: flex;">
                                    <v-icon start size="18" :color="selectedPlugin === def.name ? 'white' : 'primary'">
                                        {{ selectedPlugin === def.name ? 'mdi-check-circle' : 'mdi-puzzle-outline' }}
                                    </v-icon>
                                    <span :class="selectedPlugin === def.name ? 'text-white' : ''">{{ def.name }}</span>
                                </div>
                            </template>
                            <template #subtitle>
                                <span :class="selectedPlugin === def.name ? 'text-white' : 'text-grey'">
                                    {{ def.description }}
                                </span>
                            </template>
                        </v-card-item>
                    </v-card>
                </v-col>
            </v-row>

            <div v-else-if="!loadingDefs" class="text-body-2 text-grey pa-3 text-center">
                暂无可用的插件定义
            </div>
        </v-card>

        <!-- Error -->
        <v-card v-if="error" color="error" variant="tonal" class="pa-4 mb-4">
            <v-card-text>{{ error }}</v-card-text>
        </v-card>

        <!-- Prompt to select a plugin -->
        <v-card v-if="!selectedPlugin && !loadingReleases && !result" variant="outlined" class="pa-6 text-center">
            <v-icon size="48" color="grey" class="mb-2">mdi-arrow-up-bold</v-icon>
            <div class="text-body-1 text-grey">请先选择上方插件以查看版本列表</div>
        </v-card>

        <!-- Loading releases -->
        <v-card v-if="loadingReleases" variant="outlined" class="pa-6 text-center">
            <v-progress-circular indeterminate color="primary" class="mb-2" />
            <div class="text-body-2 text-grey">正在获取 {{ selectedPlugin }} 的版本列表...</div>
        </v-card>

        <!-- Empty releases -->
        <v-card v-if="result && (!result.plugins || result.plugins.length === 0) && !result.error && !loadingReleases"
            variant="outlined" class="pa-6 text-center">
            <v-icon size="48" color="grey" class="mb-2">mdi-package-variant-closed</v-icon>
            <div class="text-body-1 text-grey">暂无可用版本</div>
        </v-card>

        <!-- Plugin version list -->
        <div v-if="result?.plugins?.length">
            <div class="d-flex align-center mb-2">
                <span class="text-body-2 text-grey">
                    {{ selectedPlugin }} 共 {{ result.plugins.length }} 个版本
                </span>
                <v-spacer />
                <v-btn variant="text" size="small" prepend-icon="mdi-refresh" :loading="loadingReleases"
                    @click="fetchReleases(selectedPlugin)">
                    刷新版本
                </v-btn>
            </div>

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

<style scoped>
.plugin-card {
    transition: all 0.2s ease;
}

.plugin-card:hover {
    border-color: rgb(var(--v-theme-primary));
    transform: translateY(-1px);
}

.plugin-card.selected {
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
}
</style>
