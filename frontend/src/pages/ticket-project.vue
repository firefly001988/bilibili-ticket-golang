<script lang="ts" setup>
import { ref, computed } from 'vue';
import { GetProjectInformation, GetTicketSkuIDsByProjectID } from '../../wailsjs/go/biliutils/BiliClient';
import { AddTicket, AddTicketTask, FetchRealNameBuyers } from '../../wailsjs/go/scheduler/SchedulerService';
import type { _return } from '../../wailsjs/go/models';
import { useMessagesStore } from '@/stores/snackbar';
import { useRouter } from 'vue-router';
import { DEFAULT_INTERVAL_MS, DEFAULT_EXPIRE_DAYS, SECONDS_PER_DAY } from '@/composables/defaults';
import { useDebug } from '@/composables/useDebug';

const router = useRouter();
const { debugLog, debugGroup } = useDebug();
const messages = useMessagesStore();

const projectId = ref('');
const loading = ref(false);
const projectInfo = ref<_return.ProjectInformation | null>(null);
const tickets = ref<_return.TicketSkuScreenID[]>([]);

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
    intervalMs: DEFAULT_INTERVAL_MS,
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
        messages.add({ text: 'Please enter a project ID.', color: 'warning', timeout: 2000 });
        return;
    }
    loading.value = true;
    try {
        const [info, tks] = await Promise.all([
            GetProjectInformation(projectId.value.trim()),
            GetTicketSkuIDsByProjectID(projectId.value.trim()),
        ]);

        // Debug: print raw API response structs to browser console
        debugGroup(`[DEBUG] Project Lookup: ${projectId.value}`, () => {
            debugLog('ProjectInformation:', JSON.stringify(info, null, 2), info);
            debugLog('TicketSkuList:', JSON.stringify(tks, null, 2), tks);
        });

        projectInfo.value = info;
        tickets.value = tks || [];
    } catch (e: any) {
        messages.add({ text: `Error: ${e}`, color: 'error', timeout: 4000 });
    } finally {
        loading.value = false;
    }
}

function openCreateDialog(ticket: _return.TicketSkuScreenID) {
    selectedTicket.value = ticket;

    // Extract start/end from the API:
    //   - start: SKU's saleStat.start (when polling should begin)
    //   - expire: SKU's saleStat.end   (when polling should stop)
    //   - fallback to project-level StartTime/EndTime
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
        apiStartLabel.value = '(立即)';
    }

    if (skuEnd && toUnix(skuEnd) > 0) {
        apiEndUnix.value = toUnix(skuEnd);
        apiEndLabel.value = fmt(new Date(skuEnd as any));
    } else if (projEnd) {
        apiEndUnix.value = toUnix(projEnd);
        apiEndLabel.value = fmt(new Date(projEnd as any));
    } else {
        // Default: 30 days from now
        apiEndUnix.value = Math.floor(Date.now() / 1000) + SECONDS_PER_DAY * DEFAULT_EXPIRE_DAYS;
        apiEndLabel.value = fmt(new Date(Date.now() + SECONDS_PER_DAY * DEFAULT_EXPIRE_DAYS * 1000));
    }

    buyerForm.value = { buyerName: '', buyerTel: '', buyerId: 0, intervalMs: DEFAULT_INTERVAL_MS };
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
            messages.add({ text: '未找到实名购票人，请先在 Bilibili App 中添加', color: 'warning', timeout: 3000 });
        }
    } catch (e: any) {
        messages.add({ text: `获取购票人失败: ${e}`, color: 'error', timeout: 4000 });
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

        await AddTicketTask(hash, buyerForm.value.intervalMs);

        debugLog('[AddTicketTask] started for hash:', hash);
        messages.add({ text: `任务已创建并启动 (${hash.slice(0, 8)}...)`, color: 'success', timeout: 3000 });
        showCreateDialog.value = false;

        if (goScheduler.value) {
            router.push('/scheduler');
        }
    } catch (e: any) {
        messages.add({ text: `创建失败: ${e}`, color: 'error', timeout: 4000 });
    } finally {
        creating.value = false;
    }
}

function formatPrice(price: number): string {
    return (price / 100).toFixed(2);
}
</script>

