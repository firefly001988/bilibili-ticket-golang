<script lang="ts" setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import {
    Snapshot,
    BeginAccountLogin,
    PollAccountLogin,
    DeleteAccount,
    ImportAccount,
} from '../../../bindings/bilibili-ticket-golang/cmd/gui/clusterservice'

const { t } = useI18n()
const messages = useMessagesStore()

// ── Types (mirror Go types from ClusterSnapshot) ──────────────
interface AccountSummary {
    id: string
    name: string
    enabled: boolean
    vipStatus: number
    cooldownUntil?: string
    credentialVersion: number
}

// ── State ─────────────────────────────────────────────────────
const accounts = ref<AccountSummary[]>([])
const loading = ref(true)

// QR login dialog
const showLoginDialog = ref(false)
const loginQR = ref('')
const loginSessionID = ref('')
const loginStatusMsg = ref('')
const loginQRExpiry = ref(0)
const loginPolling = ref(false)
let loginTimer: ReturnType<typeof setInterval> | null = null

// Import dialog
const showImportDialog = ref(false)
const importDocument = ref('')

// Delete dialog
const showDeleteDialog = ref(false)
const deleteTarget = ref<AccountSummary | null>(null)
const deleting = ref(false)

// ── Data loading ──────────────────────────────────────────────
async function load() {
    loading.value = true
    try {
        const snap = await Snapshot()
        accounts.value = (snap.accounts || []) as AccountSummary[]
    } catch (e: any) {
        messages.add({ text: t('account.loadFailed', { error: String(e) }), color: 'error' })
    }
    loading.value = false
}

onMounted(load)

onUnmounted(() => {
    if (loginTimer) clearInterval(loginTimer)
})

// ── QR Login ──────────────────────────────────────────────────
async function startLogin() {
    try {
        const result = await BeginAccountLogin('')
        loginQR.value = result.url
        loginSessionID.value = result.sessionId
        loginStatusMsg.value = ''
        loginQRExpiry.value = Date.now() + 180000
        loginPolling.value = true
        startLoginPolling()
    } catch (e: any) {
        messages.add({ text: t('account.loginStartFailed', { error: String(e) }), color: 'error' })
    }
}

function startLoginPolling() {
    if (loginTimer) clearInterval(loginTimer)
    loginTimer = setInterval(async () => {
        qrExpirySeconds.value = Math.max(0, Math.floor((loginQRExpiry.value - Date.now()) / 1000))
        if (Date.now() > loginQRExpiry.value) {
            stopLoginPolling()
            loginStatusMsg.value = t('account.qrExpired')
            loginQR.value = ''
            return
        }
        try {
            const result = await PollAccountLogin(loginSessionID.value)
            if (result.code === 0) {
                stopLoginPolling()
                loginStatusMsg.value = t('account.loginSuccess')
                closeLoginDialog()
                await load()
                messages.add({ text: t('account.loginSuccess'), color: 'success' })
            } else if (result.code === 86038) {
                stopLoginPolling()
                loginStatusMsg.value = t('account.qrExpired')
                loginQR.value = ''
            } else if (result.message) {
                loginStatusMsg.value = result.message
            }
        } catch (e: any) {
            stopLoginPolling()
            messages.add({ text: t('account.loginPollFailed', { error: String(e) }), color: 'error' })
        }
    }, 1000)
}

function stopLoginPolling() {
    loginPolling.value = false
    if (loginTimer) { clearInterval(loginTimer); loginTimer = null }
}

function cancelLogin() {
    stopLoginPolling()
    loginQR.value = ''
    loginSessionID.value = ''
    loginStatusMsg.value = ''
}

function closeLoginDialog() {
    stopLoginPolling()
    showLoginDialog.value = false
    loginQR.value = ''
    loginSessionID.value = ''
    loginStatusMsg.value = ''
}

