<script lang="ts" setup>
import { ref, onMounted, watch, nextTick } from 'vue'
import { useTaskLogs } from '@/composables/useTaskLogs'
import type { LogEntry } from '@/composables/schedulerTypes'

const props = defineProps<{
    taskId: string
}>()

const { logs, subscribe, clear } = useTaskLogs(props.taskId)

const vsRef = ref<any>(null) // v-virtual-scroll component instance

const ITEM_HEIGHT = 20 // px per log line (11px font * 1.6 line-height ≈ 18px + border)

// Virtual scroll needs a fixed container height; card max-height=360px, header≈41px
const containerH = 300

// Auto-scroll to bottom whenever new logs arrive (throttled to ~1000ms)
let lastAutoScroll = 0
watch(
    () => logs.value.length,
    () => {
        const now = Date.now()
        if (now - lastAutoScroll < 1000) return
        lastAutoScroll = now
        nextTick(() => scrollToBottom())
    },
)

onMounted(async () => {
    await subscribe()
})

// Color mapping for log levels
function levelColor(level: string): string {
    switch (level) {
        case 'success': return 'text-green'
        case 'error': return 'text-red'
        case 'warn': return 'text-orange'
        case 'debug': return 'text-grey-lighten-1'
        default: return 'text-grey-lighten-2'
    }
}

function levelIcon(level: string): string {
    switch (level) {
        case 'success': return 'mdi-check-circle'
        case 'error': return 'mdi-alert-circle'
        case 'warn': return 'mdi-alert'
        case 'debug': return 'mdi-information'
        default: return 'mdi-information'
    }
}

function formatTime(ts: any): string {
    try {
        const d = ts instanceof Date ? ts : new Date(ts)
        if (isNaN(d.getTime())) return String(ts ?? '')
        const mm = String(d.getMonth() + 1).padStart(2, '0')
        const dd = String(d.getDate()).padStart(2, '0')
        const hh = String(d.getHours()).padStart(2, '0')
        const mi = String(d.getMinutes()).padStart(2, '0')
        const ss = String(d.getSeconds()).padStart(2, '0')
        return `${mm}-${dd} ${hh}:${mi}:${ss}`
    } catch {
        return String(ts ?? '')
    }
}

function scrollToBottom() {
    // v-virtual-scroll wraps a scrollable container internally
    const el = vsRef.value?.$el?.querySelector?.('.v-virtual-scroll__container') as HTMLElement | null
    if (el) {
        el.scrollTop = el.scrollHeight
    }
}
</script>

<template>
    <v-card variant="outlined" class="log-viewer">
        <v-card-title class="d-flex align-center py-2 px-3">
            <span class="text-body-2">任务日志</span>
            <v-spacer />
            <v-btn icon="mdi-delete-outline" size="x-small" variant="text" @click="clear" />
            <v-btn icon="mdi-arrow-down" size="x-small" variant="text" @click="scrollToBottom" />
        </v-card-title>
        <v-divider />
        <v-card-text class="log-container pa-2" style="overflow:hidden">
            <div v-if="logs.length === 0" class="text-grey text-caption pa-4 text-center">
                暂无日志 — 等待任务启动...
            </div>
            <v-virtual-scroll v-else ref="vsRef" :items="logs" :height="containerH" class="log-virtual">
                <template #default="{ item: entry, index: idx }">
                    <div class="log-line d-flex align-center ga-1 text-caption py-0">
                        <span class="text-grey-darken-1 text-no-wrap">{{ formatTime(entry.timestamp) }}</span>
                        <v-icon :icon="levelIcon(entry.level)" size="14" :class="levelColor(entry.level)" />
                        <span :class="levelColor(entry.level)" class="text-truncate flex-grow-1">{{ entry.message
                            }}</span>
                    </div>
                </template>
            </v-virtual-scroll>
        </v-card-text>
    </v-card>
</template>

<style scoped>
.log-viewer {
    max-height: 360px;
    display: flex;
    flex-direction: column;
}

.log-container {
    flex: 1;
    overflow: hidden;
    /* virtual scroll handles its own scrollbar */
}

.log-virtual {
    font-family: 'Cascadia Code', 'Fira Code', 'Consolas', monospace;
    font-size: 11px;
    line-height: 1.6;
    background: rgb(var(--v-theme-surface));
}

.log-line {
    height: v-bind(ITEM_HEIGHT + 'px');
    overflow: hidden;
    border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.04);
}
</style>
