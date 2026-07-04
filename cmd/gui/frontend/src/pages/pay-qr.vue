<script lang="ts" setup>
import { ref, onMounted, onUnmounted, computed, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Window } from '@wailsio/runtime'
import VueQr from 'vue-qr'

const route = useRoute()
const { t } = useI18n()

const pageRef = ref<HTMLElement | null>(null)
const link = ref('')
const title = ref('')
const project = ref('')
const screen = ref('')
const sku = ref('')
const buyer = ref('')
const account = ref('')
const expire = ref(0)
const orderTime = ref(0)

onMounted(() => {
    link.value = (route.query.link as string) || ''
    title.value = (route.query.title as string) || t('payQR.defaultTitle')
    project.value = (route.query.project as string) || ''
    screen.value = (route.query.screen as string) || ''
    sku.value = (route.query.sku as string) || ''
    buyer.value = (route.query.buyer as string) || ''
    account.value = (route.query.account as string) || ''
    expire.value = parseInt(route.query.expire as string) || 0
    orderTime.value = parseInt(route.query.orderTime as string) || 0
})

const remaining = ref('')
let timer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
    if (expire.value > 0) {
        const update = () => {
            const left = expire.value - Math.floor(Date.now() / 1000)
            if (left <= 0) {
                remaining.value = t('payQR.expired')
                if (timer) { clearInterval(timer); timer = null }
                return
            }
            const m = Math.floor(left / 60)
            const s = left % 60
            remaining.value = `${m}:${String(s).padStart(2, '0')}`
        }
        update()
        timer = setInterval(update, 1000)
    }
})

onUnmounted(() => { if (timer) clearInterval(timer) })

// Auto-resize window to fit content.
// Uses scrollWidth/scrollHeight (real content size, not viewport-clipped rect)
// and retries to account for async-rendered vue-qr canvas.
function measureAndResize() {
    const el = pageRef.value
    if (!el) return
    const w = el.scrollWidth
    const h = el.scrollHeight
    if (w > 0 && h > 0) {
        Window.SetSize(Math.ceil(w) + 32, Math.ceil(h) + 32)
        return true
    }
    return false
}

let resizeRetries = 0
let resizeTimer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
    // Try immediately, then retry every 150ms up to 15 times (~2.25s)
    // for async components (vue-qr canvas) to finish rendering.
    nextTick(() => {
        requestAnimationFrame(() => {
            if (measureAndResize()) return
            resizeTimer = setInterval(() => {
                resizeRetries++
                if (measureAndResize() || resizeRetries >= 15) {
                    if (resizeTimer) { clearInterval(resizeTimer); resizeTimer = null }
                }
            }, 150)
        })
    })
})

onUnmounted(() => {
    if (resizeTimer) clearInterval(resizeTimer)
})

const copied = ref(false)
const expired = computed(() => expire.value > 0 && remaining.value === t('payQR.expired'))

async function copyLink() {
    if (!link.value || expired.value) return
    try {
        await navigator.clipboard.writeText(link.value)
        copied.value = true
        setTimeout(() => { copied.value = false }, 2000)
    } catch {
        const el = document.createElement('textarea')
        el.value = link.value
        el.style.position = 'fixed'
        el.style.opacity = '0'
        document.body.appendChild(el)
        el.select()
        document.execCommand('copy')
        document.body.removeChild(el)
        copied.value = true
        setTimeout(() => { copied.value = false }, 2000)
    }
}

const displayOrderTime = computed(() => {
    if (orderTime.value <= 0) return ''
    return new Date(orderTime.value * 1000).toLocaleString()
})


</script>

<template>
    <div ref="pageRef" class="pay-qr-window">
        <div v-if="!link" class="text-center pa-8">
            <v-icon size="64" color="medium-emphasis" class="mb-3">mdi-qrcode</v-icon>
            <p class="text-body-1 text-medium-emphasis">{{ t('payQR.noLink') }}</p>
        </div>
        <template v-else>
            <div class="text-center">
                <h2 class="text-h6 mb-1">{{ title }}</h2>
                <p v-if="remaining" class="text-caption mb-2"
                    :class="{ 'text-error': remaining === t('payQR.expired') }">
                    {{ t('payQR.expireIn') }}: {{ remaining }}
                </p>
            </div>

            <!-- QR code -->
            <div class="qr-wrapper">
                <vue-qr :text="link" :size="220" :margin="8" style="border-radius: 8px;" />
            </div>

            <!-- Info -->
            <div class="info-section">
                <div v-if="project" class="info-row">
                    <span class="info-label">{{ t('payQR.project') }}</span>
                    <span class="info-value">{{ project }}</span>
                </div>
                <div v-if="screen" class="info-row">
                    <span class="info-label">{{ t('payQR.screen') }}</span>
                    <span class="info-value">{{ screen }}</span>
                </div>
                <div v-if="sku" class="info-row">
                    <span class="info-label">{{ t('payQR.sku') }}</span>
                    <span class="info-value">{{ sku }}</span>
                </div>
                <div v-if="buyer" class="info-row">
                    <span class="info-label">{{ t('payQR.buyer') }}</span>
                    <span class="info-value">{{ buyer }}</span>
                </div>
                <div v-if="account" class="info-row">
                    <span class="info-label">{{ t('payQR.account') }}</span>
                    <span class="info-value">{{ account }}</span>
                </div>
                <div v-if="displayOrderTime" class="info-row">
                    <span class="info-label">{{ t('payQR.orderTime') }}</span>
                    <span class="info-value">{{ displayOrderTime }}</span>
                </div>
            </div>

            <!-- Copy link button -->
            <div v-if="!expired" class="text-center mt-4">
                <v-btn :prepend-icon="copied ? 'mdi-check' : 'mdi-content-copy'" variant="tonal" size="small"
                    :color="copied ? 'success' : undefined" @click="copyLink">
                    {{ copied ? t('payQR.copied') : t('payQR.copyLink') }}
                </v-btn>
            </div>
        </template>
    </div>
</template>

<style scoped>
.pay-qr-window {
    padding: 20px 24px;
    box-sizing: border-box;
    overflow: hidden;
}

.qr-wrapper {
    display: flex;
    justify-content: center;
    margin: 16px 0;
}



.info-section {
    background: rgba(var(--v-theme-surface-variant), 0.3);
    border-radius: 8px;
    padding: 12px 16px;
}

.info-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 4px 0;
}

.info-row+.info-row {
    border-top: 1px solid rgba(var(--v-theme-surface-variant), 0.3);
}

.info-label {
    font-size: 0.75rem;
    color: rgba(var(--v-theme-on-surface), 0.6);
}

.info-value {
    font-size: 0.8rem;
    font-weight: 500;
    text-align: right;
    max-width: 60%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}
</style>

<style>
/* Ensure body fills the window and doesn't scroll during auto-resize */
html,
body,
#app {
    height: 100% !important;
    min-height: 100% !important;
    margin: 0 !important;
    padding: 0 !important;
    overflow: hidden !important;
}
</style>
