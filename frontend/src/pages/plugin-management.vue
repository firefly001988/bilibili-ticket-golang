<script lang="ts" setup>
import { onMounted, ref } from 'vue'
import { GetAllVersions } from '../../wailsjs/go/plugins/PluginManager'
import type { pcommon } from '../../wailsjs/go/models'

const plugins = ref<pcommon.VersionInfo[]>([])
const loading = ref(false)
const error = ref('')

async function refresh() {
    loading.value = true
    error.value = ''
    try {
        const data = await GetAllVersions()
        plugins.value = Array.isArray(data) ? data : []
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
                <h1 class="text-h4 font-weight-bold">插件管理</h1>
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
        <v-row v-if="loading && plugins.length === 0">
            <v-col cols="12" sm="6" lg="4">
                <v-skeleton-loader type="list-item-three-line" />
            </v-col>
        </v-row>

        <!-- Empty -->
        <v-card v-if="!loading && !error && plugins.length === 0" variant="outlined" class="pa-8 text-center"
            rounded="lg">
            <v-icon icon="mdi-puzzle-outline" size="48" class="mb-3" color="medium-emphasis" />
            <p class="text-body-1 text-medium-emphasis">没有已加载的插件</p>
        </v-card>

        <!-- Plugin list -->
        <v-list v-if="plugins.length > 0" lines="two" rounded="lg" class="border">
            <v-list-item v-for="p in plugins" :key="p.Name" :title="p.Name" :subtitle="`版本 ${p.Version}`">
                <template #prepend>
                    <v-icon icon="mdi-puzzle" color="primary" />
                </template>
                <template #append>
                    <v-chip size="small" variant="tonal" color="info" class="font-mono">
                        {{ p.GitCommit }}
                    </v-chip>
                </template>
            </v-list-item>
        </v-list>
    </div>
</template>