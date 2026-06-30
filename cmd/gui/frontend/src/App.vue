<script lang="ts" setup>
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n'
import router from './router';
import VerifiedOverlay from './components/VerifiedOverlay.vue';
import { useMessagesStore } from './stores/snackbar';
import {
  Snapshot,
  SaveTaskGroup,
  DeleteTaskGroup,
} from '../bindings/bilibili-ticket-golang/cmd/gui/cluster_service/clusterservice'

const { t, locale } = useI18n()
const messages = useMessagesStore();

const verified = ref(false)
const showLangPicker = ref(false)

// Detect OS language for the picker default
function detectOSLocale(): string {
  const nav = navigator.language
  if (nav.startsWith('zh')) return 'zh-CN'
  if (nav.startsWith('en')) return 'en-US'
  return 'zh-CN'
}

function selectLanguage(loc: string) {
  locale.value = loc
  localStorage.setItem('app_locale', loc)
  showLangPicker.value = false
}

const calculatedPath = computed(() => {
  const p = router.currentRoute.value.path.replace('/', '');
  // Match partial paths (e.g. "account/list" should match "account")
  for (const seg of ['home', 'account', 'cluster', 'notify', 'settings', 'scheduler']) {
    if (p.startsWith(seg)) return seg;
  }
  return p;
})

const isPayQR = computed(() => router.currentRoute.value.path.startsWith('/pay-qr'))

// ── Task groups (loaded inline in sidebar) ──────────────────
interface TaskGroup {
  id: string
  name: string
  createdAt: string
}
const taskGroups = ref<TaskGroup[]>([])
const addingGroup = ref(false)
const newGroupName = ref('')
const deletingGroup = ref<Record<string, boolean>>({})

async function loadTaskGroups() {
  try {
    const snap = await Snapshot()
    taskGroups.value = (snap.taskGroups || []) as TaskGroup[]
  } catch { /* silent */ }
}

async function addTaskGroup() {
  const name = newGroupName.value.trim()
  if (!name) return
  addingGroup.value = true
  try {
    await SaveTaskGroup(JSON.stringify({ name }))
    newGroupName.value = ''
    await loadTaskGroups()
  } catch (e: any) {
    messages.add({ text: String(e), color: 'error' })
  }
  addingGroup.value = false
}

async function removeTaskGroup(id: string) {
  deletingGroup.value[id] = true
  try {
    await DeleteTaskGroup(id)
    await loadTaskGroups()
  } catch (e: any) {
    messages.add({ text: String(e), color: 'error' })
  }
  deletingGroup.value[id] = false
}

function goToTaskGroup(id: string) {
  router.push(`/cluster/task-group/${id}`)
}

onMounted(async () => {
  // Load saved locale or show language picker on first startup
  const saved = localStorage.getItem('app_locale')
  if (saved) {
    locale.value = saved
  } else {
    showLangPicker.value = true
  }
  await loadTaskGroups()
})
</script>

<template>
  <VerifiedOverlay @verified="verified = true" />

  <!-- First-startup language picker -->
  <v-overlay v-model="showLangPicker" class="align-center justify-center" persistent :opacity="0.95">
    <v-card width="400" class="pa-6 rounded-lg" elevation="8">
      <v-card-title class="text-h5 text-center">
        🌐 选择语言 / Select Language
      </v-card-title>
      <v-card-text class="text-center mt-4">
        <v-btn block size="large" variant="outlined" class="mb-3"
          :color="detectOSLocale() === 'zh-CN' ? 'primary' : undefined" @click="selectLanguage('zh-CN')">
          🇨🇳 简体中文
        </v-btn>
        <v-btn block size="large" variant="outlined" :color="detectOSLocale() === 'en-US' ? 'primary' : undefined"
          @click="selectLanguage('en-US')">
          🇺🇸 English
        </v-btn>
      </v-card-text>
    </v-card>
  </v-overlay>

  <v-app v-if="verified && !showLangPicker" class="rounded rounded-md">
    <v-navigation-drawer v-if="!isPayQR" expand-on-hover permanent>
      <v-list density="compact" nav activatable :activated="calculatedPath">
        <v-list-item :title="t('nav.home')" value="home" prepend-icon="mdi-home" @click="router.push('/')" />

        <v-divider class="mt-1" />
        <v-list-subheader>
          {{ t('nav.account') }}
        </v-list-subheader>
        <v-list-item :title="t('nav.account')" value="account/list" @click="router.push('/account/list')"
          prepend-icon="mdi-account-multiple" />
        <v-list-item :title="t('nav.buyers')" value="account/buyers" @click="router.push('/account/buyers')"
          prepend-icon="mdi-account-details" />

        <v-divider class="mt-1" />
        <v-list-subheader>
          {{ t('nav.clusterArea') }}
        </v-list-subheader>
        <v-list-item :title="t('nav.worker')" value="cluster/worker" @click="router.push('/cluster/worker')"
          prepend-icon="mdi-server-network" />
        <v-list-item :title="t('nav.logs')" value="cluster/logs" @click="router.push('/cluster/logs')"
          prepend-icon="mdi-text-box-search-outline" />
        <v-list-item :title="t('nav.events')" value="cluster/events" @click="router.push('/cluster/events')"
          prepend-icon="mdi-monitor-dashboard" />
        <v-list-item :title="t('nav.orders')" value="cluster/orders" @click="router.push('/cluster/orders')"
          prepend-icon="mdi-receipt-text-check-outline" />

        <!-- Collapsible task group section -->
        <v-list-group value="task-groups">
          <template v-slot:activator="{ props }">
            <v-list-item v-bind="props" :title="t('nav.taskGroups')" prepend-icon="mdi-folder-multiple" />
          </template>

          <v-list-item v-for="g in taskGroups" :key="g.id" :value="'tg-' + g.id" :title="g.name"
            @click="goToTaskGroup(g.id)">
            <template v-slot:append>
              <v-btn icon="mdi-close" size="x-small" variant="text" density="compact" :loading="deletingGroup[g.id]"
                @click.stop="removeTaskGroup(g.id)" />
            </template>
          </v-list-item>

          <v-list-item>
            <v-text-field v-model="newGroupName" density="compact" variant="outlined" hide-details
              :placeholder="t('nav.groupNamePlaceholder')" @keydown.enter="addTaskGroup"
              style="max-width:130px;font-size:12px;flex:none" />
            <template v-slot:append>
              <v-btn icon="mdi-plus" size="x-small" variant="text" :loading="addingGroup" @click="addTaskGroup" />
            </template>
          </v-list-item>
        </v-list-group>

        <v-divider class="mt-1" />
        <v-list-subheader>
          {{ t('nav.settingsArea') }}
        </v-list-subheader>
        <v-list-item :title="t('nav.notify')" value="notify" @click="router.push('/notify')"
          prepend-icon="mdi-bell-ring" />
        <v-list-item :title="t('nav.settings')" value="settings" @click="router.push('/settings')"
          prepend-icon="mdi-cog" />
      </v-list>
    </v-navigation-drawer>
    <v-main>
      <v-container>
        <router-view />
      </v-container>
    </v-main>
    <v-snackbar-queue v-model="messages.queue" closable :total-visible="3" collapsed display-strategy="overflow"
      location="bottom center">
      <template v-slot:actions="{ props }">
        <v-icon-btn aria-label="Close" icon="mdi-close" size="small" variant="text" v-bind="props"></v-icon-btn>
      </template>
    </v-snackbar-queue>
  </v-app>
</template>

<style lang="scss">
.v-container {
  max-width: 1185px;
  padding-left: 24px !important;
  padding-right: 24px !important;
}
</style>
