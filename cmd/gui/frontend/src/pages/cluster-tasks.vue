<script lang="ts" setup>
import { computed, ref } from 'vue'
import { useCluster } from '@/composables/useCluster'
import { useConfirm } from '@/composables/useConfirm'
import { clusterCall, type CatalogSKU, type ProjectCatalog } from '@/composables/clusterTypes'
import { useMessagesStore } from '@/stores/snackbar'

const messages = useMessagesStore()
const { snapshot, loading, invoke } = useCluster()

const startingMacroId = ref('')
const projectId = ref('')
const project = ref<ProjectCatalog | null>(null)
const projectLoading = ref(false)
const selectedSKU = ref<CatalogSKU | null>(null)
const eventDayConfirmed = ref(false)
const expandedMacros = ref<string[]>([])
const taskEditorPanel = ref<number | null>(null)

const { show: showConfirm } = useConfirm()

const taskGroup = ref({ name: '' })
const macro = ref({ id: '', taskGroupId: '', projectId: 0, projectName: '', screenId: 0, screenName: '', skuId: 0, skuName: '', eventDay: '', orderCapacity: 4, capacitySource: 'default', smartMerge: false, priority: 0, desiredReplicas: 1, hardConcurrency: 1, startAt: '', deadline: '' })
const purchase = ref({ id: '', macroTaskId: '', allowSplit: false, buyerIds: [] as string[], createdAt: '' })

function localDateTime(value?: string) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const offset = date.getTimezoneOffset() * 60000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}

async function startMacro(id: string) {
  startingMacroId.value = id
  try {
    await clusterCall('StartMacro', id)
    messages.add({ text: '准点任务已创建并成功下发到 Worker。', color: 'success', timeout: 3000 })
    await invoke('', '')
  } catch (e: any) { messages.add({ text: String(e), color: 'error', timeout: 5000 }) }
  finally { startingMacroId.value = '' }
}

async function stopMacro(id: string) {
  const ok = await showConfirm('停止宏任务', '确定停止该宏任务及其全部执行中的 Worker 任务吗？')
  if (!ok) return
  await invoke('StopMacro', id).catch(() => { })
}

async function loadProject() {
  if (!projectId.value.trim()) { messages.add({ text: '请输入项目 ID', color: 'warning', timeout: 3000 }); return }
  projectLoading.value = true
  try {
    project.value = await clusterCall<ProjectCatalog>('LoadProject', projectId.value.trim())
    selectedSKU.value = null; eventDayConfirmed.value = false
  } catch (e: any) { messages.add({ text: String(e), color: 'error', timeout: 5000 }) }
  finally { projectLoading.value = false }
}

function chooseSKU(ticket: CatalogSKU) {
  selectedSKU.value = ticket; eventDayConfirmed.value = false
  const eventDay = ticket.eventTime ? ticket.eventTime.slice(0, 10) : ''
  Object.assign(macro.value, { id: `macro-${project.value?.id}-${ticket.skuId}-${Date.now()}`, taskGroupId: macro.value.taskGroupId || snapshot.value.taskGroups[0]?.id || '', projectId: Number(project.value?.id), projectName: project.value?.name || '', screenId: ticket.screenId, screenName: ticket.screenName, skuId: ticket.skuId, skuName: ticket.skuName, eventDay, orderCapacity: ticket.orderCapacity || 4, capacitySource: ticket.orderCapacity > 0 ? 'api' : 'default', startAt: localDateTime(ticket.saleStart || project.value?.start), deadline: localDateTime(ticket.saleEnd || project.value?.end) })
}

async function saveTaskGroup() {
  if (!taskGroup.value.name.trim()) { messages.add({ text: '请输入任务组名称', color: 'warning', timeout: 3000 }); return }
  if (await invoke('SaveTaskGroup', JSON.stringify(taskGroup.value))) taskGroup.value.name = ''
}

async function saveMacro() {
  if (!selectedSKU.value || !eventDayConfirmed.value) { messages.add({ text: '请选择票种并确认活动日期', color: 'warning', timeout: 3000 }); return }
  if (!macro.value.taskGroupId || !macro.value.startAt || !macro.value.deadline) { messages.add({ text: '任务组、开始时间和截止时间不能为空', color: 'warning', timeout: 3000 }); return }
  const doc = { ...macro.value, eventDayConfirmed: true, needsReview: false, startAt: new Date(macro.value.startAt).toISOString(), deadline: new Date(macro.value.deadline).toISOString() }
  await invoke('SaveMacro', JSON.stringify(doc))
}

