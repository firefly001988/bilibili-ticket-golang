<script lang="ts" setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { clusterCall, type CatalogSKU, type ClusterSnapshot, type ProjectCatalog, type ResourceRole } from '@/composables/clusterTypes'
import VueQr from 'vue-qr'

const tab = ref('tasks')
const loading = ref(false)
const error = ref('')
const notice = ref('')
const startingMacroId = ref('')
const snapshot = ref<ClusterSnapshot>({ taskGroups: [], accounts: [], buyers: [], workers: [], macros: [], attempts: [] })
const accountJSON = ref('')
const worker = ref({ id: '', name: '', baseUrl: 'http://127.0.0.1:18080', key: '', role: 'primary' as ResourceRole })
const taskGroup = ref({ name: '' })
const macro = ref({ id: '', taskGroupId: '', projectId: 0, projectName: '', screenId: 0, screenName: '', skuId: 0, skuName: '', eventDay: '', orderCapacity: 4, capacitySource: 'default', smartMerge: false, priority: 0, desiredReplicas: 1, hardConcurrency: 1, startAt: '', deadline: '' })
const purchase = ref({ macroTaskId: '', allowSplit: false, buyerIds: [] as string[] })
const login = ref({ name: '', role: 'primary' as ResourceRole, sessionId: '', url: '', message: '' })
const projectId = ref('')
const project = ref<ProjectCatalog | null>(null)
const projectLoading = ref(false)
const selectedSKU = ref<CatalogSKU | null>(null)
const eventDayConfirmed = ref(false)
const expandedMacros = ref<string[]>([])
let timer: number | undefined
let loginTimer: number | undefined

async function refresh() {
  try {
    const next = await clusterCall<ClusterSnapshot>('Snapshot')
    next.taskGroups ||= []
    next.accounts ||= []
    next.buyers ||= []
    next.workers ||= []
    next.macros ||= []
    next.attempts ||= []
    snapshot.value = next
    error.value = ''
  } catch (e) { error.value = String(e) }
}

async function invoke(method: string, ...args: any[]) {
  loading.value = true
  try { await clusterCall(method, ...args); await refresh() }
  catch (e) { error.value = String(e) }
  finally { loading.value = false }
}

async function startMacro(id: string) {
  startingMacroId.value = id
  notice.value = ''
  try {
    await clusterCall('StartMacro', id)
    notice.value = '准点任务已创建并成功下发到 Worker。'
    error.value = ''
    await refresh()
  } catch (e) { error.value = String(e) }
  finally { startingMacroId.value = '' }
}

const importAccount = () => invoke('ImportAccount', accountJSON.value)
const addWorker = () => invoke('AddWorker', JSON.stringify(worker.value))
const switchReflow = () => invoke('SwitchToReflow')

async function saveTaskGroup() {
  if (!taskGroup.value.name.trim()) { error.value = '请输入任务组名称'; return }
  await invoke('SaveTaskGroup', JSON.stringify(taskGroup.value))
  taskGroup.value.name = ''
}

async function saveMacro() {
  if (!selectedSKU.value || !eventDayConfirmed.value) { error.value = '请选择票种并确认活动日期'; return }
  if (!macro.value.taskGroupId || !macro.value.startAt || !macro.value.deadline) { error.value = '任务组、开始时间和截止时间不能为空'; return }
  const document = { ...macro.value, eventDayConfirmed: true, needsReview: false, startAt: new Date(macro.value.startAt).toISOString(), deadline: new Date(macro.value.deadline).toISOString() }
  await invoke('SaveMacro', JSON.stringify(document))
}

async function savePurchase() {
  const selected = new Set(purchase.value.buyerIds)
  const buyers = snapshot.value.buyers.filter(item => selected.has(item.logicalId))
  if (!purchase.value.macroTaskId || buyers.length === 0) { error.value = '请选择宏任务并至少填写一名购票人'; return }
  await invoke('SavePurchaseGroup', JSON.stringify({ id: `purchase-${Date.now()}`, macroTaskId: purchase.value.macroTaskId, allowSplit: purchase.value.allowSplit, buyers }))
  purchase.value.buyerIds = []
}

