<script lang="ts" setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import {
    Snapshot,
    DeleteWorker,
    DisconnectWorker,
    ReconnectWorker,
    ForceReconnectWorker,
    AddWorkerFromEncodedConfig,
    ForceAddWorkerFromEncodedConfig,
    UpdateWorker,
    StartLocalWorker,
    StopLocalWorker,
    AddLocalWorker,
    GenerateRemoteWorkerConfig,
    SelectWorkerBinary,
    StartBatchDeployRemoteWorkers,
    GetRemoteWorkerDeployJob,
    CancelRemoteWorkerDeployJob,
} from '../../../bindings/bilibili-ticket-golang/cmd/gui/cluster_service/clusterservice'

const { t } = useI18n()
const messages = useMessagesStore()

// ── Types ─────────────────────────────────────────────────────
interface WorkerCooldownInfo {
    cooledDown: boolean
    cooldownEnd?: string
    startedAt?: string
    reason?: string
    remainingMs: number
    totalDurationMs: number
}

interface WorkerSummary {
    id: string
    name: string
    address: string
    type: string
    enabled: boolean
    healthy: boolean
    versionBlocked?: boolean
    activeAttemptId?: string
    version?: string
    skipVersionCheck?: boolean
    bilibiliOffsetMs: number
    ntpOffsetMs: number
    cooldown?: WorkerCooldownInfo
    lastHeartbeatAt?: string
    lastHeartbeatLatencyMs: number
}

interface SnapshotExt {
    workers: WorkerSummary[]
    employerVersion: string
}

interface DeployTarget {
    host: string
    sshPort: number
    username: string
    password: string
    workerPort: number
    name: string
    workerId: string
}

interface DeployItemStatus {
    index: number
    host: string
    sshPort: number
    workerId?: string
    name?: string
    address?: string
    stage: string
    status: string
    message?: string
    logs?: string[]
}

interface DeployJob {
    id: string
    status: string
    message?: string
    items: DeployItemStatus[]
}

// ── State ─────────────────────────────────────────────────────
const workers = ref<WorkerSummary[]>([])
const loading = ref(true)
const employerVersion = ref('')

// Import dialog
const showImportDialog = ref(false)
const importEncodedConfig = ref('')
const importOverrideAddress = ref('')
const importing = ref(false)

// Edit dialog
const showEditDialog = ref(false)
const editTarget = ref<WorkerSummary | null>(null)
const editAddress = ref('')
const saving = ref(false)

// Delete dialog
const showDeleteDialog = ref(false)
const deleteTarget = ref<WorkerSummary | null>(null)
const deleting = ref(false)

// Add local worker dialog
const showAddLocalDialog = ref(false)
const newLocalId = ref('')
const newLocalName = ref('')
const newLocalAddress = ref('127.0.0.1:37900')
const addingLocal = ref(false)

// Generate config dialog (standalone)
const showGenerateConfigDialog = ref(false)
const configId = ref('')
const configListen = ref('0.0.0.0:37900')
const configHosts = ref('')
const configResult = ref('')
const generating = ref(false)

// Quick-add after generating config
const showConfigAddConfirm = ref(false)

// Version mismatch warning dialog
const showVersionMismatchDialog = ref(false)
const versionMismatchError = ref('')
const versionMismatchEncoded = ref('')
const versionMismatchAddress = ref('')
const forceImporting = ref(false)

// Connecting state
const connecting = ref<Record<string, boolean>>({})

// Batch deploy dialog
const showBatchDeployDialog = ref(false)
const deployTargets = ref<DeployTarget[]>([])
const deployPackageType = ref<'binary' | 'targz'>('binary')
const deployBinarySource = ref<'local' | 'url'>('local')
const deployLocalBinaryPath = ref('')
const deployDownloadUrl = ref('')
const deployInstallDir = ref('~/bilibili-ticket-golang')
const deployStartMode = ref('nohup')
const deployOverwriteBinary = ref(true)
const deployRestartExisting = ref(true)
const deploySaveTraffic = ref(false)
const deployConcurrency = ref(3)
const deployJob = ref<DeployJob | null>(null)
const deploying = ref(false)
const deployActiveTargetRows = ref<number[]>([])
const deployPrunedJobIds = new Set<string>()
let deployPollInterval: ReturnType<typeof setInterval> | null = null

// Expandable worker detail rows
const expandedWorkers = ref<Set<string>>(new Set())

function toggleExpand(workerId: string) {
    if (expandedWorkers.value.has(workerId)) {
        expandedWorkers.value.delete(workerId)
    } else {
        expandedWorkers.value.add(workerId)
    }
}

// ── Cooldown countdown timers (per worker, in seconds) ───────────
const cooldownTimers = ref<Record<string, number>>({})
let cooldownInterval: ReturnType<typeof setInterval> | null = null

function updateCooldownTimers() {
    const now = Date.now()
    const updated: Record<string, number> = {}
    for (const w of workers.value) {
        if (w.cooldown?.cooledDown && w.cooldown.cooldownEnd) {
            const end = new Date(w.cooldown.cooldownEnd).getTime()
            const remaining = Math.max(0, Math.floor((end - now) / 1000))
            if (remaining > 0) {
                updated[w.id] = remaining
            }
        }
    }
    cooldownTimers.value = updated
}

