<script lang="ts" setup>
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Window } from '@wailsio/runtime'
import VueQr from 'vue-qr'

const { t } = useI18n()
const route = useRoute()

const copied = ref(false)
const pageRef = ref<HTMLElement | null>(null)

const payLink = computed(() => (route.query.link as string) || '')
const projectTitle = computed(() => (route.query.title as string) || t('payQR.defaultTitle'))
const projectName = computed(() => (route.query.project as string) || '')
const screenName = computed(() => (route.query.screen as string) || '')
const skuName = computed(() => (route.query.sku as string) || '')
const buyerName = computed(() => (route.query.buyer as string) || '')
const expireAt = computed(() => {
  const v = Number(route.query.expire)
  return v > 0 ? v * 1000 : 0
})
const orderTime = computed(() => {
  const v = Number(route.query.orderTime)
  return v > 0 ? new Date(v * 1000).toLocaleString() : ''
})

const countdownMs = ref(0)
const countdownText = computed(() => {
  if (countdownMs.value <= 0) return t('payQR.expired')
  const m = Math.floor(countdownMs.value / 60000)
  const s = Math.floor((countdownMs.value % 60000) / 1000)
  return `${m}:${String(s).padStart(2, '0')}`
})

let countdownTimer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  if (expireAt.value > 0) {
    const tick = () => {
      countdownMs.value = Math.max(0, expireAt.value - Date.now())
    }
    tick()
    countdownTimer = setInterval(tick, 1000)
  }
  nextTick(() => {
    requestAnimationFrame(() => {
      setTimeout(() => {
        const el = pageRef.value
        if (el) {
          const r = el.getBoundingClientRect()
          Window.SetSize(Math.ceil(r.width) + 16, Math.ceil(r.height) + 16)
        }
      }, 300)
    })
  })
})

onUnmounted(() => {
  if (countdownTimer) clearInterval(countdownTimer)
})

async function copyLink() {
  if (!payLink.value) return
  try {
    await navigator.clipboard.writeText(payLink.value)
  } catch {
    const el = document.createElement('textarea')
    el.value = payLink.value
    el.style.position = 'fixed'
    el.style.opacity = '0'
    document.body.appendChild(el)
    el.select()
    document.execCommand('copy')
    document.body.removeChild(el)
  }
  copied.value = true
  setTimeout(() => { copied.value = false }, 2000)
}
</script>

<template>
  <div ref="pageRef" class="pay-qr-page">
    <h2>{{ projectTitle }}</h2>

    <div v-if="projectName" class="order-detail">
      <div class="order-row">
        <span class="order-label">{{ t('payQR.project') }}</span>
        <span class="order-value">{{ projectName }}</span>
      </div>
      <div class="order-row">
        <span class="order-label">{{ t('payQR.screen') }}</span>
        <span class="order-value">{{ screenName }}</span>
      </div>
      <div class="order-row">
        <span class="order-label">{{ t('payQR.sku') }}</span>
        <span class="order-value">{{ skuName }}</span>
      </div>
      <div v-if="buyerName" class="order-row">
        <span class="order-label">{{ t('payQR.buyer') }}</span>
        <span class="order-value">{{ buyerName }}</span>
      </div>
      <div v-if="orderTime" class="order-row">
        <span class="order-label">{{ t('payQR.orderTime') }}</span>
        <span class="order-value">{{ orderTime }}</span>
      </div>
      <div v-if="expireAt > 0" class="order-row">
        <span class="order-label">{{ t('payQR.expireIn') }}</span>
        <span :class="['order-value', { 'text-warning': countdownMs < 300000 }]">
          {{ countdownText }}
        </span>
      </div>
    </div>

    <div class="qr-container">
      <vue-qr
        v-if="payLink"
        :text="payLink"
        :size="240"
        :margin="8"
        :background-dimming="'rgba(0,0,0,255)'"
        style="border-radius: 8px;"
      />
      <p v-else class="text-error">{{ t('payQR.noLink') }}</p>
    </div>

    <p class="hint">{{ t('payQR.hint') }}</p>

    <v-btn
      v-if="payLink"
      class="mt-2 mb-2"
      variant="outlined"
      size="small"
      :prepend-icon="copied ? 'mdi-check' : 'mdi-content-copy'"
      :color="copied ? 'success' : undefined"
      @click="copyLink"
    >
      {{ copied ? t('payQR.copied') : t('payQR.copyLink') }}
    </v-btn>
  </div>
</template>

<style scoped>
.pay-qr-page {
  background: #1b2636;
  color: #fff;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  padding: 24px;
  user-select: none;
  box-sizing: border-box;
}

h2 {
  font-size: 16px;
  margin-bottom: 16px;
  text-align: center;
  opacity: 0.9;
  font-weight: 500;
}

.order-detail {
  width: 100%;
  max-width: 300px;
  margin-bottom: 16px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.order-row {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  line-height: 1.6;
}

.order-label {
  opacity: 0.5;
  flex-shrink: 0;
  margin-right: 12px;
}

.order-value {
  text-align: right;
  word-break: break-all;
}

.text-warning {
  color: #ffb74d !important;
}

.qr-container {
  background: #fff;
  border-radius: 12px;
  padding: 16px;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.3);
  display: flex;
  align-items: center;
  justify-content: center;
}

.text-error {
  color: #ef5350;
  font-size: 14px;
}

.hint {
  margin-top: 20px;
  font-size: 13px;
  opacity: 0.6;
  text-align: center;
  line-height: 1.6;
}
</style>

<style>
html,
body,
#app {
  height: 100% !important;
  min-height: 100% !important;
  margin: 0 !important;
  padding: 0 !important;
  background: #1b2636 !important;
  overflow: hidden !important;
}
</style>