async function savePurchase() {
  const buyers = snapshot.value.buyers.filter(b => purchase.value.buyerIds.includes(b.logicalId))
  if (!purchase.value.macroTaskId || buyers.length === 0) { messages.add({ text: '请选择宏任务并至少填写一名购票人', color: 'warning', timeout: 3000 }); return }
  await invoke('SavePurchaseGroup', JSON.stringify({ id: purchase.value.id || `purchase-${Date.now()}`, macroTaskId: purchase.value.macroTaskId, allowSplit: purchase.value.allowSplit, buyers, createdAt: purchase.value.createdAt || undefined }))
  resetPurchaseEditor()
}

function resetPurchaseEditor() { purchase.value = { id: '', macroTaskId: purchase.value.macroTaskId, allowSplit: false, buyerIds: [], createdAt: '' } }

function toggleMacro(id: string) {
  expandedMacros.value = expandedMacros.value.includes(id) ? expandedMacros.value.filter(v => v !== id) : [...expandedMacros.value, id]
}

function editMacro(item: typeof snapshot.value.macros[number]) {
  selectedSKU.value = { screenId: item.screenId, skuId: item.skuId, screenName: item.screenName || '', skuName: item.skuName || '', price: 0, orderCapacity: item.orderCapacity }
  project.value = { id: String(item.projectId), name: item.projectName || `项目 ${item.projectId}`, forceRealName: true, idBind: 2, tickets: [selectedSKU.value] }
  projectId.value = String(item.projectId); eventDayConfirmed.value = item.eventDayConfirmed
  Object.assign(macro.value, { id: item.id, taskGroupId: item.taskGroupId, projectId: item.projectId, projectName: item.projectName || '', screenId: item.screenId, screenName: item.screenName || '', skuId: item.skuId, skuName: item.skuName || '', eventDay: item.eventDay, orderCapacity: item.orderCapacity, capacitySource: item.capacitySource || 'default', smartMerge: item.smartMerge, priority: item.priority, desiredReplicas: item.desiredReplicas, hardConcurrency: item.hardConcurrency, startAt: localDateTime(item.startAt), deadline: localDateTime(item.deadline) })
  taskEditorPanel.value = 1
  window.setTimeout(() => document.querySelector('[data-macro-editor]')?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 0)
}

async function deleteMacro(item: typeof snapshot.value.macros[number]) {
  const name = `${item.projectName || `项目 ${item.projectId}`} · ${item.skuName || item.skuId}`
  const ok = await showConfirm('删除宏任务', `确定删除宏任务「${name}」吗？`)
  if (!ok) return
  if (await invoke('DeleteMacro', item.id)) expandedMacros.value = expandedMacros.value.filter(id => id !== item.id)
}

function editPurchase(group: typeof snapshot.value.macros[number]['purchaseGroups'][number]) {
  purchase.value = { id: group.id, macroTaskId: group.macroTaskId, allowSplit: group.allowSplit, buyerIds: group.buyers.map(b => b.logicalId), createdAt: group.createdAt }
  taskEditorPanel.value = 2
  window.setTimeout(() => document.querySelector('[data-purchase-editor]')?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 0)
}

async function deletePurchase(group: typeof snapshot.value.macros[number]['purchaseGroups'][number]) {
  const ok = await showConfirm('删除购票组', '确定删除该购票组吗？')
  if (!ok) return
  if (await invoke('DeletePurchaseGroup', group.macroTaskId, group.id) && purchase.value.id === group.id) resetPurchaseEditor()
}

const selectedMacro = computed(() => snapshot.value.macros.find(m => m.id === purchase.value.macroTaskId))
const selectedBuyerCount = computed(() => purchase.value.buyerIds.length)

async function switchToReflow() {
  const ok = await showConfirm('切换到回流', '确定停止所有准点任务并切换到回流阶段吗？')
  if (!ok) return
  await invoke('SwitchToReflow')
}
</script>

