<script lang="ts" setup>
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { IsVerified, Verify } from '../../wailsjs/go/main/App'

const { t } = useI18n()

const emit = defineEmits<{
    (e: 'verified'): void
}>()

const show = ref(true)
const input = ref('')
const error = ref(false)
const checking = ref(true)

onMounted(async () => {
    try {
        const ok = await IsVerified()
        if (ok) {
            show.value = false
            emit('verified')
        }
    } catch {
        // If the call fails, stay on the overlay
    }
    checking.value = false
})

async function submit() {
    if (input.value.trim() !== '黄牛死全家') {
        error.value = true
        return
    }
    try {
        const ok = await Verify(input.value.trim())
        if (ok) {
            show.value = false
            emit('verified')
        } else {
            error.value = true
        }
    } catch {
        error.value = true
    }
}
</script>

<template>
    <v-overlay v-model="show" class="align-center justify-center" persistent :opacity="0.95">
        <v-card v-if="!checking" width="480" class="pa-6 rounded-lg" elevation="8">
            <v-card-title class="text-h5 text-center text-wrap">
                {{ t('verified.title') }}
            </v-card-title>

            <v-card-text class="text-center">
                <p class="text-body-1 mb-2">
                    {{ t('verified.description') }}
                </p>
                <p class="text-body-2 text-medium-emphasis mb-4">
                    Github Repo: <br />
                    <code>firefly001988/bilibili-ticket-golang</code>
                </p>

                <v-divider class="mb-4" />

                <p class="text-body-2 text-medium-emphasis mb-2">
                    {{ t('verified.instruction') }}
                </p>

                <v-text-field v-model="input" variant="outlined" density="compact" autofocus :error="error"
                    :error-messages="error ? t('verified.error') : ''" @keydown.enter="submit" @input="error = false" />

                <v-btn block color="primary" class="mt-2" @click="submit">
                    {{ t('verified.confirm') }}
                </v-btn>
            </v-card-text>
        </v-card>

        <v-card v-else width="320" class="pa-6 rounded-lg text-center" elevation="8">
            <v-progress-circular indeterminate color="primary" class="mb-3" />
            <p class="text-body-2 text-medium-emphasis">{{ t('verified.checking') }}</p>
        </v-card>
    </v-overlay>
</template>
