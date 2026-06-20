<script lang="ts" setup>
import { onMounted, onUnmounted, ref } from 'vue'
import { clusterCall, type ClusterSnapshot, type ResourceRole } from '@/composables/clusterTypes'
import VueQr from 'vue-qr'

const tab = ref('tasks')
const loading = ref(false)
const error = ref('')
const snapshot = ref<ClusterSnapshot>({ taskGroups: [], accounts: [], workers: [], macros: [], attempts: [] })
const accountJSON = ref('')
const worker = ref({ id: '', name: '', baseUrl: 'http://127.0.0.1:18080', key: '', role: 'primary' as ResourceRole })
const provisionJSON = ref('')
const taskGroup = ref({ name: '' })
const macro = ref({ id: '', taskGroupId: '', projectId: 0, screenId: 0, skuId: 0, eventDay: '', orderCapacity: 4, capacitySource: 'default', smartMerge: false, priority: 0, desiredReplicas: 1, hardConcurrency: 1, startAt: '', deadline: '' })
const purchase = ref({ macroTaskId: '', allowSplit: false, buyers: [{ logicalId: '', name: '', tel: '', idCard: '', type: 0 }] })
const login = ref({ name: '', role: 'primary' as ResourceRole, sessionId: '', url: '', message: '' })
const skuInspect = ref({ projectId: 0, screenId: 0, skuId: 0, eventDay: '', orderCapacity: 4, capacitySource: 'default', saleStart: '', saleEnd: '', confirmed: false })
let timer: number | undefined
let loginTimer: number | undefined

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
const provisionBuyer = () => invoke('ProvisionBuyer', provisionJSON.value, true)
const switchReflow = () => invoke('SwitchToReflow')

async function saveTaskGroup() {
  if (!taskGroup.value.name.trim()) { error.value = '请输入任务组名称'; return }
  await invoke('SaveTaskGroup', JSON.stringify(taskGroup.value))
  taskGroup.value.name = ''
}

async function saveMacro() {
  if (!skuInspect.value.confirmed) { error.value = '必须人工确认活动日期'; return }
  if (!macro.value.taskGroupId || !macro.value.startAt || !macro.value.deadline) { error.value = '任务组、开始时间和截止时间不能为空'; return }
  const document = { ...macro.value, eventDayConfirmed: true, needsReview: false, startAt: new Date(macro.value.startAt).toISOString(), deadline: new Date(macro.value.deadline).toISOString() }
  await invoke('SaveMacro', JSON.stringify(document))
}

async function savePurchase() {
  const buyers = purchase.value.buyers.filter(item => item.logicalId.trim())
  if (!purchase.value.macroTaskId || buyers.length === 0) { error.value = '请选择宏任务并至少填写一名购票人'; return }
  await invoke('SavePurchaseGroup', JSON.stringify({ id: `purchase-${Date.now()}`, macroTaskId: purchase.value.macroTaskId, allowSplit: purchase.value.allowSplit, buyers }))
}

const addBuyer = () => purchase.value.buyers.push({ logicalId: '', name: '', tel: '', idCard: '', type: 0 })
const removeBuyer = (index: number) => { if (purchase.value.buyers.length > 1) purchase.value.buyers.splice(index, 1) }

function localDateTime(value?: string) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const offset = date.getTimezoneOffset() * 60000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}

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

async function inspectSKU() {
  const result = await clusterCall<any>('InspectSKU', skuInspect.value.projectId, skuInspect.value.screenId, skuInspect.value.skuId)
  Object.assign(skuInspect.value, result, { confirmed: false })
  Object.assign(macro.value, { projectId: skuInspect.value.projectId, screenId: skuInspect.value.screenId, skuId: skuInspect.value.skuId, eventDay: result.eventDay, orderCapacity: result.orderCapacity, capacitySource: result.capacitySource, startAt: localDateTime(result.saleStart), deadline: localDateTime(result.saleEnd) })
}

function prepareMacro() {
  if (!skuInspect.value.confirmed) { error.value = '必须人工确认活动日期'; return }
  Object.assign(macro.value, { id: macro.value.id || `macro-${skuInspect.value.projectId}-${skuInspect.value.skuId}-${Date.now()}`, taskGroupId: macro.value.taskGroupId || snapshot.value.taskGroups[0]?.id || '', projectId: skuInspect.value.projectId, screenId: skuInspect.value.screenId, skuId: skuInspect.value.skuId, eventDay: skuInspect.value.eventDay, orderCapacity: skuInspect.value.orderCapacity, capacitySource: skuInspect.value.capacitySource })
}

