<script lang="ts" setup>
import { ref, watch, computed, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import WorkerPicker from '@/components/cluster/WorkerPicker.vue'
import AccountPicker from '@/components/cluster/AccountPicker.vue'
import BuyerPicker from '@/components/cluster/BuyerPicker.vue'
import type { SearchableBuyer } from '@/composables/buyerSearch'
import { Snapshot, SaveMacro, DeleteMacro, SavePurchaseGroup, DeletePurchaseGroup, StopTaskGroup, ForceStopTaskGroup, ForceRestartTaskGroup, StartTaskGroup, StopIntent, SaveTaskGroup } from '../../../bindings/bilibili-ticket-golang/cmd/gui/cluster_service/clusterservice'
import { GetProjectInformationNew, GetTicketSkuIDsByProjectIDNew } from '../../../bindings/bilibili-ticket-golang/lib/biliutils/biliclient'

const route = useRoute(); const { t, locale } = useI18n(); const messages = useMessagesStore()
const START_REFLOW_NOW_TOKEN = '__cluster_reflow_now__'

interface TaskGroupSummary { id: string; name: string; accountIds?: string[]; primaryWorkerIds?: string[]; standbyWorkerIds?: string[]; paymentTimeoutMinutes?: number; waveDurationMinutes?: number; maxWaves?: number; reflowStockCheck?: boolean; createdAt?: string }
interface AccountBrief { id: string; name: string; enabled: boolean; vipStatus?: number; tags?: string[] }
interface WorkerBrief { id: string; name: string; address: string; type: string; healthy: boolean; tags?: string[] }
interface BuyerBrief extends SearchableBuyer { logicalId: string; name: string; idCard: string; tel: string }
interface MacroSummary { id: string; taskGroupId: string; projectId: number; projectName?: string; screenId: number; screenName?: string; skuId: number; skuName?: string; eventDay: string; needsReview: boolean; orderCapacity: number; startAt: string; deadline: string; phase?: string; purchaseGroups?: any[] }
interface AttemptBrief { id: string; intentId: string; accountId: string; workerId: string; state: string; orderId?: string }
interface IntentBrief { id: string; macroTaskId: string; purchaseGroupId?: string; phase: string; weight: number; priority: number; buyerCount: number; succeeded: boolean; terminal: boolean; armed: boolean; activeCount: number; deficit: number; failureReason?: string }

const group = ref<TaskGroupSummary | null>(null); const macros = ref<MacroSummary[]>([]); const attempts = ref<AttemptBrief[]>([]); const intents = ref<IntentBrief[]>([]); const loading = ref(true)
const dispatching = ref<Record<string, boolean>>({}); const dispatchingAll = ref(false)
const activeTaskGroup = ref('')
const accountList = ref<AccountBrief[]>([])
const groupAccountIds = ref<string[]>([])
const workerList = ref<WorkerBrief[]>([])
const groupPrimaryWorkerIds = ref<string[]>([])
const groupStandbyWorkerIds = ref<string[]>([])
const groupPaymentTimeoutMinutes = ref(10)
const groupWaveDurationMinutes = ref(3)
const groupMaxWaves = ref(3)
const groupReflowStockCheck = ref(false)
const groupConfigDirty = ref(false)
const savingGroupConfig = ref(false)
const lookupProjectId = ref(''); const lookupLoading = ref(false); const projectInfo = ref<any>(null); const tickets = ref<any[]>([])
const selectedScreenId = ref(0); const selectedSkuId = ref(0); const addingMacro = ref(false); const showSkuList = ref(false)
const customStartAt = ref('') // user-defined override for macro start time (ISO datetime)
const customStartRef = ref<any>(null)
function openDatetimePicker() {
    ; (customStartRef.value?.$el?.querySelector('input') as HTMLInputElement)?.showPicker()
}
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
const expandedMacro = ref(0); const allBuyers = ref<BuyerBrief[]>([])
const selectedPgBuyerIds = ref<string[]>([]); const savedPgBuyerIds = ref(new Map<string, string[]>()); const savingPg = ref(false); const deletingPg = ref<Record<string, boolean>>({})
const editingPgId = ref(''); const editingPgMacroId = ref('')
const allowSplit = ref(false)
const pgWeight = ref<number | string>(1)
const pgPriority = ref<number | string>(0)

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

function onGroupPrimaryWorkerSelectionChange(vals: string[]) {
    groupPrimaryWorkerIds.value = vals
    groupStandbyWorkerIds.value = groupStandbyWorkerIds.value.filter(id => !vals.includes(id))
    groupConfigDirty.value = true
}

function onGroupStandbyWorkerSelectionChange(vals: string[]) {
    groupStandbyWorkerIds.value = vals.filter(id => !groupPrimaryWorkerIds.value.includes(id))
    groupConfigDirty.value = true
}

function onGroupAccountSelectionChange(vals: string[]) {
    groupAccountIds.value = vals
    groupConfigDirty.value = true
}

function markGroupConfigDirty() {
    groupConfigDirty.value = true
}

function syncGroupDraft(nextGroup: TaskGroupSummary | null, force = false) {
    if (!nextGroup) return
    if (!force && groupConfigDirty.value && activeTaskGroup.value !== nextGroup.id) return
    groupAccountIds.value = [...(nextGroup.accountIds || [])]
    groupPrimaryWorkerIds.value = [...(nextGroup.primaryWorkerIds || [])]
    groupStandbyWorkerIds.value = [...(nextGroup.standbyWorkerIds || [])].filter(id => !groupPrimaryWorkerIds.value.includes(id))
    groupPaymentTimeoutMinutes.value = nextGroup.paymentTimeoutMinutes || 10
    groupWaveDurationMinutes.value = nextGroup.waveDurationMinutes || 3
    groupMaxWaves.value = nextGroup.maxWaves || 3
    groupReflowStockCheck.value = !!nextGroup.reflowStockCheck
    groupConfigDirty.value = false
}

async function loadAll(id: string, silent = false) {
    if (!silent) loading.value = true
    if (!silent) { group.value = null; macros.value = []; intents.value = []; attempts.value = [] }
    try {
        const snap = await Snapshot() as any
        const nextGroup = ((snap.taskGroups || []) as TaskGroupSummary[]).find(g => g.id === id) || null
        group.value = nextGroup
        activeTaskGroup.value = snap.activeTaskGroup || ''
        const allMacros = ((snap.macros || []) as MacroSummary[]).filter(m => m.taskGroupId === id)
        macros.value = allMacros
        intents.value = ((snap.intents || []) as IntentBrief[]).filter(i => allMacros.some(m => m.id === i.macroTaskId))
        attempts.value = ((snap.attempts || []) as AttemptBrief[])
        accountList.value = ((snap.accounts || []) as AccountBrief[])
        workerList.value = ((snap.workers || []) as WorkerBrief[])
        syncGroupDraft(nextGroup, !silent)
        if (allBuyers.value.length === 0) {
            allBuyers.value = ((snap.buyers || []) as any[]).map((b: any) => ({ logicalId: b.logicalId, name: b.name || '', idCard: b.idCard || '', tel: b.tel || '', accounts: b.accounts || [] }))
        }
    } catch { }
    if (!silent) loading.value = false
}

// ── Auto-polling ──────────────────────────────────────────────
let pollTimer: ReturnType<typeof setInterval> | null = null
const currentGroupId = ref('')
const POLL_INTERVAL_MS = 5000

function startPolling(groupId: string) {
    stopPolling()
    currentGroupId.value = groupId
    pollTimer = setInterval(() => {
        if (currentGroupId.value) {
            loadAll(currentGroupId.value, true)
        }
    }, POLL_INTERVAL_MS)
}
function stopPolling() {
    if (pollTimer !== null) {
        clearInterval(pollTimer)
        pollTimer = null
    }
    currentGroupId.value = ''
}
onUnmounted(stopPolling)

watch(() => route.params.id, (newId) => { if (newId) { loadAll(newId as string); startPolling(newId as string) } else { stopPolling() } }, { immediate: true })

/** The currently selected ticket in the SKU list. */
const selectedTicket = computed(() => {
    if (!selectedScreenId.value || !selectedSkuId.value) return null
    return (tickets.value as any[]).find((t: any) => t.screenId === selectedScreenId.value && t.skuId === selectedSkuId.value) || null
})

function activeLocale() {
    const loc = String(locale.value || '')
    return loc === 'en' ? 'en-US' : loc || 'zh-CN'
}

function parseDateInput(value: string | undefined, dateOnly = false) {
    if (!value) return null
    const raw = String(value).trim()
    if (!raw) return null
    const normalized = dateOnly && /^\d{4}-\d{2}-\d{2}$/.test(raw) ? `${raw}T00:00:00` : raw
    const date = new Date(normalized)
    return Number.isNaN(date.getTime()) ? null : date
}

function formatEventDay(value: string | undefined) {
    const date = parseDateInput(value, true)
    if (!date) return value || '—'
    return new Intl.DateTimeFormat(activeLocale(), {
        year: 'numeric',
        month: 'long',
        day: 'numeric',
        weekday: 'short',
    }).format(date)
}

function formatDateTime(value: string | undefined) {
    const date = parseDateInput(value)
    if (!date) return value || '—'
    return new Intl.DateTimeFormat(activeLocale(), {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
    }).format(date)
}

function formatDateTimeRange(start: string | undefined, end: string | undefined) {
    return `${formatDateTime(start)} ~ ${formatDateTime(end)}`
}

function buyerIdTail(b: any) {
    const idCard = String(b?.idCard || '').replace(/\s+/g, '')
    return idCard ? idCard.slice(-4) : ''
}

function buyerDisplayName(b: any) {
    const buyer = b?.logicalId ? (buyerByLogicalId(b.logicalId) || b) : b
    const name = buyer?.name || b?.name || buyer?.logicalId || b?.logicalId || '—'
    const tail = buyerIdTail(buyer)
    return tail ? `${name} · ${tail}` : name
}

function buyerDisplayNameFullId(b: any) {
    const buyer = b?.logicalId ? (buyerByLogicalId(b.logicalId) || b) : b
    const name = buyer?.name || b?.name || buyer?.logicalId || b?.logicalId || '—'
    const idCard = String(buyer?.idCard || '').replace(/\s+/g, '')
    return idCard ? `${name} · ${idCard}` : name
}

function buyerSubtitle(b: any) {
    const buyer = b?.logicalId ? (buyerByLogicalId(b.logicalId) || b) : b
    const tel = buyer?.tel || ''
    const tail = buyerIdTail(buyer)
    if (tel && tail) return `${tel} · 证件尾号 ${tail}`
    if (tail) return `证件尾号 ${tail}`
    return tel || '—'
}

function purchaseGroupBuyerNames(pg: any) {
    const names = (pg.buyers || []).map((b: any) => buyerDisplayName(b)).filter(Boolean)
    return names.length > 0 ? names.join('、') : '—'
}

/** When the user picks a ticket, sync its sale start into the picker. */
watch(selectedTicket, (t) => {
    const start = t?.saleStat?.start || ''
    if (start) {
        const d = new Date(start)
        if (!isNaN(d.getTime())) customStartAt.value = formatDatetimeLocal(d)
    } else {
        customStartAt.value = ''
    }
})

async function lookupProject() { const pid = lookupProjectId.value.trim(); if (!pid) { messages.add({ text: t('taskGroup.projectIdRequired'), color: 'warning' }); return } lookupLoading.value = true; projectInfo.value = null; tickets.value = []; selectedScreenId.value = 0; selectedSkuId.value = 0; try { const [info, tks] = await Promise.all([GetProjectInformationNew(pid), GetTicketSkuIDsByProjectIDNew(pid)]); if (!info) messages.add({ text: t('taskGroup.projectNotFound'), color: 'warning' }); else { projectInfo.value = info; tickets.value = tks || [] } } catch (e: any) { messages.add({ text: t('taskGroup.lookupFailed', { error: String(e) }), color: 'error' }) } lookupLoading.value = false }

async function addMacro() { if (!projectInfo.value || !selectedScreenId.value || !selectedSkuId.value || !group.value) return; const ticket = selectedTicket.value; addingMacroInfo.value = { projectName: projectInfo.value.ProjectName || '', eventDay: ticket?.eventTime || projectInfo.value.StartTime || '', screenName: ticket?.name || '', skuName: ticket?.desc || '', price: ((ticket?.price || 0) / 100), buyLimit: ticket?.buyLimit || 1, saleStart: customStartAt.value || ticket?.saleStat?.start || '', saleEnd: ticket?.saleStat?.end || '', isRealName: projectInfo.value.IsForceRealName || false }; showAddConfirmDialog.value = true }

/** Format a Date as YYYY-MM-DDTHH:mm:ss for &lt;input type="datetime-local" step="1"&gt;. */
function formatDatetimeLocal(d: Date): string {
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

async function confirmAddMacro() {
    if (!addingMacroInfo.value || !group.value) return
    addingMacro.value = true
    showAddConfirmDialog.value = false
    const info = addingMacroInfo.value
    const ticket = tickets.value.find((t: any) => t.screenId === selectedScreenId.value && t.skuId === selectedSkuId.value)
    // Use custom start time if provided, otherwise fall back to the project sale start.
    const startAt = customStartAt.value ? new Date(customStartAt.value).toISOString() : (ticket?.saleStat?.start || '')
    try { await SaveMacro(JSON.stringify({ id: randomId('macro'), taskGroupId: group.value.id, projectId: Number(projectInfo.value!.ProjectID), projectName: projectInfo.value!.ProjectName || '', screenId: selectedScreenId.value, screenName: ticket?.name || '', skuId: selectedSkuId.value, skuName: ticket?.desc || '', eventDay: info.eventDay, eventDayConfirmed: true, needsReview: false, orderCapacity: ticket?.buyLimit || 1, startAt, deadline: ticket?.saleStat?.end || '' })); projectInfo.value = null; selectedScreenId.value = 0; selectedSkuId.value = 0; lookupProjectId.value = ''; customStartAt.value = ''; await loadAll(group.value!.id); messages.add({ text: t('taskGroup.macroAdded'), color: 'success' }) } catch (e: any) { messages.add({ text: t('taskGroup.macroAddFailed', { error: String(e) }), color: 'error' }) }
    addingMacro.value = false
    addingMacroInfo.value = null
}

function cancelAddMacro() {
    showAddConfirmDialog.value = false
    addingMacroInfo.value = null
    customStartAt.value = ''
}

async function removeMacro(m: MacroSummary) { deletingMacro.value[m.id] = true; try { await DeleteMacro(m.id); await loadAll(group.value!.id); messages.add({ text: t('taskGroup.macroDeleted'), color: 'success' }) } catch (e: any) { messages.add({ text: t('taskGroup.macroDeleteFailed', { error: String(e) }), color: 'error' }) } deletingMacro.value[m.id] = false }

async function savePurchaseGroup(m: MacroSummary) {
    if (selectedPgBuyerIds.value.length === 0) { messages.add({ text: t('taskGroup.pgSelectBuyer'), color: 'warning' }); return }
    const isEdit = !!editingPgId.value && editingPgMacroId.value === m.id
    savingPg.value = true
    try {
        const buyers = selectedPgBuyerIds.value.map(id => { const b = allBuyers.value.find(x => x.logicalId === id)!; return { logicalId: id, name: b.name, idCard: b.idCard, tel: b.tel } })
        await SavePurchaseGroup(JSON.stringify({ id: isEdit ? editingPgId.value : '', macroTaskId: m.id, buyers, allowSplit: allowSplit.value, weight: normalizeInt(pgWeight.value, 1, 1), priority: normalizeInt(pgPriority.value, 0) }))
        editingPgId.value = ''; editingPgMacroId.value = ''
        allowSplit.value = false
        pgWeight.value = 1
        pgPriority.value = 0
        const restored = isEdit ? [...(savedPgBuyerIds.value.get(m.id) || [])] : []
        savedPgBuyerIds.value.set(m.id, [])
        selectedPgBuyerIds.value = restored
        await loadAll(group.value!.id)
        messages.add({ text: isEdit ? t('taskGroup.pgUpdated') : t('taskGroup.pgAdded'), color: 'success' })
    } catch (e: any) { messages.add({ text: isEdit ? t('taskGroup.pgUpdateFailed', { error: String(e) }) : t('taskGroup.pgAddFailed', { error: String(e) }), color: 'error' }) }
    savingPg.value = false
}

function normalizeInt(value: unknown, fallback: number, min?: number): number {
    const n = Number(value)
    if (!Number.isFinite(n)) return fallback
    const i = Math.trunc(n)
    if (min !== undefined && i < min) return min
    return i
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

/** 按权重降序、优先级降序排列购票组，反映实际调度顺序 */
function sortedPurchaseGroups(m: MacroSummary) {
    const groups = [...(m.purchaseGroups || [])]
    groups.sort((a, b) => {
        const wa = a.weight || 1, wb = b.weight || 1
        if (wa !== wb) return wb - wa
        const pa = a.priority || 0, pb = b.priority || 0
        if (pa !== pb) return pb - pa
        return (a.id || '').localeCompare(b.id || '')
    })
    return groups
}

async function loadBuyersOnce() { if (allBuyers.value.length > 0) return; try { const snap = await Snapshot(); allBuyers.value = ((snap.buyers || []) as any[]).map((b: any) => ({ logicalId: b.logicalId, name: b.name || '', idCard: b.idCard || '', tel: b.tel || '', accounts: b.accounts || [] })) } catch { } }

function randomId(prefix: string) { const arr = new Uint8Array(6); crypto.getRandomValues(arr); return prefix + '-' + Array.from(arr).map(b => b.toString(16).padStart(2, '0')).join('') }

// ── Dispatch ─────────────────────────────────────────────────
const macroIntents = (m: MacroSummary) => intents.value.filter(i => i.macroTaskId === m.id)
const hasIntent = (m: MacroSummary) => macroIntents(m).length > 0
const hasLiveIntent = (m: MacroSummary) => macroIntents(m).some(i => i.armed && !i.terminal && !i.succeeded)
const isRunning = (m: MacroSummary) => (dispatching.value[m.id] || hasIntent(m))

// ── Task status helpers ──────────────────────────────────────
/** Whether the macro's StartAt has passed but it hasn't been dispatched yet */
const isPendingAutoStart = (m: MacroSummary): boolean => {
    if (m.needsReview) return false
    if (hasIntent(m)) return false // already dispatched
    const startAt = Number.isFinite(Date.parse(m.startAt)) ? new Date(m.startAt).getTime() : 0
    if (startAt === 0 || Date.now() < startAt) return false
    const deadline = Number.isFinite(Date.parse(m.deadline)) ? new Date(m.deadline).getTime() : 0
    if (deadline > 0 && Date.now() > deadline) return false
    return true
}

/** Reasons why a macro hasn't auto-started even though StartAt has passed */
const startBlockers = (m: MacroSummary): string[] => {
    const blockers: string[] = []
    if (workerList.value.filter(w => w.healthy).length === 0) {
        blockers.push(t('taskGroup.blockerNoWorkers'))
    }
    // Check if any buyer in purchase groups has been synced to any account
    const pgs = m.purchaseGroups || []
    if (pgs.length > 0) {
        let hasMappedBuyer = false
        for (const pg of pgs) {
            for (const b of (pg.buyers || [])) {
                const found = allBuyers.value.find(x => x.logicalId === b.logicalId)
                if (found && (found as any).accounts && (found as any).accounts.length > 0) {
                    hasMappedBuyer = true
                    break
                }
            }
            if (hasMappedBuyer) break
        }
        if (!hasMappedBuyer) {
            blockers.push(t('taskGroup.blockerNoBuyerMapping'))
        }
    }
    return blockers
}

/** Human-readable status label for a macro */
const macroStatusLabel = (m: MacroSummary): string => {
    if (hasIntent(m)) {
        const ds = dispatchStats(m)
        if (macroIntents(m).some(i => i.phase === 'reflow')) return t('taskGroup.phaseReflow')
        if (ds.succeeded > 0) return t('taskGroup.succeeded', { count: ds.succeeded })
        if (ds.running > 0) return t('taskGroup.running', { count: ds.running })
        if (ds.deficit > 0) return t('taskGroup.queued', { count: ds.deficit })
        return t('taskGroup.phaseRunning')
    }
    if (groupRunning.value) return t('taskGroup.waitingNextWave')
    if (isPendingAutoStart(m)) return t('taskGroup.pendingAutoStart')
    return t('taskGroup.pendingConfig')
}

/** Color for macro status chip */
const macroStatusColor = (m: MacroSummary): string => {
    if (hasIntent(m)) {
        if (macroIntents(m).some(i => i.phase === 'reflow')) return 'warning'
        const ds = dispatchStats(m)
        if (ds.succeeded > 0) return 'success'
        if (ds.running > 0) return 'info'
        if (ds.deficit > 0) return 'warning'
        return 'info'
    }
    if (groupRunning.value) return 'info'
    if (isPendingAutoStart(m)) return 'warning'
    return 'grey'
}

function dispatchStats(m: MacroSummary) {
    const macroIntents = intents.value.filter(i => i.macroTaskId === m.id && i.armed && !i.terminal && !i.succeeded)
    const running = macroIntents.reduce((sum, i) => sum + (i.activeCount || 0), 0)
    const deficit = macroIntents.reduce((sum, i) => sum + (i.deficit || 0), 0)
    const succeeded = intents.value.filter(i => i.macroTaskId === m.id && i.succeeded).length
    const failed = intents.value.filter(i => i.macroTaskId === m.id && i.terminal && !i.succeeded).length
    return { running, deficit, succeeded, failed, total: macroIntents.length, intents: macroIntents }
}

/** Per-purchase-group dispatch stats: running/queued/succeeded/failed */
function pgStats(m: MacroSummary, pgId: string) {
    const pgIntents = intents.value.filter(i => i.macroTaskId === m.id && i.purchaseGroupId === pgId && i.armed && !i.terminal && !i.succeeded)
    const running = pgIntents.reduce((sum, i) => sum + (i.activeCount || 0), 0)
    const deficit = pgIntents.reduce((sum, i) => sum + (i.deficit || 0), 0)
    const succeeded = intents.value.filter(i => i.macroTaskId === m.id && i.purchaseGroupId === pgId && i.succeeded).length
    const failed = intents.value.filter(i => i.macroTaskId === m.id && i.purchaseGroupId === pgId && i.terminal && !i.succeeded).length
    const total = pgIntents.length + succeeded + failed
    return { running, deficit, succeeded, failed, total, intents: pgIntents }
}


async function saveGroupConfig() {
    if (!group.value) return
    savingGroupConfig.value = true
    try {
        await SaveTaskGroup(JSON.stringify({
            ...group.value,
            accountIds: groupAccountIds.value,
            primaryWorkerIds: groupPrimaryWorkerIds.value,
            standbyWorkerIds: groupStandbyWorkerIds.value,
            paymentTimeoutMinutes: Number(groupPaymentTimeoutMinutes.value) || 10,
            waveDurationMinutes: Number(groupWaveDurationMinutes.value) || 3,
            maxWaves: Number(groupMaxWaves.value) || 3,
            reflowStockCheck: groupReflowStockCheck.value,
        }))
        groupConfigDirty.value = false
        await loadAll(group.value.id)
        messages.add({ text: t('taskGroup.groupConfigSaved'), color: 'success' })
    } catch (e: any) {
        messages.add({ text: t('taskGroup.groupConfigSaveFailed', { error: String(e) }), color: 'error' })
    }
    savingGroupConfig.value = false
}

async function startAllMacros() {
    if (!group.value) return
    if (groupConfigDirty.value) {
        await saveGroupConfig()
        if (groupConfigDirty.value) return
    }
    if (groupPrimaryWorkerIds.value.length + groupStandbyWorkerIds.value.length === 0) {
        messages.add({ text: t('taskGroup.noWorkersConfigured'), color: 'warning' })
        return
    }
    if (groupAccountIds.value.length === 0) {
        messages.add({ text: t('taskGroup.noAccountsConfigured'), color: 'warning' })
        return
    }
    dispatchingAll.value = true
    try {
        await StartTaskGroup(group.value.id, '')
        await loadAll(group.value.id); messages.add({ text: t('taskGroup.allStarted'), color: 'success' })
    }
    catch (e: any) { messages.addError(e, t('taskGroup.allStartFailed', { error: String(e) })) }
    dispatchingAll.value = false
}

async function startReflowNow() {
    if (!group.value) return
    if (groupConfigDirty.value) {
        await saveGroupConfig()
        if (groupConfigDirty.value) return
    }
    if (groupPrimaryWorkerIds.value.length + groupStandbyWorkerIds.value.length === 0) {
        messages.add({ text: t('taskGroup.noWorkersConfigured'), color: 'warning' })
        return
    }
    if (groupAccountIds.value.length === 0) {
        messages.add({ text: t('taskGroup.noAccountsConfigured'), color: 'warning' })
        return
    }
    dispatchingAll.value = true
    try {
        await StartTaskGroup(group.value.id, START_REFLOW_NOW_TOKEN)
        await loadAll(group.value.id)
        messages.add({ text: t('taskGroup.reflowNowStarted'), color: 'success' })
    }
    catch (e: any) { messages.addError(e, t('taskGroup.reflowNowFailed', { error: String(e) })) }
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

async function forceStopAllMacros() {
    if (!group.value) return
    dispatchingAll.value = true
    try {
        await ForceStopTaskGroup(group.value.id)
        await loadAll(group.value.id); messages.add({ text: t('taskGroup.allForceStopped'), color: 'info' })
    }
    catch (e: any) { messages.add({ text: t('taskGroup.forceStopFailed', { error: String(e) }), color: 'error' }) }
    dispatchingAll.value = false
}

async function forceRestartAllMacros() {
    if (!group.value) return
    if (groupConfigDirty.value) {
        await saveGroupConfig()
        if (groupConfigDirty.value) return
    }
    if (groupPrimaryWorkerIds.value.length + groupStandbyWorkerIds.value.length === 0) {
        messages.add({ text: t('taskGroup.noWorkersConfigured'), color: 'warning' })
        return
    }
    if (groupAccountIds.value.length === 0) {
        messages.add({ text: t('taskGroup.noAccountsConfigured'), color: 'warning' })
        return
    }
    dispatchingAll.value = true
    try {
        await ForceRestartTaskGroup(group.value.id, '')
        await loadAll(group.value.id); messages.add({ text: t('taskGroup.allForceRestarted'), color: 'success' })
    }
    catch (e: any) { messages.add({ text: t('taskGroup.forceRestartFailed', { error: String(e) }), color: 'error' }) }
    dispatchingAll.value = false
}

async function stopSingleIntent(intentID: string) {
    if (!group.value) return
    try {
        await StopIntent(intentID)
        await loadAll(group.value.id, true)
    }
    catch (e: any) { messages.add({ text: t('taskGroup.stopIntentFailed', { error: String(e) }), color: 'error' }) }
}

const dispatchableMacros = computed(() => macros.value.filter(m => m.purchaseGroups && m.purchaseGroups.length > 0))
const anyRunning = computed(() => dispatchableMacros.value.some(m => hasLiveIntent(m)))
const groupRunning = computed(() => anyRunning.value || (!!group.value && activeTaskGroup.value === group.value.id))
const editingDisabled = computed(() => groupRunning.value || dispatchingAll.value)
const accountPoolConfigured = computed(() => groupAccountIds.value.length > 0)
const workerPoolConfigured = computed(() => groupPrimaryWorkerIds.value.length + groupStandbyWorkerIds.value.length > 0)
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

/** All purchase groups across all macros with per-PG stats */
const allPurchaseGroups = computed(() => {
    const result: Array<{ macro: MacroSummary; pg: any; stats: ReturnType<typeof pgStats> }> = []
    for (const m of macros.value) {
        for (const pg of (m.purchaseGroups || [])) {
            const stats = pgStats(m, pg.id)
            if (stats.total > 0) {
                result.push({ macro: m, pg, stats })
            }
        }
    }
    return result
})
</script>

<template>
    <v-container>
        <v-row v-if="loading" justify="center" class="mt-6"><v-progress-circular indeterminate
                color="primary" /></v-row>
        <div v-else-if="group">
            <div class="page-title-bar">
                <h1 class="page-title">{{ group.name }}</h1>
            </div>

            <!-- Task group scheduling config -->
            <v-card class="mb-4" elevation="2">
                <v-card-title class="text-subtitle-1 d-flex align-center">
                    {{ t('taskGroup.groupConfig') }}
                    <v-spacer />
                    <v-chip v-if="groupRunning" color="info" size="small" variant="tonal">
                        {{ t('taskGroup.groupRunningLocked') }}
                    </v-chip>
                </v-card-title>
                <v-card-text>
                    <v-row dense>
                        <v-col cols="12">
                            <AccountPicker :model-value="groupAccountIds"
                                @update:model-value="onGroupAccountSelectionChange" :accounts="accountList"
                                :label="t('taskGroup.groupAccounts')" :hint="t('taskGroup.groupAccountsHint')"
                                :disabled="editingDisabled" />
                        </v-col>
                        <v-col cols="12" md="6">
                            <WorkerPicker :model-value="groupPrimaryWorkerIds"
                                @update:model-value="onGroupPrimaryWorkerSelectionChange" :workers="workerList"
                                :label="t('taskGroup.groupPrimaryWorkers')"
                                :hint="t('taskGroup.groupPrimaryWorkersHint')" :disabled="editingDisabled" />
                        </v-col>
                        <v-col cols="12" md="6">
                            <WorkerPicker :model-value="groupStandbyWorkerIds"
                                @update:model-value="onGroupStandbyWorkerSelectionChange" :workers="workerList"
                                :label="t('taskGroup.groupStandbyWorkers')"
                                :hint="t('taskGroup.groupStandbyWorkersHint')" :disabled="editingDisabled" />
                        </v-col>
                    </v-row>
                    <v-row dense class="mt-4">
                        <v-col cols="12" md="4">
                            <v-text-field v-model.number="groupPaymentTimeoutMinutes"
                                :label="t('taskGroup.paymentTimeoutMinutes')" type="number" min="1" variant="outlined"
                                density="compact" :disabled="editingDisabled"
                                @update:model-value="markGroupConfigDirty" />
                        </v-col>
                        <v-col cols="12" md="4">
                            <v-text-field v-model.number="groupWaveDurationMinutes"
                                :label="t('taskGroup.waveDurationMinutes')" type="number" min="1" variant="outlined"
                                density="compact" :disabled="editingDisabled"
                                @update:model-value="markGroupConfigDirty" />
                        </v-col>
                        <v-col cols="12" md="4">
                            <v-text-field v-model.number="groupMaxWaves" :label="t('taskGroup.maxWaves')" type="number"
                                min="1" variant="outlined" density="compact" :disabled="editingDisabled"
                                @update:model-value="markGroupConfigDirty" />
                        </v-col>
                        <v-col cols="12">
                            <v-checkbox v-model="groupReflowStockCheck" :label="t('taskGroup.reflowStockCheck')"
                                :hint="t('taskGroup.reflowStockCheckHint')" density="compact" hide-details
                                persistent-hint :disabled="editingDisabled"
                                @update:model-value="markGroupConfigDirty" />
                        </v-col>
                    </v-row>
                    <div class="d-flex align-center mt-2" style="gap:8px">
                        <v-chip v-if="!accountPoolConfigured" size="small" color="warning" variant="tonal">
                            {{ t('taskGroup.noAccountsConfigured') }}
                        </v-chip>
                        <v-chip v-if="!workerPoolConfigured" size="small" color="warning" variant="tonal">
                            {{ t('taskGroup.noWorkersConfigured') }}
                        </v-chip>
                        <v-spacer />
                        <v-btn color="primary" variant="tonal" :loading="savingGroupConfig"
                            :disabled="editingDisabled || !groupConfigDirty" @click="saveGroupConfig">
                            {{ t('common.save') }}
                        </v-btn>
                    </div>
                </v-card-text>
            </v-card>

            <!-- Dispatch bar -->
            <v-card v-if="macros.length > 0" class="mb-4" elevation="2">
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
                        <v-btn v-if="!groupRunning" prepend-icon="mdi-play-circle-outline" color="success"
                            variant="tonal" size="small" :loading="dispatchingAll"
                            :disabled="dispatchableMacros.length === 0 || !workerPoolConfigured"
                            @click="startAllMacros">
                            {{ t('taskGroup.startAll') }}
                        </v-btn>
                        <v-btn v-if="!groupRunning" prepend-icon="mdi-fast-forward" color="warning" variant="tonal"
                            size="small" :loading="dispatchingAll"
                            :disabled="dispatchableMacros.length === 0 || !workerPoolConfigured"
                            @click="startReflowNow">
                            {{ t('taskGroup.startReflowNow') }}
                        </v-btn>
                        <template v-else>
                            <v-btn prepend-icon="mdi-stop-circle-outline" color="error" variant="tonal" size="small"
                                :loading="dispatchingAll" @click="stopAllMacros">
                                {{ t('taskGroup.stopAll') }}
                            </v-btn>
                            <v-btn prepend-icon="mdi-alert-octagon" color="deep-orange" variant="tonal" size="small"
                                :loading="dispatchingAll" @click="forceStopAllMacros" class="ml-1">
                                {{ t('taskGroup.forceStopAll') }}
                            </v-btn>
                        </template>
                        <v-btn prepend-icon="mdi-refresh" color="warning" variant="tonal" size="small"
                            :loading="dispatchingAll" :disabled="!workerPoolConfigured" @click="forceRestartAllMacros"
                            class="ml-1">
                            {{ t('taskGroup.forceRestartAll') }}
                        </v-btn>
                    </div>
                </v-card-text>
            </v-card>

            <!-- Purchase Group Status -->
            <v-card v-if="allPurchaseGroups.length > 0" class="mb-4" elevation="2">
                <v-card-title class="text-subtitle-1 py-2 px-4">{{ t('taskGroup.purchaseGroups') }} ({{
                    allPurchaseGroups.length
                    }})</v-card-title>
                <v-card-text class="py-1 px-4">
                    <div v-for="row in allPurchaseGroups" :key="row.pg.id"
                        style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;padding:4px 0">
                        <span class="text-caption text-medium-emphasis" style="min-width:80px">{{ row.macro.skuName ||
                            row.macro.skuId }}</span>
                        <v-chip v-for="b in (row.pg.buyers || [])" :key="b.logicalId" size="x-small" variant="tonal">{{
                            buyerDisplayName(b) }}</v-chip>
                        <v-spacer />
                        <v-chip v-if="row.stats.succeeded > 0" size="x-small" variant="tonal" color="success">{{
                            t('taskGroup.succeeded', { count: row.stats.succeeded }) }}</v-chip>
                        <v-chip v-if="row.stats.running > 0" size="x-small" variant="tonal" color="info">{{
                            t('taskGroup.running', { count: row.stats.running }) }}</v-chip>
                        <v-chip v-if="row.stats.deficit > 0" size="x-small" variant="tonal" color="warning">{{
                            t('taskGroup.queued', { count: row.stats.deficit }) }}</v-chip>
                        <v-chip v-if="row.stats.failed > 0" size="x-small" variant="tonal" color="error">{{
                            t('taskGroup.failed', { count: row.stats.failed }) }}</v-chip>
                    </div>
                </v-card-text>
            </v-card>

            <!-- Macro list -->
            <v-card elevation="2">
                <v-card-title class="text-subtitle-1">{{ t('taskGroup.macroList') }} ({{ macros.length
                    }})</v-card-title>
                <v-expansion-panels v-model="expandedMacro" variant="accordion"
                    @update:model-value="onMacroPanelChange">
                    <v-expansion-panel v-for="(m, idx) in macros" :key="m.id" :value="idx + 1" class="macro-panel">
                        <v-expansion-panel-title class="macro-panel-title">
                            <div class="macro-summary">
                                <div class="macro-summary__top">
                                    <div class="macro-summary__identity">
                                        <div class="macro-summary__project text-truncate">
                                            {{ m.projectName || '—' }}
                                        </div>
                                        <div class="macro-summary__sku-line">
                                            <v-chip size="x-small" variant="tonal" color="primary">
                                                {{ m.screenName || m.screenId }}
                                            </v-chip>
                                            <span class="macro-summary__sku text-truncate">{{ m.skuName || m.skuId
                                            }}</span>
                                            <span class="macro-summary__ids">SKU {{ m.skuId }}</span>
                                        </div>
                                    </div>
                                    <div class="macro-summary__actions">
                                        <v-chip v-if="macroStatusLabel(m)" size="x-small" variant="tonal"
                                            :color="macroStatusColor(m)" class="macro-summary__status">
                                            {{ macroStatusLabel(m) }}
                                        </v-chip>
                                        <v-tooltip :text="t('common.delete')" location="top">
                                            <template #activator="{ props: tipProps }">
                                                <v-btn icon="mdi-delete-outline" size="small" variant="text"
                                                    color="error" class="macro-summary__delete"
                                                    :loading="deletingMacro[m.id]" :disabled="editingDisabled"
                                                    v-bind="tipProps" @click.stop="removeMacro(m)" />
                                            </template>
                                        </v-tooltip>
                                    </div>
                                </div>

                                <div class="macro-summary__meta">
                                    <div class="macro-meta">
                                        <v-icon size="16" color="primary">mdi-calendar-month</v-icon>
                                        <div class="macro-meta__content">
                                            <span>{{ t('taskGroup.eventDay') }}</span>
                                            <strong>{{ formatEventDay(m.eventDay) }}</strong>
                                        </div>
                                    </div>
                                    <div class="macro-meta">
                                        <v-icon size="16" color="info">mdi-clock-start</v-icon>
                                        <div class="macro-meta__content">
                                            <span>{{ t('taskGroup.saleStartTime') }}</span>
                                            <strong>{{ formatDateTime(m.startAt) }}</strong>
                                        </div>
                                    </div>
                                    <div class="macro-meta macro-meta--wide">
                                        <v-icon size="16" color="medium-emphasis">mdi-timer-sand</v-icon>
                                        <div class="macro-meta__content">
                                            <span>{{ t('taskGroup.saleTime') }}</span>
                                            <strong>{{ formatDateTimeRange(m.startAt, m.deadline) }}</strong>
                                        </div>
                                    </div>
                                </div>

                                <div class="macro-group-preview">
                                    <div class="macro-group-preview__header">
                                        <v-icon size="15" color="primary">mdi-account-group</v-icon>
                                        <span>{{ t('taskGroup.purchaseGroups') }}</span>
                                        <v-chip size="x-small" variant="tonal">{{ (m.purchaseGroups || []).length
                                        }}</v-chip>
                                    </div>
                                    <div v-if="(m.purchaseGroups || []).length > 0" class="macro-group-preview__list">
                                        <div v-for="pg in sortedPurchaseGroups(m)" :key="pg.id" class="pg-mini-card">
                                            <span class="pg-mini-card__buyers text-truncate">
                                                {{ purchaseGroupBuyerNames(pg) }}
                                            </span>
                                            <span class="pg-mini-card__meta">×{{ pg.weight || 1 }} · P{{
                                                pg.priority || 0 }}</span>
                                            <v-chip v-if="pg.allowSplit" size="x-small" variant="tonal" color="primary">
                                                {{ t('taskGroup.pgAllowSplit') }}
                                            </v-chip>
                                        </div>
                                    </div>
                                    <span v-else class="text-caption text-medium-emphasis">
                                        {{ t('taskGroup.pgEmpty') }}
                                    </span>
                                </div>

                                <div v-if="isPendingAutoStart(m) && startBlockers(m).length > 0"
                                    class="macro-summary__warnings">
                                    <div v-for="blocker in startBlockers(m)" :key="blocker">
                                        ⚠ {{ blocker }}
                                    </div>
                                </div>
                            </div>
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
                                <v-list v-if="dispatchStats(m).intents.length > 0" density="compact" class="py-0 mt-1">
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
                                            <v-btn v-if="i.activeCount > 0" icon="mdi-stop" size="x-small"
                                                variant="text" color="error" class="ml-1"
                                                @click.stop="stopSingleIntent(i.id)" />
                                        </template>
                                    </v-list-item>
                                </v-list>
                            </v-card>
                            <div class="purchase-group-section mb-3">
                                <div class="purchase-group-section__header">
                                    <div>
                                        <div class="purchase-group-section__title">
                                            {{ t('taskGroup.purchaseGroups') }}
                                        </div>
                                    </div>
                                    <v-chip size="small" variant="tonal" color="primary">
                                        {{ (m.purchaseGroups || []).length }}
                                    </v-chip>
                                </div>
                                <div v-if="(m.purchaseGroups || []).length > 0" class="purchase-group-grid">
                                    <div v-for="pg in sortedPurchaseGroups(m)" :key="pg.id" class="purchase-group-card">
                                        <div class="purchase-group-card__main">
                                            <div class="purchase-group-card__buyers">
                                                <v-chip v-for="b in (pg.buyers || [])" :key="b.logicalId" size="small"
                                                    variant="tonal">
                                                    {{ buyerDisplayNameFullId(b) }}
                                                </v-chip>
                                            </div>
                                            <div class="purchase-group-card__meta">
                                                <v-chip size="x-small" variant="outlined" color="info">
                                                    ×{{ pg.weight || 1 }}
                                                </v-chip>
                                                <v-chip size="x-small" variant="outlined"
                                                    :color="(pg.priority || 0) > 0 ? 'success' : (pg.priority || 0) < 0 ? 'warning' : ''">
                                                    P{{ pg.priority || 0 }}
                                                </v-chip>
                                                <v-chip v-if="pg.allowSplit" color="primary" size="x-small"
                                                    variant="tonal">
                                                    {{ t('taskGroup.pgAllowSplit') }}
                                                </v-chip>
                                            </div>
                                        </div>
                                        <div class="purchase-group-card__actions">
                                            <v-tooltip :text="t('taskGroup.pgEdit')" location="top">
                                                <template #activator="{ props: tipProps }">
                                                    <v-btn icon="mdi-pencil" size="small" variant="text" color="primary"
                                                        v-bind="tipProps" :disabled="editingDisabled"
                                                        @click.stop="openEditPg(m.id, pg)" />
                                                </template>
                                            </v-tooltip>
                                            <v-tooltip :text="t('taskGroup.pgDelete')" location="top">
                                                <template #activator="{ props: tipProps }">
                                                    <v-btn icon="mdi-delete-outline" size="small" variant="text"
                                                        color="error" :loading="deletingPg[pg.id]"
                                                        :disabled="editingDisabled" v-bind="tipProps"
                                                        @click.stop="removePurchaseGroup(m.id, pg.id)" />
                                                </template>
                                            </v-tooltip>
                                        </div>
                                    </div>
                                </div>
                                <div v-else class="purchase-group-empty">
                                    <v-icon size="20" color="medium-emphasis">mdi-account-multiple-plus</v-icon>
                                    <span>{{ t('taskGroup.pgEmpty') }}</span>
                                </div>
                            </div>
                            <v-card variant="text"><v-card-text class="pa-0">
                                    <div class="purchase-group-form-title">
                                        {{ editingPgId && editingPgMacroId === m.id ? t('taskGroup.pgEditTitle') :
                                            t('taskGroup.pgAdd') }}
                                    </div>
                                    <BuyerPicker v-if="allBuyers.length > 0" :model-value="selectedPgBuyerIds"
                                        @update:model-value="onBuyerSelectionChange" :buyers="allBuyers"
                                        :max="currentMacroOrderCapacity" :label="t('taskGroup.pgSelectBuyerShort')"
                                        :hint="t('taskGroup.pgMaxLabel', { max: currentMacroOrderCapacity })"
                                        class="mb-2" :disabled="editingDisabled" />
                                    <v-checkbox-btn v-model="allowSplit" color="primary" density="compact"
                                        :label="t('taskGroup.pgAllowSplitHint')" hide-details class="mb-2"
                                        :disabled="editingDisabled" />
                                    <v-row dense class="mb-2">
                                        <v-col cols="6">
                                            <v-text-field v-model.number="pgWeight" :label="t('taskGroup.pgWeight')"
                                                type="number" min="1" variant="outlined" density="compact" hide-details
                                                :hint="t('taskGroup.pgWeightHint')" persistent-hint
                                                :disabled="editingDisabled" />
                                        </v-col>
                                        <v-col cols="6">
                                            <v-text-field v-model.number="pgPriority" :label="t('taskGroup.pgPriority')"
                                                type="number" variant="outlined" density="compact" hide-details
                                                :hint="t('taskGroup.pgPriorityHint')" persistent-hint
                                                :disabled="editingDisabled" />
                                        </v-col>
                                    </v-row>
                                    <p v-if="allBuyers.length === 0" class="text-caption text-medium-emphasis mb-2">
                                        {{
                                            t('taskGroup.pgNoBuyers') }}</p>
                                    <v-btn color="primary" :loading="savingPg"
                                        :disabled="editingDisabled || selectedPgBuyerIds.length === 0 || allBuyers.length === 0"
                                        @click="savePurchaseGroup(m)">{{
                                            editingPgId && editingPgMacroId === m.id ? t('taskGroup.pgSave') :
                                                t('taskGroup.pgAdd')
                                        }}</v-btn>
                                    <v-btn v-if="editingPgId && editingPgMacroId === m.id" variant="text" class="ml-1"
                                        @click="cancelEditPg">{{ t('common.cancel')
                                        }}</v-btn>
                                </v-card-text></v-card>
                        </v-expansion-panel-text>
                    </v-expansion-panel>
                    <v-expansion-panel :value="-1">
                        <v-expansion-panel-title class="add-macro-title">
                            <div class="add-macro-title__content">
                                <v-avatar color="primary" variant="tonal" size="34">
                                    <v-icon size="20">mdi-plus-circle-outline</v-icon>
                                </v-avatar>
                                <div>
                                    <div class="add-macro-title__text">{{ t('taskGroup.addMacro') }}</div>
                                </div>
                            </div>
                        </v-expansion-panel-title>
                        <v-expansion-panel-text>
                            <v-alert v-if="editingDisabled" type="info" variant="tonal" density="compact" class="mb-3">
                                {{ t('taskGroup.editLockedHint') }}
                            </v-alert>
                            <div class="add-sku-panel">
                                <v-card variant="outlined" class="add-sku-lookup">
                                    <div class="add-sku-step">
                                        <v-avatar color="primary" variant="tonal" size="28">1</v-avatar>
                                        <div>
                                            <div class="add-sku-step__title">{{ t('taskGroup.projectIdLabel') }}</div>
                                            <div class="text-caption text-medium-emphasis">
                                                {{ t('taskGroup.projectIdHelp') }}
                                            </div>
                                        </div>
                                    </div>
                                    <div class="add-sku-lookup__form">
                                        <v-text-field v-model="lookupProjectId" :label="t('taskGroup.projectIdLabel')"
                                            :placeholder="t('taskGroup.projectIdPlaceholder')" variant="outlined"
                                            density="compact" hide-details :disabled="editingDisabled"
                                            @keydown.enter="lookupProject" />
                                        <v-btn :loading="lookupLoading" color="primary" :disabled="editingDisabled"
                                            @click="lookupProject">
                                            {{ t('taskGroup.lookup') }}
                                        </v-btn>
                                    </div>
                                </v-card>
                                <v-expand-transition>
                                    <div v-if="projectInfo" class="add-sku-results">
                                        <v-card class="project-overview-card" variant="outlined">
                                            <div class="project-overview-card__head">
                                                <div class="project-overview-card__identity">
                                                    <div class="project-overview-card__name">
                                                        {{ projectInfo.ProjectName }}
                                                    </div>
                                                    <div class="project-overview-card__id">
                                                        {{ t('taskGroup.projectId') }} {{ projectInfo.ProjectID }}
                                                    </div>
                                                </div>
                                                <div class="project-overview-card__badges">
                                                    <v-chip v-if="projectInfo.IsHotProject" color="error" size="small"
                                                        variant="tonal">
                                                        {{ t('taskGroup.hot') }}
                                                    </v-chip>
                                                    <v-chip v-if="projectInfo.IsForceRealName" color="warning"
                                                        size="small" variant="tonal">
                                                        {{ t('taskGroup.realNameRequired') }}
                                                    </v-chip>
                                                    <v-chip v-if="projectInfo.contactRequired" color="warning"
                                                        size="small" variant="tonal">
                                                        {{ t('taskGroup.contactRequired') }}
                                                    </v-chip>
                                                </div>
                                            </div>
                                            <v-divider class="my-3" />
                                            <div class="project-overview-card__meta">
                                                <div class="macro-meta">
                                                    <v-icon size="16" color="info">mdi-clock-start</v-icon>
                                                    <div class="macro-meta__content">
                                                        <span>{{ t('taskGroup.sale') }}</span>
                                                        <strong>
                                                            {{ formatDateTimeRange(projectInfo.StartTime,
                                                                projectInfo.EndTime) }}
                                                        </strong>
                                                    </div>
                                                </div>
                                                <div class="macro-meta">
                                                    <v-icon size="16" color="primary">mdi-ticket-confirmation</v-icon>
                                                    <div class="macro-meta__content">
                                                        <span>{{ t('taskGroup.tickets', { count: tickets.length })
                                                            }}</span>
                                                        <strong>{{ tickets.length }}</strong>
                                                    </div>
                                                </div>
                                            </div>
                                        </v-card>
                                        <v-card v-if="tickets.length > 0" class="sku-picker-card" variant="outlined">
                                            <div class="sku-picker-card__head" @click="showSkuList = !showSkuList">
                                                <div class="add-sku-step">
                                                    <v-avatar color="primary" variant="tonal" size="28">2</v-avatar>
                                                    <div>
                                                        <div class="add-sku-step__title">
                                                            {{ t('taskGroup.tickets', { count: tickets.length }) }}
                                                        </div>
                                                        <div class="text-caption text-medium-emphasis">
                                                            {{ selectedSkuId ? `${selectedScreenId} / SKU
                                                            ${selectedSkuId}` :
                                                            t('taskGroup.filterPlaceholder') }}
                                                        </div>
                                                    </div>
                                                </div>
                                                <v-icon class="sku-chevron"
                                                    :class="{ 'sku-chevron--open': showSkuList }"
                                                    size="small">mdi-chevron-down</v-icon>
                                            </div>
                                            <v-expand-transition>
                                                <div v-show="showSkuList" class="sku-picker-card__body">
                                                    <v-text-field v-model="filterName"
                                                        :label="t('taskGroup.filterPlaceholder')"
                                                        prepend-inner-icon="mdi-magnify" variant="outlined"
                                                        density="compact" hide-details clearable class="mb-2" />
                                                    <v-list class="sku-list" density="compact">
                                                        <v-list-group v-for="sc in filteredScreens" :key="sc.screenId"
                                                            :value="'screen-' + sc.screenId">
                                                            <template #activator="{ props: groupProps }">
                                                                <v-list-item v-bind="groupProps" class="sku-screen-item"
                                                                    :title="sc.screenName">
                                                                    <template #append>
                                                                        <v-icon
                                                                            class="screen-chevron">mdi-chevron-down</v-icon>
                                                                    </template>
                                                                </v-list-item>
                                                            </template>
                                                            <v-list-item v-for="t in sc.tickets"
                                                                :key="`${t.screenId}-${t.skuId}`"
                                                                class="sku-ticket-item"
                                                                :active="selectedScreenId === t.screenId && selectedSkuId === t.skuId"
                                                                @click="selectedScreenId = t.screenId; selectedSkuId = t.skuId">
                                                                <template #title>
                                                                    <div class="sku-ticket-item__title">
                                                                        <span class="text-body-2 text-truncate"
                                                                            style="min-width:0">{{ t.desc || t.skuId
                                                                            }}</span>
                                                                        <v-chip v-if="t.flags?.display_name"
                                                                            size="small" variant="tonal"
                                                                            class="flex-shrink-0"
                                                                            :color="t.flags.display_name.includes('售罄') || t.flags.display_name.includes('停售') ? 'red' : t.flags.display_name.includes('未开') ? 'grey' : t.flags.display_name.includes('不可') ? 'yellow' : 'green'">
                                                                            {{ t.flags.display_name }}
                                                                        </v-chip>
                                                                    </div>
                                                                </template>
                                                                <template #subtitle>
                                                                    <span class="text-body-2">SKU:{{ t.skuId }} · ¥{{
                                                                        ((t.price || 0) / 100).toFixed(0) }}</span>
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
                                        <v-card class="add-sku-confirm-card" variant="outlined">
                                            <div class="add-sku-step">
                                                <v-avatar color="success" variant="tonal" size="28">3</v-avatar>
                                                <div>
                                                    <div class="add-sku-step__title">{{ t('taskGroup.customStartAt') }}
                                                    </div>
                                                    <div class="text-caption text-medium-emphasis">
                                                        {{ t('taskGroup.customStartAtHint') }}
                                                    </div>
                                                </div>
                                            </div>
                                            <v-text-field ref="customStartRef" v-model="customStartAt"
                                                :label="t('taskGroup.customStartAt')" type="datetime-local" step="1"
                                                variant="outlined" density="compact" hide-details
                                                :disabled="editingDisabled" @click="openDatetimePicker" />
                                            <v-btn color="success" :loading="addingMacro"
                                                :disabled="editingDisabled || !selectedScreenId || !selectedSkuId"
                                                @click="addMacro">
                                                {{ t('taskGroup.confirmAdd') }}
                                            </v-btn>
                                        </v-card>
                                    </div>
                                </v-expand-transition>
                            </div>
                        </v-expansion-panel-text>
                    </v-expansion-panel>
                </v-expansion-panels>
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
                        <div class="info-row">
                            <span class="info-label">{{ t('taskGroup.saleStartTime') }}</span>
                            <span class="info-value" style="color:rgb(var(--v-theme-info));font-weight:600">{{
                                addingMacroInfo?.saleStart || '—' }}</span>
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
    </v-container>
</template>

<style scoped>
.macro-panel {
    border-radius: 12px;
    overflow: hidden;
}

.macro-panel+.macro-panel {
    margin-top: 8px;
}

:deep(.macro-panel-title) {
    align-items: stretch;
    padding: 12px 16px;
}

:deep(.macro-panel-title .v-expansion-panel-title__icon) {
    align-self: center;
    margin-left: 12px;
}

.macro-summary {
    width: 100%;
    min-width: 0;
}

.macro-summary__top {
    display: flex;
    align-items: flex-start;
    gap: 12px;
    min-width: 0;
}

.macro-summary__identity {
    min-width: 0;
    flex: 1;
}

.macro-summary__project {
    font-size: 1rem;
    font-weight: 700;
    line-height: 1.35;
}

.macro-summary__sku-line {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    margin-top: 6px;
}

.macro-summary__sku {
    min-width: 0;
    max-width: 420px;
    font-size: 0.8rem;
    color: rgba(var(--v-theme-on-surface), 0.72);
}

.macro-summary__ids {
    flex-shrink: 0;
    font-size: 0.72rem;
    color: rgba(var(--v-theme-on-surface), 0.48);
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
}

.macro-summary__status {
    flex-shrink: 0;
}

.macro-summary__actions {
    display: flex;
    align-items: flex-start;
    gap: 6px;
    flex-shrink: 0;
}

.macro-summary__delete {
    margin-top: -6px;
    margin-right: -6px;
}

.macro-summary__meta {
    display: grid;
    grid-template-columns: minmax(180px, 1fr) minmax(210px, 1fr) minmax(280px, 1.3fr);
    gap: 8px;
    margin-top: 12px;
}

.macro-meta {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    min-width: 0;
    padding: 8px 10px;
    border: 1px solid rgba(var(--v-theme-on-surface), 0.08);
    border-radius: 10px;
    background: rgba(var(--v-theme-surface-variant), 0.18);
}

.macro-meta__content {
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 2px;
}

.macro-meta__content span {
    font-size: 0.7rem;
    color: rgba(var(--v-theme-on-surface), 0.52);
}

.macro-meta__content strong {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 0.78rem;
    font-weight: 600;
    color: rgba(var(--v-theme-on-surface), 0.86);
}

.macro-group-preview {
    margin-top: 10px;
    padding: 10px;
    border: 1px solid rgba(var(--v-theme-on-surface), 0.08);
    border-radius: 12px;
    background: rgba(var(--v-theme-surface-variant), 0.12);
}

.macro-group-preview__header {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.72rem;
    color: rgba(var(--v-theme-on-surface), 0.58);
    margin-bottom: 8px;
}

.macro-group-preview__list {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 6px;
}

.pg-mini-card {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    padding: 6px 8px;
    border-radius: 9px;
    background: rgba(var(--v-theme-surface), 0.72);
    border: 1px solid rgba(var(--v-theme-on-surface), 0.06);
}

.pg-mini-card__buyers {
    min-width: 0;
    flex: 1;
    font-size: 0.78rem;
    font-weight: 600;
    color: rgba(var(--v-theme-on-surface), 0.86);
}

.pg-mini-card__meta {
    flex-shrink: 0;
    font-size: 0.72rem;
    color: rgba(var(--v-theme-on-surface), 0.52);
}

.macro-summary__warnings {
    margin-top: 8px;
    font-size: 0.75rem;
    line-height: 1.45;
    color: rgb(var(--v-theme-warning));
}

.purchase-group-section {
    padding: 12px;
    border: 1px solid rgba(var(--v-theme-on-surface), 0.08);
    border-radius: 12px;
    background: rgba(var(--v-theme-surface-variant), 0.12);
}

.purchase-group-section__header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 10px;
}

.purchase-group-section__title {
    font-size: 0.92rem;
    font-weight: 700;
    color: rgba(var(--v-theme-on-surface), 0.9);
}

.purchase-group-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: 8px;
}

.purchase-group-card {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
    min-width: 0;
    padding: 10px;
    border: 1px solid rgba(var(--v-theme-on-surface), 0.08);
    border-radius: 12px;
    background: rgb(var(--v-theme-surface));
}

.purchase-group-card__main {
    min-width: 0;
    flex: 1;
}

.purchase-group-card__buyers {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    min-width: 0;
}

.purchase-group-card__meta {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 8px;
}

.purchase-group-card__actions {
    display: flex;
    align-items: flex-start;
    gap: 2px;
    flex-shrink: 0;
    margin-top: -4px;
    margin-right: -4px;
}

.purchase-group-empty {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 44px;
    padding: 10px;
    border: 1px dashed rgba(var(--v-theme-on-surface), 0.16);
    border-radius: 10px;
    color: rgba(var(--v-theme-on-surface), 0.58);
    font-size: 0.82rem;
}

.purchase-group-form-title {
    margin-bottom: 10px;
    font-size: 0.9rem;
    font-weight: 700;
    color: rgba(var(--v-theme-on-surface), 0.9);
}

.add-macro-title__content {
    display: flex;
    align-items: center;
    gap: 10px;
}

.add-macro-title__text {
    font-size: 0.95rem;
    font-weight: 700;
}

.add-sku-panel {
    display: flex;
    flex-direction: column;
    gap: 12px;
}

.add-sku-results {
    display: flex;
    flex-direction: column;
    gap: 12px;
}

.add-sku-lookup,
.project-overview-card,
.sku-picker-card,
.add-sku-confirm-card {
    padding: 14px;
    border-radius: 14px;
}

.add-sku-step {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
}

.add-sku-step__title {
    font-size: 0.9rem;
    font-weight: 700;
    color: rgba(var(--v-theme-on-surface), 0.9);
}

.add-sku-lookup {
    display: grid;
    grid-template-columns: minmax(180px, 0.8fr) minmax(260px, 1.2fr);
    gap: 14px;
    align-items: center;
}

.add-sku-lookup__form {
    display: flex;
    align-items: center;
    gap: 8px;
}

.project-overview-card__head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
}

.project-overview-card__identity {
    min-width: 0;
}

.project-overview-card__name {
    font-size: 1rem;
    font-weight: 700;
    line-height: 1.35;
}

.project-overview-card__id {
    margin-top: 4px;
    font-size: 0.76rem;
    color: rgba(var(--v-theme-on-surface), 0.56);
}

.project-overview-card__badges {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 6px;
    flex-shrink: 0;
}

.project-overview-card__meta {
    display: grid;
    grid-template-columns: minmax(260px, 1.2fr) minmax(120px, 0.8fr);
    gap: 8px;
}

.sku-picker-card__head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    cursor: pointer;
}

.sku-picker-card__body {
    margin-top: 12px;
}

.sku-list {
    border: 1px solid rgba(var(--v-theme-on-surface), 0.08);
    border-radius: 12px;
    overflow: hidden;
    background: rgba(var(--v-theme-surface-variant), 0.08);
}

.sku-screen-item {
    background: rgba(var(--v-theme-surface-variant), 0.18);
}

.sku-ticket-item {
    cursor: pointer;
    margin: 4px 8px;
    border-radius: 10px;
}

.sku-ticket-item__title {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
}

.add-sku-confirm-card {
    display: grid;
    grid-template-columns: minmax(180px, 0.8fr) minmax(240px, 1.2fr) auto;
    gap: 12px;
    align-items: center;
}

@media (max-width: 900px) {
    .macro-summary__meta {
        grid-template-columns: 1fr;
    }

    .add-sku-lookup,
    .project-overview-card__meta,
    .add-sku-confirm-card {
        grid-template-columns: 1fr;
    }

    .add-sku-lookup__form {
        flex-direction: column;
        align-items: stretch;
    }
}

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
