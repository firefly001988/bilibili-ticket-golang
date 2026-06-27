<script lang="ts" setup>
import { ref, watch, computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useMessagesStore } from '@/stores/snackbar'
import { Snapshot, SaveMacro, DeleteMacro, SavePurchaseGroup, DeletePurchaseGroup } from '../../../bindings/bilibili-ticket-golang/cmd/gui/clusterservice'
import { GetProjectInformationNew, GetTicketSkuIDsByProjectIDNew } from '../../../bindings/bilibili-ticket-golang/lib/biliutils/biliclient'

const route = useRoute(); const { t } = useI18n(); const messages = useMessagesStore()

interface MacroSummary { id: string; taskGroupId: string; projectId: number; projectName?: string; screenId: number; screenName?: string; skuId: number; skuName?: string; eventDay: string; eventDayConfirmed: boolean; needsReview: boolean; orderCapacity: number; desiredReplicas: number; hardConcurrency: number; startAt: string; deadline: string; phase?: string; purchaseGroups?: any[] }

const group = ref<any>(null); const macros = ref<MacroSummary[]>([]); const loading = ref(true)
const lookupProjectId = ref(''); const lookupLoading = ref(false); const projectInfo = ref<any>(null); const tickets = ref<any[]>([])
const selectedScreenId = ref(0); const selectedSkuId = ref(0); const addingMacro = ref(false); const showSkuList = ref(false)
const deletingMacro = ref<Record<string, boolean>>({}); const confirmingMacro = ref<Record<string, boolean>>({})
const filterName = ref('')
const filteredTickets = computed(() => { const kw = filterName.value.trim().toLowerCase(); if (!kw) return tickets.value; return tickets.value.filter((t: any) => (t.name || '').toLowerCase().includes(kw) || (t.desc || '').toLowerCase().includes(kw) || String(t.skuId).includes(kw)) })

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

async function loadAll(id: string) { loading.value = true; group.value = null; macros.value = []; try { const snap = await Snapshot(); group.value = ((snap.taskGroups || []) as any[]).find(g => g.id === id) || null; macros.value = ((snap.macros || []) as MacroSummary[]).filter(m => m.taskGroupId === id); if (allBuyers.value.length === 0) { allBuyers.value = ((snap.buyers || []) as any[]).map((b: any) => ({ logicalId: b.logicalId, name: b.name || '', idCard: b.idCard || '', tel: b.tel || '' })) } } catch { } loading.value = false }
watch(() => route.params.id, (newId) => { if (newId) loadAll(newId as string) }, { immediate: true })

async function lookupProject() { const pid = lookupProjectId.value.trim(); if (!pid) { messages.add({ text: t('taskGroup.projectIdRequired'), color: 'warning' }); return } lookupLoading.value = true; projectInfo.value = null; tickets.value = []; selectedScreenId.value = 0; selectedSkuId.value = 0; try { const [info, tks] = await Promise.all([GetProjectInformationNew(pid), GetTicketSkuIDsByProjectIDNew(pid)]); if (!info) messages.add({ text: t('taskGroup.projectNotFound'), color: 'warning' }); else { projectInfo.value = info; tickets.value = tks || [] } } catch (e: any) { messages.add({ text: t('taskGroup.lookupFailed', { error: String(e) }), color: 'error' }) } lookupLoading.value = false }

async function addMacro() { if (!projectInfo.value || !selectedScreenId.value || !selectedSkuId.value || !group.value) return; addingMacro.value = true; const ticket = tickets.value.find((t: any) => t.screenId === selectedScreenId.value && t.skuId === selectedSkuId.value); try { await SaveMacro(JSON.stringify({ id: randomId('macro'), taskGroupId: group.value.id, projectId: Number(projectInfo.value.ProjectID), projectName: projectInfo.value.ProjectName || '', screenId: selectedScreenId.value, screenName: ticket?.name || '', skuId: selectedSkuId.value, skuName: ticket?.desc || '', eventDay: projectInfo.value.StartTime || '', eventDayConfirmed: false, needsReview: projectInfo.value.IsForceRealName || false, orderCapacity: ticket?.buyLimit || 1, desiredReplicas: 1, hardConcurrency: 1, startAt: ticket?.saleStat?.start || '', deadline: ticket?.saleStat?.end || '' })); projectInfo.value = null; selectedScreenId.value = 0; selectedSkuId.value = 0; lookupProjectId.value = ''; await loadAll(group.value.id); messages.add({ text: t('taskGroup.macroAdded'), color: 'success' }) } catch (e: any) { messages.add({ text: t('taskGroup.macroAddFailed', { error: String(e) }), color: 'error' }) } addingMacro.value = false }

