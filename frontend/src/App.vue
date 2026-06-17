<script lang="ts" setup>
import noface from '@/assets/noface.png';
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n'
import router from './router';
import { useMessagesStore } from './stores/snackbar';
import { useAuthStore } from './stores/auth';
import VerifiedOverlay from './components/VerifiedOverlay.vue';

const { t, locale } = useI18n()
const auth = useAuthStore();
const messages = useMessagesStore();

const verified = ref(false)
const showLangPicker = ref(false)

// Detect OS language for the picker default
function detectOSLocale(): string {
  const nav = navigator.language
  if (nav.startsWith('zh')) return 'zh-CN'
  if (nav.startsWith('en')) return 'en'
  return 'zh-CN'
}

function selectLanguage(loc: string) {
  locale.value = loc
  localStorage.setItem('app_locale', loc)
  showLangPicker.value = false
}

// BWS 仅在 7月8日 00:00 ~ 7月11日 24:00 期间可用；dev 环境始终可用
const bwsAvailable = computed(() => {
  if (import.meta.env.DEV) return true;
  const now = new Date();
  const year = now.getFullYear();
  const start = new Date(year, 6, 8, 0, 0, 0);
  const end = new Date(year, 6, 12, 0, 0, 0);
  return now >= start && now < end;
});

const bwsTooltip = computed(() =>
  bwsAvailable.value ? t('nav.bwsTooltip') : t('nav.bwsUnavailableTooltip')
);

const calculatedPath = computed(() => {
  return router.currentRoute.value.path.replace('/', '') || 'home';
});

onMounted(async () => {
  await auth.checkLoginStatus();

  // Load saved locale or show language picker on first startup
  const saved = localStorage.getItem('app_locale')
  if (saved) {
    locale.value = saved
  } else {
    showLangPicker.value = true
  }
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
        <v-btn
          block
          size="large"
          variant="outlined"
          class="mb-3"
          :color="detectOSLocale() === 'zh-CN' ? 'primary' : undefined"
          @click="selectLanguage('zh-CN')"
        >
          🇨🇳 简体中文
        </v-btn>
        <v-btn
          block
          size="large"
          variant="outlined"
          :color="detectOSLocale() === 'en' ? 'primary' : undefined"
          @click="selectLanguage('en')"
        >
          🇺🇸 English
        </v-btn>
      </v-card-text>
    </v-card>
  </v-overlay>

  <v-app v-if="verified && !showLangPicker" class="rounded rounded-md">
    <v-navigation-drawer expand-on-hover permanent rail>
      <v-list :activated="calculatedPath">
        <v-list-item v-if="!auth.isLogin" :prepend-avatar="noface" subtitle="UID: -" :title="t('nav.notLoggedIn')" />
        <v-list-item v-else :prepend-avatar="auth.avatarDataUri || noface" :subtitle="`UID: ${auth.uid}`"
          :title="auth.username" />
      </v-list>
      <v-divider />
      <v-list density="compact" nav activatable :activated="calculatedPath">
        <v-list-subheader>
          {{ t('nav.uncategorized') }}
        </v-list-subheader>
        <v-list-item :title="t('nav.home')" value="home" prepend-icon="mdi-home" @click="router.push('/')" />
        <v-list-item :title="t('nav.account')" value="account" :class="{
          'text-red': !auth.isLogin && calculatedPath !== 'account',
          'text-red-darken-2': !auth.isLogin && calculatedPath === 'account',
        }" @click="router.push('/account')">
          <template #prepend>
            <v-icon v-if="auth.isLogin">mdi-account-check</v-icon>
            <v-icon v-else>mdi-account-alert</v-icon>
          </template>
        </v-list-item>
        <v-divider class="mt-1" />
        <v-list-subheader>
          {{ t('nav.ticketArea') }}
        </v-list-subheader>
        <v-list-item :title="t('nav.overview')" value="ticket-overview" :disabled="!auth.isLogin"
          @click="router.push('/ticket-overview')" prepend-icon="mdi-eye" />
        <v-list-item :title="t('nav.projectLookup')" value="ticket-project" :disabled="!auth.isLogin"
          @click="router.push('/ticket-project')" prepend-icon="mdi-magnify" />
        <v-list-item :title="t('nav.bwsReservation')" value="bws-reservation" :disabled="!auth.isLogin || !bwsAvailable"
          @click="router.push('/bws-reservation')" prepend-icon="mdi-ticket-confirmation">
          <v-tooltip v-if="auth.isLogin && !bwsAvailable" activator="parent" location="right">
            {{ bwsTooltip }}
          </v-tooltip>
        </v-list-item>
        <v-list-item :title="t('nav.scheduler')" value="scheduler" :disabled="!auth.isLogin" @click="router.push('/scheduler')"
          prepend-icon="mdi-calendar-clock" />
        <v-divider class="mt-1" />
        <v-list-subheader>
          {{ t('nav.pluginArea') }}
        </v-list-subheader>
        <v-list-item :title="t('nav.pluginDownload')" value="plugin-download" @click="router.push('/plugin-download')"
          prepend-icon="mdi-puzzle" />
        <v-list-item :title="t('nav.pluginManagement')" value="plugin-management" @click="router.push('/plugin-management')"
          prepend-icon="mdi-puzzle-edit" />
        <v-divider class="mt-1" />
        <v-list-subheader>
          {{ t('nav.settingsArea') }}
        </v-list-subheader>
        <v-list-item :title="t('nav.notify')" value="notify" @click="router.push('/notify')" prepend-icon="mdi-bell-ring" />
        <v-list-item :title="t('nav.settings')" value="settings" @click="router.push('/settings')" prepend-icon="mdi-cog" />
        <v-list-item :title="t('nav.update')" value="update" @click="router.push('/update')" prepend-icon="mdi-update" />
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