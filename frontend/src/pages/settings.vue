<script lang="ts" setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { useMessagesStore } from '@/stores/snackbar'
import {
    GetRetryInterval, SetRetryInterval,
    GetStartDelay, SetStartDelay,
    GetBilibiliOffset, GetNTPOffset,
} from '../../wailsjs/go/scheduler/SchedulerService'

const messages = useMessagesStore()

// ── State ──────────────────────────────────────────────
const intervalMs = ref(500)
const startDelayMs = ref(50)
const biliOffsetMs = ref<number | null>(null)
const ntpOffsetMs = ref<number | null>(null)
const loading = ref(false)
const savingInterval = ref(false)
const savingDelay = ref(false)

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
    } catch (e: any) {
        console.error('Load settings failed:', e)
        messages.add({ text: `加载设置失败: ${e}`, color: 'error', timeout: 4000 })
    } finally {
        loading.value = false
    }

    // Fetch clock offsets immediately after load
    fetchOffsets()
}

// ── Actions ────────────────────────────────────────────
async function saveInterval() {
    savingInterval.value = true
    try {
        await SetRetryInterval(intervalMs.value)
        messages.add({ text: '重试间隔已保存', color: 'success', timeout: 2000 })
    } catch (e: any) {
        messages.add({ text: `保存失败: ${e}`, color: 'error', timeout: 4000 })
    } finally {
        savingInterval.value = false
    }
}

async function saveStartDelay() {
    savingDelay.value = true
    try {
        await SetStartDelay(startDelayMs.value)
        messages.add({ text: '启动延时已保存', color: 'success', timeout: 2000 })
    } catch (e: any) {
        messages.add({ text: `保存失败: ${e}`, color: 'error', timeout: 4000 })
    } finally {
        savingDelay.value = false
    }
}

// ── Lifecycle ──────────────────────────────────────────
async function fetchOffsets() {
    try {
        biliOffsetMs.value = await GetBilibiliOffset()
    } catch { /* ignore */ }
    try {
        ntpOffsetMs.value = await GetNTPOffset()
    } catch { /* ignore */ }
}

let offsetTimer: ReturnType<typeof setInterval> | null = null

onMounted(async () => {
    await load()
    // Poll both clock offsets every 10s
    offsetTimer = setInterval(fetchOffsets, 10000)
})

onUnmounted(() => {
    if (offsetTimer) { clearInterval(offsetTimer); offsetTimer = null }
})
</script>

