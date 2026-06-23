<script lang="ts" setup>
import { ref, computed } from 'vue'
import { useCluster } from '@/composables/useCluster'
import { useConfirm } from '@/composables/useConfirm'
import { clusterCall } from '@/composables/clusterTypes'
import { useMessagesStore } from '@/stores/snackbar'
import type { ResourceRole, LogicalBuyer, AccountSummary } from '@/composables/clusterTypes'
import VueQr from 'vue-qr'

const messages = useMessagesStore()
const { snapshot, refresh, invoke } = useCluster()

const accountJSON = ref('')
const login = ref({ name: '', role: 'primary' as ResourceRole, sessionId: '', url: '', message: '' })
let loginTimer: number | undefined

const { show: showConfirm } = useConfirm()

async function beginLogin() {
  const result = await clusterCall<{ sessionId: string; url: string }>('BeginAccountLogin', login.value.name, login.value.role)
  login.value.sessionId = result.sessionId; login.value.url = result.url
  if (loginTimer) window.clearInterval(loginTimer)
  loginTimer = window.setInterval(async () => {
    const state = await clusterCall<{ code: number; message: string; accountId?: string }>('PollAccountLogin', login.value.sessionId)
    login.value.message = state.message
    if (state.accountId) { window.clearInterval(loginTimer); loginTimer = undefined; login.value.url = ''; await refresh() }
  }, 2000)
}

const importAccount = () => invoke('ImportAccount', accountJSON.value).catch(() => { })

async function syncBuyers(accountId: string) {
  try { await clusterCall('SyncAccountBuyers', accountId); await refresh() }
  catch (e: any) { messages.add({ text: String(e), color: 'error', timeout: 5000 }) }
}

async function deleteAccount(id: string, name: string) {
  const ok = await showConfirm('删除账号', `确定删除账号「${name}」及其购票人映射吗？`)
  if (!ok) return
  await invoke('DeleteAccount', id).catch(() => { })
}

const syncingAll = ref(false)

/** Identity type → single char abbreviation. */
function idTypeChar(type: number): string {
  switch (type) {
    case 0: return '身'
    case 1: return '护'
    case 2: return '港'
    case 3: return '台'
    default: return '?'
  }
}

/** Identity type → full Chinese name. */
function idTypeName(type: number): string {
  switch (type) {
    case 0: return '身份证'
    case 1: return '护照'
    case 2: return '港澳通行证'
    case 3: return '台湾通行证'
    default: return '未知'
  }
}

/** Identity type → chip color. */
function idTypeColor(type: number): string {
  switch (type) {
    case 0: return 'primary'
    case 1: return 'success'
    case 2: return 'warning'
    case 3: return 'error'
    default: return 'grey'
  }
}

async function syncAllBuyers() {
  syncingAll.value = true
  try { await clusterCall('SyncAllAccountBuyers'); await refresh() }
  catch (e: any) { messages.add({ text: String(e), color: 'error', timeout: 5000 }) }
  finally { syncingAll.value = false }
}

// ── Sync buyer to other accounts ──

const syncDialogOpen = ref(false)
const syncTargetBuyer = ref<LogicalBuyer | null>(null)
const syncTargetAccountId = ref('')
const syncingBuyer = ref(false)

/** Accounts that do NOT already have this buyer. */
const availableSyncAccounts = computed<AccountSummary[]>(() => {
  if (!syncTargetBuyer.value) return []
  const existingIds = new Set((syncTargetBuyer.value.accounts || []).map(a => a.accountId))
  return snapshot.value.accounts.filter(a => a.enabled && !existingIds.has(a.id))
})

function openSyncDialog(buyer: LogicalBuyer) {
  syncTargetBuyer.value = buyer
  syncTargetAccountId.value = ''
  syncDialogOpen.value = true
}

async function doSyncBuyerToAccount() {
  if (!syncTargetBuyer.value || !syncTargetAccountId.value) return
  syncingBuyer.value = true
  try {
    await clusterCall('SyncBuyerToAccount', syncTargetBuyer.value.logicalId, syncTargetAccountId.value)
    await refresh()
    syncDialogOpen.value = false
    messages.add({ text: `已将「${syncTargetBuyer.value.name}」同步到目标账号`, color: 'success', timeout: 3000 })
  } catch (e: any) {
    messages.add({ text: String(e), color: 'error', timeout: 5000 })
  } finally {
    syncingBuyer.value = false
  }
}

const syncingBuyerAll = ref(false)

async function doSyncBuyerToAllAccounts() {
  if (!syncTargetBuyer.value) return
  const count = availableSyncAccounts.value.length
  if (count === 0) {
    messages.add({ text: '所有已启用的账号均已关联该购票人，无需同步', color: 'info', timeout: 3000 })
    return
  }
  syncingBuyerAll.value = true
  try {
    await clusterCall('SyncBuyerToAllAccounts', syncTargetBuyer.value.logicalId)
    await refresh()
    syncDialogOpen.value = false
    messages.add({ text: `已将「${syncTargetBuyer.value.name}」同步到 ${count} 个账号`, color: 'success', timeout: 3000 })
  } catch (e: any) {
    messages.add({ text: String(e), color: 'error', timeout: 5000 })
  } finally {
    syncingBuyerAll.value = false
  }
}
</script>

