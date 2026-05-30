<script lang="ts" setup>
import noface from '@/assets/noface.png';
import { computed, onMounted, ref } from 'vue';
import router from './router';
import { useMessagesStore } from './stores/snackbar';
import { useAuthStore } from './stores/auth';
import VerifiedOverlay from './components/VerifiedOverlay.vue';

const auth = useAuthStore();
const messages = useMessagesStore();

const verified = ref(false)

// BWS 仅在 7月8日 00:00 ~ 7月11日 24:00 期间可用；dev 环境始终可用
const bwsAvailable = computed(() => {
  if (import.meta.env.DEV) return true;
  const now = new Date();
  const year = now.getFullYear();
  const start = new Date(year, 6, 8, 0, 0, 0);  // July 8 00:00
  const end = new Date(year, 6, 12, 0, 0, 0);  // July 12 00:00 = July 11 24:00
  return now >= start && now < end;
});

const bwsTooltip = computed(() =>
  bwsAvailable.value ? 'BWS 活动抢票' : 'BWS 仅在 7月8日 至 7月11日 期间开放'
);

const calculatedPath = computed(() => {
  console.log('Current route path:', router.currentRoute.value.path.replace('/', ''));
  return router.currentRoute.value.path.replace('/', '') || 'home';
});

onMounted(async () => {
  await auth.checkLoginStatus();
})
</script>

<template>
  <VerifiedOverlay @verified="verified = true" />
  <v-app v-if="verified" class="rounded rounded-md">
    <v-navigation-drawer expand-on-hover permanent rail>
      <v-list :activated="calculatedPath">
        <v-list-item v-if="!auth.isLogin" :prepend-avatar="noface" subtitle="UID: -" title="Not logged in" />
        <v-list-item v-else :prepend-avatar="auth.avatarDataUri || noface" :subtitle="`UID: ${auth.uid}`"
          :title="auth.username" />
      </v-list>
      <v-divider />
      <v-list density="compact" nav activatable :activated="calculatedPath">
        <v-list-subheader>
          Uncategorized
        </v-list-subheader>
        <v-list-item title="Home" value="home" prepend-icon="mdi-home" @click="router.push('/')" />
        <v-list-item title="Account" value="account" :class="{
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
          Ticket Area
        </v-list-subheader>
        <v-list-item title="Overview" value="ticket-overview" :disabled="!auth.isLogin"
          @click="router.push('/ticket-overview')" prepend-icon="mdi-eye" />
        <v-list-item title="Project Lookup" value="ticket-project" :disabled="!auth.isLogin"
          @click="router.push('/ticket-project')" prepend-icon="mdi-magnify" />
        <v-list-item title="BWS Reservation" value="bws-reservation" :disabled="!auth.isLogin || !bwsAvailable"
          @click="router.push('/bws-reservation')" prepend-icon="mdi-ticket-confirmation">
          <v-tooltip v-if="auth.isLogin && !bwsAvailable" activator="parent" location="right">
            {{ bwsTooltip }}
          </v-tooltip>
        </v-list-item>
        <v-list-item title="Scheduler" value="scheduler" :disabled="!auth.isLogin" @click="router.push('/scheduler')"
          prepend-icon="mdi-calendar-clock" />
        <v-divider class="mt-1" />
        <v-list-subheader>
          Plugin Area
        </v-list-subheader>
        <v-list-item title="Plugin Download" value="plugin-download" @click="router.push('/plugin-download')"
          prepend-icon="mdi-puzzle" />
        <v-list-item title="Plugin Management" value="plugin-management" @click="router.push('/plugin-management')"
          prepend-icon="mdi-puzzle-edit" />
        <v-divider class="mt-1" />
        <v-list-subheader>
          Settings Area
        </v-list-subheader>
        <v-list-item title="Notify" value="notify" @click="router.push('/notify')" prepend-icon="mdi-bell-ring" />
        <v-list-item title="Settings" value="settings" @click="router.push('/settings')" prepend-icon="mdi-cog" />
        <v-list-item title="Update" value="update" @click="router.push('/update')" prepend-icon="mdi-update" />
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