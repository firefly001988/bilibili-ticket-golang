<script lang="ts" setup>
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { clusterCall, type GenerateRemoteWorkerConfigResponse } from '@/composables/clusterTypes'
import { useMessagesStore } from '@/stores/snackbar'

const { t } = useI18n()
const messages = useMessagesStore()

const workerId = ref('')
const listen = ref('0.0.0.0:18080')
const hosts = ref('')
const loading = ref(false)
const result = ref<GenerateRemoteWorkerConfigResponse | null>(null)
const copied = ref(false)

async function generate() {
  if (!workerId.value.trim()) {
    messages.add({ text: t('workerConfig.workerIdRequired'), color: 'warning', timeout: 2000 })
    return
  }
  loading.value = true
  result.value = null
  try {
    const resp = await clusterCall<GenerateRemoteWorkerConfigResponse>(
      'GenerateRemoteWorkerConfig',
      workerId.value.trim(),
      listen.value.trim() || '0.0.0.0:18080',
      hosts.value.trim()
    )
    result.value = resp
    messages.add({ text: t('workerConfig.result'), color: 'success', timeout: 3000 })
  } catch (e: any) {
    messages.add({ text: String(e), color: 'error', timeout: 5000 })
  } finally {
    loading.value = false
  }
}

async function copyConfig() {
  if (!result.value?.encodedConfig) return
  try {
    await navigator.clipboard.writeText(result.value.encodedConfig)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch {
    // Fallback: select and copy
    const el = document.getElementById('encoded-config')
    if (el) {
      const range = document.createRange()
      range.selectNodeContents(el)
      const sel = window.getSelection()
      sel?.removeAllRanges()
      sel?.addRange(range)
      document.execCommand('copy')
      copied.value = true
      setTimeout(() => { copied.value = false }, 2000)
    }
  }
}
</script>

<template>
  <div>
    <h2 class="text-h5 mb-4">{{ t('workerConfig.title') }}</h2>

    <v-card class="mb-4" :loading="loading">
      <v-card-text>
        <v-text-field
          v-model="workerId"
          :label="t('workerConfig.workerId')"
          :hint="t('workerConfig.workerIdHint')"
          persistent-hint
          variant="outlined"
          density="compact"
          class="mb-3"
          :disabled="loading"
        />

        <v-text-field
          v-model="listen"
          :label="t('workerConfig.listen')"
          :hint="t('workerConfig.listenHint')"
          persistent-hint
          variant="outlined"
          density="compact"
          class="mb-3"
          :disabled="loading"
        />

        <v-text-field
          v-model="hosts"
          :label="t('workerConfig.hosts')"
          :hint="t('workerConfig.hostsHint')"
          persistent-hint
          variant="outlined"
          density="compact"
          :disabled="loading"
        />
      </v-card-text>

      <v-card-actions>
        <v-btn
          color="primary"
          variant="tonal"
          :loading="loading"
          :disabled="!workerId.trim()"
          @click="generate"
        >
          {{ loading ? t('workerConfig.generating') : t('workerConfig.generate') }}
        </v-btn>
      </v-card-actions>
    </v-card>

    <v-card v-if="result" variant="outlined" class="mb-4">
      <v-card-text>
        <div class="text-subtitle-2 mb-2">
          {{ t('workerConfig.usage') }}
        </div>
        <v-alert density="compact" type="info" class="mb-2">
          <code>{{ t('workerConfig.usageCommand') }}</code>
        </v-alert>

        <div
          id="encoded-config"
          class="text-caption py-3 px-4 rounded-lg bg-surface-variant"
          style="word-break: break-all; max-height: 300px; overflow-y: auto; font-family: monospace;"
        >
          {{ result.encodedConfig }}
        </div>
      </v-card-text>

      <v-card-actions>
        <v-btn
          prepend-icon="mdi-content-copy"
          variant="tonal"
          size="small"
          :color="copied ? 'success' : undefined"
          @click="copyConfig"
        >
          {{ copied ? t('workerConfig.copied') : t('workerConfig.copy') }}
        </v-btn>
      </v-card-actions>
    </v-card>

    <v-alert v-if="result" density="compact" type="success" variant="tonal" class="mt-2">
      {{ t('workerConfig.notice') }}
    </v-alert>

    <v-alert v-if="!result && !loading" density="compact" type="info" variant="tonal" class="mt-4">
      <p class="mb-1">
        {{ t('workerConfig.usage') }}
      </p>
      <code class="text-caption">ticket-worker import "&lt;{{ t('workerConfig.copy').toLowerCase() }} string&gt;"</code>
    </v-alert>
  </div>
</template>
