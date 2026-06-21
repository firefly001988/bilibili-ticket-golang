<script lang="ts" setup>
import { computed } from "vue"
import { useRouter, useRoute } from "vue-router"

const router = useRouter()
const route = useRoute()

const tabs = [
  { value: "tasks", title: "任务规划", to: "/scheduler/tasks" },
  { value: "accounts", title: "账号池", to: "/scheduler/accounts" },
  { value: "workers", title: "Worker 池", to: "/scheduler/workers" },
  { value: "attempts", title: "执行监控", to: "/scheduler/attempts" },
]

const currentTab = computed(() => {
  const path = route.path
  if (path.includes("accounts")) return "accounts"
  if (path.includes("workers")) return "workers"
  if (path.includes("attempts")) return "attempts"
  return "tasks"
})

function navigate(to: string) { router.push(to) }
</script>

<template>
  <v-card>
    <v-card-title class="d-flex align-center ga-3">
      <v-icon>mdi-server-network</v-icon>
      雇主—雇员集群调度
    </v-card-title>
    <v-tabs :model-value="currentTab" grow>
      <v-tab v-for="tab in tabs" :key="tab.value" :value="tab.value" @click="navigate(tab.to)">
        {{ tab.title }}
      </v-tab>
    </v-tabs>
    <v-card-text>
      <router-view />
    </v-card-text>
  </v-card>
</template>
