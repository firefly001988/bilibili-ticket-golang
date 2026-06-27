<script lang="ts" setup>
import { ref, onMounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import {
    Snapshot,
    DeleteWorker,
    DisconnectWorker,
    ReconnectWorker,
    AddWorkerFromEncodedConfig,
    UpdateWorker,
    StartLocalWorker,
    StopLocalWorker,
    AddLocalWorker,
    GenerateRemoteWorkerConfig,
} from '../../../bindings/bilibili-ticket-golang/cmd/gui/clusterservice'

const { t } = useI18n()
const messages = useMessagesStore()

// ── Types ─────────────────────────────────────────────────────
interface WorkerSummary {
    id: string
    name: string
    address: string
    type: string
    role: string
    enabled: boolean
    healthy: boolean
    activeAttemptId?: string
    version?: string
}

// ── State ─────────────────────────────────────────────────────
const workers = ref<WorkerSummary[]>([])
const loading = ref(true)

// Import dialog
const showImportDialog = ref(false)
const importEncodedConfig = ref('')
const importOverrideAddress = ref('')
const importing = ref(false)

// Edit dialog
const showEditDialog = ref(false)
const editTarget = ref<WorkerSummary | null>(null)
const editAddress = ref('')
const editRole = ref('')
const saving = ref(false)

// Delete dialog
const showDeleteDialog = ref(false)
const deleteTarget = ref<WorkerSummary | null>(null)
const deleting = ref(false)

// Add local worker dialog
const showAddLocalDialog = ref(false)
const newLocalId = ref('')
const newLocalName = ref('')
const newLocalAddress = ref('127.0.0.1:18080')
const addingLocal = ref(false)

// Generate config dialog (standalone)
const showGenerateConfigDialog = ref(false)
const configId = ref('')
const configListen = ref('0.0.0.0:18080')
const configHosts = ref('')
const configResult = ref('')
const generating = ref(false)

// Quick-add after generating config
const showConfigAddConfirm = ref(false)

// Connecting state
const connecting = ref<Record<string, boolean>>({})

// ── Data loading ──────────────────────────────────────────────
async function load() {
    loading.value = true
    try {
        const snap = await Snapshot()
        workers.value = (snap.workers || []) as WorkerSummary[]
    } catch (e: any) {
        messages.add({ text: t('worker.loadFailed', { error: String(e) }), color: 'error' })
    }
    loading.value = false
}

onMounted(load)

// ── Import ────────────────────────────────────────────────────
async function doImport() {
    if (!importEncodedConfig.value.trim()) {
        messages.add({ text: t('worker.importDocRequired'), color: 'warning' })
        return
    }
    importing.value = true
    try {
        await AddWorkerFromEncodedConfig(importEncodedConfig.value.trim(), importOverrideAddress.value.trim())
        showImportDialog.value = false
        importEncodedConfig.value = ''
        importOverrideAddress.value = ''
        await load()
        messages.add({ text: t('worker.importSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('worker.importFailed', { error: String(e) }), color: 'error' })
    }
    importing.value = false
}

// ── Edit ──────────────────────────────────────────────────────
function openEdit(w: WorkerSummary) {
    editTarget.value = w
    editAddress.value = w.address
    editRole.value = w.role
    showEditDialog.value = true
}

async function saveEdit() {
    if (!editTarget.value || !editAddress.value.trim()) {
        messages.add({ text: t('worker.editAddressRequired'), color: 'warning' }); return
    }
    saving.value = true
    try {
        await UpdateWorker(JSON.stringify({
            id: editTarget.value.id,
            name: editTarget.value.name,
            address: editAddress.value.trim(),
            caCert: '',
            clientCert: '',
            clientKey: '',
            tlsServerName: '',
            role: editRole.value || 'primary',
        }))
        showEditDialog.value = false
        await load()
        messages.add({ text: t('worker.editSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('worker.editFailed', { error: String(e) }), color: 'error' })
    }
    saving.value = false
}

// ── Delete ────────────────────────────────────────────────────
async function confirmDelete() {
    if (!deleteTarget.value) return
    deleting.value = true
    try {
        await DeleteWorker(deleteTarget.value.id)
        showDeleteDialog.value = false
        deleteTarget.value = null
        await load()
        messages.add({ text: t('worker.deleteSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('worker.deleteFailed', { error: String(e) }), color: 'error' })
    }
    deleting.value = false
}

function promptDelete(w: WorkerSummary) {
    deleteTarget.value = w
    showDeleteDialog.value = true
}

// ── Connect / Disconnect ──────────────────────────────────────
async function doDisconnect(w: WorkerSummary) {
    connecting.value[w.id] = true
    try {
        await DisconnectWorker(w.id)
        await load()
        messages.add({ text: t('worker.disconnectSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('worker.disconnectFailed', { error: String(e) }), color: 'error' })
    }
    connecting.value[w.id] = false
}

async function doReconnect(w: WorkerSummary) {
    connecting.value[w.id] = true
    try {
        await ReconnectWorker(w.id)
        await load()
        messages.add({ text: t('worker.reconnectSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('worker.reconnectFailed', { error: String(e) }), color: 'error' })
    }
    connecting.value[w.id] = false
}

// ── Local worker toggle ──────────────────────────────────────
async function toggleLocalWorker(w: WorkerSummary) {
    connecting.value[w.id] = true
    try {
        if (w.healthy) {
            await StopLocalWorker(w.id)
            messages.add({ text: t('worker.localStopped'), color: 'info' })
        } else {
            await StartLocalWorker(w.id)
            messages.add({ text: t('worker.localStarted'), color: 'success' })
        }
        await load()
    } catch (e: any) {
        messages.add({ text: t('worker.localToggleFailed', { error: String(e) }), color: 'error' })
    }
    connecting.value[w.id] = false
}

async function doAddLocalWorker() {
    if (!newLocalAddress.value.trim()) {
        messages.add({ text: t('worker.localAddressRequired'), color: 'warning' }); return
    }
    addingLocal.value = true
    try {
        await AddLocalWorker(newLocalId.value.trim(), newLocalName.value.trim(), newLocalAddress.value.trim())
        showAddLocalDialog.value = false
        newLocalId.value = ''; newLocalName.value = ''; newLocalAddress.value = '127.0.0.1:18080'
        await load()
        messages.add({ text: t('worker.localAddSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('worker.localAddFailed', { error: String(e) }), color: 'error' })
    }
    addingLocal.value = false
}

// ── Generate config ─────────────────────────────────────────
function openGenerateConfig() {
    configId.value = ''
    configListen.value = '0.0.0.0:18080'
    configHosts.value = ''
    configResult.value = ''
    showGenerateConfigDialog.value = true
}

async function doGenerateConfig() {
    if (!configId.value.trim()) {
        messages.add({ text: t('worker.configIdRequired'), color: 'warning' }); return
    }
    generating.value = true
    try {
        const resp = await GenerateRemoteWorkerConfig(
            configId.value.trim(),
            configListen.value.trim() || '0.0.0.0:18080',
            configHosts.value.trim() || configId.value.trim(),
        )
        configResult.value = resp.encodedConfig
    } catch (e: any) {
        messages.add({ text: t('worker.generateConfigFailed', { error: String(e) }), color: 'error' })
    }
    generating.value = false
}

function copyConfig() {
    if (!configResult.value) return
    navigator.clipboard.writeText(configResult.value)
    messages.add({ text: t('worker.configCopied'), color: 'success' })
    showConfigAddConfirm.value = true
}

function confirmAddFromConfig() {
    showConfigAddConfirm.value = false
    showGenerateConfigDialog.value = false
    importEncodedConfig.value = configResult.value
    importOverrideAddress.value = ''
    showImportDialog.value = true
}

// ── Computed ──────────────────────────────────────────────────
const roleColor = (role: string) => role === 'primary' ? 'primary' : 'secondary'

const isLocalWorker = (w: WorkerSummary) => w.type === 'local'
</script>

<template>
    <v-container>
        <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">
            <h1>{{ t('worker.title') }}</h1>
            <v-spacer />
            <v-btn prepend-icon="mdi-import" variant="tonal" @click="showImportDialog = true">
                {{ t('worker.importWorker') }}
            </v-btn>
            <v-btn prepend-icon="mdi-cog-outline" variant="tonal" color="info" class="ml-2" @click="openGenerateConfig">
                {{ t('worker.generateConfig') }}
            </v-btn>
            <v-btn prepend-icon="mdi-plus-circle-outline" variant="tonal" color="primary" class="ml-2"
                @click="showAddLocalDialog = true">
                {{ t('worker.addLocalWorker') }}
            </v-btn>
        </div>

        <v-divider class="mt-2 mb-4" thickness="3" />

        <!-- Loading -->
        <v-row v-if="loading" justify="center" class="mt-6">
            <v-progress-circular indeterminate color="primary" />
        </v-row>

        <!-- Empty state -->
        <v-card v-else-if="workers.length === 0" class="mt-4 pa-6 text-center" variant="outlined">
            <v-card-text class="text-medium-emphasis">
                <v-icon size="48" class="mb-3">mdi-server-network-off</v-icon>
                <p>{{ t('worker.emptyHint') }}</p>
                <v-btn prepend-icon="mdi-import" color="primary" class="mt-3" @click="showImportDialog = true">
                    {{ t('worker.importWorker') }}
                </v-btn>
            </v-card-text>
        </v-card>

        <!-- Worker list -->
        <v-table v-else>
            <thead>
                <tr>
                    <th class="text-no-wrap">{{ t('worker.colName') }}</th>
                    <th class="text-no-wrap">{{ t('worker.colId') }}</th>
                    <th class="text-no-wrap">{{ t('worker.colAddress') }}</th>
                    <th class="text-no-wrap">{{ t('worker.colRole') }}</th>
                    <th class="text-no-wrap" style="width:1%;white-space:nowrap">{{ t('worker.colStatus') }}</th>
                    <th class="text-no-wrap" style="width:1%;white-space:nowrap">{{ t('worker.colActions') }}</th>
                </tr>
            </thead>
            <tbody>
                <tr v-for="w in workers" :key="w.id">
                    <td style="max-width:200px">
                        <div class="d-flex align-center text-truncate" style="min-width:0">
                            <v-icon start size="small" class="mr-1 flex-shrink-0">mdi-server-network</v-icon>
                            <span class="text-truncate font-weight-bold" style="min-width:0">{{ w.name || w.id }}</span>
                            <v-chip v-if="isLocalWorker(w)" size="x-small" color="info" variant="tonal"
                                class="ml-1 flex-shrink-0">
                                {{ t('worker.localLabel') }}
                            </v-chip>
                            <v-chip v-else size="x-small" color="warning" variant="tonal" class="ml-1 flex-shrink-0">
                                {{ t('worker.remoteLabel') }}
                            </v-chip>
                        </div>
                    </td>
                    <td style="max-width:140px">
                        <span class="text-caption text-truncate d-block">{{ w.id }}</span>
                    </td>
                    <td style="max-width:200px">
                        <span class="text-caption font-monospace text-truncate d-block">{{ w.address }}</span>
                    </td>
                    <td>
                        <v-chip :color="roleColor(w.role)" size="small" variant="tonal">
                            {{ w.role }}
                        </v-chip>
                    </td>
                    <td class="text-no-wrap" style="white-space:nowrap">
                        <v-chip :color="w.healthy ? 'success' : 'error'" size="small" variant="tonal">
                            <v-icon start size="x-small">{{ w.healthy ? 'mdi-check-circle' : 'mdi-close-circle'
                            }}</v-icon>
                            {{ w.healthy ? t('worker.online') : t('worker.offline') }}
                        </v-chip>
                        <v-chip v-if="w.activeAttemptId" size="x-small" color="orange" variant="tonal" class="ml-1">
                            {{ t('worker.busy') }}
                        </v-chip>
                        <v-chip v-if="w.version" size="x-small" variant="tonal" class="ml-1">
                            v{{ w.version }}
                        </v-chip>
                    </td>
                    <td class="text-no-wrap" style="white-space:nowrap">
                        <div style="display:flex;gap:4px">
                            <!-- Local workers: toggle on/off -->
                            <template v-if="isLocalWorker(w)">
                                <v-btn v-if="w.healthy" icon="mdi-stop-circle-outline" size="small" variant="text"
                                    color="warning" :loading="connecting[w.id]" :title="t('worker.localStop')"
                                    @click="toggleLocalWorker(w)" />
                                <v-btn v-else icon="mdi-play-circle-outline" size="small" variant="text" color="success"
                                    :loading="connecting[w.id]" :title="t('worker.localStart')"
                                    @click="toggleLocalWorker(w)" />
                                <v-btn icon="mdi-delete" size="small" variant="text" color="error"
                                    @click="promptDelete(w)" />
                            </template>
                            <!-- Remote workers -->
                            <template v-else>
                                <v-btn v-if="w.healthy" icon="mdi-link-off" size="small" variant="text"
                                    :loading="connecting[w.id]" :title="t('worker.disconnect')"
                                    @click="doDisconnect(w)" />
                                <v-btn v-else icon="mdi-link" size="small" variant="text" :loading="connecting[w.id]"
                                    :title="t('worker.reconnect')" @click="doReconnect(w)" />
                                <v-btn icon="mdi-pencil" size="small" variant="text" color="primary"
                                    :title="t('worker.edit')" @click="openEdit(w)" />
                                <v-btn icon="mdi-delete" size="small" variant="text" color="error"
                                    @click="promptDelete(w)" />
                            </template>
                        </div>
                    </td>
                </tr>
            </tbody>
        </v-table>

        <!-- ═══ Import Worker Dialog ═══ -->
        <v-dialog v-model="showImportDialog" max-width="560">
            <v-card class="pa-4">
                <v-card-title>{{ t('worker.importTitle') }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">
                        {{ t('worker.importHint') }}
                    </p>
                    <v-textarea v-model="importEncodedConfig" :label="t('worker.importLabel')"
                        :placeholder="t('worker.importPlaceholder')" variant="outlined" rows="4" max-rows="6"
                        class="font-monospace mb-3" />
                    <v-text-field v-model="importOverrideAddress" :label="t('worker.overrideAddressLabel')"
                        :placeholder="t('worker.overrideAddressPlaceholder')" variant="outlined" density="compact"
                        :hint="t('worker.overrideAddressHint')" persistent-hint />
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showImportDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" :loading="importing" :disabled="!importEncodedConfig.trim()"
                        @click="doImport">
                        {{ t('worker.importBtn') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ═══ Edit Worker Dialog ═══ -->
        <v-dialog v-model="showEditDialog" max-width="460" persistent>
            <v-card class="pa-4">
                <v-card-title>{{ t('worker.editTitle') }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">
                        {{ t('worker.editHint', { name: editTarget?.name || editTarget?.id }) }}
                    </p>
                    <v-text-field v-model="editAddress" :label="t('worker.colAddress')"
                        :placeholder="t('worker.editAddressPlaceholder')" variant="outlined" density="compact"
                        class="mb-3" />
                    <v-select v-model="editRole" :label="t('worker.colRole')" variant="outlined" density="compact"
                        :items="[{ title: 'primary', value: 'primary' }, { title: 'secondary', value: 'secondary' }]" />
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showEditDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" :loading="saving" :disabled="!editAddress.trim()" @click="saveEdit">
                        {{ t('worker.editSave') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ═══ Add Local Worker Dialog ═══ -->
        <v-dialog v-model="showAddLocalDialog" max-width="460" persistent>
            <v-card class="pa-4">
                <v-card-title>{{ t('worker.addLocalTitle') }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">{{ t('worker.addLocalHint') }}</p>
                    <v-text-field v-model="newLocalId" :label="t('worker.colId')"
                        :placeholder="t('worker.localIdPlaceholder')" variant="outlined" density="compact"
                        :hint="t('worker.localIdHint')" persistent-hint class="mb-2" />
                    <v-text-field v-model="newLocalName" :label="t('worker.colName')"
                        :placeholder="t('worker.localNamePlaceholder')" variant="outlined" density="compact"
                        class="mb-2" />
                    <v-text-field v-model="newLocalAddress" :label="t('worker.colAddress')"
                        placeholder="127.0.0.1:18081" variant="outlined" density="compact" />
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showAddLocalDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" :loading="addingLocal" :disabled="!newLocalAddress.trim()"
                        @click="doAddLocalWorker">
                        {{ t('worker.addLocalBtn') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ═══ Delete Confirmation Dialog ═══ -->
        <v-dialog v-model="showDeleteDialog" max-width="420">
            <v-card class="pa-4">
                <v-card-title class="text-error">{{ t('worker.deleteTitle') }}</v-card-title>
                <v-card-text>
                    <p>{{ t('worker.deleteConfirm', { name: deleteTarget?.name || deleteTarget?.id }) }}</p>
                    <p class="text-caption text-medium-emphasis">{{ t('worker.deleteWarning') }}</p>
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

        <!-- ═══ Generate Config Dialog ═══ -->
        <v-dialog v-model="showGenerateConfigDialog" max-width="560" persistent>
            <v-card class="pa-4">
                <v-card-title>{{ t('worker.generateConfigTitle') }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">
                        {{ t('worker.generateConfigHint') }}
                    </p>
                    <v-text-field v-model="configId" :label="t('worker.configId')"
                        :placeholder="t('worker.configIdHint')" :hint="t('worker.configIdHint')" persistent-hint
                        variant="outlined" density="compact" class="mb-2" />
                    <v-text-field v-model="configListen" :label="t('worker.configListen')" variant="outlined"
                        density="compact" class="mb-2" />
                    <v-text-field v-model="configHosts" :label="t('worker.configHosts')"
                        :hint="t('worker.configHostsHint')" persistent-hint variant="outlined" density="compact"
                        class="mb-3" />
                    <template v-if="configResult">
                        <p class="text-caption text-medium-emphasis mb-1">{{ t('worker.configResult') }}</p>
                        <v-textarea :model-value="configResult" readonly variant="outlined" rows="3" hide-details
                            class="font-monospace text-caption mb-2" density="compact" />
                        <v-btn prepend-icon="mdi-content-copy" variant="tonal" color="primary" style="width:100%"
                            @click="copyConfig">{{ t('worker.configCopyBtn') }}</v-btn>
                    </template>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showGenerateConfigDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="info" :loading="generating" @click="doGenerateConfig">
                        {{ t('worker.generateConfig') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

        <!-- ═══ Config add confirmation dialog ═══ -->
        <v-dialog v-model="showConfigAddConfirm" max-width="420">
            <v-card class="pa-4">
                <v-card-title>{{ t('worker.configAddConfirmTitle') }}</v-card-title>
                <v-card-text>
                    <p>{{ t('worker.configAddConfirmHint') }}</p>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showConfigAddConfirm = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="primary" @click="confirmAddFromConfig">
                        {{ t('worker.configAddConfirmBtn') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </v-container>
</template>
