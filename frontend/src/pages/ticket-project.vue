<script lang="ts" setup>
import { ref, computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { GetProjectInformationNew, GetTicketSkuIDsByProjectIDNew } from '../../wailsjs/go/biliutils/BiliClient';
import { AddTicket, AddTicketTask, FetchRealNameBuyers } from '../../wailsjs/go/scheduler/SchedulerService';
import type { _return } from '../../wailsjs/go/models';
import { useMessagesStore } from '@/stores/snackbar';
import { useRouter } from 'vue-router';
import { DEFAULT_EXPIRE_DAYS, SECONDS_PER_DAY } from '@/composables/defaults';
import { useDebug } from '@/composables/useDebug';

const { t } = useI18n();
const router = useRouter();
const { debugLog, debugGroup } = useDebug();
const messages = useMessagesStore();

const projectId = ref('');
const loading = ref(false);
const projectInfo = ref<_return.ProjectInformation | null>(null);
const tickets = ref<_return.TicketSkuScreenID[]>([]);

// ── Filter ────────────────────────────────────────────
const filterStatus = ref('all');

/** Unique display_name values from the current ticket list */
const filterOptions = computed(() => {
    const names = new Set<string>();
    for (const t of tickets.value) {
        const dn = t.flags?.display_name;
        if (dn) names.add(dn);
    }
    return Array.from(names);
});

/** Filtered ticket list based on the selected display_name */
const filteredTickets = computed(() => {
    if (filterStatus.value === 'all') return tickets.value;
    return tickets.value.filter(t => t.flags?.display_name === filterStatus.value);
});

// ── Quick-create dialog ───────────────────────────────
const showCreateDialog = ref(false);
const selectedTicket = ref<_return.TicketSkuScreenID | null>(null);
const creating = ref(false);
const goScheduler = ref(true);

// ── API-derived timestamps ─────────────────────────────
const apiStartUnix = ref(0);
const apiEndUnix = ref(0);
const apiStartLabel = ref('');
const apiEndLabel = ref('');

// ── Real-name buyer states ────────────────────────────
const fetchingBuyers = ref(false);
const buyerList = ref<Array<{ id: number; name: string; tel: string; personalId: string; idType: number }>>([]);
const selectedBuyerId = ref<number | null>(null);

const buyerForm = ref({
    buyerName: '',
    buyerTel: '',
    buyerId: 0,
});

// ── Form validation ──────────────────────────────────
const formValid = computed(() => {
    if (!projectInfo.value || !selectedTicket.value) return false;
    if (projectInfo.value.IsForceRealName) {
        return selectedBuyerId.value != null;
    }
    return buyerForm.value.buyerName.trim() !== '';
});

async function lookupProject() {
    if (!projectId.value.trim()) {
        messages.add({ text: t('ticketProject.enterProjectId'), color: 'warning', timeout: 2000 });
        return;
    }
    loading.value = true;
    filterStatus.value = 'all';
    try {
        const [info, tks] = await Promise.all([
            GetProjectInformationNew(projectId.value.trim()),
            GetTicketSkuIDsByProjectIDNew(projectId.value.trim()),
        ]);

        debugGroup(`[DEBUG] Project Lookup: ${projectId.value}`, () => {
            debugLog('ProjectInformation:', JSON.stringify(info, null, 2), info);
            debugLog('TicketSkuList:', JSON.stringify(tks, null, 2), tks);
        });

        projectInfo.value = info;
        tickets.value = tks || [];
    } catch (e: any) {
        messages.add({ text: t('ticketProject.error', { error: String(e) }), color: 'error', timeout: 4000 });
    } finally {
        loading.value = false;
    }
}

function openCreateDialog(ticket: _return.TicketSkuScreenID) {
    selectedTicket.value = ticket;

    const fmt = (d: Date) => d.toLocaleString('zh-CN', { hour12: false });

    function toUnix(ts: any): number {
        if (!ts) return 0;
        const d = new Date(ts);
        return isNaN(d.getTime()) ? 0 : Math.floor(d.getTime() / 1000);
    }

    const skuStart = ticket.saleStat?.start;
    const skuEnd = ticket.saleStat?.end;
    const projStart = projectInfo.value?.StartTime;
    const projEnd = projectInfo.value?.EndTime;

    if (skuStart && toUnix(skuStart) > 0) {
        apiStartUnix.value = toUnix(skuStart);
        apiStartLabel.value = fmt(new Date(skuStart as any));
    } else if (projStart) {
        apiStartUnix.value = toUnix(projStart);
        apiStartLabel.value = fmt(new Date(projStart as any));
    } else {
        apiStartUnix.value = Math.floor(Date.now() / 1000);
        apiStartLabel.value = t('ticketProject.immediate');
    }

    if (skuEnd && toUnix(skuEnd) > 0) {
        apiEndUnix.value = toUnix(skuEnd);
        apiEndLabel.value = fmt(new Date(skuEnd as any));
    } else if (projEnd) {
        apiEndUnix.value = toUnix(projEnd);
        apiEndLabel.value = fmt(new Date(projEnd as any));
    } else {
        apiEndUnix.value = Math.floor(Date.now() / 1000) + SECONDS_PER_DAY * DEFAULT_EXPIRE_DAYS;
        apiEndLabel.value = fmt(new Date(Date.now() + SECONDS_PER_DAY * DEFAULT_EXPIRE_DAYS * 1000));
    }

    buyerForm.value = { buyerName: '', buyerTel: '', buyerId: 0 };
    buyerList.value = [];
    selectedBuyerId.value = null;
    showCreateDialog.value = true;
}

async function fetchBuyers() {
    if (!selectedTicket.value || !projectInfo.value) return;
    fetchingBuyers.value = true;
    try {
        const buyers = await FetchRealNameBuyers();
        buyerList.value = buyers || [];
        debugLog('[fetchBuyers] result:', buyers);
        if (buyerList.value.length === 0) {
            messages.add({ text: t('ticketProject.noBuyerFound'), color: 'warning', timeout: 3000 });
        }
    } catch (e: any) {
        messages.add({ text: t('ticketProject.fetchBuyerFailed', { error: String(e) }), color: 'error', timeout: 4000 });
    } finally {
        fetchingBuyers.value = false;
    }
}

function selectBuyer(id: number) {
    selectedBuyerId.value = id;
    const b = buyerList.value.find(x => x.id === id);
    if (b) {
        buyerForm.value.buyerName = b.name;
        buyerForm.value.buyerTel = b.tel;
        buyerForm.value.buyerId = b.id;
    }
}

async function submitCreateAndStart() {
    if (!selectedTicket.value || !projectInfo.value) return;
    creating.value = true;
    try {
        const hash = await AddTicket({
            hash: '',
            projectId: Number(projectInfo.value.ProjectID),
            projectName: projectInfo.value.ProjectName,
            screenId: selectedTicket.value.screenId,
            screenName: selectedTicket.value.name,
            skuId: selectedTicket.value.skuId,
            skuName: selectedTicket.value.desc,
            start: apiStartUnix.value,
            expire: apiEndUnix.value,
            buyerName: buyerForm.value.buyerName,
            buyerTel: buyerForm.value.buyerTel,
            buyerId: Number(buyerForm.value.buyerId),
            stat: 0,
        });

        debugLog('[AddTicket] hash:', hash);

        await AddTicketTask(hash);

        debugLog('[AddTicketTask] started for hash:', hash);
        messages.add({ text: t('ticketProject.taskCreated', { hash: hash.slice(0, 8) }), color: 'success', timeout: 3000 });
        showCreateDialog.value = false;

        if (goScheduler.value) {
            router.push('/scheduler');
        }
    } catch (e: any) {
        messages.add({ text: t('ticketProject.createFailed', { error: String(e) }), color: 'error', timeout: 4000 });
    } finally {
        creating.value = false;
    }
}

function formatPrice(price: number): string {
    return (price / 100).toFixed(2);
}
</script>

<template>
    <h1>{{ t('ticketProject.title') }}</h1>
    <v-divider thickness="3" />

    <div class="mt-4 d-flex ga-2">
        <v-text-field v-model="projectId" :label="t('ticketProject.projectIdLabel')"
            :placeholder="t('ticketProject.projectIdPlaceholder')" variant="outlined" density="compact"
            hide-details="auto" style="max-width: 300px;" @keyup.enter="lookupProject" />
        <v-btn :loading="loading" color="primary" @click="lookupProject">{{ t('ticketProject.search') }}</v-btn>
    </div>

    <!-- Project Info -->
    <v-card v-if="projectInfo" class="mt-4 pa-4" variant="outlined">
        <v-card-title>{{ projectInfo.ProjectName }}
            <v-chip v-if="projectInfo.IsHotProject" color="error" size="small">{{ t('ticketProject.hot') }}</v-chip>
        </v-card-title>
        <v-card-text>
            <p>{{ t('ticketProject.projectId') }}: {{ projectInfo.ProjectID }}</p>
            <p>
                {{ t('ticketProject.sale') }}: {{ projectInfo.StartTime }} ~ {{ projectInfo.EndTime }}
            </p>
            <p v-if="projectInfo.IsForceRealName">{{ t('ticketProject.realNameRequired') }}</p>
            <p v-if="projectInfo.IsNeedContact">{{ t('ticketProject.contactRequired') }}</p>
        </v-card-text>
    </v-card>

    <!-- Ticket SKU list -->
    <v-card v-if="tickets.length > 0" class="mt-4" variant="outlined">
        <v-card-title>{{ t('ticketProject.tickets', { count: tickets.length }) }}</v-card-title>

        <!-- Filter by display_name -->
        <v-card-text class="pb-2">
            <v-select v-model="filterStatus" :items="[
                { title: t('ticketProject.filterAll'), value: 'all' },
                ...filterOptions.map(name => ({ title: name, value: name }))
            ]" item-title="title" item-value="value" :label="t('ticketProject.filterPlaceholder')" variant="outlined"
                density="compact" hide-details style="max-width: 280px;" />
        </v-card-text>

        <v-list density="compact">
            <v-list-item v-for="t_item in filteredTickets" :key="`${t_item.screenId}-${t_item.skuId}`"
                class="clickable-row" @click="openCreateDialog(t_item)">
                <template #title>
                    {{ t_item.name }} — {{ t_item.desc }}
                    <v-chip v-if="t_item.flags.display_name" size="x-small" variant="tonal" class="ml-1"
                        :color="t_item.flags.display_name.includes('售罄') || t_item.flags.display_name.includes('停售') ? 'red' : t_item.flags.display_name.includes('未开') ? 'grey' : t_item.flags.display_name.includes('不可') ? 'yellow' : 'green'">
                        {{ t_item.flags.display_name }}
                    </v-chip>
                </template>
                <template #subtitle>
                    {{ t('ticketProject.sku') }}: {{ t_item.skuId }} | {{ t('ticketProject.screen') }}: {{
                        t_item.screenId }} | {{ t('ticketProject.price') }}: ¥{{ formatPrice(t_item.price) }}
                </template>
                <template #append>
                    <v-btn icon="mdi-plus-circle-outline" size="x-small" variant="text" color="primary"
                        @click.stop="openCreateDialog(t_item)" />
                </template>
            </v-list-item>
        </v-list>
    </v-card>

    <!-- Quick Create & Start Dialog -->
    <v-dialog v-model="showCreateDialog" max-width="520">
        <v-card :title="t('ticketProject.createTicket')">
            <v-card-text>
                <v-alert v-if="selectedTicket && projectInfo" density="compact" variant="tonal" color="info"
                    class="mb-3">
                    <strong>{{ projectInfo.ProjectName }}</strong>
                    <br />
                    {{ selectedTicket.name }} — {{ selectedTicket.desc }}
                    <br />
                    ¥{{ formatPrice(selectedTicket.price) }}
                </v-alert>

                <v-row dense>
                    <!-- Real-name buyer picker (shown when project requires real-name) -->
                    <v-col v-if="projectInfo?.IsForceRealName" cols="12">
                        <div class="d-flex align-center ga-2 mb-2">
                            <v-btn :loading="fetchingBuyers" prepend-icon="mdi-account-search" color="warning"
                                variant="tonal" size="small" @click="fetchBuyers">
                                {{ t('ticketProject.fetchBuyers') }}
                            </v-btn>
                            <span v-if="buyerList.length > 0" class="text-caption text-green">
                                {{ t('ticketProject.buyersLoaded', { count: buyerList.length }) }}
                            </span>
                            <span v-else-if="!fetchingBuyers" class="text-caption text-grey">
                                {{ t('ticketProject.clickToFetchBuyers') }}
                            </span>
                        </div>

                        <v-select v-if="buyerList.length > 0" v-model="selectedBuyerId" :items="buyerList"
                            item-title="name" item-value="id" :label="t('ticketProject.selectBuyer')" variant="outlined"
                            density="compact" hide-details="auto" @update:model-value="(v: number) => selectBuyer(v)">
                            <template #item="{ props, item: internalItem }">
                                <v-list-item v-bind="props"
                                    :subtitle="`${internalItem.tel} · ${t('ticketProject.idCard')}: ${internalItem.personalId || '—'}`" />
                            </template>
                        </v-select>

                        <div v-if="selectedBuyerId" class="mt-2 text-caption text-grey">
                            {{ t('ticketProject.selected') }}: {{ buyerForm.buyerName }} ({{ buyerForm.buyerTel }})
                        </div>
                    </v-col>

                    <!-- Ordinary buyer fields (shown for non-real-name projects) -->
                    <template v-else>
                        <v-col cols="6">
                            <v-text-field v-model="buyerForm.buyerName" :label="t('ticketProject.buyerNameLabel')"
                                variant="outlined" density="compact" hide-details="auto" required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="buyerForm.buyerTel" :label="t('ticketProject.buyerTelLabel')"
                                variant="outlined" density="compact" hide-details="auto" />
                        </v-col>
                    </template>

                    <v-col cols="12">
                        <v-alert density="compact" variant="tonal" color="grey" class="text-caption">
                            <div>{{ t('ticketProject.saleStart') }}: {{ apiStartLabel }}</div>
                            <div>{{ t('ticketProject.saleEnd') }}: {{ apiEndLabel }}</div>
                            <div class="text-grey-lighten-1">{{ t('ticketProject.timeFromApi') }}</div>
                        </v-alert>
                    </v-col>
                </v-row>

                <v-checkbox v-model="goScheduler" :label="t('ticketProject.goToScheduler')" density="compact"
                    hide-details class="mt-2" />
            </v-card-text>
            <v-card-actions>
                <v-spacer />
                <v-btn variant="text" @click="showCreateDialog = false">{{ t('common.cancel') }}</v-btn>
                <v-btn color="primary" variant="tonal" :loading="creating" :disabled="!formValid"
                    @click="submitCreateAndStart">
                    {{ t('ticketProject.createAndStart') }}
                </v-btn>
            </v-card-actions>
        </v-card>
    </v-dialog>
</template>

<style scoped>
.clickable-row {
    cursor: pointer;
}
</style>