// ── Data loading ──────────────────────────────────────────────
async function load() {
    loading.value = true
    try {
        const snap = await Snapshot() as SnapshotExt
        workers.value = snap.workers || []
        employerVersion.value = snap.employerVersion || ''
        updateCooldownTimers()
    } catch (e: any) {
        messages.add({ text: t('worker.loadFailed', { error: String(e) }), color: 'error' })
    }
    loading.value = false
}

onMounted(() => {
    load()
    cooldownInterval = setInterval(updateCooldownTimers, 1000)
    pollInterval = setInterval(pollLoad, 5000)
})
onUnmounted(() => {
    if (cooldownInterval) clearInterval(cooldownInterval)
    if (pollInterval) clearInterval(pollInterval)
    if (deployPollInterval) clearInterval(deployPollInterval)
})

// ── Auto-polling (silent background refresh) ──────────────────
let pollInterval: ReturnType<typeof setInterval> | null = null

async function pollLoad() {
    try {
        const snap = await Snapshot() as SnapshotExt
        workers.value = snap.workers || []
        employerVersion.value = snap.employerVersion || ''
        updateCooldownTimers()
    } catch {
        // silent — don't spam snackbar on network errors
    }
}

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
        const errMsg = String(e)
        if (errMsg.includes('protocol version mismatch') || errMsg.includes('version mismatch')) {
            // Show red warning dialog — user can choose to force.
            versionMismatchError.value = errMsg
            versionMismatchEncoded.value = importEncodedConfig.value.trim()
            versionMismatchAddress.value = importOverrideAddress.value.trim()
            showVersionMismatchDialog.value = true
        } else if (errMsg.includes('local') && (errMsg.includes('reserved') || errMsg.includes('import'))) {
            messages.add({ text: t('worker.importLocalRejected'), color: 'error' })
        } else {
            messages.add({ text: t('worker.importFailed', { error: errMsg }), color: 'error' })
        }
    }
    importing.value = false
}

