<script lang="ts" setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { clusterCall, type ClusterSnapshot, type ResourceRole } from '@/composables/clusterTypes'

const tab = ref('tasks')
const loading = ref(false)
const error = ref('')
const snapshot = ref<ClusterSnapshot>({ accounts: [], workers: [], macros: [], attempts: [] })
const accountJSON = ref('')
const worker = ref({ id: '', name: '', baseUrl: 'http://127.0.0.1:18080', key: '', role: 'primary' as ResourceRole })
const macroJSON = ref('')
const purchaseJSON = ref('')
const provisionJSON = ref('')
let timer: number | undefined

async function refresh() {
  try {
    snapshot.value = await clusterCall<ClusterSnapshot>('Snapshot')
    error.value = ''
  } catch (e) { error.value = String(e) }
}

async function invoke(method: string, ...args: any[]) {
  loading.value = true
  try { await clusterCall(method, ...args); await refresh() }
  catch (e) { error.value = String(e) }
  finally { loading.value = false }
}

const importAccount = () => invoke('ImportAccount', accountJSON.value)
const addWorker = () => invoke('AddWorker', JSON.stringify(worker.value))
const saveMacro = () => invoke('SaveMacro', macroJSON.value)
const savePurchase = () => invoke('SavePurchaseGroup', purchaseJSON.value)
const provisionBuyer = () => invoke('ProvisionBuyer', provisionJSON.value, true)
const switchReflow = () => invoke('SwitchToReflow')

onMounted(async () => { await refresh(); timer = window.setInterval(refresh, 5000) })
onUnmounted(() => { if (timer) window.clearInterval(timer) })
</script>