// ── Import ────────────────────────────────────────────────────
async function doImport() {
    if (!importDocument.value.trim()) {
        messages.add({ text: t('account.importDocRequired'), color: 'warning' })
        return
    }
    try {
        await ImportAccount(importDocument.value.trim())
        showImportDialog.value = false
        importDocument.value = ''
        await load()
        messages.add({ text: t('account.importSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('account.importFailed', { error: String(e) }), color: 'error' })
    }
}

// ── Delete ────────────────────────────────────────────────────
async function confirmDelete() {
    if (!deleteTarget.value) return
    deleting.value = true
    try {
        await DeleteAccount(deleteTarget.value.id)
        showDeleteDialog.value = false
        deleteTarget.value = null
        await load()
        messages.add({ text: t('account.deleteSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('account.deleteFailed', { error: String(e) }), color: 'error' })
    }
    deleting.value = false
}

function promptDelete(account: AccountSummary) {
    deleteTarget.value = account
    showDeleteDialog.value = true
}

// ── Computed ──────────────────────────────────────────────────
const qrExpirySeconds = ref(0)

const loginQRCodeUrl = computed(() => {
    if (!loginQR.value) return ''
    return `https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(loginQR.value)}`
})
</script>

<template>
    <v-container>
        <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">
            <h1>{{ t('account.title') }}</h1>
            <v-spacer />
            <v-btn prepend-icon="mdi-import" variant="tonal" @click="showImportDialog = true">
                {{ t('account.importAccount') }}
            </v-btn>
            <v-btn prepend-icon="mdi-plus" color="primary" @click="showLoginDialog = true">
                {{ t('account.addAccount') }}
            </v-btn>
        </div>

        <v-divider class="mt-2 mb-4" thickness="3" />

        <!-- Loading -->
        <v-row v-if="loading" justify="center" class="mt-6">
            <v-progress-circular indeterminate color="primary" />
        </v-row>

        <!-- Empty state -->
        <v-card v-else-if="accounts.length === 0" class="mt-4 pa-6 text-center" variant="outlined">
            <v-card-text class="text-medium-emphasis">
                <v-icon size="48" class="mb-3">mdi-account-multiple-plus</v-icon>
                <p>{{ t('account.emptyHint') }}</p>
                <v-btn prepend-icon="mdi-plus" color="primary" class="mt-3" @click="showLoginDialog = true">
                    {{ t('account.addAccount') }}
                </v-btn>
            </v-card-text>
        </v-card>

        <!-- Account list -->
        <v-table v-else>
            <thead>
                <tr>
                    <th>{{ t('account.colName') }}</th>
                    <th>{{ t('account.colId') }}</th>
                    <th>{{ t('account.colStatus') }}</th>
                    <th>{{ t('account.colActions') }}</th>
                </tr>
            </thead>
            <tbody>
                <tr v-for="acc in accounts" :key="acc.id">
                    <td>
                        <v-icon start size="small" class="mr-1">mdi-account</v-icon>
                        {{ acc.name || t('account.unnamed') }}
                    </td>
                    <td class="text-caption">{{ acc.id }}</td>
                    <td>
                        <v-chip :color="acc.enabled ? 'success' : 'grey'" size="small" variant="tonal">
                            {{ acc.enabled ? t('account.enabled') : t('account.disabled') }}
                        </v-chip>
                        <v-chip v-if="acc.vipStatus === 1" color="pink" size="small" variant="tonal" class="ml-1"
                            prepend-icon="mdi-crown">
                            {{ t('account.vip') }}
                        </v-chip>
                        <v-chip v-if="acc.cooldownUntil" color="warning" size="small" variant="tonal" class="ml-1">
                            {{ t('account.cooldown') }}
                        </v-chip>
                    </td>
                    <td>
                        <div style="display:flex;gap:4px">
                            <v-btn icon="mdi-delete" size="small" variant="text" color="error"
                                @click="promptDelete(acc)" />
                        </div>
                    </td>
                </tr>
            </tbody>
        </v-table>

        <!-- ═══ Add Account (QR Login) Dialog ═══ -->
        <v-dialog v-model="showLoginDialog" max-width="480" persistent>
            <v-card class="pa-4">
                <v-card-title>{{ t('account.addAccountTitle') }}</v-card-title>
                <v-card-text>
                    <!-- QR code display -->
                    <div v-if="loginQR" class="text-center mt-4">
                        <img :src="loginQRCodeUrl" alt="QR Login" style="max-width:200px" class="elevation-2" />
                        <p class="text-caption text-medium-emphasis mt-2">
                            {{ t('account.qrExpiresIn') }}
                            <strong>{{ qrExpirySeconds }}s</strong>
                        </p>
                        <v-chip v-if="loginStatusMsg" color="warning" size="small" class="mt-1">
                            {{ loginStatusMsg }}
                        </v-chip>
                    </div>

                    <div v-else class="text-center py-6">
                        <v-icon size="48" class="mb-2">mdi-qrcode-scan</v-icon>
                        <p class="text-body-2 text-medium-emphasis">{{ t('account.qrHint') }}</p>
                    </div>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn v-if="!loginPolling" variant="text" @click="closeLoginDialog">
                        {{ t('common.cancel') }}
                    </v-btn>
                    <v-btn v-if="!loginPolling" color="primary" @click="startLogin">
                        {{ t('account.generateQR') }}
                    </v-btn>
                    <v-btn v-else variant="tonal" color="warning" @click="cancelLogin">
                        {{ t('account.cancelLogin') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ═══ Import Account Dialog ═══ -->
        <v-dialog v-model="showImportDialog" max-width="520">
            <v-card class="pa-4">
                <v-card-title>{{ t('account.importTitle') }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">
                        {{ t('account.importHint') }}
                    </p>
                    <v-textarea v-model="importDocument" :label="t('account.importLabel')"
                        :placeholder="t('account.importPlaceholder')" variant="outlined" rows="5" auto-grow
                        class="font-monospace" />
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showImportDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" :disabled="!importDocument.trim()" @click="doImport">
                        {{ t('account.importBtn') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ═══ Delete Confirmation Dialog ═══ -->
        <v-dialog v-model="showDeleteDialog" max-width="420">
            <v-card class="pa-4">
                <v-card-title class="text-error">{{ t('account.deleteTitle') }}</v-card-title>
                <v-card-text>
                    <p>{{ t('account.deleteConfirm', { name: deleteTarget?.name || deleteTarget?.id }) }}</p>
                    <p class="text-caption text-medium-emphasis">{{ t('account.deleteWarning') }}</p>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showDeleteDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="error" :loading="deleting" @click="confirmDelete">
                        {{ t('common.delete') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </v-container>
</template>