async function doForceImport() {
    showVersionMismatchDialog.value = false
    forceImporting.value = true
    try {
        await ForceAddWorkerFromEncodedConfig(versionMismatchEncoded.value, versionMismatchAddress.value)
        showImportDialog.value = false
        importEncodedConfig.value = ''
        importOverrideAddress.value = ''
        versionMismatchEncoded.value = ''
        versionMismatchAddress.value = ''
        await load()
        messages.add({ text: t('worker.importSuccess'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('worker.forceImportFailed', { error: String(e) }), color: 'error' })
    }
    forceImporting.value = false
}

// ── Edit ──────────────────────────────────────────────────────
function openEdit(w: WorkerSummary) {
    editTarget.value = w
    editAddress.value = w.address
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

// ── Local worker start/stop ─────────────────────────────────
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
        newLocalId.value = ''; newLocalName.value = ''; newLocalAddress.value = '127.0.0.1:37900'
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
    configListen.value = '0.0.0.0:37900'
    configHosts.value = ''
    configResult.value = ''
    showGenerateConfigDialog.value = true
}

async function doGenerateConfig() {
    if (!configId.value.trim()) {
        messages.add({ text: t('worker.configIdRequired'), color: 'warning' }); return
    }
    if (configId.value.trim() === 'local') {
        messages.add({ text: t('worker.configIdReserved'), color: 'error' }); return
    }
    generating.value = true
    try {
        const resp = await GenerateRemoteWorkerConfig(
            configId.value.trim(),
            configListen.value.trim() || '0.0.0.0:37900',
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

const isLocalWorker = (w: WorkerSummary) => w.type === 'local'
const isPrimaryLocal = (w: WorkerSummary) => w.id === 'local'
const isVersionBlocked = (w: WorkerSummary) => w.versionBlocked === true
const isReachable = (w: WorkerSummary) => w.healthy || w.versionBlocked === true

// ── Force Reconnect (version mismatch bypass) ─────────────────
async function doForceReconnect(w: WorkerSummary) {
    connecting.value[w.id] = true
    try {
        await ForceReconnectWorker(w.id)
        await load()
        messages.add({ text: t('worker.forceReconnectSuccess'), color: 'warning' })
    } catch (e: any) {
        messages.add({ text: t('worker.forceReconnectFailed', { error: String(e) }), color: 'error' })
    }
    connecting.value[w.id] = false
}

// ── Batch remote deploy ───────────────────────────────────────
function defaultDeployTarget(): DeployTarget {
    return {
        host: '',
        sshPort: 22,
        username: 'root',
        password: '',
        workerPort: 37900,
        name: '',
        workerId: '',
    }
}

function openBatchDeploy() {
    if (deployTargets.value.length === 0) {
        deployTargets.value = [defaultDeployTarget()]
    }
    deployJob.value = null
    showBatchDeployDialog.value = true
}

function addDeployTarget() {
    deployTargets.value.push(defaultDeployTarget())
}

function removeDeployTarget(index: number) {
    deployTargets.value.splice(index, 1)
    if (deployTargets.value.length === 0) {
        deployTargets.value.push(defaultDeployTarget())
    }
}

async function chooseWorkerBinary() {
    try {
        const path = await SelectWorkerBinary()
        if (path) deployLocalBinaryPath.value = path
    } catch (e: any) {
        messages.add({ text: t('worker.deploySelectBinaryFailed', { error: String(e) }), color: 'error' })
    }
}

function deployStageText(stage: string) {
    return t(`worker.deployStage.${stage}`)
}

function deployStatusColor(status: string) {
    if (status === 'succeeded') return 'success'
    if (status === 'failed') return 'error'
    if (status === 'cancelled') return 'grey'
    if (status === 'running') return 'info'
    if (status === 'partial_failed') return 'warning'
    return 'default'
}

const deployableTargets = computed(() => deployTargets.value
    .map((target, index) => ({ target, index }))
    .filter(entry => entry.target.host.trim()))

const hasDeployableTargets = computed(() => deployableTargets.value.length > 0)
const deployLocalPathLabel = computed(() => deployPackageType.value === 'targz'
    ? t('worker.deployLocalTarGzPath')
    : t('worker.deployLocalBinaryPath'))
const deployDownloadUrlLabel = computed(() => deployPackageType.value === 'targz'
    ? t('worker.deployTarGzDownloadUrl')
    : t('worker.deployDownloadUrl'))
const deployDownloadUrlPlaceholder = computed(() => deployPackageType.value === 'targz'
    ? 'https://example.com/ticket-worker-linux-amd64.tar.gz'
    : 'https://example.com/ticket-worker-linux-amd64')

function heartbeatColor(ms: number) {
    if (ms <= 2000) return 'success'
    if (ms <= 8000) return 'warning'
    return 'error'
}

function offsetColor(ms: number) {
    const value = Math.abs(ms || 0)
    if (value <= 200) return 'success'
    if (value <= 1000) return 'warning'
    return 'error'
}

function signedMs(ms: number) {
    return `${ms > 0 ? '+' : ''}${ms || 0}ms`
}

function validateDeployForm(): boolean {
    const targets = deployableTargets.value
    if (targets.length === 0) {
        messages.add({ text: t('worker.deployNeedTarget'), color: 'warning' })
        return false
    }
    for (const { target } of targets) {
        if (!target.username.trim() || !target.password) {
            messages.add({ text: t('worker.deployNeedSSH', { host: target.host || '-' }), color: 'warning' })
            return false
        }
    }
    if (deployBinarySource.value === 'local' && !deployLocalBinaryPath.value.trim()) {
        messages.add({ text: t('worker.deployNeedLocalPackage'), color: 'warning' })
        return false
    }
    if (deployBinarySource.value === 'url' && !deployDownloadUrl.value.trim()) {
        messages.add({ text: t('worker.deployNeedDownloadUrl'), color: 'warning' })
        return false
    }
    return true
}

async function startBatchDeploy() {
    if (!validateDeployForm()) return
    deploying.value = true
    deployJob.value = null
    try {
        const targets = deployableTargets.value
        deployActiveTargetRows.value = targets.map(entry => entry.index)
        const payload = {
            targets: targets.map(({ target: t }) => ({
                host: t.host.trim(),
                sshPort: Number(t.sshPort) || 22,
                username: t.username.trim(),
                password: t.password,
                workerPort: Number(t.workerPort) || 37900,
                name: t.name.trim(),
                workerId: t.workerId.trim(),
            })),
            packageType: deployPackageType.value,
            binarySource: deployBinarySource.value,
            localBinaryPath: deployLocalBinaryPath.value.trim(),
            downloadUrl: deployDownloadUrl.value.trim(),
            installDir: deployInstallDir.value.trim() || '~/bilibili-ticket-golang',
            startMode: deployStartMode.value || 'nohup',
            overwriteBinary: deployOverwriteBinary.value,
            restartExisting: deployRestartExisting.value,
            saveTraffic: deployPackageType.value === 'binary' && deployBinarySource.value === 'local' && deploySaveTraffic.value,
            concurrency: Number(deployConcurrency.value) || 3,
        }
        const jobID = await StartBatchDeployRemoteWorkers(JSON.stringify(payload))
        await pollDeployJob(jobID)
        if (deployPollInterval) clearInterval(deployPollInterval)
        deployPollInterval = setInterval(() => pollDeployJob(jobID), 1500)
    } catch (e: any) {
        messages.add({ text: t('worker.deployStartFailed', { error: String(e) }), color: 'error' })
        deploying.value = false
    }
}

async function pollDeployJob(jobID?: string) {
    const id = jobID || deployJob.value?.id
    if (!id) return
    try {
        const job = await GetRemoteWorkerDeployJob(id) as DeployJob
        deployJob.value = job
        if (['succeeded', 'failed', 'partial_failed', 'cancelled'].includes(job.status)) {
            pruneSucceededDeployTargets(job)
            if (deployPollInterval) {
                clearInterval(deployPollInterval)
                deployPollInterval = null
            }
            deploying.value = false
            await load()
        }
    } catch (e: any) {
        messages.add({ text: t('worker.deployPollFailed', { error: String(e) }), color: 'error' })
    }
}

function pruneSucceededDeployTargets(job: DeployJob) {
    if (!job.id || deployPrunedJobIds.has(job.id)) return
    deployPrunedJobIds.add(job.id)
    const originalIndexes = job.items
        .filter(item => item.status === 'succeeded')
        .map(item => deployActiveTargetRows.value[item.index])
        .filter((index): index is number => index !== undefined)
        .sort((a, b) => b - a)
    for (const index of originalIndexes) {
        deployTargets.value.splice(index, 1)
    }
    if (deployTargets.value.length === 0) {
        deployTargets.value.push(defaultDeployTarget())
    }
    deployActiveTargetRows.value = []
}

async function cancelBatchDeploy() {
    if (!deployJob.value?.id) return
    try {
        await CancelRemoteWorkerDeployJob(deployJob.value.id)
    } catch (e: any) {
        messages.add({ text: t('worker.deployCancelFailed', { error: String(e) }), color: 'error' })
    }
}
</script>

<template>
    <v-container>
        <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">
            <h1 style="margin: 0;">{{ t('worker.title') }}</h1>
            <v-spacer />
            <div style="display:flex;gap:4px;flex-wrap:wrap">

                <v-btn prepend-icon="mdi-cloud-upload-outline" variant="tonal" color="success" @click="openBatchDeploy">
                    {{ t('worker.batchDeploy') }}
                </v-btn>
                <v-btn prepend-icon="mdi-import" variant="tonal" class="ml-2" @click="showImportDialog = true">
                    {{ t('worker.importWorker') }}
                </v-btn>
                <v-btn prepend-icon="mdi-cog-outline" variant="tonal" color="info" class="ml-2"
                    @click="openGenerateConfig">
                    {{ t('worker.generateConfig') }}
                </v-btn>
                <v-btn prepend-icon="mdi-plus-circle-outline" variant="tonal" color="primary" class="ml-2"
                    @click="showAddLocalDialog = true">
                    {{ t('worker.addLocalWorker') }}
                </v-btn>
                <v-btn prepend-icon="mdi-refresh" variant="tonal" :loading="loading" class="ml-2" @click="load">
                    {{ t('common.refresh') }}
                </v-btn>
            </div>
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
                    <th style="width:28px"></th>
                    <th class="text-no-wrap">{{ t('worker.colName') }}</th>
                    <th class="text-no-wrap">{{ t('worker.colAddress') }}</th>
                    <th class="text-no-wrap" style="width:1%;white-space:nowrap">{{ t('worker.colStatus') }}</th>
                    <th class="text-no-wrap" style="width:1%;white-space:nowrap">{{ t('worker.colActions') }}</th>
                </tr>
            </thead>
            <tbody>
                <template v-for="w in workers" :key="w.id">
                    <tr @click="toggleExpand(w.id)" style="cursor:pointer">
                        <td class="text-center">
                            <v-icon size="small">{{ expandedWorkers.has(w.id) ? 'mdi-chevron-down' : 'mdi-chevron-right'
                            }}</v-icon>
                        </td>
                        <td style="max-width:200px">
                            <div class="d-flex align-center text-truncate" style="min-width:0">
                                <v-icon start size="small" class="mr-1 flex-shrink-0">mdi-server-network</v-icon>
                                <span class="text-truncate font-weight-bold" style="min-width:0">{{ w.name || w.id
                                }}</span>
                                <v-chip v-if="isLocalWorker(w)" size="x-small" color="info" variant="tonal"
                                    class="ml-1 flex-shrink-0">
                                    {{ t('worker.localLabel') }}
                                </v-chip>
                                <v-chip v-else size="x-small" color="warning" variant="tonal"
                                    class="ml-1 flex-shrink-0">
                                    {{ t('worker.remoteLabel') }}
                                </v-chip>
                            </div>
                        </td>
                        <td style="max-width:200px">
                            <span class="text-caption font-monospace text-truncate d-block">{{ w.address }}</span>
                        </td>
                        <td class="text-no-wrap" style="white-space:nowrap">
                            <v-chip v-if="w.skipVersionCheck" color="warning" size="small" variant="tonal">
                                <v-icon start size="x-small">mdi-alert</v-icon>
                                {{ t('worker.versionIgnored') }}
                            </v-chip>
                            <v-chip v-else :color="w.healthy ? 'success' : 'error'" size="small" variant="tonal">
                                <v-icon start size="x-small">{{ w.healthy ? 'mdi-check-circle' : 'mdi-close-circle'
                                }}</v-icon>
                                {{ w.healthy ? t('worker.online') : t('worker.offline') }}
                            </v-chip>
                            <template v-if="w.healthy">
                                <v-chip size="x-small" :color="heartbeatColor(w.lastHeartbeatLatencyMs)" variant="tonal"
                                    class="ml-1">
                                    <v-icon start size="x-small">mdi-heart-pulse</v-icon>
                                    {{ w.lastHeartbeatLatencyMs }}ms
                                </v-chip>
                                <v-chip size="x-small" :color="offsetColor(w.bilibiliOffsetMs)" variant="tonal"
                                    class="ml-1">
                                    <v-icon start size="x-small">mdi-clock-outline</v-icon>
                                    Bili {{ signedMs(w.bilibiliOffsetMs) }}
                                </v-chip>
                            </template>
                            <v-chip v-if="w.activeAttemptId" size="x-small" color="orange" variant="tonal" class="ml-1">
                                {{ t('worker.busy') }}
                            </v-chip>
                            <v-menu v-if="isVersionBlocked(w)" location="bottom end">
                                <template #activator="{ props: menuProps }">
                                    <v-chip size="x-small" color="error" variant="text" class="ml-1"
                                        style="cursor:pointer" v-bind="menuProps">
                                        <v-icon start size="x-small">mdi-chevron-down</v-icon>
                                        {{ t('worker.versionBlocked') }}
                                    </v-chip>
                                </template>
                                <v-list density="compact" class="pa-2" style="min-width:260px">
                                    <v-list-item density="compact" disabled>
                                        <div style="font-size:0.8rem;line-height:1.4">
                                            <div class="text-caption text-medium-emphasis">{{
                                                t('worker.employerVersion') }}</div>
                                            <div class="text-warning font-weight-bold">{{ employerVersion || '—' }}
                                            </div>
                                        </div>
                                    </v-list-item>
                                    <v-list-item density="compact" disabled>
                                        <div style="font-size:0.8rem;line-height:1.4">
                                            <div class="text-caption text-medium-emphasis">{{ t('worker.workerVersion')
                                            }}</div>
                                            <div class="text-warning font-weight-bold">{{ w.version || '—' }}</div>
                                        </div>
                                    </v-list-item>
                                    <v-divider class="my-1" />
                                    <v-list-item @click="doForceReconnect(w)" :title="t('worker.forceReconnectTitle')"
                                        :subtitle="t('worker.forceReconnectSubtitle')">
                                        <template #prepend>
                                            <v-icon color="error">mdi-alert-circle</v-icon>
                                        </template>
                                        <template #title>
                                            <span class="text-error font-weight-bold">{{ t('worker.forceReconnectTitle')
                                                }}</span>
                                        </template>
                                    </v-list-item>
                                </v-list>
                            </v-menu>
                        </td>
                        <td class="text-no-wrap" style="white-space:nowrap" @click.stop>
                            <div style="display:flex;gap:4px">
                                <!-- Primary local worker — read-only, no stop/delete -->
                                <template v-if="isPrimaryLocal(w)">
                                    <v-chip size="x-small" color="info" variant="tonal" class="mr-1">
                                        {{ t('worker.primaryLocal') }}
                                    </v-chip>
                                </template>
                                <!-- Other local workers: start/stop + delete -->
                                <template v-else-if="isLocalWorker(w)">
                                    <v-btn v-if="w.healthy" icon="mdi-stop-circle-outline" size="small" variant="text"
                                        color="warning" :loading="connecting[w.id]" :title="t('worker.localStop')"
                                        @click="toggleLocalWorker(w)" />
                                    <v-btn v-else icon="mdi-play-circle-outline" size="small" variant="text"
                                        color="success" :loading="connecting[w.id]" :title="t('worker.localStart')"
                                        @click="toggleLocalWorker(w)" />
                                    <v-btn icon="mdi-delete" size="small" variant="text" color="error"
                                        @click="promptDelete(w)" />
                                </template>
                                <!-- Remote workers -->
                                <template v-else>
                                    <v-btn v-if="w.healthy" icon="mdi-link-off" size="small" variant="text"
                                        :loading="connecting[w.id]" :title="t('worker.disconnect')"
                                        @click="doDisconnect(w)" />
                                    <v-btn v-else-if="isVersionBlocked(w)" icon="mdi-link-lock" size="small"
                                        variant="text" color="grey" disabled :title="t('worker.reconnectLocked')" />
                                    <v-btn v-else icon="mdi-link" size="small" variant="text"
                                        :loading="connecting[w.id]" :title="t('worker.reconnect')"
                                        @click="doReconnect(w)" />
                                    <v-btn icon="mdi-pencil" size="small" variant="text" color="primary"
                                        :title="t('worker.edit')" @click="openEdit(w)" />
                                    <v-btn icon="mdi-delete" size="small" variant="text" color="error"
                                        @click="promptDelete(w)" />
                                </template>
                            </div>
                        </td>
                    </tr>
                    <!-- Expandable detail row -->
                    <tr v-if="expandedWorkers.has(w.id)">
                        <td></td>
                        <td :colspan="4" class="pa-3">
                            <v-row dense>
                                <v-col cols="6" md="3">
                                    <div class="text-caption text-medium-emphasis">Worker ID</div>
                                    <div class="text-body-2 font-monospace">{{ w.id }}</div>
                                </v-col>
                                <v-col cols="6" md="3">
                                    <div class="text-caption text-medium-emphasis">{{ t('worker.colAddress') }}</div>
                                    <div class="text-body-2 font-monospace">{{ w.address }}</div>
                                </v-col>
                                <v-col cols="6" md="3">
                                    <div class="text-caption text-medium-emphasis">类型</div>
                                    <div class="text-body-2">{{ isLocalWorker(w) ? t('worker.localLabel') :
                                        t('worker.remoteLabel') }}</div>
                                </v-col>
                                <v-col v-if="isReachable(w) && w.version" cols="6" md="3">
                                    <div class="text-caption text-medium-emphasis">{{ t('worker.colVersion') }}</div>
                                    <div class="text-body-2"
                                        :class="{ 'text-warning font-weight-bold': w.skipVersionCheck }">
                                        {{ w.version }}
                                    </div>
                                </v-col>
                                <v-col v-else-if="isReachable(w) && !w.version" cols="6" md="3">
                                    <div class="text-caption text-medium-emphasis">{{ t('worker.colVersion') }}</div>
                                    <div class="text-body-2 text-medium-emphasis">—</div>
                                </v-col>
                                <v-col v-if="w.activeAttemptId" cols="6" md="3">
                                    <div class="text-caption text-medium-emphasis">当前任务</div>
                                    <div class="text-body-2 font-monospace">{{ w.activeAttemptId }}</div>
                                </v-col>
                                <!-- Heartbeat & Latency -->
                                <v-col v-if="w.lastHeartbeatAt" cols="6" md="3">
                                    <div class="text-caption text-medium-emphasis">最后心跳</div>
                                    <div class="text-body-2">{{ new Date(w.lastHeartbeatAt).toLocaleTimeString() }}
                                    </div>
                                </v-col>
                                <v-col v-if="w.lastHeartbeatLatencyMs" cols="6" md="3">
                                    <div class="text-caption text-medium-emphasis">心跳延迟</div>
                                    <div class="text-body-2">{{ w.lastHeartbeatLatencyMs }}ms</div>
                                </v-col>
                                <v-col v-if="w.skipVersionCheck" cols="12">
                                    <v-divider class="my-1" />
                                    <div class="text-caption text-error font-weight-bold mt-1">
                                        ⛔ {{ t('worker.skipVersionCheckDesc') }}
                                    </div>
                                </v-col>
                                <!-- Global clock offsets — only shown when worker is reachable -->
                                <template v-if="isReachable(w)">
                                    <v-col cols="12">
                                        <v-divider class="my-1" />
                                        <div class="text-caption text-medium-emphasis mt-1">{{
                                            t('worker.clockOffsetTitle')
                                            }}</div>
                                    </v-col>
                                    <v-col cols="6" md="3">
                                        <div class="text-caption text-medium-emphasis">Bilibili API</div>
                                        <div class="text-body-2"
                                            :class="Math.abs(w.bilibiliOffsetMs) > 1000 ? 'text-red' : 'text-green'">
                                            {{ w.bilibiliOffsetMs > 0 ? '+' : '' }}{{ w.bilibiliOffsetMs }}ms
                                        </div>
                                    </v-col>
                                    <v-col cols="6" md="3">
                                        <div class="text-caption text-medium-emphasis">NTP (阿里云)</div>
                                        <div class="text-body-2"
                                            :class="Math.abs(w.ntpOffsetMs) > 1000 ? 'text-red' : 'text-green'">
                                            {{ w.ntpOffsetMs > 0 ? '+' : '' }}{{ w.ntpOffsetMs }}ms
                                        </div>
                                    </v-col>
                                </template>
                                <!-- Cooldown detail section -->
                                <template v-if="w.cooldown?.cooledDown">
                                    <v-col cols="12">
                                        <v-divider class="my-1" />
                                        <div class="text-caption text-warning font-weight-bold mt-1">{{
                                            t('worker.cooldown') }}</div>
                                    </v-col>
                                    <v-col cols="6" md="3">
                                        <div class="text-caption text-medium-emphasis">原因</div>
                                        <div class="text-body-2">{{ w.cooldown.reason || '412 限流' }}</div>
                                    </v-col>
                                    <v-col cols="6" md="3">
                                        <div class="text-caption text-medium-emphasis">冷却开始</div>
                                        <div class="text-body-2">{{ w.cooldown.startedAt ? new
                                            Date(w.cooldown.startedAt).toLocaleTimeString() : '-' }}</div>
                                    </v-col>
                                    <v-col cols="6" md="3">
                                        <div class="text-caption text-medium-emphasis">冷却结束</div>
                                        <div class="text-body-2">{{ new
                                            Date(w.cooldown.cooldownEnd!).toLocaleTimeString() }}</div>
                                    </v-col>
                                    <v-col cols="6" md="3">
                                        <div class="text-caption text-medium-emphasis">总冷却时长</div>
                                        <div class="text-body-2">{{ Math.round((w.cooldown.totalDurationMs || 0) / 1000)
                                        }}s</div>
                                    </v-col>
                                    <v-col cols="6" md="3">
                                        <div class="text-caption text-medium-emphasis">剩余</div>
                                        <div class="text-body-2 text-warning font-weight-bold">{{ cooldownTimers[w.id]
                                            || 0 }}s</div>
                                    </v-col>
                                </template>
                            </v-row>
                        </td>
                    </tr>
                </template>
            </tbody>
        </v-table>

        <!-- ═══ Batch Deploy Remote Workers Dialog ═══ -->
        <v-dialog v-model="showBatchDeployDialog" max-width="1280" persistent scrollable>
            <v-card class="pa-4">
                <v-card-title class="d-flex align-center">
                    <v-icon start>mdi-cloud-upload-outline</v-icon>
                    {{ t('worker.batchDeployTitle') }}
                    <v-spacer />
                    <v-chip v-if="deployJob" :color="deployStatusColor(deployJob.status)" variant="tonal" size="small">
                        {{ deployJob.status }}
                    </v-chip>
                </v-card-title>
                <v-card-text>
                    <v-alert type="info" variant="tonal" density="compact" class="mb-4">
                        {{ t('worker.batchDeployHint') }}
                    </v-alert>

                    <div class="text-subtitle-2 mb-2">{{ t('worker.deployTargets') }}</div>
                    <v-table density="compact" class="mb-3">
                        <thead>
                            <tr>
                                <th>{{ t('worker.deployHost') }}</th>
                                <th style="width:90px">{{ t('worker.deploySSHPort') }}</th>
                                <th>{{ t('worker.deployUsername') }}</th>
                                <th>{{ t('worker.deployPassword') }}</th>
                                <th style="width:110px">{{ t('worker.deployWorkerPort') }}</th>
                                <th>{{ t('worker.colName') }}</th>
                                <th>{{ t('worker.colId') }}</th>
                                <th style="width:48px"></th>
                            </tr>
                        </thead>
                        <tbody>
                            <tr v-for="(target, index) in deployTargets" :key="index">
                                <td><v-text-field v-model="target.host" density="compact" hide-details
                                        placeholder="1.2.3.4" /></td>
                                <td><v-text-field v-model.number="target.sshPort" type="number" density="compact"
                                        hide-details class="no-spin" /></td>
                                <td><v-text-field v-model="target.username" density="compact" hide-details /></td>
                                <td><v-text-field v-model="target.password" type="password" density="compact"
                                        hide-details /></td>
                                <td><v-text-field v-model.number="target.workerPort" type="number" density="compact"
                                        hide-details class="no-spin" /></td>
                                <td><v-text-field v-model="target.name" density="compact" hide-details
                                        :placeholder="t('worker.deployAuto')" /></td>
                                <td><v-text-field v-model="target.workerId" density="compact" hide-details
                                        :placeholder="t('worker.deployAuto')" /></td>
                                <td>
                                    <v-btn icon="mdi-delete" size="small" variant="text" color="error"
                                        @click="removeDeployTarget(index)" />
                                </td>
                            </tr>
                        </tbody>
                    </v-table>
                    <v-btn prepend-icon="mdi-plus" variant="tonal" size="small" class="mb-5" @click="addDeployTarget">
                        {{ t('worker.deployAddTarget') }}
                    </v-btn>

                    <v-row dense>
                        <v-col cols="12" md="3">
                            <v-radio-group v-model="deployPackageType" :label="t('worker.deployPackageType')"
                                density="compact">
                                <v-radio :label="t('worker.deployPackageBinary')" value="binary" />
                                <v-radio :label="t('worker.deployPackageTarGz')" value="targz" />
                            </v-radio-group>
                        </v-col>
                        <v-col cols="12" md="3">
                            <v-radio-group v-model="deployBinarySource" :label="t('worker.deployBinarySource')"
                                density="compact">
                                <v-radio :label="t('worker.deployBinaryLocal')" value="local" />
                                <v-radio :label="t('worker.deployBinaryUrl')" value="url" />
                            </v-radio-group>
                        </v-col>
                        <v-col v-if="deployBinarySource === 'local'" cols="12" md="6">
                            <v-text-field v-model="deployLocalBinaryPath" :label="deployLocalPathLabel"
                                variant="outlined" density="compact" class="mb-2">
                                <template #append>
                                    <v-btn variant="tonal" size="small" @click="chooseWorkerBinary">
                                        {{ t('worker.deployBrowse') }}
                                    </v-btn>
                                </template>
                            </v-text-field>
                        </v-col>
                        <v-col v-else cols="12" md="6">
                            <v-text-field v-model="deployDownloadUrl" :label="deployDownloadUrlLabel"
                                :placeholder="deployDownloadUrlPlaceholder" variant="outlined" density="compact" />
                        </v-col>
                    </v-row>

                    <v-expansion-panels variant="accordion" class="mb-4">
                        <v-expansion-panel>
                            <v-expansion-panel-title>{{ t('worker.deployAdvanced') }}</v-expansion-panel-title>
                            <v-expansion-panel-text>
                                <v-row dense>
                                    <v-col cols="12" md="6">
                                        <v-text-field v-model="deployInstallDir"
                                            :label="t('worker.deployInstallDir')" variant="outlined"
                                            density="compact" />
                                    </v-col>
                                    <v-col cols="12" md="3">
                                        <v-select v-model="deployStartMode"
                                            :items="[{ title: 'nohup', value: 'nohup' }, { title: 'systemd --user', value: 'systemd-user' }]"
                                            :label="t('worker.deployStartMode')" variant="outlined"
                                            density="compact" />
                                    </v-col>
                                    <v-col cols="12" md="3">
                                        <v-text-field v-model.number="deployConcurrency" type="number"
                                            :label="t('worker.deployConcurrency')" variant="outlined"
                                            density="compact" min="1" max="10" :hint="t('worker.deployConcurrencyHint')"
                                            persistent-hint />
                                    </v-col>
                                    <v-col cols="12" md="6">
                                        <v-switch v-model="deployOverwriteBinary"
                                            :label="t('worker.deployOverwriteBinary')" color="primary" />
                                    </v-col>
                                    <v-col cols="12" md="6">
                                        <v-switch v-model="deployRestartExisting"
                                            :label="t('worker.deployRestartExisting')" color="primary" />
                                    </v-col>
                                    <v-col v-if="deployPackageType === 'binary' && deployBinarySource === 'local'"
                                        cols="12">
                                        <v-switch v-model="deploySaveTraffic" :label="t('worker.deploySaveTraffic')"
                                            :hint="t('worker.deploySaveTrafficHint')" persistent-hint color="primary" />
                                    </v-col>
                                </v-row>
                            </v-expansion-panel-text>
                        </v-expansion-panel>
                    </v-expansion-panels>

                    <template v-if="deployJob">
                        <v-divider class="my-3" />
                        <div class="d-flex align-center mb-2">
                            <div class="text-subtitle-2">{{ t('worker.deployProgress') }}</div>
                            <v-spacer />
                            <span class="text-caption text-medium-emphasis">{{ deployJob.message }}</span>
                        </div>
                        <v-table density="compact">
                            <thead>
                                <tr>
                                    <th>{{ t('worker.deployHost') }}</th>
                                    <th>{{ t('worker.colId') }}</th>
                                    <th>{{ t('worker.deployStageLabel') }}</th>
                                    <th>{{ t('worker.colStatus') }}</th>
                                    <th>{{ t('worker.deployMessage') }}</th>
                                </tr>
                            </thead>
                            <tbody>
                                <tr v-for="item in deployJob.items" :key="item.index">
                                    <td>
                                        <div>{{ item.host }}:{{ item.sshPort }}</div>
                                        <div class="text-caption text-medium-emphasis">{{ item.address }}</div>
                                    </td>
                                    <td class="font-monospace">{{ item.workerId || '—' }}</td>
                                    <td>{{ deployStageText(item.stage) }}</td>
                                    <td>
                                        <v-chip :color="deployStatusColor(item.status)" size="small" variant="tonal">
                                            {{ item.status }}
                                        </v-chip>
                                    </td>
                                    <td style="max-width:360px">
                                        <div class="text-body-2">{{ item.message }}</div>
                                        <details v-if="item.logs?.length" class="text-caption">
                                            <summary>{{ t('worker.deployShowLogs') }}</summary>
                                            <pre style="white-space:pre-wrap">{{ item.logs.join('\n') }}</pre>
                                        </details>
                                    </td>
                                </tr>
                            </tbody>
                        </v-table>
                    </template>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn v-if="deploying && deployJob" variant="text" color="warning" @click="cancelBatchDeploy">
                        {{ t('worker.deployCancel') }}
                    </v-btn>
                    <v-btn variant="text" :disabled="deploying" @click="showBatchDeployDialog = false">
                        {{ t('common.cancel') }}
                    </v-btn>
                    <v-btn v-if="hasDeployableTargets || deploying" color="success" :loading="deploying"
                        :disabled="!hasDeployableTargets" @click="startBatchDeploy">
                        {{ t('worker.deployStart') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>

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

        <!-- ═══ Version Mismatch Warning Dialog ═══ -->
        <v-dialog v-model="showVersionMismatchDialog" max-width="520" persistent>
            <v-card class="pa-4">
                <v-card-title class="text-error" style="font-weight:700">
                    <v-icon start color="error">mdi-alert-circle</v-icon>
                    {{ t('worker.versionMismatchTitle') }}
                </v-card-title>
                <v-card-text>
                    <v-alert type="error" variant="tonal" class="mb-3">
                        <p class="text-body-2 mb-1">
                            {{ t('worker.versionMismatchWarning') }}
                        </p>
                        <p class="text-caption text-medium-emphasis mb-0"
                            style="white-space:pre-wrap;word-break:break-all">
                            {{ versionMismatchError }}
                        </p>
                    </v-alert>
                    <p class="text-body-2 text-error font-weight-bold">
                        {{ t('worker.versionMismatchRisk') }}
                    </p>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showVersionMismatchDialog = false;
                    versionMismatchEncoded = '';
                    versionMismatchAddress = ''">
                        {{ t('common.cancel') }}
                    </v-btn>
                    <v-btn color="error" variant="flat" :loading="forceImporting" @click="doForceImport">
                        {{ t('worker.forceConnectBtn') }}
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
                        :placeholder="t('worker.editAddressPlaceholder')" variant="outlined" density="compact" />
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

<style scoped>
.no-spin :deep(input[type='number']) {
    appearance: textfield;
    -moz-appearance: textfield;
}

.no-spin :deep(input[type='number']::-webkit-outer-spin-button),
.no-spin :deep(input[type='number']::-webkit-inner-spin-button) {
    -webkit-appearance: none;
    margin: 0;
}
</style>