<template>
    <h1>Project Lookup</h1>
    <v-divider thickness="3" />

    <div class="mt-4 d-flex ga-2">
        <v-text-field v-model="projectId" label="Project ID" placeholder="e.g. 103601" variant="outlined"
            density="compact" hide-details="auto" style="max-width: 300px;" @keyup.enter="lookupProject" />
        <v-btn :loading="loading" color="primary" @click="lookupProject">Search</v-btn>
    </div>

    <!-- Project Info -->
    <v-card v-if="projectInfo" class="mt-4 pa-4" variant="outlined">
        <v-card-title>{{ projectInfo.ProjectName }} <v-chip v-if="projectInfo.IsHotProject" color="error"
                size="small">Hot
            </v-chip></v-card-title>
        <v-card-text>
            <p>Project ID: {{ projectInfo.ProjectID }}</p>
            <p>
                Sale: {{ projectInfo.StartTime }} ~ {{ projectInfo.EndTime }}
            </p>
            <p v-if="projectInfo.IsForceRealName">⚠ Real-name required</p>
            <p v-if="projectInfo.IsNeedContact">⚠ Contact info required</p>
        </v-card-text>
    </v-card>

    <!-- Ticket SKU list -->
    <v-card v-if="tickets.length > 0" class="mt-4" variant="outlined">
        <v-card-title>Tickets ({{ tickets.length }})</v-card-title>
        <v-list density="compact">
            <v-list-item v-for="t in tickets" :key="`${t.screenId}-${t.skuId}`" class="clickable-row"
                @click="openCreateDialog(t)">
                <template #title>
                    {{ t.name }} — {{ t.desc }}
                    <v-chip v-if="t.flags.display_name" size="x-small" variant="tonal" class="ml-1"
                        :color="t.flags.display_name.includes('售罄') || t.flags.display_name.includes('停售') ? 'red' : t.flags.display_name.includes('未开') ? 'grey' : 'green'">
                        {{ t.flags.display_name }}
                    </v-chip>
                </template>
                <template #subtitle>
                    SKU: {{ t.skuId }} | Screen: {{ t.screenId }} | Price: ¥{{ formatPrice(t.price) }}
                </template>
                <template #append>
                    <v-btn icon="mdi-plus-circle-outline" size="x-small" variant="text" color="primary"
                        @click.stop="openCreateDialog(t)" />
                </template>
            </v-list-item>
        </v-list>
    </v-card>

    <!-- Quick Create & Start Dialog -->
    <v-dialog v-model="showCreateDialog" max-width="520">
        <v-card :title="'发起抢票'">
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
                                获取实名购票人
                            </v-btn>
                            <span v-if="buyerList.length > 0" class="text-caption text-green">
                                已加载 {{ buyerList.length }} 人
                            </span>
                            <span v-else-if="!fetchingBuyers" class="text-caption text-grey">
                                点击获取已认证的购票人
                            </span>
                        </div>

                        <v-select v-if="buyerList.length > 0" v-model="selectedBuyerId" :items="buyerList"
                            item-title="name" item-value="id" label="选择实名购票人" variant="outlined" density="compact"
                            hide-details="auto" @update:model-value="(v: number) => selectBuyer(v)">
                            <template #item="{ props, item: internalItem }">
                                <v-list-item v-bind="props"
                                    :subtitle="`${internalItem.tel} · 证件: ${internalItem.personalId || '—'}`" />
                            </template>
                        </v-select>

                        <div v-if="selectedBuyerId" class="mt-2 text-caption text-grey">
                            已选: {{ buyerForm.buyerName }} ({{ buyerForm.buyerTel }})
                        </div>
                    </v-col>

                    <!-- Ordinary buyer fields (shown for non-real-name projects) -->
                    <template v-else>
                        <v-col cols="6">
                            <v-text-field v-model="buyerForm.buyerName" label="购票人姓名" variant="outlined"
                                density="compact" hide-details="auto" required />
                        </v-col>
                        <v-col cols="6">
                            <v-text-field v-model="buyerForm.buyerTel" label="购票人电话" variant="outlined"
                                density="compact" hide-details="auto" />
                        </v-col>
                    </template>

                    <v-col cols="6">
                        <v-text-field v-model="buyerForm.intervalMs" label="提交间隔 (ms)" type="number" variant="outlined"
                            density="compact" hide-details="auto" :hint="`默认 ${DEFAULT_INTERVAL_MS}ms`"
                            persistent-hint />
                    </v-col>
                    <v-col cols="12">
                        <v-alert density="compact" variant="tonal" color="grey" class="text-caption">
                            <div>开售: {{ apiStartLabel }}</div>
                            <div>截止: {{ apiEndLabel }}</div>
                            <div class="text-grey-lighten-1">(时间信息来自接口返回，无需手动填写)</div>
                        </v-alert>
                    </v-col>
                </v-row>

                <v-checkbox v-model="goScheduler" label="创建后跳转到任务管理页面" density="compact" hide-details class="mt-2" />
            </v-card-text>
            <v-card-actions>
                <v-spacer />
                <v-btn variant="text" @click="showCreateDialog = false">取消</v-btn>
                <v-btn color="primary" variant="tonal" :loading="creating" :disabled="!formValid"
                    @click="submitCreateAndStart">
                    创建并启动
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