<template>
  <v-card>
    <v-card-title class="d-flex align-center ga-3">
      <v-icon>mdi-server-network</v-icon>
      雇主—雇员集群调度
      <v-spacer />
      <v-btn :loading="loading" prepend-icon="mdi-refresh" variant="text" @click="refresh">刷新</v-btn>
    </v-card-title>
    <v-alert v-if="error" type="error" closable class="ma-4" @click:close="error = ''">{{ error }}</v-alert>
    <v-tabs v-model="tab" grow>
      <v-tab value="tasks">任务规划</v-tab>
      <v-tab value="accounts">账号池</v-tab>
      <v-tab value="workers">Worker 池</v-tab>
      <v-tab value="attempts">执行监控</v-tab>
    </v-tabs>

    <v-window v-model="tab">
      <v-window-item value="tasks">
        <v-card-text>
          <div class="d-flex align-center mb-4">
            <div>
              <div class="text-h6">SKU 宏任务</div>
              <div class="text-medium-emphasis">活动日期必须确认；准点可智能合并，回流只按原组或单人拆分。</div>
            </div>
            <v-spacer />
            <v-btn color="warning" prepend-icon="mdi-swap-horizontal" @click="switchReflow">停止准点并切换回流</v-btn>
          </div>
          <v-table density="compact">
            <thead><tr><th>项目 / SKU</th><th>活动日</th><th>容量</th><th>副本</th><th>优先级</th><th>阶段 / 审核</th></tr></thead>
            <tbody>
              <tr v-for="item in snapshot.macros" :key="item.id">
                <td>{{ item.projectId }} / {{ item.skuId }}</td><td>{{ item.eventDay || '未设置' }}</td><td>{{ item.orderCapacity }}</td>
                <td>{{ item.desiredReplicas }} / {{ item.hardConcurrency }}</td><td>{{ item.priority }}</td>
                <td><v-chip size="small" :color="item.needsReview ? 'warning' : 'success'">{{ item.phase }} · {{ item.needsReview ? '待确认' : '可调度' }}</v-chip></td>
              </tr>
            </tbody>
          </v-table>
          <v-expansion-panels class="mt-5">
            <v-expansion-panel title="创建或更新宏任务（JSON）"><v-expansion-panel-text><v-textarea v-model="macroJSON" rows="8" label="MacroTask JSON" /><v-btn color="primary" @click="saveMacro">保存宏任务</v-btn></v-expansion-panel-text></v-expansion-panel>
            <v-expansion-panel title="添加购票组（JSON）"><v-expansion-panel-text><v-textarea v-model="purchaseJSON" rows="7" label="PurchaseGroup JSON（allowSplit 仅影响回流）" /><v-btn color="primary" @click="savePurchase">保存购票组</v-btn></v-expansion-panel-text></v-expansion-panel>
          </v-expansion-panels>
        </v-card-text>
      </v-window-item>

      <v-window-item value="accounts">
        <v-card-text>
          <v-row>
            <v-col cols="7"><v-table density="compact"><thead><tr><th>账号</th><th>角色</th><th>凭据版本</th><th>状态</th></tr></thead><tbody><tr v-for="item in snapshot.accounts" :key="item.id"><td>{{ item.name || item.id }}</td><td><v-chip size="small">{{ item.role }}</v-chip></td><td>{{ item.credentialVersion }}</td><td>{{ item.enabled ? (item.cooldownUntil ? `冷却至 ${item.cooldownUntil}` : '可用') : '停用' }}</td></tr></tbody></v-table></v-col>
            <v-col cols="5"><v-textarea v-model="accountJSON" label="标准凭据 JSON" rows="8" /><v-btn color="primary" block @click="importAccount">导入账号</v-btn></v-col>
          </v-row>
          <v-divider class="my-5" />
          <div class="text-h6 mb-2">显式补全购票人</div>
          <v-alert type="warning" variant="tonal" class="mb-3">确认后会调用 Bilibili API 修改指定账号；系统不会自动拆单或未经确认创建购票人。</v-alert>
          <v-textarea v-model="provisionJSON" label="{ accountId, buyer }" rows="5" /><v-btn color="warning" @click="provisionBuyer">确认并补全</v-btn>
        </v-card-text>
      </v-window-item>

      <v-window-item value="workers">
        <v-card-text>
          <v-row>
            <v-col cols="7"><v-table density="compact"><thead><tr><th>Worker</th><th>地址</th><th>角色</th><th>健康 / 活动任务</th></tr></thead><tbody><tr v-for="item in snapshot.workers" :key="item.id"><td>{{ item.name || item.id }}</td><td>{{ item.baseUrl }}</td><td>{{ item.role }}</td><td><v-chip size="small" :color="item.healthy ? 'success' : 'error'">{{ item.healthy ? (item.activeAttemptId || '空闲') : '失联' }}</v-chip></td></tr></tbody></v-table></v-col>
            <v-col cols="5"><v-text-field v-model="worker.id" label="Worker ID" /><v-text-field v-model="worker.name" label="名称" /><v-text-field v-model="worker.baseUrl" label="HTTP 地址" /><v-text-field v-model="worker.key" label="独立控制密钥" type="password" /><v-select v-model="worker.role" :items="['primary', 'standby']" label="角色" /><v-btn color="primary" block @click="addWorker">添加 Worker</v-btn></v-col>
          </v-row>
        </v-card-text>
      </v-window-item>

      <v-window-item value="attempts">
        <v-card-text><v-table density="compact"><thead><tr><th>Attempt</th><th>Intent</th><th>账号</th><th>Worker</th><th>状态</th><th>订单 / 原因</th></tr></thead><tbody><tr v-for="item in snapshot.attempts" :key="item.id"><td>{{ item.id }}</td><td>{{ item.intentId }}</td><td>{{ item.accountId }}</td><td>{{ item.workerId }}</td><td><v-chip size="small">{{ item.state }}</v-chip></td><td>{{ item.orderId || item.reason || '-' }}</td></tr></tbody></v-table></v-card-text>
      </v-window-item>
    </v-window>
  </v-card>
</template>