const selectedMacro = computed(() => snapshot.value.macros.find(item => item.id === purchase.value.macroTaskId))
const selectedBuyerCount = computed(() => purchase.value.buyerIds.length)

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

async function loadProject() {
  if (!projectId.value.trim()) { error.value = '请输入项目 ID'; return }
  projectLoading.value = true
  try {
    project.value = await clusterCall<ProjectCatalog>('LoadProject', projectId.value.trim())
    selectedSKU.value = null
    eventDayConfirmed.value = false
    error.value = ''
  } catch (e) { error.value = String(e) }
  finally { projectLoading.value = false }
}

function chooseSKU(ticket: CatalogSKU) {
  selectedSKU.value = ticket
  eventDayConfirmed.value = false
  const eventDay = ticket.eventTime ? ticket.eventTime.slice(0, 10) : ''
  Object.assign(macro.value, { id: `macro-${project.value?.id}-${ticket.skuId}-${Date.now()}`, taskGroupId: macro.value.taskGroupId || snapshot.value.taskGroups[0]?.id || '', projectId: Number(project.value?.id), projectName: project.value?.name || '', screenId: ticket.screenId, screenName: ticket.screenName, skuId: ticket.skuId, skuName: ticket.skuName, eventDay, orderCapacity: ticket.orderCapacity || 4, capacitySource: ticket.orderCapacity > 0 ? 'api' : 'default', startAt: localDateTime(ticket.saleStart || project.value?.start), deadline: localDateTime(ticket.saleEnd || project.value?.end) })
}

async function syncBuyers(accountId: string) {
  loading.value = true
  try { await clusterCall('SyncAccountBuyers', accountId); await refresh() }
  catch (e) { error.value = String(e) }
  finally { loading.value = false }
}

function toggleMacro(id: string) {
  expandedMacros.value = expandedMacros.value.includes(id) ? expandedMacros.value.filter(value => value !== id) : [...expandedMacros.value, id]
}

async function deleteAccount(id: string, name: string) {
  if (!window.confirm(`确定删除账号“${name}”及其购票人映射吗？`)) return
  await invoke('DeleteAccount', id)
}

