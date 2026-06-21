<script lang="ts" setup>
import { ref } from 'vue'
import { useCluster } from '@/composables/useCluster'
import { clusterCall, type WorkerLogEntry } from '@/composables/clusterTypes'
import { useMessagesStore } from '@/stores/snackbar'

const messages = useMessagesStore()
const { snapshot, refresh } = useCluster()

const logAttemptId = ref('')
const attemptLogs = ref<WorkerLogEntry[]>([])
const logsLoading = ref(false)

function logTime(value: string) { return new Date(value).toLocaleTimeString() }

async function loadAttemptLogs(attemptId: string) {
  logAttemptId.value = attemptId; logsLoading.value = true
  try { attemptLogs.value = await clusterCall<WorkerLogEntry[]>('AttemptLogs', attemptId) }
  catch (e: any) { messages.add({ text: `读取 Worker 日志失败：${e}`, color: 'error', timeout: 5000 }) }
  finally { logsLoading.value = false }
}

function closeAttemptLogs() { logAttemptId.value = ''; attemptLogs.value = [] }

async function stopAttempt(attemptId: string) {
  try {
    await clusterCall('StopAttempt', attemptId)
    messages.add({ text: '执行任务已发送停止指令。', color: 'info', timeout: 3000 })
    await refresh()
  } catch (e: any) { messages.add({ text: String(e), color: 'error', timeout: 5000 }) }
}
</script>

<template>
  <div>
    <v-table density="compact">
      <thead><tr><th>Attempt</th><th>Intent</th><th>账号</th><th>Worker</th><th>状态</th><th>订单 / 原因</th><th>操作</th></tr></thead>
      <tbody>
        <tr v-for="item in snapshot.attempts" :key="item.id" :class="{ 'bg-blue-lighten-5': logAttemptId === item.id }">
          <td>{{ item.id }}</td><td>{{ item.intentId }}</td><td>{{ item.accountId }}</td><td>{{ item.workerId }}</td>
          <td><v-chip size="small">{{ item.state }}</v-chip></td><td>{{ item.orderId || item.reason || '-' }}</td>
          <td class="text-no-wrap">
            <v-btn v-if="item.state !== 'stopped' && item.state !== 'succeeded' && item.state !== 'failed'" size="small" variant="text" color="error" icon="mdi-stop" @click.stop="stopAttempt(item.id)" />
            <v-btn size="small" variant="tonal" prepend-icon="mdi-text-box-search-outline" :loading="logsLoading && logAttemptId === item.id" @click="loadAttemptLogs(item.id)">查看</v-btn>
          </td>
        </tr>
      </tbody>
    </v-table>

    <v-card v-if="logAttemptId" class="mt-4" variant="outlined">
      <v-card-title class="d-flex align-center">
        <v-icon class="mr-2">mdi-console-line</v-icon>Worker 日志 · {{ logAttemptId }}
        <v-spacer />
        <v-btn icon="mdi-close" variant="text" @click="closeAttemptLogs" />
      </v-card-title>
      <v-card-text>
        <v-table v-if="attemptLogs.length" density="compact">
          <thead><tr><th>时间</th><th>阶段</th><th>消息</th><th>状态码</th></tr></thead>
          <tbody>
            <tr v-for="(entry, i) in attemptLogs" :key="i">
              <td>{{ logTime(entry.time) }}</td>
              <td><v-chip size="x-small">{{ entry.stage }}</v-chip></td>
              <td>{{ entry.message }}</td>
              <td>{{ entry.code || '-' }}</td>
            </tr>
          </tbody>
        </v-table>
        <div v-else class="text-medium-emphasis">暂无日志</div>
      </v-card-text>
    </v-card>
  </div>
</template>