<template>
  <div>
    <v-row class="mb-4">
      <v-col cols="8">
        <div class="text-h6 mb-2">逐账号扫码登录</div>
        <v-text-field v-model="login.name" label="账号备注" />
        <v-select v-model="login.role" :items="['primary', 'standby']" label="角色" />
        <v-btn color="primary" @click="beginLogin">生成独立二维码</v-btn>
        <div class="mt-2">{{ login.message }}</div>
      </v-col>
      <v-col cols="4" class="text-center">
        <VueQr v-if="login.url" :text="login.url" :size="180" />
      </v-col>
    </v-row>

    <v-divider class="mb-4" />

    <v-row>
      <v-col cols="7">
        <v-table density="compact">
          <thead>
            <tr>
              <th>账号</th>
              <th>角色</th>
              <th>状态</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in snapshot.accounts" :key="item.id">
              <td>{{ item.name || item.id }}</td>
              <td><v-chip size="small">{{ item.role }}</v-chip></td>
              <td>{{ item.enabled ? (item.cooldownUntil ? `冷却至 ${item.cooldownUntil}` : '可用') : '停用' }}</td>
              <td>
                <v-btn size="small" variant="tonal" prepend-icon="mdi-account-sync"
                  @click="syncBuyers(item.id)">同步</v-btn>
                <v-btn class="ml-1" size="small" variant="text" color="error" icon="mdi-delete"
                  @click="deleteAccount(item.id, item.name || item.id)" />
              </td>
            </tr>
          </tbody>
        </v-table>
      </v-col>
      <v-col cols="5">
        <v-textarea v-model="accountJSON" label="标准凭据 JSON" rows="8" />
        <v-btn color="primary" block @click="importAccount">导入账号</v-btn>
      </v-col>
    </v-row>

    <v-divider class="my-5" />

    <div class="d-flex align-center mb-2">
      <div class="text-h6 mr-3">已识别购票人</div>
      <v-btn size="small" variant="tonal" color="primary" prepend-icon="mdi-refresh" :loading="syncingAll"
        @click="syncAllBuyers">刷新全部</v-btn>
    </div>
    <v-expansion-panels multiple>
      <v-row>
        <v-col v-for="buyer in snapshot.buyers" :key="buyer.logicalId" cols="12" md="6" lg="4">
          <v-expansion-panel>
            <v-expansion-panel-title>
              <template #default>
                <v-tooltip location="top">
                  <template #activator="{ props }">
                    <v-chip size="x-small" class="mr-2 flex-shrink-0 pa-0" v-bind="props"
                      :color="idTypeColor(buyer.type)" variant="flat"
                      style="width:20px;height:20px;justify-content:center">
                      {{ idTypeChar(buyer.type) }}
                    </v-chip>
                  </template>
                  <span>{{ idTypeName(buyer.type) }} · {{ buyer.name }}{{ buyer.tel ? ` · ${buyer.tel}` : '' }}{{
                    buyer.idCard ? ` ·
                    ${buyer.idCard}` : '' }}</span>
                </v-tooltip>
                <span class="font-weight-bold flex-shrink-0">{{ buyer.name }}</span>
                <span class="text-medium-emphasis text-truncate ml-2 d-none d-sm-inline"
                  style="min-width:0;flex-shrink:1" v-if="buyer.tel">{{ buyer.tel }}</span>
                <span class="text-medium-emphasis text-truncate ml-2 d-none d-sm-inline"
                  style="min-width:0;flex-shrink:1" v-if="buyer.idCard">{{ buyer.idCard }}</span>
                <v-spacer />
                <v-chip size="x-small" class="flex-shrink-0" color="info" variant="flat">{{ buyer.accounts?.length || 0
                }} 个账号</v-chip>
              </template>
            </v-expansion-panel-title>
            <v-expansion-panel-text>
              <div class="d-flex flex-wrap align-center ga-1 bt-2">
                <v-chip v-for="acc in buyer.accounts" :key="acc.accountId" size="small">
                  {{ acc.accountName || acc.accountId }}({{ acc.uid }})
                </v-chip>
                <span v-if="!buyer.accounts || buyer.accounts.length === 0"
                  class="text-body-2 text-medium-emphasis">无关联账号</span>
                <v-spacer />
                <v-btn size="x-small" variant="tonal" color="primary" icon="mdi-account-multiple-plus"
                  @click.stop="openSyncDialog(buyer)" />
              </div>
            </v-expansion-panel-text>
          </v-expansion-panel>
        </v-col>
      </v-row>
    </v-expansion-panels>

    <!-- Sync buyer to other account dialog -->
    <v-dialog v-model="syncDialogOpen" max-width="420">
      <v-card>
        <v-card-title class="text-h6">
          同步购票人到其他账号
        </v-card-title>
        <v-card-text>
          <div class="mb-3">
            将 <strong>{{ syncTargetBuyer?.name }}</strong>
            <span v-if="syncTargetBuyer?.idCard">（{{ syncTargetBuyer?.idCard }}）</span>
            添加到所选 B 站账号的实名列表中。
          </div>
          <v-select v-model="syncTargetAccountId" :items="availableSyncAccounts" item-title="name" item-value="id"
            label="选择目标账号" :subtitle="(item: AccountSummary) => item.id" :disabled="availableSyncAccounts.length === 0"
            :no-data-text="availableSyncAccounts.length === 0 ? '所有已启用的账号均已关联该购票人' : '无可用账号'" />
        </v-card-text>
        <v-card-actions>
          <v-btn variant="text" color="primary" :loading="syncingBuyerAll"
            :disabled="availableSyncAccounts.length === 0" @click="doSyncBuyerToAllAccounts">同步到全部（{{
              availableSyncAccounts.length }}）</v-btn>
          <v-spacer />
          <v-btn variant="text" @click="syncDialogOpen = false">取消</v-btn>
          <v-btn color="primary" variant="tonal" :loading="syncingBuyer" :disabled="!syncTargetAccountId"
            @click="doSyncBuyerToAccount">同步</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>
