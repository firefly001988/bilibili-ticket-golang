<script lang="ts" setup>
import { onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { GetAccountStatus, GetBUVID, GetFingerprint, GetAppVersion } from '../../wailsjs/go/biliutils/BiliClient';
import { useAuthStore } from '@/stores/auth';

const { t } = useI18n()
const auth = useAuthStore();

const sysInfo = ref({
    buvid: '',
    appVersion: '',
    loginName: '',
    loginUid: 0,
});

onMounted(async () => {
    await auth.checkLoginStatus();
    try {
        const [buvid, fp, ver] = await Promise.all([
            GetBUVID(),
            GetFingerprint(),
            GetAppVersion(),
        ]);
        sysInfo.value.buvid = buvid;
        sysInfo.value.appVersion = ver?.version || 'unknown';
        sysInfo.value.loginName = auth.username;
        sysInfo.value.loginUid = auth.uid;
    } catch { /* ignore */ }
});
</script>

<style lang="css" scoped>
.v-card-text p {
    margin: 0 0;
}
</style>

<template>
    <h1>{{ t('ticketOverview.title') }}</h1>
    <v-divider thickness="3" />

    <!-- System Status -->
    <v-card class="mt-4 pa-4" variant="outlined">
        <v-card-title>
            <strong>{{ t('ticketOverview.systemStatus') }}</strong>
        </v-card-title>
        <v-card-text>
            <p><strong>{{ t('ticketOverview.login') }}:</strong> {{ auth.isLogin ? `${sysInfo.loginName} (UID: ${sysInfo.loginUid})` : t('ticketOverview.notLoggedIn') }}</p>
            <p><strong>{{ t('ticketOverview.buvid') }}:</strong> {{ sysInfo.buvid || '—' }}</p>
            <p><strong>{{ t('ticketOverview.appVersion') }}:</strong> {{ sysInfo.appVersion || '—' }}</p>
        </v-card-text>
    </v-card>

    <v-card v-if="auth.isLogin" class="mt-4 pa-4" variant="outlined">
        <v-card-title>{{ t('ticketOverview.quickLinks') }}</v-card-title>
        <v-card-text>
            <v-btn variant="tonal" color="primary" class="mr-2" @click="$router.push('/scheduler')">
                <v-icon start>mdi-calendar-clock</v-icon>
                {{ t('ticketOverview.taskManagement') }}
            </v-btn>
            <v-btn variant="tonal" color="primary" @click="$router.push('/ticket-project')">
                <v-icon start>mdi-magnify</v-icon>
                {{ t('ticketOverview.projectLookup') }}
            </v-btn>
        </v-card-text>
    </v-card>

    <v-card v-else class="mt-4 pa-4" color="warning" variant="tonal">
        <v-card-text>
            {{ t('ticketOverview.loginFirst') }}
        </v-card-text>
    </v-card>
</template>
