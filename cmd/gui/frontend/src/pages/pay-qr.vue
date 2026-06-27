<script lang="ts" setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'

const route = useRoute()
const { t } = useI18n()

const link = ref('')
const title = ref('')
const project = ref('')
const screen = ref('')
const sku = ref('')
const buyer = ref('')
const expire = ref(0)
const orderTime = ref(0)

onMounted(() => {
    link.value = (route.query.link as string) || ''
    title.value = (route.query.title as string) || t('payQR.defaultTitle')
    project.value = (route.query.project as string) || ''
    screen.value = (route.query.screen as string) || ''
    sku.value = (route.query.sku as string) || ''
    buyer.value = (route.query.buyer as string) || ''
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

function copyLink() {
    navigator.clipboard.writeText(link.value)
}

const displayOrderTime = computed(() => {
    if (orderTime.value <= 0) return ''
    return new Date(orderTime.value * 1000).toLocaleString()
})
</script>

<template>
    <div class="pay-qr-window">
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
                <img :src="link" alt="QR Code" class="qr-image" />
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
                <div v-if="displayOrderTime" class="info-row">
                    <span class="info-label">{{ t('payQR.orderTime') }}</span>
                    <span class="info-value">{{ displayOrderTime }}</span>
                </div>
            </div>

            <!-- Copy link button -->
            <div class="text-center mt-4">
                <v-btn prepend-icon="mdi-content-copy" variant="tonal" size="small" @click="copyLink">
                    {{ t('payQR.copyLink') }}
                </v-btn>
            </div>
        </template>
    </div>
</template>

<style scoped>
.pay-qr-window {
    padding: 20px 24px;
    min-width: 320px;
    max-width: 400px;
    margin: 0 auto;
}

.qr-wrapper {
    display: flex;
    justify-content: center;
    margin: 16px 0;
}

.qr-image {
    width: 220px;
    height: 220px;
    border-radius: 8px;
    border: 2px solid rgba(var(--v-theme-surface-variant), 0.5);
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