onMounted(async () => { await refresh(); timer = window.setInterval(refresh, 5000) })
onUnmounted(() => { if (timer) window.clearInterval(timer); if (loginTimer) window.clearInterval(loginTimer) })
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
            <thead><tr><th>项目 / SKU</th><th>活动日</th><th>容量</th><th>副本</th><th>优先级</th><th>阶段 / 审核</th><th>操作</th></tr></thead>
            <tbody>
              <tr v-for="item in snapshot.macros" :key="item.id">
                <td>{{ item.projectId }} / {{ item.skuId }}</td><td>{{ item.eventDay || '未设置' }}</td><td>{{ item.orderCapacity }}</td>
                <td>{{ item.desiredReplicas }} / {{ item.hardConcurrency }}</td><td>{{ item.priority }}</td>
                <td><v-chip size="small" :color="item.needsReview ? 'warning' : 'success'">{{ item.phase }} · {{ item.needsReview ? '待确认' : '可调度' }}</v-chip></td>
                <td><v-btn size="small" :disabled="item.needsReview || !item.eventDayConfirmed" @click="invoke('StartMacro', item.id)">启动准点</v-btn></td>
              </tr>
            </tbody>
          </v-table>
          <v-expansion-panels class="mt-5">
            <v-expansion-panel title="创建任务组"><v-expansion-panel-text><div class="mb-2">现有：{{ snapshot.taskGroups.map(g => `${g.name} (${g.id})`).join('、') || '暂无' }}</div><v-text-field v-model="taskGroup.name" label="任务组名称" /><v-btn color="primary" @click="saveTaskGroup">保存任务组</v-btn></v-expansion-panel-text></v-expansion-panel>
            <v-expansion-panel title="创建或更新宏任务"><v-expansion-panel-text>
              <v-row><v-col><v-text-field v-model.number="skuInspect.projectId" label="Project ID" /></v-col><v-col><v-text-field v-model.number="skuInspect.screenId" label="Screen ID" /></v-col><v-col><v-text-field v-model.number="skuInspect.skuId" label="SKU ID" /></v-col></v-row>
              <v-btn variant="outlined" @click="inspectSKU">从 API 预填日期、售卖时间与订单上限</v-btn>
              <v-row class="mt-2"><v-col><v-text-field v-model="skuInspect.eventDay" label="活动日期 YYYY-MM-DD" /></v-col><v-col><v-text-field v-model.number="skuInspect.orderCapacity" label="单订单人数上限" /></v-col></v-row>
              <v-checkbox v-model="skuInspect.confirmed" label="我已人工确认活动日期正确" /><v-btn class="mb-3" @click="prepareMacro">应用 SKU 信息</v-btn>
              <v-row><v-col cols="6"><v-select v-model="macro.taskGroupId" :items="snapshot.taskGroups" item-title="name" item-value="id" label="所属任务组" /></v-col><v-col cols="6"><v-text-field v-model="macro.id" label="宏任务 ID" /></v-col></v-row>
              <v-row><v-col><v-text-field v-model="macro.startAt" type="datetime-local" label="开始执行时间" /></v-col><v-col><v-text-field v-model="macro.deadline" type="datetime-local" label="绝对截止时间" /></v-col></v-row>
              <v-row><v-col><v-text-field v-model.number="macro.priority" type="number" label="优先级" /></v-col><v-col><v-text-field v-model.number="macro.desiredReplicas" type="number" min="1" label="期望并发副本" /></v-col><v-col><v-text-field v-model.number="macro.hardConcurrency" type="number" min="1" label="硬并发上限" /></v-col></v-row>
              <v-switch v-model="macro.smartMerge" label="准点阶段智能合并购票组" color="primary" />
              <v-btn color="primary" @click="saveMacro">保存宏任务</v-btn>
            </v-expansion-panel-text></v-expansion-panel>
            <v-expansion-panel title="添加购票组"><v-expansion-panel-text>
              <v-select v-model="purchase.macroTaskId" :items="snapshot.macros" :item-title="item => `${item.projectId} / ${item.skuId} (${item.id})`" item-value="id" label="宏任务" />
              <v-switch v-model="purchase.allowSplit" label="回流阶段允许拆成单人订单" color="warning" />
              <v-row v-for="(buyer, index) in purchase.buyers" :key="index" align="center">
                <v-col cols="3"><v-text-field v-model="buyer.logicalId" label="购票人逻辑 ID" /></v-col><v-col cols="2"><v-text-field v-model="buyer.name" label="姓名" /></v-col><v-col cols="2"><v-text-field v-model="buyer.tel" label="手机号" /></v-col><v-col cols="3"><v-text-field v-model="buyer.idCard" label="证件号" /></v-col><v-col cols="1"><v-text-field v-model.number="buyer.type" type="number" label="类型" /></v-col><v-col cols="1"><v-btn icon="mdi-delete" variant="text" :disabled="purchase.buyers.length === 1" @click="removeBuyer(index)" /></v-col>
              </v-row>
              <v-btn variant="text" prepend-icon="mdi-plus" @click="addBuyer">添加购票人</v-btn><v-btn color="primary" class="ml-3" @click="savePurchase">保存购票组</v-btn>
            </v-expansion-panel-text></v-expansion-panel>
          </v-expansion-panels>
        </v-card-text>
      </v-window-item>

      <v-window-item value="accounts">
        <v-card-text>
          <v-row class="mb-4">
            <v-col cols="8"><div class="text-h6 mb-2">逐账号扫码登录</div><v-text-field v-model="login.name" label="账号备注" /><v-select v-model="login.role" :items="['primary', 'standby']" label="角色" /><v-btn color="primary" @click="beginLogin">生成独立二维码</v-btn><div class="mt-2">{{ login.message }}</div></v-col>
            <v-col cols="4" class="text-center"><VueQr v-if="login.url" :text="login.url" :size="180" /></v-col>
          </v-row>
          <v-divider class="mb-4" />
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