async function deleteWorker(id: string, name: string) {
  if (!window.confirm(`确定删除 Worker“${name}”吗？`)) return
  await invoke('DeleteWorker', id)
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
    <v-alert v-if="notice" type="success" closable class="ma-4" @click:close="notice = ''">{{ notice }}</v-alert>
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
              <template v-for="item in snapshot.macros" :key="item.id">
              <tr class="cursor-pointer" @click="toggleMacro(item.id)">
                <td><div>{{ item.projectName || `项目 ${item.projectId}` }}</div><div class="text-caption text-medium-emphasis">{{ item.screenName || item.screenId }} — {{ item.skuName || item.skuId }}</div></td><td>{{ item.eventDay || '未设置' }}</td><td>{{ item.orderCapacity }}</td>
                <td>{{ item.desiredReplicas }} / {{ item.hardConcurrency }}</td><td>{{ item.priority }}</td>
                <td><v-chip size="small" :color="item.needsReview ? 'warning' : 'success'">{{ item.phase }} · {{ item.needsReview ? '待确认' : '可调度' }}</v-chip></td>
                <td><v-btn size="small" :loading="startingMacroId === item.id" :disabled="item.needsReview || !item.eventDayConfirmed" @click.stop="startMacro(item.id)">启动准点</v-btn><v-btn class="ml-1" size="small" variant="text" :icon="expandedMacros.includes(item.id) ? 'mdi-chevron-up' : 'mdi-chevron-down'" @click.stop="toggleMacro(item.id)" /></td>
              </tr>
              <tr v-if="expandedMacros.includes(item.id)"><td colspan="7" class="bg-grey-lighten-4 pa-4">
                <div v-if="item.purchaseGroups.length === 0" class="text-medium-emphasis">尚未配置购票组。</div>
                <v-row v-else dense><v-col v-for="(group, index) in item.purchaseGroups" :key="group.id" cols="12" md="6"><v-card variant="outlined"><v-card-title class="text-subtitle-1">购票组 {{ index + 1 }}<v-chip class="ml-2" size="x-small" :color="group.allowSplit ? 'warning' : 'default'">{{ group.allowSplit ? '回流可拆单' : '保持整单' }}</v-chip></v-card-title><v-card-text><v-chip v-for="buyer in group.buyers" :key="buyer.logicalId" class="mr-2 mb-1" prepend-icon="mdi-account">{{ buyer.name }}<v-tooltip activator="parent">{{ buyer.tel || '无手机号' }}</v-tooltip></v-chip></v-card-text></v-card></v-col></v-row>
              </td></tr>
              </template>
            </tbody>
          </v-table>
          <v-expansion-panels class="mt-5">
            <v-expansion-panel title="创建任务组"><v-expansion-panel-text><div class="mb-2">现有：{{ snapshot.taskGroups.map(g => g.name).join('、') || '暂无' }}</div><v-text-field v-model="taskGroup.name" label="任务组名称" /><v-btn color="primary" @click="saveTaskGroup">保存任务组</v-btn></v-expansion-panel-text></v-expansion-panel>
            <v-expansion-panel title="创建或更新宏任务"><v-expansion-panel-text>
              <div class="d-flex ga-2 mb-3"><v-text-field v-model="projectId" label="Bilibili 项目 ID" hide-details @keyup.enter="loadProject" /><v-btn color="primary" :loading="projectLoading" @click="loadProject">读取项目</v-btn></div>
              <v-alert v-if="project" type="info" variant="tonal" class="mb-3"><strong>{{ project.name }}</strong><span class="ml-2">{{ project.forceRealName ? '实名制项目' : '非强制实名项目' }}</span></v-alert>
              <v-list v-if="project?.tickets.length" border rounded class="mb-4" max-height="300"><v-list-item v-for="ticket in project.tickets" :key="`${ticket.screenId}-${ticket.skuId}`" :active="selectedSKU?.skuId === ticket.skuId && selectedSKU?.screenId === ticket.screenId" @click="chooseSKU(ticket)"><template #title>{{ ticket.screenName }} — {{ ticket.skuName }}</template><template #subtitle>¥{{ (ticket.price / 100).toFixed(2) }} · {{ ticket.status || '状态未知' }} · 单订单最多 {{ ticket.orderCapacity }} 人</template><template #append><v-icon>{{ selectedSKU?.skuId === ticket.skuId ? 'mdi-check-circle' : 'mdi-chevron-right' }}</v-icon></template></v-list-item></v-list>
              <template v-if="selectedSKU">
              <v-row><v-col cols="6"><v-select v-model="macro.taskGroupId" :items="snapshot.taskGroups" item-title="name" item-value="id" label="所属任务组" /></v-col><v-col cols="6"><v-text-field v-model="macro.eventDay" type="date" label="活动日期" /></v-col></v-row>
              <v-checkbox v-model="eventDayConfirmed" label="我已确认活动日期正确（用于防止同一购票人同日冲突）" />
              <v-row><v-col><v-text-field v-model="macro.startAt" type="datetime-local" label="开始执行时间" /></v-col><v-col><v-text-field v-model="macro.deadline" type="datetime-local" label="绝对截止时间" /></v-col></v-row>
              <v-row><v-col><v-text-field v-model.number="macro.priority" type="number" label="优先级" /></v-col><v-col><v-text-field v-model.number="macro.desiredReplicas" type="number" min="1" label="期望并发副本" /></v-col><v-col><v-text-field v-model.number="macro.hardConcurrency" type="number" min="1" label="硬并发上限" /></v-col></v-row>
              <v-expansion-panels variant="accordion" class="mb-3"><v-expansion-panel title="高级选项"><v-expansion-panel-text><v-text-field v-model.number="macro.orderCapacity" type="number" min="1" label="单订单人数上限（API 已预填）" /><v-switch v-model="macro.smartMerge" label="准点阶段智能合并购票组" color="primary" /></v-expansion-panel-text></v-expansion-panel></v-expansion-panels>
              <v-btn color="primary" @click="saveMacro">保存宏任务</v-btn>
              </template>
            </v-expansion-panel-text></v-expansion-panel>
            <v-expansion-panel title="添加购票组"><v-expansion-panel-text>
              <v-select v-model="purchase.macroTaskId" :items="snapshot.macros" :item-title="item => `${item.projectName || `项目 ${item.projectId}`} · ${item.screenName || item.screenId} · ${item.skuName || item.skuId}`" item-value="id" label="宏任务" />
              <v-switch v-model="purchase.allowSplit" label="回流阶段允许拆成单人订单" color="warning" />
              <v-alert v-if="snapshot.buyers.length === 0" type="warning" variant="tonal" class="mb-3">尚未同步购票人。请先到“账号池”对至少一个账号执行“同步购票人”。</v-alert>
              <v-select v-model="purchase.buyerIds" :items="snapshot.buyers" item-title="name" item-value="logicalId" label="选择购票人" multiple chips closable-chips><template #item="{ props, item }"><v-list-item v-bind="props" :subtitle="`${item.tel || '无手机号'} · ${item.idCard || '无证件信息'}`" /></template></v-select>
              <v-alert v-if="selectedMacro && selectedBuyerCount > selectedMacro.orderCapacity" type="error" density="compact" class="mb-3">已选 {{ selectedBuyerCount }} 人，超过该 SKU 单订单 {{ selectedMacro.orderCapacity }} 人上限。</v-alert>
              <v-btn color="primary" :disabled="!selectedMacro || selectedBuyerCount === 0 || selectedBuyerCount > (selectedMacro?.orderCapacity || 0)" @click="savePurchase">保存购票组</v-btn>
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
            <v-col cols="7"><v-table density="compact"><thead><tr><th>账号</th><th>角色</th><th>状态</th><th>操作</th></tr></thead><tbody><tr v-for="item in snapshot.accounts" :key="item.id"><td>{{ item.name || item.id }}</td><td><v-chip size="small">{{ item.role }}</v-chip></td><td>{{ item.enabled ? (item.cooldownUntil ? `冷却至 ${item.cooldownUntil}` : '可用') : '停用' }}</td><td><v-btn size="small" variant="tonal" prepend-icon="mdi-account-sync" @click="syncBuyers(item.id)">同步</v-btn><v-btn class="ml-1" size="small" variant="text" color="error" icon="mdi-delete" @click="deleteAccount(item.id, item.name || item.id)" /></td></tr></tbody></v-table></v-col>
            <v-col cols="5"><v-textarea v-model="accountJSON" label="标准凭据 JSON" rows="8" /><v-btn color="primary" block @click="importAccount">导入账号</v-btn></v-col>
          </v-row>
          <v-divider class="my-5" />
          <div class="text-h6 mb-2">已识别购票人</div>
          <v-alert type="info" variant="tonal" class="mb-3">系统根据账号中的实名购票人自动建立跨账号映射；内部逻辑 ID 无需用户管理。</v-alert>
          <v-chip v-for="buyer in snapshot.buyers" :key="buyer.logicalId" class="mr-2 mb-2" prepend-icon="mdi-account">{{ buyer.name }} · {{ buyer.tel || '无手机号' }}</v-chip>
        </v-card-text>
      </v-window-item>

      <v-window-item value="workers">
        <v-card-text>
          <v-row>
            <v-col cols="7"><v-table density="compact"><thead><tr><th>Worker</th><th>地址</th><th>角色</th><th>健康 / 活动任务</th><th>操作</th></tr></thead><tbody><tr v-for="item in snapshot.workers" :key="item.id"><td>{{ item.name || item.id }}</td><td>{{ item.baseUrl }}</td><td>{{ item.role }}</td><td><v-chip size="small" :color="item.healthy ? 'success' : 'error'">{{ item.healthy ? (item.activeAttemptId || '空闲') : '失联' }}</v-chip></td><td><v-tooltip :text="item.id === 'local' ? '本机 Worker 由雇主自动管理' : '删除 Worker'"><template #activator="{ props }"><span v-bind="props"><v-btn size="small" variant="text" color="error" icon="mdi-delete" :disabled="item.id === 'local'" @click="deleteWorker(item.id, item.name || item.id)" /></span></template></v-tooltip></td></tr></tbody></v-table></v-col>
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
