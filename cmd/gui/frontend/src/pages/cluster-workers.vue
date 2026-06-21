<script lang="ts" setup>
import { ref } from 'vue'
import { useCluster } from '@/composables/useCluster'
import { clusterCall, type WorkerConfigResponse } from '@/composables/clusterTypes'
import { useMessagesStore } from '@/stores/snackbar'
import type { ResourceRole } from '@/composables/clusterTypes'

const messages = useMessagesStore()
const { snapshot, invoke } = useCluster()

const EMPTY = { id: '', name: '', address: '127.0.0.1:18080', caCert: '', clientCert: '', clientKey: '', tlsServerName: '', role: 'primary' as ResourceRole }
const worker = ref({ ...EMPTY })
const editing = ref(false)
const encodedImport = ref('')
const importing = ref(false)

const addWorker = () => invoke('AddWorker', JSON.stringify(worker.value)).catch(() => {})
const updateWorker = () => invoke('UpdateWorker', JSON.stringify(worker.value)).catch(() => {})

async function importFromEncoded() {
  if (!encodedImport.value.trim()) return
  importing.value = true
  try {
    await invoke('AddWorkerFromEncodedConfig', encodedImport.value.trim())
    encodedImport.value = ''
    messages.add({ text: 'Worker 导入成功', color: 'success', timeout: 3000 })
  } catch (e: any) {
    messages.add({ text: String(e), color: 'error', timeout: 5000 })
  } finally {
    importing.value = false
  }
}

function resetForm() {
  worker.value = { ...EMPTY }
  editing.value = false
}

async function editWorker(id: string) {
  try {
    const cfg = await clusterCall<WorkerConfigResponse>('GetWorkerConfig', id)
    worker.value = {
      id: cfg.id || '',
      name: cfg.name || '',
      address: cfg.address || '127.0.0.1:18080',
      caCert: cfg.caCert || '',
      clientCert: cfg.clientCert || '',
      clientKey: cfg.clientKey || '',
      tlsServerName: cfg.tlsServerName || '',
      role: cfg.role || 'primary',
    }
    editing.value = true
  } catch (e: any) {
    messages.add({ text: String(e), color: 'error', timeout: 3000 })
  }
}

async function deleteWorker(id: string, name: string) {
  if (!window.confirm(`确定删除 Worker"${name}"吗？`)) return
  await invoke('DeleteWorker', id).catch(() => {})
}
</script>

<template>
  <v-row>
    <v-col cols="7">
      <v-table density="compact">
        <thead><tr><th>Worker</th><th>地址</th><th>角色</th><th>健康 / 活动任务</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="item in snapshot.workers" :key="item.id">
            <td>{{ item.name || item.id }}</td>
            <td>{{ item.address }}</td>
            <td>{{ item.role }}</td>
            <td><v-chip size="small" :color="item.healthy ? 'success' : 'error'">{{ item.healthy ? (item.activeAttemptId || '空闲') : '失联' }}</v-chip></td>
            <td>
              <v-tooltip :text="item.id === 'local' ? '本机 Worker 由雇主自动管理' : '编辑 Worker'">
                <template #activator="{ props }"><span v-bind="props"><v-btn size="small" variant="text" color="info" icon="mdi-pencil" :disabled="item.id === 'local'" @click="editWorker(item.id)" /></span></template>
              </v-tooltip>
              <v-tooltip :text="item.id === 'local' ? '本机 Worker 由雇主自动管理' : '删除 Worker'">
                <template #activator="{ props }"><span v-bind="props"><v-btn size="small" variant="text" color="error" icon="mdi-delete" :disabled="item.id === 'local'" @click="deleteWorker(item.id, item.name || item.id)" /></span></template>
              </v-tooltip>
            </td>
          </tr>
        </tbody>
      </v-table>
    </v-col>
    <v-col cols="5">
      <v-text-field v-model="worker.id" label="Worker ID" :disabled="editing" />
      <v-text-field v-model="worker.name" label="名称" />
      <v-text-field v-model="worker.address" label="gRPC 地址（host:port）" placeholder="127.0.0.1:18080" />
      <v-text-field v-model="worker.tlsServerName" label="TLS SNI（可选）" placeholder="localhost" />
      <v-textarea v-model="worker.caCert" label="CA 证书 (PEM)" rows="2" />
      <v-textarea v-model="worker.clientCert" label="客户端证书 (PEM)" rows="2" />
      <v-textarea v-model="worker.clientKey" label="客户端私钥 (PEM)" rows="2" type="password" />
      <v-select v-model="worker.role" :items="['primary', 'standby']" label="角色" />
      <v-row dense>
        <v-col v-if="editing" cols="6">
          <v-btn color="warning" variant="tonal" block @click="resetForm">取消</v-btn>
        </v-col>
        <v-col :cols="editing ? 6 : 12">
          <v-btn v-if="editing" color="primary" block @click="updateWorker">更新 Worker</v-btn>
          <v-btn v-else color="primary" block @click="addWorker">添加 Worker</v-btn>
        </v-col>
      </v-row>

      <v-divider class="my-4" />

      <details class="mb-2">
        <summary class="text-caption cursor-pointer">从编码字符串导入</summary>
        <v-textarea
          v-model="encodedImport"
          label="编码后的 Worker 配置 (Base4096)"
          rows="2"
          density="compact"
          class="mt-2"
        />
        <v-btn
          color="secondary"
          variant="tonal"
          size="small"
          block
          :disabled="!encodedImport.trim()"
          :loading="importing"
          @click="importFromEncoded"
        >
          导入 Worker
        </v-btn>
      </details>
    </v-col>
  </v-row>
</template>
