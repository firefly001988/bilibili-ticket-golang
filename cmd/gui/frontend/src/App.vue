<script lang="ts" setup>
</script>

<template>
  <v-app>
    <v-main>
      <router-view />
    </v-main>
  </v-app>
</template>

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
    <v-navigation-drawer expand-on-hover permanent rail>
      <v-list density="compact" nav activatable :activated="calculatedPath">
        <v-list-subheader>
          {{ t('nav.uncategorized') }}
        </v-list-subheader>
        <v-list-item :title="t('nav.home')" value="home" prepend-icon="mdi-home" @click="router.push('/')" />
        <v-divider class="mt-1" />
        <v-list-subheader>
          {{ t('nav.ticketArea') }}
        </v-list-subheader>
        <v-list-item :title="t('nav.bwsReservation')" value="bws-reservation" :disabled="!auth.isLogin || !bwsAvailable"
          @click="router.push('/bws-reservation')" prepend-icon="mdi-ticket-confirmation">
          <v-tooltip v-if="auth.isLogin && !bwsAvailable" activator="parent" location="right">
            {{ bwsTooltip }}
          </v-tooltip>
        </v-list-item>
        <v-list-item :title="t('nav.scheduler')" value="scheduler" @click="router.push('/scheduler')"
          prepend-icon="mdi-calendar-clock" />
        <v-divider class="mt-1" />
        <v-list-subheader>
          {{ t('nav.pluginArea') }}
        </v-list-subheader>
        <v-list-item :title="t('nav.pluginDownload')" value="plugin-download" @click="router.push('/plugin-download')"
          prepend-icon="mdi-puzzle" />
        <v-list-item :title="t('nav.pluginManagement')" value="plugin-management"
          @click="router.push('/plugin-management')" prepend-icon="mdi-puzzle-edit" />
        <v-divider class="mt-1" />
        <v-list-subheader>
          {{ t('nav.settingsArea') }}
        </v-list-subheader>
        <v-list-item :title="t('nav.notify')" value="notify" @click="router.push('/notify')"
          prepend-icon="mdi-bell-ring" />
        <v-list-item :title="t('nav.workerConfig')" value="worker-config" @click="router.push('/worker-config')"
          prepend-icon="mdi-server-network" />
        <v-list-item :title="t('nav.settings')" value="settings" @click="router.push('/settings')"
          prepend-icon="mdi-cog" />
        <v-list-item :title="t('nav.update')" value="update" @click="router.push('/update')"
          prepend-icon="mdi-update" />
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

    <ConfirmDialog />
  </v-app>
</template>

<style lang="scss">
.v-container {
  max-width: 1185px;
  padding-left: 24px !important;
  padding-right: 24px !important;
}
</style>