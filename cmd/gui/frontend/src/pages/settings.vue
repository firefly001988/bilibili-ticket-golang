<script lang="ts" setup>
import { ref, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import WorkerPicker from '@/components/cluster/WorkerPicker.vue'
import {
    GetRetryInterval, SetRetryInterval,
    GetStartDelay, SetStartDelay,
    GetBuyerManagerWorkerIDs, SetBuyerManagerWorkerIDs,
    Snapshot,
} from '../../bindings/bilibili-ticket-golang/cmd/gui/cluster_service/clusterservice'

const { t, locale } = useI18n()
const messages = useMessagesStore()

const currentLocale = ref(locale.value)
const langOptions = computed(() => [
    { title: t('language.zhCN'), value: 'zh-CN' },
    { title: t('language.en'), value: 'en' },
])

function changeLocale(lang: string) {
    locale.value = lang
    localStorage.setItem('app_locale', lang)
}

// ── State ──────────────────────────────────────────────
const intervalMs = ref(500)
const startDelayMs = ref(50)
const buyerManagerWorkerIds = ref<string[]>(['local'])
const workers = ref<Array<{ id: string; name: string; address: string; type: string; healthy: boolean; tags?: string[] }>>([])
const loading = ref(false)
const savingInterval = ref(false)
const savingDelay = ref(false)
const savingBuyerWorkers = ref(false)

// ── Data loading ───────────────────────────────────────
async function load() {
    loading.value = true
    try {
        const [iv, sd] = await Promise.all([
            GetRetryInterval(),
            GetStartDelay(),
        ])
        intervalMs.value = iv
        startDelayMs.value = sd
        const [buyerWorkers, snap] = await Promise.all([
            GetBuyerManagerWorkerIDs(),
            Snapshot(),
        ])
        buyerManagerWorkerIds.value = (buyerWorkers && buyerWorkers.length > 0) ? buyerWorkers : ['local']
        workers.value = (snap.workers || []) as any[]
    } catch (e: any) {
        console.error('Load settings failed:', e)
        messages.add({ text: t('settings.loadFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    } finally {
        loading.value = false
    }
}

// ── Actions ────────────────────────────────────────────
async function saveInterval() {
    savingInterval.value = true
    try {
        await SetRetryInterval(intervalMs.value)
        messages.add({ text: t('settings.saveRetryInterval'), color: 'success', timeout: 2000 })
    } catch (e: any) {
        messages.add({ text: t('settings.saveFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    } finally {
        savingInterval.value = false
    }
}

async function saveStartDelay() {
    savingDelay.value = true
    try {
        await SetStartDelay(startDelayMs.value)
        messages.add({ text: t('settings.saveStartDelay'), color: 'success', timeout: 2000 })
    } catch (e: any) {
        messages.add({ text: t('settings.saveFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    } finally {
        savingDelay.value = false
    }
}

async function saveBuyerManagerWorkers() {
    savingBuyerWorkers.value = true
    try {
        const ids = buyerManagerWorkerIds.value.length > 0 ? buyerManagerWorkerIds.value : ['local']
        await SetBuyerManagerWorkerIDs(ids)
        buyerManagerWorkerIds.value = ids
        messages.add({ text: t('settings.saveBuyerManagerWorkers'), color: 'success', timeout: 2000 })
    } catch (e: any) {
        messages.add({ text: t('settings.saveFailed', { error: String(e) }), color: 'error', timeout: 4000 })
    } finally {
        savingBuyerWorkers.value = false
    }
}

// ── Lifecycle ──────────────────────────────────────────
onMounted(async () => {
    await load()
})
</script>

<template>
    <div>
        <div class="d-flex align-center">
            <h1 class="text-h5">{{ t('settings.title') }}</h1>
            <v-spacer />
        </div>
        <v-divider thickness="3" class="mb-4" />

        <!-- Language -->
        <v-card variant="outlined" class="mb-4">
            <v-card-title class="text-body-1 py-3 px-4">
                <v-icon start size="20" color="primary">mdi-translate</v-icon>
                {{ t('language.label') }}
            </v-card-title>
            <v-divider />
            <v-card-text class="pa-4">
                <v-select v-model="currentLocale" :items="langOptions" item-title="title" item-value="value"
                    variant="outlined" density="compact" hide-details style="max-width: 300px"
                    @update:model-value="changeLocale" />
            </v-card-text>
        </v-card>

        <!-- Retry interval -->
        <v-card variant="outlined" :loading="loading">
            <v-card-title class="text-body-1 py-3 px-4">
                <v-icon start size="20" color="primary">mdi-timer-sand</v-icon>
                {{ t('settings.retryInterval') }}
            </v-card-title>
            <v-divider />
            <v-card-text class="pa-4">
                <p class="text-body-2 text-medium-emphasis mb-4">
                    {{ t('settings.retryIntervalDesc') }}
                </p>

                <div class="d-flex align-center ga-4">
                    <v-slider v-model="intervalMs" :min="50" :max="2000" :step="50" thumb-label="hover" color="primary"
                        density="compact" show-ticks="always"
                        :ticks="{ 50: '50ms', 500: '500ms', 1000: '1s', 2000: '2s' }" style="flex: 1" />

                    <v-text-field v-model.number="intervalMs" type="number" :label="t('settings.intervalLabel')"
                        variant="outlined" density="compact" :min="50" :max="2000" :step="50" style="max-width: 140px"
                        hide-details suffix="ms" />
                </div>

                <div class="mt-6">
                    <v-alert v-if="intervalMs < 500" type="warning" variant="tonal" density="compact">
                        {{ t('settings.lowDelayWarning') }}
                    </v-alert>
                    <v-alert v-else-if="intervalMs >= 500" type="info" variant="tonal" density="compact">
                        {{ t('settings.conservativeModeInfo') }}
                    </v-alert>
                    <v-alert v-if="intervalMs >= 1000" type="error" variant="tonal" density="compact" class="mt-4">
                        {{ t('settings.highIntervalWarning') }}
                    </v-alert>
                </div>
            </v-card-text>
            <v-card-actions class="px-4 pb-4">
                <v-spacer />
                <v-btn color="primary" variant="tonal" :loading="savingInterval" @click="saveInterval">
                    {{ t('settings.saveSettings') }}
                </v-btn>
            </v-card-actions>
        </v-card>

        <!-- Start delay -->
        <v-card variant="outlined" :loading="loading" class="mt-4">
            <v-card-title class="text-body-1 py-3 px-4">
                <v-icon start size="20" color="secondary">mdi-timer-play</v-icon>
                {{ t('settings.startDelay') }}
            </v-card-title>
            <v-divider />
            <v-card-text class="pa-4">
                <p class="text-body-2 text-medium-emphasis mb-4">
                    {{ t('settings.startDelayDesc') }}
                </p>

                <div class="d-flex align-center ga-4">
                    <v-slider v-model="startDelayMs" :min="0" :max="500" :step="10" thumb-label="hover"
                        color="secondary" density="compact" show-ticks="always"
                        :ticks="{ 0: '0', 100: '100ms', 200: '200ms', 300: '300ms', 400: '400ms', 500: '500ms' }"
                        style="flex: 1" />

                    <v-text-field v-model.number="startDelayMs" type="number" :label="t('settings.delayLabel')"
                        variant="outlined" density="compact" :min="0" :max="500" :step="10" style="max-width: 140px"
                        hide-details suffix="ms" />
                </div>

                <div class="mt-4">
                    <v-alert type="warning" variant="tonal" density="compact">
                        {{ t('settings.startDelayWarning') }}
                    </v-alert>
                    <v-alert v-if="startDelayMs === 0" type="info" variant="tonal" density="compact" class="mt-4">
                        {{ t('settings.immediateStart') }}
                    </v-alert>
                    <v-alert v-else-if="startDelayMs <= 100" type="info" variant="tonal" density="compact" class="mt-4">
                        {{ t('settings.lightDelay') }}
                    </v-alert>
                    <v-alert v-else type="warning" variant="tonal" density="compact" class="mt-4">
                        {{ t('settings.extendedDelay') }}
                    </v-alert>
                </div>
            </v-card-text>
            <v-card-actions class="px-4 pb-4">
                <v-spacer />
                <v-btn color="secondary" variant="tonal" :loading="savingDelay" @click="saveStartDelay">
                    {{ t('settings.saveSettings') }}
                </v-btn>
            </v-card-actions>
        </v-card>

        <!-- Buyer management workers -->
        <v-card variant="outlined" :loading="loading" class="mt-4">
            <v-card-title class="text-body-1 py-3 px-4">
                <v-icon start size="20" color="primary">mdi-account-hard-hat</v-icon>
                {{ t('settings.buyerManagerWorkers') }}
            </v-card-title>
            <v-divider />
            <v-card-text class="pa-4">
                <p class="text-body-2 text-medium-emphasis mb-4">
                    {{ t('settings.buyerManagerWorkersDesc') }}
                </p>
                <WorkerPicker :model-value="buyerManagerWorkerIds"
                    @update:model-value="buyerManagerWorkerIds = $event.length > 0 ? $event : ['local']"
                    :workers="workers" :label="t('settings.buyerManagerWorkers')" />
                <v-alert type="info" variant="tonal" density="compact" class="mt-4">
                    {{ t('settings.buyerManagerWorkersHint') }}
                </v-alert>
            </v-card-text>
            <v-card-actions class="px-4 pb-4">
                <v-spacer />
                <v-btn color="primary" variant="tonal" :loading="savingBuyerWorkers"
                    @click="saveBuyerManagerWorkers">
                    {{ t('settings.saveSettings') }}
                </v-btn>
            </v-card-actions>
        </v-card>

        <!-- About section -->
        <v-card variant="outlined" class="mt-4">
            <v-card-title class="text-body-1 py-3 px-4">
                <v-icon start size="20" color="grey">mdi-information-outline</v-icon>
                关于
            </v-card-title>
            <v-divider />
            <v-card-text class="pa-4 text-body-2 text-medium-emphasis">
                <p>保存后立即生效，包括正在运行的抢票任务。</p>
                <p class="mt-2">重试间隔默认值：500ms（最小值：50ms）；启动延时默认值：0ms（最大值：500ms）。</p>
            </v-card-text>
        </v-card>
    </div>
</template>