<template>
  <div>
    <div class="d-flex align-center mb-4">
      <div>
        <div class="text-h6">SKU 宏任务</div>
        <div class="text-medium-emphasis">活动日期必须确认；准点可智能合并，回流只按原组或单人拆分。</div>
      </div>
      <v-spacer />
      <v-btn color="warning" prepend-icon="mdi-swap-horizontal" @click="switchToReflow">停止准点并切换回流</v-btn>
    </div>

    <v-table density="compact">
      <thead>
        <tr>
          <th>项目 / SKU</th>
          <th>活动日</th>
          <th>容量</th>
          <th>副本</th>
          <th>优先级</th>
          <th>阶段 / 审核</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>
        <template v-for="item in snapshot.macros" :key="item.id">
          <tr class="cursor-pointer" @click="toggleMacro(item.id)">
            <td>
              <div>{{ item.projectName || `项目 ${item.projectId}` }}</div>
              <div class="text-caption text-medium-emphasis">{{ item.screenName || item.screenId }} — {{ item.skuName ||
                item.skuId }}</div>
            </td>
            <td>{{ item.eventDay || '未设置' }}</td>
            <td>{{ item.orderCapacity }}</td>
            <td>{{ item.desiredReplicas }} / {{ item.hardConcurrency }}</td>
            <td>{{ item.priority }}</td>
            <td><v-chip size="small" :color="item.needsReview ? 'warning' : 'success'">{{ item.phase }} · {{
              item.needsReview ? '待确认' : '可调度' }}</v-chip></td>
            <td class="text-no-wrap">
              <v-btn size="small" :loading="startingMacroId === item.id"
                :disabled="item.needsReview || !item.eventDayConfirmed" @click.stop="startMacro(item.id)">启动准点</v-btn>
              <v-btn class="ml-1" size="small" variant="text" color="warning" icon="mdi-stop"
                @click.stop="stopMacro(item.id)" />
              <v-btn class="ml-1" size="small" variant="text" icon="mdi-pencil" @click.stop="editMacro(item)" />
              <v-btn size="small" variant="text" color="error" icon="mdi-delete" @click.stop="deleteMacro(item)" />
              <v-btn size="small" variant="text"
                :icon="expandedMacros.includes(item.id) ? 'mdi-chevron-up' : 'mdi-chevron-down'"
                @click.stop="toggleMacro(item.id)" />
            </td>
          </tr>
          <tr v-if="expandedMacros.includes(item.id)">
            <td colspan="7" class="bg-grey-lighten-4 pa-4">
              <div v-if="item.purchaseGroups.length === 0" class="text-medium-emphasis">尚未配置购票组。</div>
              <v-row v-else dense>
                <v-col v-for="(group, index) in item.purchaseGroups" :key="group.id" cols="12" md="6">
                  <v-card variant="outlined">
                    <v-card-title class="d-flex align-center text-subtitle-1">
                      购票组 {{ index + 1 }}
                      <v-chip class="ml-2" size="x-small" :color="group.allowSplit ? 'warning' : 'default'">{{
                        group.allowSplit ? '回流可拆单' : '保持整单' }}</v-chip>
                      <v-spacer />
                      <v-btn size="x-small" variant="text" icon="mdi-pencil" @click="editPurchase(group)" />
                      <v-btn size="x-small" variant="text" color="error" icon="mdi-delete"
                        @click="deletePurchase(group)" />
                    </v-card-title>
                    <v-card-text>
                      <v-chip v-for="buyer in group.buyers" :key="buyer.logicalId" class="mr-2 mb-1"
                        prepend-icon="mdi-account">{{ buyer.name }}<v-tooltip activator="parent">{{ buyer.tel || '无手机号'
                        }}{{ buyer.idCard ? ` · ${buyer.idCard}` : '' }}</v-tooltip></v-chip>
                    </v-card-text>
                  </v-card>
                </v-col>
              </v-row>
            </td>
          </tr>
        </template>
      </tbody>
    </v-table>

    <v-expansion-panels v-model="taskEditorPanel" class="mt-5">
      <v-expansion-panel title="创建任务组">
        <v-expansion-panel-text>
          <div class="mb-2">现有：{{snapshot.taskGroups.map(g => g.name).join('、') || '暂无'}}</div>
          <v-text-field v-model="taskGroup.name" label="任务组名称" />
          <v-btn color="primary" @click="saveTaskGroup">保存任务组</v-btn>
        </v-expansion-panel-text>
      </v-expansion-panel>

      <v-expansion-panel title="创建或更新宏任务" data-macro-editor>
        <v-expansion-panel-text>
          <div class="d-flex ga-2 mb-3">
            <v-text-field v-model="projectId" label="Bilibili 项目 ID" hide-details @keyup.enter="loadProject" />
            <v-btn color="primary" :loading="projectLoading" @click="loadProject">读取项目</v-btn>
          </div>
          <v-alert v-if="project" type="info" variant="tonal" class="mb-3"><strong>{{ project.name }}</strong><span
              class="ml-2">{{ project.forceRealName ? '实名制项目' : '非强制实名项目' }}</span>
            <div v-if="project.forceRealName" class="text-caption mt-1">
              <v-icon size="small" class="mr-1">mdi-information</v-icon>
              <span v-if="project.idBind === 1">此项目单人实名可购买多张票，系统会自动拆分为每张票单独下单。</span>
              <span v-else>此项目一票一实名，每张票需要对应的实名购票人。</span>
            </div>
          </v-alert>
          <v-list v-if="project?.tickets.length" border rounded class="mb-4" max-height="300">
            <v-list-item v-for="ticket in project.tickets" :key="`${ticket.screenId}-${ticket.skuId}`"
              :active="selectedSKU?.skuId === ticket.skuId && selectedSKU?.screenId === ticket.screenId"
              @click="chooseSKU(ticket)">
              <template #title>{{ ticket.screenName }} — {{ ticket.skuName }}</template>
              <template #subtitle>¥{{ (ticket.price / 100).toFixed(2) }} · {{ ticket.status || '状态未知' }} · 单订单最多 {{
                ticket.orderCapacity }} 人</template>
              <template #append><v-icon>{{ selectedSKU?.skuId === ticket.skuId ? 'mdi-check-circle' :
                'mdi-chevron-right' }}</v-icon></template>
            </v-list-item>
          </v-list>
          <template v-if="selectedSKU">
            <v-row><v-col cols="6"><v-select v-model="macro.taskGroupId" :items="snapshot.taskGroups" item-title="name"
                  item-value="id" label="所属任务组" /></v-col><v-col cols="6"><v-text-field v-model="macro.eventDay"
                  type="date" label="活动日期" /></v-col></v-row>
            <v-checkbox v-model="eventDayConfirmed" label="我已确认活动日期正确" />
            <v-row><v-col><v-text-field v-model="macro.startAt" type="datetime-local"
                  label="开始执行时间" /></v-col><v-col><v-text-field v-model="macro.deadline" type="datetime-local"
                  label="绝对截止时间" /></v-col></v-row>
            <v-row><v-col><v-text-field v-model.number="macro.priority" type="number"
                  label="优先级" /></v-col><v-col><v-text-field v-model.number="macro.desiredReplicas" type="number"
                  min="1" label="期望并发副本" /></v-col><v-col><v-text-field v-model.number="macro.hardConcurrency"
                  type="number" min="1" label="硬并发上限" /></v-col></v-row>
            <v-expansion-panels variant="accordion" class="mb-3"><v-expansion-panel
                title="高级选项"><v-expansion-panel-text><v-text-field v-model.number="macro.orderCapacity" type="number"
                    min="1" label="单订单人数上限（API 已预填）" /><v-switch v-model="macro.smartMerge" label="准点阶段智能合并购票组"
                    color="primary" /></v-expansion-panel-text></v-expansion-panel></v-expansion-panels>
            <v-btn color="primary" @click="saveMacro">保存宏任务</v-btn>
          </template>
        </v-expansion-panel-text>
      </v-expansion-panel>

      <v-expansion-panel :title="purchase.id ? '编辑购票组' : '添加购票组'" data-purchase-editor>
        <v-expansion-panel-text>
          <v-select v-model="purchase.macroTaskId" :items="snapshot.macros"
            :item-title="m => `${m.projectName || `项目 ${m.projectId}`} · ${m.screenName || m.screenId} · ${m.skuName || m.skuId}`"
            item-value="id" label="宏任务" />
          <v-switch v-model="purchase.allowSplit" label="回流阶段允许拆成单人订单" color="warning" />
          <v-alert v-if="snapshot.buyers.length === 0" type="warning" variant="tonal"
            class="mb-3">尚未同步购票人。请先到"账号池"对至少一个账号执行"同步购票人"。</v-alert>
          <v-select v-model="purchase.buyerIds" :items="snapshot.buyers" item-title="name" item-value="logicalId"
            label="选择购票人" multiple chips closable-chips><template #item="{ props, item }"><v-list-item v-bind="props"
                :subtitle="`${item.tel || '无手机号'} · ${item.idCard || '无证件信息'}`" /></template></v-select>
          <v-alert v-if="selectedMacro && selectedBuyerCount > selectedMacro.orderCapacity" type="error"
            density="compact" class="mb-3">已选 {{ selectedBuyerCount }} 人，超过单订单 {{ selectedMacro.orderCapacity }}
            人上限。</v-alert>
          <v-btn color="primary" :disabled="!selectedMacro || selectedBuyerCount === 0" @click="savePurchase">{{
            purchase.id
              ? '更新购票组' : '保存购票组' }}</v-btn>
          <v-btn v-if="purchase.id" class="ml-2" variant="text" @click="resetPurchaseEditor">取消编辑</v-btn>
        </v-expansion-panel-text>
      </v-expansion-panel>
    </v-expansion-panels>
  </div>
</template>
