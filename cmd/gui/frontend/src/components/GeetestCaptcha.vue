<script lang="ts" setup>
import { ref, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'

const props = defineProps<{
    gt: string
    challenge: string
    modelValue: boolean
}>()

const emit = defineEmits<{
    (e: 'update:modelValue', value: boolean): void
    (e: 'solved', result: { validate: string; seccode: string }): void
}>()

const dialog = ref(props.modelValue)
const loading = ref(false)
const error = ref('')
const solved = ref(false)
const validateVal = ref('')
const seccodeVal = ref('')
const captchaContainer = ref<HTMLElement | null>(null)

let captchaObj: any = null
let scriptLoaded = false

watch(() => props.modelValue, (val) => {
    dialog.value = val
    if (val) {
        resetCaptchaState()
    } else {
        destroyCaptcha()
    }
})

onMounted(() => {
    if (props.modelValue) {
        resetCaptchaState()
    }
})

function close() {
    dialog.value = false
    emit('update:modelValue', false)
}

onBeforeUnmount(() => {
    destroyCaptcha()
})

function destroyCaptcha() {
    if (captchaObj) {
        try { captchaObj.destroy?.() } catch { /* ignore */ }
        captchaObj = null
    }
    if (captchaContainer.value) captchaContainer.value.innerHTML = ''
}

function resetCaptchaState() {
    solved.value = false
    validateVal.value = ''
    seccodeVal.value = ''
    error.value = ''
    nextTick(() => initCaptcha())
}

function loadScript(url: string): Promise<void> {
    return new Promise((resolve, reject) => {
        const existing = document.querySelector(`script[src="${url}"]`)
        if (existing) { resolve(); return }
        const script = document.createElement('script')
        script.src = url
        script.onload = () => resolve()
        script.onerror = () => reject(new Error(`Failed to load ${url}`))
        document.head.appendChild(script)
    })
}

async function initCaptcha() {
    if (!props.gt || !props.challenge) {
        error.value = '缺少 gt 或 challenge 参数'
        return
    }
    loading.value = true
    error.value = ''
    try {
        // Load gt.js if not already loaded
        if (!scriptLoaded) {
            await loadScript('/gt.js')
            scriptLoaded = true
        }

        const win = window as any
        if (typeof win.initGeetest !== 'function') {
            throw new Error('initGeetest 未定义，gt.js 加载失败')
        }

        destroyCaptcha()

        // Wait for the container to be available
        await nextTick()
        if (!captchaContainer.value) {
            throw new Error('验证码容器未渲染')
        }

        win.initGeetest({
            gt: props.gt,
            challenge: props.challenge,
            offline: false,
            new_captcha: true,
            product: 'popup',
            width: '300px',
            https: true,
        }, (obj: any) => {
            captchaObj = obj
            captchaObj.appendTo(captchaContainer.value)
            captchaObj.onReady(() => {
                loading.value = false
            })
            captchaObj.onError((err: any) => {
                error.value = '验证码加载失败: ' + JSON.stringify(err)
                loading.value = false
            })
            captchaObj.onSuccess(() => {
                submitCaptchaResult()
            })
        })
    } catch (e: any) {
        error.value = String(e)
        loading.value = false
    }
}

function confirmResult() {
    submitCaptchaResult()
}

function submitCaptchaResult() {
    if (solved.value) return
    if (!captchaObj) return
    const result = captchaObj.getValidate()
    if (!result) return
    validateVal.value = result.geetest_validate || ''
    seccodeVal.value = result.geetest_seccode || ''
    if (!validateVal.value) {
        error.value = '请先完成验证'
        return
    }
    solved.value = true
    emit('solved', {
        validate: validateVal.value,
        seccode: seccodeVal.value,
    })
    close()
}
</script>

<template>
    <v-dialog v-model="dialog" max-width="360" persistent>
        <v-card class="pa-4">
            <v-card-title>极验验证码</v-card-title>
            <v-card-text>
                <div v-if="loading" class="text-center py-6">
                    <v-progress-circular indeterminate color="primary" />
                    <p class="text-caption text-medium-emphasis mt-2">加载验证码中...</p>
                </div>

                <div v-else-if="error" class="text-center py-4">
                    <v-icon size="40" color="error">mdi-alert-circle</v-icon>
                    <p class="text-body-2 text-error mt-2">{{ error }}</p>
                </div>

                <template v-if="!loading && !error">
                    <p class="text-caption text-medium-emphasis mb-3">
                        请完成下方验证，完成后点击"确认"
                    </p>
                </template>
                <div ref="captchaContainer" class="geetest-captcha-container d-flex justify-center"
                    :class="{ 'd-none': error }"></div>
            </v-card-text>

            <v-card-actions>
                <v-spacer />
                <v-btn variant="text" @click="close">取消</v-btn>
                <v-btn v-if="!loading && !error" color="primary" @click="confirmResult">
                    确认
                </v-btn>
            </v-card-actions>
        </v-card>
    </v-dialog>
</template>

<style scoped>
.geetest-captcha-container {
    min-height: 160px;
}
</style>
