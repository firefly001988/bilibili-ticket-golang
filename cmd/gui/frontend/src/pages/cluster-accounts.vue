<script lang="ts" setup>
import { ref } from 'vue'
import { useCluster } from '@/composables/useCluster'
import { useConfirm } from '@/composables/useConfirm'
import { clusterCall } from '@/composables/clusterTypes'
import { useMessagesStore } from '@/stores/snackbar'
import type { ResourceRole } from '@/composables/clusterTypes'
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

    <div class="text-h6 mb-2">已识别购票人</div>
    <v-alert type="info" variant="tonal" class="mb-3">系统根据账号中的实名购票人自动建立跨账号映射；内部逻辑 ID 无需用户管理。</v-alert>
    <v-chip v-for="buyer in snapshot.buyers" :key="buyer.logicalId" class="mr-2 mb-2" prepend-icon="mdi-account">{{
      buyer.name }} · {{ buyer.tel || '无手机号' }}{{ buyer.idCard ? ` · ${buyer.idCard}` : '' }}</v-chip>
  </div>
</template>