async function confirmEventDay(m: MacroSummary) { const newVal = !m.eventDayConfirmed; confirmingMacro.value[m.id] = true; try { await SaveMacro(JSON.stringify({ id: m.id, taskGroupId: m.taskGroupId, projectId: m.projectId, projectName: m.projectName || '', screenId: m.screenId, screenName: m.screenName || '', skuId: m.skuId, skuName: m.skuName || '', eventDay: m.eventDay || '', eventDayConfirmed: newVal, needsReview: m.needsReview || false, orderCapacity: m.orderCapacity || 1, desiredReplicas: m.desiredReplicas || 1, hardConcurrency: m.hardConcurrency || 1, startAt: m.startAt || '', deadline: m.deadline || '' })); m.eventDayConfirmed = newVal } catch (e: any) { messages.add({ text: t('taskGroup.macroAddFailed', { error: String(e) }), color: 'error' }) } confirmingMacro.value[m.id] = false }

async function removeMacro(m: MacroSummary) { deletingMacro.value[m.id] = true; try { await DeleteMacro(m.id); await loadAll(group.value!.id); messages.add({ text: t('taskGroup.macroDeleted'), color: 'success' }) } catch (e: any) { messages.add({ text: t('taskGroup.macroDeleteFailed', { error: String(e) }), color: 'error' }) } deletingMacro.value[m.id] = false }

async function savePurchaseGroup(m: MacroSummary) {
    if (selectedPgBuyerIds.value.length === 0) { messages.add({ text: t('taskGroup.pgSelectBuyer'), color: 'warning' }); return }
    const isEdit = !!editingPgId.value && editingPgMacroId.value === m.id
    savingPg.value = true
    try {
        const buyers = selectedPgBuyerIds.value.map(id => { const b = allBuyers.value.find(x => x.logicalId === id)!; return { logicalId: id, name: b.name, idCard: b.idCard, tel: b.tel } })
        await SavePurchaseGroup(JSON.stringify({ id: isEdit ? editingPgId.value : '', macroTaskId: m.id, buyers, allowSplit: allowSplit.value }))
        editingPgId.value = ''; editingPgMacroId.value = ''
        allowSplit.value = false
        const restored = isEdit ? [...(savedPgBuyerIds.value.get(m.id) || [])] : []
        savedPgBuyerIds.value.set(m.id, [])
        selectedPgBuyerIds.value = restored
        await loadAll(group.value!.id)
        messages.add({ text: isEdit ? t('taskGroup.pgUpdated') : t('taskGroup.pgAdded'), color: 'success' })
    } catch (e: any) { messages.add({ text: isEdit ? t('taskGroup.pgUpdateFailed', { error: String(e) }) : t('taskGroup.pgAddFailed', { error: String(e) }), color: 'error' }) }
    savingPg.value = false
}

function openEditPg(macroId: string, pg: any) {
    loadBuyersOnce()
    // Save current selection as pre-edit state
    savedPgBuyerIds.value.set(macroId, [...selectedPgBuyerIds.value])
    editingPgId.value = pg.id; editingPgMacroId.value = macroId
    selectedPgBuyerIds.value = (pg.buyers || []).map((b: any) => b.logicalId)
    allowSplit.value = pg.allowSplit || false
}

function cancelEditPg() {
    const macroId = editingPgMacroId.value
    // Restore pre-edit selection
    selectedPgBuyerIds.value = macroId ? [...(savedPgBuyerIds.value.get(macroId) || [])] : []
    editingPgId.value = ''; editingPgMacroId.value = ''
    allowSplit.value = false
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
</script>

<template>
    <v-container>
        <v-row v-if="loading" justify="center" class="mt-6"><v-progress-circular indeterminate
                color="primary" /></v-row>
        <div v-else-if="group">
            <h1>{{ group.name }}</h1>
            <v-divider class="mt-2 mb-4" thickness="3" />

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
                                    </div>
                                    <div class="text-caption text-medium-emphasis text-truncate mt-2">{{
                                        t('taskGroup.eventDay') }}: {{
                                            m.eventDay || '—' }}</div>
                                    <div class="text-caption text-medium-emphasis text-truncate">{{
                                        t('taskGroup.saleTime') }}: {{ m.startAt || '—' }} ~ {{ m.deadline || '—' }}
                                    </div>
                                </div>
                                <div>
                                    <div class="text-caption ml-auto mr-2 d-flex align-center flex-shrink-0"
                                        style="gap:2px;cursor:pointer;white-space:nowrap"
                                        @click.stop="confirmEventDay(m)">
                                        <v-checkbox-btn :model-value="m.eventDayConfirmed" color="success"
                                            density="compact" :loading="confirmingMacro[m.id]" hide-details
                                            @click.stop="confirmEventDay(m)" />
                                        {{ t('taskGroup.confirmEventDay') }}
                                    </div>
                                </div>
                                <template v-slot:actions><v-btn icon="mdi-delete" size="medium" variant="text"
                                        color="error" :loading="deletingMacro[m.id]"
                                        @click.stop="removeMacro(m)" /></template>
                            </v-expansion-panel-title>
                            <v-expansion-panel-text>
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
</style>
