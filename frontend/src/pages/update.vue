<script lang="ts" setup>
import { ref, onMounted, computed } from 'vue'
import { CheckForUpdate } from '../../wailsjs/go/biliutils/BiliClient'
import type { githubutils } from '../../wailsjs/go/models'

const info = ref<githubutils.UpdateInfo | null>(null)
const loading = ref(false)
const error = ref('')

// ── Mirror sources ─────────────────────────────────────
const mirrorOptions = [
    { title: 'GitHub (直连)', value: '' },
    { title: 'gh-proxy.com', value: 'https://gh-proxy.com/' },
    { title: 'gh.ddlc.top', value: 'https://gh.ddlc.top/' },
    { title: 'ghproxy.net', value: 'https://ghproxy.net/' },
]
const selectedMirror = ref(mirrorOptions[0].value)

const MIRROR_KEY = 'update.mirrorSource'

// Persist mirror choice in localStorage
function loadMirror() {
    const saved = localStorage.getItem(MIRROR_KEY)
    if (saved && mirrorOptions.some(m => m.value === saved)) {
        selectedMirror.value = saved
    }
}
function saveMirror(v: string) {
    selectedMirror.value = v
    localStorage.setItem(MIRROR_KEY, v)
}

const mirrorPrefix = computed(() => selectedMirror.value || '')

/** Rewrite a GitHub URL through the selected mirror (if any). */
function mirrorUrl(url: string): string {
    if (!mirrorPrefix.value || !url.startsWith('https://')) return url
    // github.com/*/releases/download/*  →  <mirror>https://github.com/...
    return mirrorPrefix.value + url
}

async function check() {
    loading.value = true
    error.value = ''
    try {
        info.value = await CheckForUpdate()
    } catch (e: any) {
        error.value = String(e)
    } finally {
        loading.value = false
    }
}

function formatSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

onMounted(() => {
    loadMirror()
    check()
})
</script>

<template>
    <div>
        <div class="d-flex align-center">
            <h1 class="text-h5">更新检测</h1>
            <v-spacer />
            <v-select v-model="selectedMirror" :items="mirrorOptions" item-title="title" item-value="value"
                label="下载加速源" variant="outlined" density="compact" hide-details style="max-width: 200px;" class="mr-2"
                @update:model-value="(v: string) => saveMirror(v as string)" />
            <v-btn prepend-icon="mdi-refresh" variant="tonal" size="default" :loading="loading" @click="check">
                检查更新
            </v-btn>
        </div>
        <v-divider thickness="3" class="mb-4" />

        <v-card v-if="error" color="error" variant="tonal" class="pa-4 mb-4">
            <v-card-text>{{ error }}</v-card-text>
        </v-card>

        <v-card v-if="info" variant="outlined" class="pa-4">
            <v-card-text>
                <div class="mb-4">
                    <span class="text-grey">当前版本:</span>
                    <v-chip size="x-small" variant="tonal" class="ml-2">{{ info.currentVersion }}</v-chip>
                </div>

                <v-alert v-if="info.hasUpdate" type="warning" variant="tonal" class="mb-4">
                    发现新版本 <strong>{{ info.latestVersion }}</strong>，
                    发布于 {{ new Date(info.publishedAt).toLocaleString('zh-CN') }}
                </v-alert>

                <v-alert v-else type="success" variant="tonal" class="mb-4">
                    已是最新版本
                </v-alert>

                <!-- Download assets -->
                <div v-if="info.assets && info.assets.length > 0" class="mb-4">
                    <div class="text-body-2 font-weight-bold mb-2">下载资源:</div>
                    <v-list density="compact">
                        <v-list-item v-for="a in info.assets" :key="a.name" :href="mirrorUrl(a.browser_download_url)"
                            target="_blank">
                            <template #prepend>
                                <v-icon size="18">mdi-download</v-icon>
                            </template>
                            <template #title>
                                {{ a.name }}
                            </template>
                            <template #subtitle>
                                {{ formatSize(a.size) }}
                            </template>
                        </v-list-item>
                    </v-list>
                </div>

                <v-btn v-if="info.latestUrl" :href="mirrorUrl(info.latestUrl)" target="_blank" color="primary"
                    variant="tonal" size="small" block>
                    前往 Release 页面
                </v-btn>
            </v-card-text>
        </v-card>

        <v-card v-else-if="!loading" variant="outlined" class="pa-6 text-center">
            <v-icon size="48" color="grey">mdi-cloud-search-outline</v-icon>
            <p class="text-grey mt-2">点击上方「检查更新」获取最新版本信息</p>
        </v-card>
    </div>
</template>
