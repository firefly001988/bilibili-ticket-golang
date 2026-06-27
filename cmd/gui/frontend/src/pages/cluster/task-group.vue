<script lang="ts" setup>
import { ref, watch, computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import { Snapshot, SaveMacro, DeleteMacro, SavePurchaseGroup, DeletePurchaseGroup, StopTaskGroup, StartTaskGroup } from '../../../bindings/bilibili-ticket-golang/cmd/gui/clusterservice'
import { GetProjectInformationNew, GetTicketSkuIDsByProjectIDNew } from '../../../bindings/bilibili-ticket-golang/lib/biliutils/biliclient'

const route = useRoute(); const { t } = useI18n(); const messages = useMessagesStore()

interface MacroSummary { id: string; taskGroupId: string; projectId: number; projectName?: string; screenId: number; screenName?: string; skuId: number; skuName?: string; eventDay: string; eventDayConfirmed: boolean; needsReview: boolean; orderCapacity: number; startAt: string; deadline: string; primaryWorkerIds?: string[]; standbyWorkerIds?: string[]; phase?: string; purchaseGroups?: any[] }
interface AttemptBrief { id: string; intentId: string; accountId: string; workerId: string; state: string; orderId?: string }
interface IntentBrief { id: string; macroTaskId: string; phase: string; weight: number; priority: number; buyerCount: number; succeeded: boolean; terminal: boolean; armed: boolean; activeCount: number; deficit: number; failureReason?: string }

const group = ref<any>(null); const macros = ref<MacroSummary[]>([]); const attempts = ref<AttemptBrief[]>([]); const intents = ref<IntentBrief[]>([]); const loading = ref(true)
const dispatching = ref<Record<string, boolean>>({}); const dispatchingAll = ref(false)
// Worker selection for start
const showWorkerSelectDialog = ref(false)
const workerList = ref<{ id: string; name: string; address: string; type: string; healthy: boolean }[]>([])
const selectedWorkerIds = ref<string[]>([])
const lookupProjectId = ref(''); const lookupLoading = ref(false); const projectInfo = ref<any>(null); const tickets = ref<any[]>([])
const selectedScreenId = ref(0); const selectedSkuId = ref(0); const addingMacro = ref(false); const showSkuList = ref(false)
const deletingMacro = ref<Record<string, boolean>>({})
const filterName = ref('')
const filteredTickets = computed(() => { const kw = filterName.value.trim().toLowerCase(); if (!kw) return tickets.value; return tickets.value.filter((t: any) => (t.name || '').toLowerCase().includes(kw) || (t.desc || '').toLowerCase().includes(kw) || String(t.skuId).includes(kw)) })

// Add macro confirmation dialog
const showAddConfirmDialog = ref(false)
const addingMacroInfo = ref<{ projectName: string; eventDay: string; screenName: string; skuName: string; price: number; buyLimit: number; saleStart: string; saleEnd: string; isRealName: boolean } | null>(null)

const eventDayHumanized = computed(() => {
    const raw = addingMacroInfo.value?.eventDay
    if (!raw) return { prefix: '', day: '—', weekPrefix: '', weekDay: '' }
    const iso = raw.slice(0, 10)
    const parts = iso.split('-')
    if (parts.length >= 3) {
        const y = parseInt(parts[0]), m = parseInt(parts[1]), d = parseInt(parts[2])
        if (!isNaN(y) && !isNaN(m) && !isNaN(d)) {
            const date = new Date(y, m - 1, d)
            return {
                prefix: `${y}-${String(m).padStart(2, '0')}-`,
                day: String(d).padStart(2, '0'),
                weekPrefix: t('taskGroup.weekPrefix'),
                weekDay: t(`taskGroup.weekDay_${date.getDay()}`),
            }
        }
    }
    return { prefix: '', day: raw, weekPrefix: '', weekDay: '' }
})

// Group tickets by screen for nested list display
const filteredScreens = computed(() => {
    const map = new Map<number, { screenId: number; screenName: string; tickets: any[] }>()
    for (const t of filteredTickets.value) {
        if (!map.has(t.screenId)) {
            map.set(t.screenId, { screenId: t.screenId, screenName: t.name || `场次 ${t.screenId}`, tickets: [] })
        }
        map.get(t.screenId)!.tickets.push(t)
    }
    return Array.from(map.values()).sort((a, b) => a.screenId - b.screenId)
})
const expandedMacro = ref(0); const allBuyers = ref<Array<{ logicalId: string; name: string; idCard: string; tel: string }>>([])
const selectedPgBuyerIds = ref<string[]>([]); const savedPgBuyerIds = ref(new Map<string, string[]>()); const savingPg = ref(false); const deletingPg = ref<Record<string, boolean>>({})
const editingPgId = ref(''); const editingPgMacroId = ref('')
const allowSplit = ref(false)
const pgWeight = ref(1)
const pgPriority = ref(0)
const macroPrimaryWorkerIds = ref<string[]>([])
const macroStandbyWorkerIds = ref<string[]>([])

// Remember the selection for the currently expanded macro so we can restore it later.
let lastExpandedMacroId = ''

function onMacroPanelChange(newVal: number) {
    const oldId = lastExpandedMacroId
    if (oldId) savedPgBuyerIds.value.set(oldId, [...selectedPgBuyerIds.value])
    const m = macros.value.find((_, i) => i + 1 === newVal)
    const newId = (newVal > 0 && m) ? m.id : ''
    lastExpandedMacroId = newId
    editingPgId.value = ''; editingPgMacroId.value = ''
    selectedPgBuyerIds.value = newId ? [...(savedPgBuyerIds.value.get(newId) || [])] : []
    loadBuyersOnce()
}

const currentMacroOrderCapacity = computed(() => {
    const m = macros.value.find(x => x.id === lastExpandedMacroId)
    return m?.orderCapacity || 1
})

function onBuyerSelectionChange(vals: string[]) {
    const cap = currentMacroOrderCapacity.value
    if (vals.length > cap) {
        selectedPgBuyerIds.value = vals.slice(0, cap)
        messages.add({ text: t('taskGroup.pgMaxBuyers', { max: cap }), color: 'warning' })
    } else {
        selectedPgBuyerIds.value = vals
    }
}

function onPrimaryWorkerSelectionChange(vals: string[]) {
    macroPrimaryWorkerIds.value = vals
    macroStandbyWorkerIds.value = macroStandbyWorkerIds.value.filter(id => !vals.includes(id))
}

function onStandbyWorkerSelectionChange(vals: string[]) {
    macroStandbyWorkerIds.value = vals.filter(id => !macroPrimaryWorkerIds.value.includes(id))
}

async function loadAll(id: string) { loading.value = true; group.value = null; macros.value = []; intents.value = []; attempts.value = []; try { const snap = await Snapshot(); group.value = ((snap.taskGroups || []) as any[]).find(g => g.id === id) || null; macros.value = ((snap.macros || []) as MacroSummary[]).filter(m => m.taskGroupId === id); intents.value = ((snap.intents || []) as IntentBrief[]).filter(i => macros.value.some(m => m.id === i.macroTaskId)); attempts.value = ((snap.attempts || []) as AttemptBrief[]); workerList.value = ((snap.workers || []) as any[]); if (allBuyers.value.length === 0) { allBuyers.value = ((snap.buyers || []) as any[]).map((b: any) => ({ logicalId: b.logicalId, name: b.name || '', idCard: b.idCard || '', tel: b.tel || '' })) } } catch { } loading.value = false }
watch(() => route.params.id, (newId) => { if (newId) loadAll(newId as string) }, { immediate: true })

async function lookupProject() { const pid = lookupProjectId.value.trim(); if (!pid) { messages.add({ text: t('taskGroup.projectIdRequired'), color: 'warning' }); return } lookupLoading.value = true; projectInfo.value = null; tickets.value = []; selectedScreenId.value = 0; selectedSkuId.value = 0; try { const [info, tks] = await Promise.all([GetProjectInformationNew(pid), GetTicketSkuIDsByProjectIDNew(pid)]); if (!info) messages.add({ text: t('taskGroup.projectNotFound'), color: 'warning' }); else { projectInfo.value = info; tickets.value = tks || [] } } catch (e: any) { messages.add({ text: t('taskGroup.lookupFailed', { error: String(e) }), color: 'error' }) } lookupLoading.value = false }

async function addMacro() { if (!projectInfo.value || !selectedScreenId.value || !selectedSkuId.value || !group.value) return; const ticket = tickets.value.find((t: any) => t.screenId === selectedScreenId.value && t.skuId === selectedSkuId.value); addingMacroInfo.value = { projectName: projectInfo.value.ProjectName || '', eventDay: projectInfo.value.StartTime || '', screenName: ticket?.name || '', skuName: ticket?.desc || '', price: ((ticket?.price || 0) / 100), buyLimit: ticket?.buyLimit || 1, saleStart: ticket?.saleStat?.start || '', saleEnd: ticket?.saleStat?.end || '', isRealName: projectInfo.value.IsForceRealName || false }; showAddConfirmDialog.value = true }

async function confirmAddMacro() {
    if (!addingMacroInfo.value || !group.value) return
    addingMacro.value = true
    showAddConfirmDialog.value = false
    const info = addingMacroInfo.value
    const ticket = tickets.value.find((t: any) => t.screenId === selectedScreenId.value && t.skuId === selectedSkuId.value)
    try { await SaveMacro(JSON.stringify({ id: randomId('macro'), taskGroupId: group.value.id, projectId: Number(projectInfo.value!.ProjectID), projectName: projectInfo.value!.ProjectName || '', screenId: selectedScreenId.value, screenName: ticket?.name || '', skuId: selectedSkuId.value, skuName: ticket?.desc || '', eventDay: info.eventDay, eventDayConfirmed: true, needsReview: projectInfo.value!.IsForceRealName || false, orderCapacity: ticket?.buyLimit || 1, startAt: ticket?.saleStat?.start || '', deadline: ticket?.saleStat?.end || '', primaryWorkerIds: macroPrimaryWorkerIds.value, standbyWorkerIds: macroStandbyWorkerIds.value })); projectInfo.value = null; selectedScreenId.value = 0; selectedSkuId.value = 0; lookupProjectId.value = ''; macroPrimaryWorkerIds.value = []; macroStandbyWorkerIds.value = []; await loadAll(group.value!.id); messages.add({ text: t('taskGroup.macroAdded'), color: 'success' }) } catch (e: any) { messages.add({ text: t('taskGroup.macroAddFailed', { error: String(e) }), color: 'error' }) }
    addingMacro.value = false
    addingMacroInfo.value = null
}

function cancelAddMacro() {
    showAddConfirmDialog.value = false
    addingMacroInfo.value = null
}

async function removeMacro(m: MacroSummary) { deletingMacro.value[m.id] = true; try { await DeleteMacro(m.id); await loadAll(group.value!.id); messages.add({ text: t('taskGroup.macroDeleted'), color: 'success' }) } catch (e: any) { messages.add({ text: t('taskGroup.macroDeleteFailed', { error: String(e) }), color: 'error' }) } deletingMacro.value[m.id] = false }

async function savePurchaseGroup(m: MacroSummary) {
    if (selectedPgBuyerIds.value.length === 0) { messages.add({ text: t('taskGroup.pgSelectBuyer'), color: 'warning' }); return }
    const isEdit = !!editingPgId.value && editingPgMacroId.value === m.id
    const shouldAutoStart = !isEdit && shouldAutoStartAfterPurchaseGroup(m)
    savingPg.value = true
    try {
        const buyers = selectedPgBuyerIds.value.map(id => { const b = allBuyers.value.find(x => x.logicalId === id)!; return { logicalId: id, name: b.name, idCard: b.idCard, tel: b.tel } })
        await SavePurchaseGroup(JSON.stringify({ id: isEdit ? editingPgId.value : '', macroTaskId: m.id, buyers, allowSplit: allowSplit.value, weight: pgWeight.value || 1, priority: pgPriority.value || 0 }))
        editingPgId.value = ''; editingPgMacroId.value = ''
        allowSplit.value = false
        pgWeight.value = 1
        pgPriority.value = 0
        const restored = isEdit ? [...(savedPgBuyerIds.value.get(m.id) || [])] : []
        savedPgBuyerIds.value.set(m.id, [])
        selectedPgBuyerIds.value = restored
        await loadAll(group.value!.id)
        messages.add({ text: isEdit ? t('taskGroup.pgUpdated') : t('taskGroup.pgAdded'), color: 'success' })
        if (shouldAutoStart) {
            await autoStartTaskGroupForPastSale()
        }
    } catch (e: any) { messages.add({ text: isEdit ? t('taskGroup.pgUpdateFailed', { error: String(e) }) : t('taskGroup.pgAddFailed', { error: String(e) }), color: 'error' }) }
    savingPg.value = false
}

function shouldAutoStartAfterPurchaseGroup(m: MacroSummary) {
    if (!m.eventDayConfirmed || m.needsReview) return false
    const startAt = Date.parse(m.startAt || '')
    if (!Number.isFinite(startAt) || Date.now() < startAt) return false
    const deadline = Date.parse(m.deadline || '')
    if (Number.isFinite(deadline) && Date.now() > deadline) return false
    const hasActiveIntent = intents.value.some(i => i.macroTaskId === m.id && i.armed && !i.terminal && !i.succeeded)
    const hasActiveAttempt = attempts.value.some(a => {
        const intent = intents.value.find(i => i.id === a.intentId)
        return intent?.macroTaskId === m.id && !['succeeded', 'failed', 'stopped'].includes(String(a.state).toLowerCase())
    })
    return !hasActiveIntent && !hasActiveAttempt
}

async function autoStartTaskGroupForPastSale() {
    if (!group.value) return
    dispatchingAll.value = true
    try {
        const snap = await Snapshot()
        const workers = ((snap.workers || []) as any[]).filter(w => w.healthy)
        if (workers.length === 0) {
            messages.add({ text: t('taskGroup.autoStartNoWorkers'), color: 'warning' })
            return
        }
        const ids = workers.map(w => w.id)
        await StartTaskGroup(group.value.id, JSON.stringify(ids))
        await loadAll(group.value.id)
        messages.add({ text: t('taskGroup.autoStarted', { count: ids.length }), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('taskGroup.autoStartFailed', { error: String(e) }), color: 'error' })
    } finally {
        dispatchingAll.value = false
    }
}

function openEditPg(macroId: string, pg: any) {
    loadBuyersOnce()
    // Save current selection as pre-edit state
    savedPgBuyerIds.value.set(macroId, [...selectedPgBuyerIds.value])
    editingPgId.value = pg.id; editingPgMacroId.value = macroId
    selectedPgBuyerIds.value = (pg.buyers || []).map((b: any) => b.logicalId)
    allowSplit.value = pg.allowSplit || false
    pgWeight.value = pg.weight || 1
    pgPriority.value = pg.priority || 0
}

function cancelEditPg() {
    const macroId = editingPgMacroId.value
    // Restore pre-edit selection
    selectedPgBuyerIds.value = macroId ? [...(savedPgBuyerIds.value.get(macroId) || [])] : []
    editingPgId.value = ''; editingPgMacroId.value = ''
    allowSplit.value = false
    pgWeight.value = 1
    pgPriority.value = 0
}

async function removePurchaseGroup(macroId: string, pgId: string) {
    if (editingPgId.value === pgId) {
        // Restore pre-edit selection
        selectedPgBuyerIds.value = [...(savedPgBuyerIds.value.get(macroId) || [])]
        editingPgId.value = ''; editingPgMacroId.value = ''
        allowSplit.value = false
    }
    deletingPg.value[pgId] = true; try { await DeletePurchaseGroup(macroId, pgId); await loadAll(group.value!.id); messages.add({ text: t('taskGroup.pgDeleted'), color: 'success' }) } catch (e: any) { messages.add({ text: t('taskGroup.pgDeleteFailed', { error: String(e) }), color: 'error' }) } deletingPg.value[pgId] = false
}

function buyerByLogicalId(id: string) { return allBuyers.value.find(b => b.logicalId === id) }

async function loadBuyersOnce() { if (allBuyers.value.length > 0) return; try { const snap = await Snapshot(); allBuyers.value = ((snap.buyers || []) as any[]).map((b: any) => ({ logicalId: b.logicalId, name: b.name || '', idCard: b.idCard || '', tel: b.tel || '' })) } catch { } }

function randomId(prefix: string) { const arr = new Uint8Array(6); crypto.getRandomValues(arr); return prefix + '-' + Array.from(arr).map(b => b.toString(16).padStart(2, '0')).join('') }

// ── Dispatch ─────────────────────────────────────────────────
const hasIntent = (m: MacroSummary) => m.phase === 'punctual' || m.phase === 'reflow'
const isRunning = (m: MacroSummary) => (dispatching.value[m.id] || hasIntent(m))

function dispatchStats(m: MacroSummary) {
    const macroIntents = intents.value.filter(i => i.macroTaskId === m.id && i.armed && !i.terminal && !i.succeeded)
    const running = macroIntents.reduce((sum, i) => sum + (i.activeCount || 0), 0)
    const deficit = macroIntents.reduce((sum, i) => sum + (i.deficit || 0), 0)
    const succeeded = intents.value.filter(i => i.macroTaskId === m.id && i.succeeded).length
    const failed = intents.value.filter(i => i.macroTaskId === m.id && i.terminal && !i.succeeded).length
    return { running, deficit, succeeded, failed, total: macroIntents.length, intents: macroIntents }
}


async function startAllMacros() {
    if (!group.value) return
    workerList.value = ((await Snapshot()).workers || []) as any[]
    if (workerList.value.length === 0) {
        messages.add({ text: t('taskGroup.noWorkersAvailable'), color: 'warning' })
        return
    }
    selectedWorkerIds.value = workerList.value.filter(w => w.healthy).map(w => w.id)
    showWorkerSelectDialog.value = true
}

async function confirmStartTaskGroup() {
    if (!group.value || selectedWorkerIds.value.length === 0) return
    showWorkerSelectDialog.value = false
    dispatchingAll.value = true
    try {
        await StartTaskGroup(group.value.id, JSON.stringify(selectedWorkerIds.value))
        await loadAll(group.value.id); messages.add({ text: t('taskGroup.allStarted'), color: 'success' })
    }
    catch (e: any) { messages.add({ text: t('taskGroup.allStartFailed', { error: String(e) }), color: 'error' }) }
    dispatchingAll.value = false
}

async function stopAllMacros() {
    if (!group.value) return
    dispatchingAll.value = true
    try {
        await StopTaskGroup(group.value.id)
        await loadAll(group.value.id); messages.add({ text: t('taskGroup.allStopped'), color: 'info' })
    }
    catch (e: any) { messages.add({ text: t('taskGroup.allStopFailed', { error: String(e) }), color: 'error' }) }
    dispatchingAll.value = false
}

const dispatchableMacros = computed(() => macros.value.filter(m => m.purchaseGroups && m.purchaseGroups.length > 0))
const anyRunning = computed(() => dispatchableMacros.value.some(m => hasIntent(m)))
const groupStats = computed(() => {
    const groupIntents = intents.value.filter(i => i.armed && !i.succeeded)
    return {
        total: groupIntents.length,
        deficit: groupIntents.reduce((sum, i) => sum + (i.deficit || 0), 0),
        running: groupIntents.reduce((sum, i) => sum + (i.activeCount || 0), 0),
        succeeded: intents.value.filter(i => i.succeeded).length,
        failed: intents.value.filter(i => i.terminal && !i.succeeded).length,
    }
})
</script>

<template>
    <v-container>
        <v-row v-if="loading" justify="center" class="mt-6"><v-progress-circular indeterminate
                color="primary" /></v-row>
        <div v-else-if="group">
            <h1>{{ group.name }}</h1>
            <v-divider class="mt-2 mb-4" thickness="3" />

            <!-- Dispatch bar -->
            <v-card v-if="macros.length > 0" class="mb-4" elevation="2" color="surface-variant">
                <v-card-text class="py-2 px-4">
                    <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap">
                        <span class="text-subtitle-2">{{ t('taskGroup.dispatch') }}</span>
                        <v-chip v-if="groupStats.total > 0" size="small" variant="tonal" color="grey">{{
                            t('taskGroup.intents', { count: groupStats.total })
                            }}</v-chip>
                        <v-chip v-if="groupStats.deficit > 0" size="small" variant="tonal" color="warning">{{
                            t('taskGroup.queued', { count: groupStats.deficit }) }}</v-chip>
                        <v-chip v-if="groupStats.running > 0" size="small" color="info" variant="tonal">{{
                            t('taskGroup.running', { count: groupStats.running }) }}</v-chip>
                        <v-chip v-if="groupStats.succeeded > 0" size="small" color="success" variant="tonal">{{
                            t('taskGroup.succeeded', { count: groupStats.succeeded }) }}</v-chip>
                        <v-chip v-if="groupStats.failed > 0" size="small" color="error" variant="tonal">{{
                            t('taskGroup.failed', { count: groupStats.failed }) }}</v-chip>
                        <v-spacer />
                        <v-btn v-if="!anyRunning" prepend-icon="mdi-play-circle-outline" color="success" variant="tonal"
                            size="small" :loading="dispatchingAll" :disabled="dispatchableMacros.length === 0"
                            @click="startAllMacros">
                            {{ t('taskGroup.startAll') }}
                        </v-btn>
                        <v-btn v-else prepend-icon="mdi-stop-circle-outline" color="error" variant="tonal" size="small"
                            :loading="dispatchingAll" @click="stopAllMacros">
                            {{ t('taskGroup.stopAll') }}
                        </v-btn>
                    </div>
                </v-card-text>
            </v-card>

            <!-- Add Macro -->
            <v-card class="mb-4" elevation="2">
                <v-card-title class="text-subtitle-1">{{ t('taskGroup.addMacro') }}</v-card-title>
                <v-card-text>
                    <v-row dense>
                        <v-col cols="5"><v-text-field v-model="lookupProjectId" :label="t('taskGroup.projectIdLabel')"
                                :placeholder="t('taskGroup.projectIdPlaceholder')" variant="outlined" density="compact"
                                hide-details @keydown.enter="lookupProject" /></v-col>
                        <v-col cols="3" class="d-flex align-center"><v-btn :loading="lookupLoading" color="primary"
                                @click="lookupProject">{{ t('taskGroup.lookup') }}</v-btn></v-col>
                    </v-row>
                    <v-expand-transition>
                        <div v-if="projectInfo" class="mt-3">
                            <v-card class="mt-3 pa-4" elevation="2">
                                <v-card-title>{{ projectInfo.ProjectName }}<v-chip v-if="projectInfo.IsHotProject"
                                        color="error" size="small">{{ t('taskGroup.hot') }}</v-chip></v-card-title>
                                <v-card-text>
                                    <p><v-label>{{ t('taskGroup.projectId') }}:</v-label> {{ projectInfo.ProjectID }}
                                    </p>
                                    <p><v-label>{{ t('taskGroup.sale') }}:</v-label> {{ projectInfo.StartTime }} ~ {{
                                        projectInfo.EndTime }}</p>
                                    <p v-if="projectInfo.IsForceRealName"><v-label color="warning">{{
                                        t('taskGroup.realNameRequired') }}</v-label></p>
                                    <p v-if="projectInfo.contactRequired"><v-label color="warning">{{
                                        t('taskGroup.contactRequired') }}</v-label></p>
                                </v-card-text>
                            </v-card>
                            <!-- SKU list -->
                            <v-card v-if="tickets.length > 0" class="mt-3" elevation="2">
                                <v-card-title class="text-subtitle-1 pa-3 d-flex align-center" style="cursor:pointer"
                                    @click="showSkuList = !showSkuList">
                                    {{ t('taskGroup.tickets', { count: tickets.length }) }}<v-spacer />
                                    <v-icon class="sku-chevron" :class="{ 'sku-chevron--open': showSkuList }"
                                        size="small">mdi-chevron-down</v-icon>
                                </v-card-title>
                                <v-expand-transition>
                                    <div v-show="showSkuList">
                                        <v-card-text class="pb-1 pt-0 px-4"><v-text-field v-model="filterName"
                                                :label="t('taskGroup.filterPlaceholder')"
                                                prepend-inner-icon="mdi-magnify" variant="outlined" density="compact"
                                                hide-details clearable /></v-card-text>
                                        <v-list class="py-0">
                                            <v-list-group v-for="sc in filteredScreens" :key="sc.screenId"
                                                :value="'screen-' + sc.screenId">
                                                <template #activator="{ props: groupProps }">
                                                    <v-list-item v-bind="groupProps" class="px-4"
                                                        :title="sc.screenName">
                                                        <template #append>
                                                            <v-icon class="screen-chevron">mdi-chevron-down</v-icon>
                                                        </template>
                                                    </v-list-item>
                                                </template>
                                                <v-divider />
                                                <v-list-item v-for="t in sc.tickets" :key="`${t.screenId}-${t.skuId}`"
                                                    class="px-4 pl-8" style="cursor:pointer"
                                                    :active="selectedScreenId === t.screenId && selectedSkuId === t.skuId"
                                                    @click="selectedScreenId = t.screenId; selectedSkuId = t.skuId">
                                                    <template #title>
                                                        <div
                                                            style="display:flex;align-items:center;gap:4px;min-width:0">
                                                            <span class="text-body-2 text-truncate"
                                                                style="min-width:0">{{ t.desc || t.skuId }}</span>
                                                            <v-chip v-if="t.flags?.display_name" size="small"
                                                                variant="tonal" class="ml-1 flex-shrink-0"
                                                                :color="t.flags.display_name.includes('售罄') || t.flags.display_name.includes('停售') ? 'red' : t.flags.display_name.includes('未开') ? 'grey' : t.flags.display_name.includes('不可') ? 'yellow' : 'green'">
                                                                {{ t.flags.display_name }}
                                                            </v-chip>
                                                        </div>
                                                    </template>
                                                    <template #subtitle>
                                                        <span class="text-body-2">SKU:{{ t.skuId }} | ¥{{ ((t.price ||
                                                            0) / 100).toFixed(0) }}</span>
                                                    </template>
                                                    <template #append>
                                                        <v-icon class="sku-check-icon"
                                                            :class="{ 'sku-check-icon--selected': selectedScreenId === t.screenId && selectedSkuId === t.skuId }"
                                                            color="primary">mdi-check-circle</v-icon>
                                                    </template>
                                                </v-list-item>
                                            </v-list-group>
                                        </v-list>
                                    </div>
                                </v-expand-transition>
                            </v-card>
                            <v-row dense class="mt-3">
                                <v-col cols="12" md="6">
                                    <v-select :model-value="macroPrimaryWorkerIds"
                                        @update:model-value="onPrimaryWorkerSelectionChange" :items="workerList"
                                        item-title="name" item-value="id" :label="t('taskGroup.macroPrimaryWorkers')"
                                        variant="outlined" density="compact" multiple chips closable-chips
                                        :hint="t('taskGroup.macroPrimaryWorkersHint')" persistent-hint>
                                        <template #item="{ props, item }">
                                            <v-list-item v-bind="props"
                                                :subtitle="`${item.address} ${item.healthy ? '· ' + t('worker.online') : '· ' + t('worker.offline')}`" />
                                        </template>
                                    </v-select>
                                </v-col>
                                <v-col cols="12" md="6">
                                    <v-select :model-value="macroStandbyWorkerIds"
                                        @update:model-value="onStandbyWorkerSelectionChange" :items="workerList"
                                        item-title="name" item-value="id" :label="t('taskGroup.macroStandbyWorkers')"
                                        variant="outlined" density="compact" multiple chips closable-chips
                                        :hint="t('taskGroup.macroStandbyWorkersHint')" persistent-hint>
                                        <template #item="{ props, item }">
                                            <v-list-item v-bind="props"
                                                :subtitle="`${item.address} ${item.healthy ? '· ' + t('worker.online') : '· ' + t('worker.offline')}`" />
                                        </template>
                                    </v-select>
                                </v-col>
                            </v-row>
                            <v-btn class="mt-3" color="success" :loading="addingMacro"
                                :disabled="!selectedScreenId || !selectedSkuId" @click="addMacro">{{
                                    t('taskGroup.confirmAdd') }}</v-btn>
                        </div>
                    </v-expand-transition>
                </v-card-text>
            </v-card>

            <!-- Macro list -->
            <v-card elevation="2">
                <v-card-title class="text-subtitle-1">{{ t('taskGroup.macroList') }} ({{ macros.length
                }})</v-card-title>
                <template v-if="macros.length > 0">
                    <v-expansion-panels v-model="expandedMacro" variant="accordion"
                        @update:model-value="onMacroPanelChange">
                        <v-expansion-panel v-for="(m, idx) in macros" :key="m.id" :value="idx + 1">
                            <v-expansion-panel-title>
                                <div style="width:100%;min-width:0">
                                    <div style="display:flex;align-items:center;gap:6px;width:100%;min-width:0">
                                        <span class="text-truncate" style="min-width:0;flex-shrink:1">
                                            {{ m.projectName || '—' }}
                                        </span>
                                        <span class="text-caption text-medium-emphasis text-truncate"
                                            style="min-width:0;flex-shrink:1">{{ m.screenName ||
                                                m.screenId }}</span>
                                        <span class="text-caption text-medium-emphasis text-truncate"
                                            style="min-width:0;flex-shrink:1">{{ m.skuName || m.skuId }}</span>
                                        <v-chip v-if="hasIntent(m)" size="x-small" variant="tonal"
                                            :color="m.phase === 'reflow' ? 'warning' : 'info'"
                                            class="ml-1 flex-shrink-0">
                                            {{ m.phase === 'reflow' ? t('taskGroup.phaseReflow') :
                                                t('taskGroup.phaseRunning') }}
                                        </v-chip>
                                    </div>
                                    <div class="text-caption text-medium-emphasis text-truncate mt-2">{{
                                        t('taskGroup.eventDay') }}: {{
                                            m.eventDay || '—' }}</div>
                                    <div class="text-caption text-medium-emphasis text-truncate">{{
                                        t('taskGroup.saleTime') }}: {{ m.startAt || '—' }} ~ {{ m.deadline || '—' }}
                                    </div>
                                    <div v-if="(m.primaryWorkerIds || []).length > 0 || (m.standbyWorkerIds || []).length > 0"
                                        class="text-caption text-medium-emphasis text-truncate">
                                        {{ t('taskGroup.macroWorkers') }}:
                                        <span v-if="(m.primaryWorkerIds || []).length > 0">
                                            {{ t('taskGroup.macroPrimaryShort') }} {{ (m.primaryWorkerIds || []).length }}
                                        </span>
                                        <span v-if="(m.standbyWorkerIds || []).length > 0" class="ml-1">
                                            {{ t('taskGroup.macroStandbyShort') }} {{ (m.standbyWorkerIds || []).length }}
                                        </span>
                                    </div>
                                </div>
                                <template v-slot:actions>
                                    <v-btn icon="mdi-delete" size="medium" variant="text" color="error"
                                        :loading="deletingMacro[m.id]" @click.stop="removeMacro(m)" />
                                </template>
                            </v-expansion-panel-title>
                            <v-expansion-panel-text>
                                <!-- Dispatch stats -->
                                <v-card v-if="dispatchStats(m).total > 0" variant="outlined" class="mb-3 pa-2">
                                    <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
                                        <span class="text-caption text-medium-emphasis">{{ t('taskGroup.dispatchStatus')
                                        }}:</span>
                                        <v-chip v-if="dispatchStats(m).deficit > 0" size="x-small" variant="tonal"
                                            color="warning">{{
                                                t('taskGroup.queued', { count: dispatchStats(m).deficit }) }}</v-chip>
                                        <v-chip v-if="dispatchStats(m).running > 0" size="x-small" variant="tonal"
                                            color="info">{{
                                                t('taskGroup.running', { count: dispatchStats(m).running }) }}</v-chip>
                                        <v-chip v-if="dispatchStats(m).succeeded > 0" size="x-small" variant="tonal"
                                            color="success">{{
                                                t('taskGroup.succeeded', { count: dispatchStats(m).succeeded }) }}</v-chip>
                                        <v-chip v-if="dispatchStats(m).failed > 0" size="x-small" variant="tonal"
                                            color="error">{{
                                                t('taskGroup.failed', { count: dispatchStats(m).failed }) }}</v-chip>
                                        <span v-if="dispatchStats(m).deficit === 0 && dispatchStats(m).running === 0"
                                            class="text-caption text-medium-emphasis">—</span>
                                    </div>
                                    <!-- Intent list -->
                                    <v-list v-if="dispatchStats(m).intents.length > 0" density="compact"
                                        class="py-0 mt-1">
                                        <v-list-item v-for="i in dispatchStats(m).intents" :key="i.id" class="px-2"
                                            :density="'compact'">
                                            <template #title>
                                                <span class="text-caption">{{ i.id.slice(0, 12) }}…</span>
                                                <v-chip size="x-small" variant="outlined" class="ml-1" color="info">×{{
                                                    i.weight }}</v-chip>
                                                <v-chip v-if="i.priority !== 0" size="x-small" variant="outlined"
                                                    class="ml-1" :color="i.priority > 0 ? 'success' : 'warning'">P{{
                                                    i.priority }}</v-chip>
                                            </template>
                                            <template #append>
                                                <v-chip size="x-small" variant="tonal"
                                                    :color="i.deficit > 0 ? 'warning' : i.activeCount > 0 ? 'info' : 'grey'">
                                                    {{ i.activeCount }}/{{ i.weight }}
                                                    <span v-if="i.deficit > 0" class="ml-1">(-{{ i.deficit }})</span>
                                                </v-chip>
                                            </template>
                                        </v-list-item>
                                    </v-list>
                                </v-card>
                                <div class="mb-3">
                                    <v-label class="text-caption mb-1">{{ t('taskGroup.purchaseGroups') }} ({{
                                        (m.purchaseGroups || []).length
                                    }})</v-label>
                                    <v-card v-if="(m.purchaseGroups || []).length > 0" elevation="2" class="mb-2">
                                        <v-list density="compact" class="py-0">
                                            <template v-for="(pg, pgIdx) in (m.purchaseGroups || [])" :key="pg.id">
                                                <v-list-item class="px-2">
                                                    <template #title>
                                                        <v-chip v-for="b in (pg.buyers || [])" :key="b.logicalId"
                                                            size="small" variant="tonal" class="mr-1">{{
                                                                buyerByLogicalId(b.logicalId)?.name || b.name || b.logicalId
                                                            }}</v-chip>
                                                        <v-chip v-if="pg.allowSplit" color="primary" size="x-small"
                                                            variant="outlined" class="ml-1">{{
                                                                t('taskGroup.pgAllowSplit') }}</v-chip>
                                                        <v-chip v-if="(pg.weight || 1) !== 1" size="x-small"
                                                            variant="outlined" class="ml-1" color="info">
                                                            ×{{ pg.weight || 1 }}
                                                        </v-chip>
                                                        <v-chip v-if="(pg.priority || 0) !== 0" size="x-small"
                                                            variant="outlined" class="ml-1"
                                                            :color="(pg.priority || 0) > 0 ? 'success' : 'warning'">
                                                            P{{ pg.priority || 0 }}
                                                        </v-chip>
                                                    </template>
                                                    <template #append>
                                                        <v-tooltip :text="t('taskGroup.pgEdit')" location="top">
                                                            <template #activator="{ props: tipProps }">
                                                                <v-btn icon="mdi-pencil" size="x-small" variant="text"
                                                                    color="primary" class="mr-1" v-bind="tipProps"
                                                                    @click.stop="openEditPg(m.id, pg)" />
                                                            </template>
                                                        </v-tooltip>
                                                        <v-tooltip :text="t('taskGroup.pgDelete')" location="top">
                                                            <template #activator="{ props: tipProps }">
                                                                <v-btn icon="mdi-delete" size="small" variant="text"
                                                                    color="error" :loading="deletingPg[pg.id]"
                                                                    v-bind="tipProps"
                                                                    @click.stop="removePurchaseGroup(m.id, pg.id)" />
                                                            </template>
                                                        </v-tooltip>
                                                    </template>
                                                </v-list-item>
                                                <v-divider v-if="pgIdx < (m.purchaseGroups || []).length - 1" />
                                            </template>
                                        </v-list>
                                    </v-card>
                                    <p v-else class="text-caption text-medium-emphasis">{{ t('taskGroup.pgEmpty') }}</p>
                                </div>
                                <v-card variant="text"><v-card-text class="pa-0">
                                        <p class="text-caption mb-2">{{ t('taskGroup.pgAddHint') }}</p>
                                        <v-select v-if="allBuyers.length > 0" :model-value="selectedPgBuyerIds"
                                            @update:model-value="onBuyerSelectionChange" :items="allBuyers"
                                            item-title="name" item-value="logicalId"
                                            :label="`${t('taskGroup.pgSelectBuyerShort')} (${t('taskGroup.pgMaxLabel', { max: currentMacroOrderCapacity })})`"
                                            variant="outlined" density="compact" multiple chips closable-chips
                                            hide-details class="mb-2">
                                            <template #item="{ props, item }"><v-list-item v-bind="props"
                                                    :subtitle="`${item.tel || ''} · ${item.idCard || '—'}`" /></template>
                                        </v-select>
                                        <v-checkbox-btn v-model="allowSplit" color="primary" density="compact"
                                            :label="t('taskGroup.pgAllowSplitHint')" hide-details class="mb-2" />
                                        <v-row dense class="mb-2">
                                            <v-col cols="6">
                                                <v-text-field v-model="pgWeight" :label="t('taskGroup.pgWeight')"
                                                    type="number" min="1" variant="outlined" density="compact"
                                                    hide-details :hint="t('taskGroup.pgWeightHint')" persistent-hint />
                                            </v-col>
                                            <v-col cols="6">
                                                <v-text-field v-model="pgPriority" :label="t('taskGroup.pgPriority')"
                                                    type="number" variant="outlined" density="compact" hide-details
                                                    :hint="t('taskGroup.pgPriorityHint')" persistent-hint />
                                            </v-col>
                                        </v-row>
                                        <p v-if="allBuyers.length === 0" class="text-caption text-medium-emphasis mb-2">
                                            {{
                                                t('taskGroup.pgNoBuyers') }}</p>
                                        <v-btn color="primary" :loading="savingPg"
                                            :disabled="selectedPgBuyerIds.length === 0 || allBuyers.length === 0"
                                            @click="savePurchaseGroup(m)">{{
                                                editingPgId && editingPgMacroId === m.id ? t('taskGroup.pgSave') :
                                                    t('taskGroup.pgAdd')
                                            }}</v-btn>
                                        <v-btn v-if="editingPgId && editingPgMacroId === m.id" variant="text"
                                            class="ml-1" @click="cancelEditPg">{{ t('common.cancel')
                                            }}</v-btn>
                                    </v-card-text></v-card>
                            </v-expansion-panel-text>
                        </v-expansion-panel>
                    </v-expansion-panels>
                </template>
                <v-card-text v-else class="text-medium-emphasis text-center py-6">{{ t('taskGroup.emptyMacro')
                }}</v-card-text>
            </v-card>
        </div>
        <v-card v-else class="mt-4 pa-6 text-center" variant="outlined"><v-card-text>{{ t('taskGroup.notFound')
        }}</v-card-text></v-card>

        <!-- ═══ Add Macro Confirmation Dialog ═══ -->
        <v-dialog v-model="showAddConfirmDialog" max-width="460" persistent>
            <v-card class="pa-4">
                <v-card-title>{{ t('taskGroup.addMacroConfirmTitle') }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 mb-3">
                        {{ t('taskGroup.addMacroConfirmHint') }}
                    </p>
                    <v-card variant="outlined" class="pa-3 mb-3">
                        <div class="info-row">
                            <span class="info-label">{{ t('taskGroup.colProject') }}</span>
                            <span class="info-value">{{ addingMacroInfo?.projectName || '—' }}</span>
                        </div>
                        <div class="info-row">
                            <span class="info-label">{{ t('taskGroup.colScreen') }}</span>
                            <span class="info-value">{{ addingMacroInfo?.screenName || '—' }}</span>
                        </div>
                        <div class="info-row">
                            <span class="info-label">{{ t('taskGroup.colSku') }}</span>
                            <span class="info-value">{{ addingMacroInfo?.skuName || '—' }}</span>
                        </div>
                        <div class="info-row">
                            <span class="info-label">{{ t('taskGroup.eventDay') }}</span>
                            <span class="info-value">{{ eventDayHumanized.prefix }}<span
                                    style="color:rgb(var(--v-theme-error));font-weight:700">{{ eventDayHumanized.day
                                    }}</span>{{
                                        eventDayHumanized.weekPrefix }}<span
                                    style="color:rgb(var(--v-theme-error));font-weight:700">{{
                                        eventDayHumanized.weekDay }}</span></span>
                        </div>
                        <div class="info-row">
                            <span class="info-label">{{ t('taskGroup.price') }}</span>
                            <span class="info-value">¥{{ addingMacroInfo?.price?.toFixed(0) || '—' }}</span>
                        </div>
                        <div class="info-row" v-if="addingMacroInfo?.isRealName">
                            <span class="info-label"></span>
                            <v-chip color="warning" size="x-small" variant="tonal">{{ t('taskGroup.realName')
                            }}</v-chip>
                        </div>
                    </v-card>
                    <p class="text-caption text-medium-emphasis">
                        {{ t('taskGroup.addMacroConfirmNote') }}
                    </p>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="cancelAddMacro">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="success" :loading="addingMacro" :disabled="!addingMacroInfo?.eventDay"
                        @click="confirmAddMacro">
                        {{ t('taskGroup.addMacroConfirmBtn') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
        <!-- ═══ Worker Selection Dialog ═══ -->
        <v-dialog v-model="showWorkerSelectDialog" max-width="480" persistent>
            <v-card class="pa-4">
                <v-card-title>{{ t('taskGroup.selectWorkersTitle') }}</v-card-title>
                <v-card-text>
                    <p class="text-body-2 text-medium-emphasis mb-3">
                        {{ t('taskGroup.selectWorkersHint') }}
                    </p>
                    <v-select v-model="selectedWorkerIds" :items="workerList" item-title="name" item-value="id"
                        :label="t('taskGroup.selectedWorkers', { count: selectedWorkerIds.length })" variant="outlined"
                        multiple chips closable-chips class="mb-3">
                        <template #item="{ props, item }">
                            <v-list-item v-bind="props"
                                :subtitle="`${item.address} ${item.healthy ? '· ' + t('worker.online') : '· ' + t('worker.offline')}`">
                                <template #append>
                                    <v-chip :color="item.healthy ? 'success' : 'error'" size="x-small" variant="tonal">
                                        {{ item.healthy ? t('worker.online') : t('worker.offline') }}
                                    </v-chip>
                                </template>
                            </v-list-item>
                        </template>
                    </v-select>
                    <p class="text-caption text-medium-emphasis">
                        {{ t('taskGroup.selectWorkersNote') }}
                    </p>
                </v-card-text>
                <v-card-actions>
                    <v-spacer />
                    <v-btn variant="text" @click="showWorkerSelectDialog = false">{{ t('common.cancel') }}</v-btn>
                    <v-btn color="success" :disabled="selectedWorkerIds.length === 0" @click="confirmStartTaskGroup">
                        {{ t('taskGroup.startAll') }}
                    </v-btn>
                </v-card-actions>
            </v-card>
        </v-dialog>
    </v-container>
</template>

<style scoped>
.sku-chevron {
    transition: transform 0.2s ease;
}

.sku-chevron--open {
    transform: rotate(180deg);
}

.screen-chevron {
    transition: transform 0.2s ease;
}

:deep(.v-list-group--open .screen-chevron) {
    transform: rotate(180deg);
}

.sku-check-icon {
    opacity: 0;
    transform: scale(0.5);
    transition: opacity 0.15s ease, transform 0.15s ease;
}

.sku-check-icon--selected {
    opacity: 1;
    transform: scale(1);
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
    white-space: nowrap;
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

.info-value--scroll {
    text-overflow: unset;
    overflow-x: auto;
}
</style>
