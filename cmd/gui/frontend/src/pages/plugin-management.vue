<script lang="ts" setup>
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { GetAllVersions } from '../../bindings/bilibili-ticket-golang/lib/plugins/pluginmanager'
import type * as plugins from '../../bindings/bilibili-ticket-golang/lib/plugins/models'

const { t } = useI18n()

const pluginsInfo = ref<plugins.LoadedPluginInfo[]>([])
const loading = ref(false)
const error = ref('')
const expanded = ref<Record<string, boolean>>({})

function toggle(name: string) {
    expanded.value[name] = !expanded.value[name]
}

async function refresh() {
    loading.value = true
    error.value = ''
    try {
        const data = await GetAllVersions()
        pluginsInfo.value = Array.isArray(data) ? data : []
    } catch (e: any) {
        error.value = e?.message || String(e)
    } finally {
        loading.value = false
    }
}

onMounted(refresh)
</script>

<template>
    <div class="plugin-management">
        <!-- Header -->
        <div class="d-flex align-center mb-4">
            <div>
                <h1 class="text-h4 font-weight-bold">{{ t('pluginManagement.title') }}</h1>
            </div>
            <v-spacer />
            <v-btn icon="mdi-refresh" variant="text" size="small" :loading="loading" @click="refresh" />
        </div>

        <v-divider class="mb-6" />

        <!-- Error -->
        <v-alert v-if="error" type="error" variant="tonal" class="mb-4" closable @click:close="error = ''">
            {{ error }}
        </v-alert>

        <!-- Loading -->
        <v-row v-if="loading && pluginsInfo.length === 0">
            <v-col v-for="n in 3" :key="n" cols="12">
                <v-skeleton-loader type="card" />
            </v-col>
        </v-row>

        <!-- Empty -->
        <v-card v-if="!loading && !error && pluginsInfo.length === 0" variant="outlined" class="pa-8 text-center"
            rounded="lg">
            <v-icon icon="mdi-puzzle-outline" size="48" class="mb-3" color="medium-emphasis" />
            <p class="text-body-1 text-medium-emphasis">{{ t('pluginManagement.noPlugins') }}</p>
        </v-card>

        <!-- Plugin cards: 每个占一整行，可伸缩 -->
        <div v-if="pluginsInfo.length > 0" class="d-flex flex-column ga-3">
            <v-card v-for="p in pluginsInfo" :key="p.Name" :elevation="2" rounded="lg" border hover>
                <!-- 折叠头部：点击展开/收起 -->
                <div class="d-flex align-center pa-4 cursor-pointer" @click="toggle(p.Name)">
                    <v-icon icon="mdi-puzzle" color="primary" size="28" class="flex-shrink-0" />

                    <div class="ml-3 overflow-hidden flex-grow-1">
                        <div class="d-flex align-center flex-wrap ga-2">
                            <span class="text-subtitle-2 font-weight-bold text-truncate">
                                {{ p.Name }}
                            </span>
                        </div>
                        <div class="d-flex align-center mt-1 flex-wrap ga-1">
                            <span class="text-caption text-medium-emphasis">
                                {{ t('pluginManagement.version', { version: p.Version }) }}
                            </span>
                            <v-chip size="x-small" class="font-mono text-medium-emphasis">
                                {{ p.GitCommit }}
                            </v-chip>
                        </div>
                    </div>

                    <!-- 展开/收起按钮 -->
                    <v-btn :icon="expanded[p.Name] ? 'mdi-chevron-up' : 'mdi-chevron-down'" variant="text" size="small"
                        density="compact" class="flex-shrink-0" @click.stop="toggle(p.Name)" />
                </div>

                <!-- 可伸缩正文 -->
                <div class="plugin-body" :class="{ 'plugin-body--expanded': expanded[p.Name] }">
                    <div class="plugin-body-inner">
                        <v-divider />

                        <v-card-text>
                            <div v-if="p.TestResult" class="text-body-2 font-mono plugin-test-result">
                                {{ p.TestResult }}
                            </div>
                            <div v-else class="text-body-2 text-medium-emphasis font-italic">
                                {{ t('pluginManagement.noTestResult') }}
                            </div>
                        </v-card-text>
                    </div>
                </div>
            </v-card>
        </div>
    </div>
</template>

<style scoped>
.cursor-pointer {
    cursor: pointer;
}

.plugin-body {
    display: grid;
    grid-template-rows: 0fr;
    transition: grid-template-rows 0.3s ease;
}

.plugin-body--expanded {
    grid-template-rows: 1fr;
}

.plugin-body-inner {
    overflow: hidden;
}

.plugin-test-result {
    white-space: pre-wrap;
    word-break: break-all;
}
</style>
