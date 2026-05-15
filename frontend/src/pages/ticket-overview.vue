<script lang="ts" setup>
import { onMounted, ref } from 'vue';
import { GetAccountStatus, GetBUVID, GetFingerprint, GetAppVersion } from '../../wailsjs/go/biliutils/BiliClient';
import { useAuthStore } from '@/stores/auth';

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
    <h1>Ticket Management</h1>
    <v-divider thickness="3" />

    <!-- System Status -->
    <v-card class="mt-4 pa-4" variant="outlined">
        <v-card-title>
            <strong>System Status</strong>
        </v-card-title>
        <v-card-text>
            <p><strong>Login:</strong> {{ auth.isLogin ? `${sysInfo.loginName} (UID: ${sysInfo.loginUid})` : `Not logged
                in ` }}</p>
            <p><strong>BUVID:</strong> {{ sysInfo.buvid || '—' }}</p>
            <p><strong>App Version:</strong> {{ sysInfo.appVersion || '—' }}</p>
        </v-card-text>
    </v-card>

    <v-card v-if="auth.isLogin" class="mt-4 pa-4" variant="outlined">
        <v-card-title>Quick Links</v-card-title>
        <v-card-text>
            <v-btn variant="tonal" color="primary" class="mr-2" @click="$router.push('/scheduler')">
                <v-icon start>mdi-calendar-clock</v-icon>
                任务管理
            </v-btn>
            <v-btn variant="tonal" color="primary" @click="$router.push('/ticket-project')">
                <v-icon start>mdi-magnify</v-icon>
                项目查找
            </v-btn>
        </v-card-text>
    </v-card>

    <v-card v-else class="mt-4 pa-4" color="warning" variant="tonal">
        <v-card-text>
            Please <router-link to="/account">login</router-link> first to manage tickets.
        </v-card-text>
    </v-card>
</template>