<template>
    <div>
        <div class="d-flex align-center">
            <h1 class="text-h5">常规设置</h1>
            <v-spacer />
        </div>
        <v-divider thickness="3" class="mb-4" />

        <!-- Retry interval -->
        <v-card variant="outlined" :loading="loading">
            <v-card-title class="text-body-1 py-3 px-4">
                <v-icon start size="20" color="primary">mdi-timer-sand</v-icon>
                重试间隔
            </v-card-title>
            <v-divider />
            <v-card-text class="pa-4">
                <p class="text-body-2 text-medium-emphasis mb-4">
                    设置每次抢票尝试之间的等待时间。较短的间隔可以提高抢票频率，但过短的间隔可能触发平台风控。
                </p>

                <div class="d-flex align-center ga-4">
                    <v-slider v-model="intervalMs" :min="50" :max="2000" :step="50" thumb-label="hover" color="primary"
                        density="compact" show-ticks="always"
                        :ticks="{ 50: '50ms', 500: '500ms', 1000: '1s', 2000: '2s' }" style="flex: 1" />

                    <v-text-field v-model.number="intervalMs" type="number" label="间隔 (ms)" variant="outlined"
                        density="compact" :min="50" :max="2000" :step="50" style="max-width: 140px" hide-details
                        suffix="ms" />
                </div>

                <div class="mt-6">
                    <v-alert v-if="intervalMs < 500" type="warning" variant="tonal" density="compact">
                        低延迟模式 (&lt;500ms)：高频率请求可能触发平台风控，请谨慎使用。
                    </v-alert>
                    <v-alert v-else-if="intervalMs >= 500" type="info" variant="tonal" density="compact">
                        保守模式 (≥500ms)：请求频率较低，风控风险较小，但可能降低抢票成功率。
                    </v-alert>
                    <v-alert v-if="intervalMs >= 1000" type="error" variant="tonal" density="compact" class="mt-4">
                        在 ≥1000ms 的设置下，抢票频率非常低，可能会错过抢票时机。建议根据实际情况调整。
                    </v-alert>
                </div>
            </v-card-text>
            <v-card-actions class="px-4 pb-4">
                <v-spacer />
                <v-btn color="primary" variant="tonal" :loading="savingInterval" @click="saveInterval">
                    保存设置
                </v-btn>
            </v-card-actions>
        </v-card>

        <!-- Start delay -->
        <v-card variant="outlined" :loading="loading" class="mt-4">
            <v-card-title class="text-body-1 py-3 px-4">
                <v-icon start size="20" color="secondary">mdi-timer-play</v-icon>
                启动延时
            </v-card-title>
            <v-divider />
            <v-card-text class="pa-4">
                <p class="text-body-2 text-medium-emphasis mb-4">
                    在进入抢票循环之前，等待一段固定时间。可用于错开多个任务的启动时机，避免瞬时请求风暴。设为 0 则立即开始抢票。
                </p>

                <div class="d-flex align-center ga-4">
                    <v-slider v-model="startDelayMs" :min="0" :max="500" :step="10" thumb-label="hover"
                        color="secondary" density="compact" show-ticks="always"
                        :ticks="{ 0: '0', 100: '100ms', 200: '200ms', 300: '300ms', 400: '400ms', 500: '500ms' }"
                        style="flex: 1" />

                    <v-text-field v-model.number="startDelayMs" type="number" label="延时 (ms)" variant="outlined"
                        density="compact" :min="0" :max="500" :step="10" style="max-width: 140px" hide-details
                        suffix="ms" />
                </div>

                <div class="mt-4">
                    <v-alert type="warning" variant="tonal" density="compact">
                        过小会导致获得风控惩罚，请合理配置启动延时，尤其是在同时运行多个抢票任务时。
                    </v-alert>
                    <v-alert v-if="startDelayMs === 0" type="info" variant="tonal" density="compact" class="mt-4">
                        立即启动：任务创建后立即进入抢票循环。
                    </v-alert>
                    <v-alert v-else-if="startDelayMs <= 100" type="info" variant="tonal" density="compact" class="mt-4">
                        轻度延迟 (≤100ms)：等待短暂时间后开始抢票。
                    </v-alert>
                    <v-alert v-else type="warning" variant="tonal" density="compact" class="mt-4">
                        较长延迟 (≤500ms)：可用于错开多个任务的启动时机。
                    </v-alert>
                </div>
            </v-card-text>
            <v-card-actions class="px-4 pb-4">
                <v-spacer />
                <v-btn color="secondary" variant="tonal" :loading="savingDelay" @click="saveStartDelay">
                    保存设置
                </v-btn>
            </v-card-actions>
        </v-card>

        <!-- Clock offset -->
        <v-card variant="outlined" class="mt-4">
            <v-card-title class="text-body-1 py-3 px-4">
                <v-icon start size="20" color="green">mdi-clock-check-outline</v-icon>
                时钟校准（每 10 秒刷新）
            </v-card-title>
            <v-divider />
            <v-card-text class="pa-4">
                <p class="text-body-2 text-medium-emphasis mb-4">
                    本地时间与服务器时间的差值。正值表示本地时钟落后于服务器。抢票瞄准的时间会自动以此修正。
                </p>

                <v-row dense>
                    <v-col cols="6">
                        <v-card variant="tonal" color="blue-grey" class="pa-4 text-center">
                            <div class="text-caption text-medium-emphasis">Bilibili API</div>
                            <div class="text-h5 mt-1"
                                :class="biliOffsetMs !== null && Math.abs(biliOffsetMs) > 1000 ? 'text-red' : 'text-blue'">
                                {{ biliOffsetMs !== null ? (biliOffsetMs > 0 ? '+' : '') + biliOffsetMs + ' ms' : '—' }}
                            </div>
                            <div class="text-caption mt-1">
                                <template v-if="biliOffsetMs === null">等待中...</template>
                                <template v-else-if="Math.abs(biliOffsetMs) < 200">
                                    <v-icon size="12" color="green">mdi-check-circle</v-icon> 良好
                                </template>
                                <template v-else-if="Math.abs(biliOffsetMs) < 500">
                                    <v-icon size="12" color="warning">mdi-alert-circle</v-icon> 略大
                                </template>
                                <template v-else>
                                    <v-icon size="12" color="red">mdi-close-circle</v-icon> 偏差大
                                </template>
                            </div>
                        </v-card>
                    </v-col>
                    <v-col cols="6">
                        <v-card variant="tonal" color="teal" class="pa-4 text-center">
                            <div class="text-caption text-medium-emphasis">NTP (阿里云)</div>
                            <div class="text-h5 mt-1"
                                :class="ntpOffsetMs !== null && Math.abs(ntpOffsetMs) > 1000 ? 'text-red' : 'text-teal'">
                                {{ ntpOffsetMs !== null ? (ntpOffsetMs > 0 ? '+' : '') + ntpOffsetMs + ' ms' : '—' }}
                            </div>
                            <div class="text-caption mt-1">
                                <template v-if="ntpOffsetMs === null">等待中...</template>
                                <template v-else-if="Math.abs(ntpOffsetMs) < 200">
                                    <v-icon size="12" color="green">mdi-check-circle</v-icon> 良好
                                </template>
                                <template v-else-if="Math.abs(ntpOffsetMs) < 500">
                                    <v-icon size="12" color="warning">mdi-alert-circle</v-icon> 略大
                                </template>
                                <template v-else>
                                    <v-icon size="12" color="red">mdi-close-circle</v-icon> 偏差大
                                </template>
                            </div>
                        </v-card>
                    </v-col>
                </v-row>

                <p class="text-caption text-medium-emphasis mt-3">
                    系统每 120 秒自动校准一次（Bilibili 优先，NTP 备用），此页面每 10 秒刷新显示。
                </p>
            </v-card-text>
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
