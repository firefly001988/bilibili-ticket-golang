<script lang="ts" setup>
import VueQr from 'vue-qr'
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { GetQRCodeUrlAndKey, GetQRLoginState, GetAccountStatus } from '../../bindings/bilibili-ticket-golang/lib/biliutils/biliclient';
import type * as api from '../../bindings/bilibili-ticket-golang/lib/models/bili/api/models';
import { useMessagesStore } from '@/stores/snackbar';
import { useAuthStore } from '@/stores/auth';

const { t } = useI18n()
const message = useMessagesStore();
const auth = useAuthStore();

const QRData = ref<{
    qr: api.QRLoginKeyStruct,
    genTimestamp: number,
    leftTime: number,
    isNeedRefresh: boolean,
    statusMessage?: string,
}>({
    qr: { url: '', qrcode_key: '' },
    genTimestamp: 0,
    leftTime: 0,
    isNeedRefresh: true,
});

onMounted(async () => {
    await auth.checkLoginStatus();
});

async function QRLogin() {
    const qr = await GetQRCodeUrlAndKey();
    QRData.value.qr = qr;
    QRData.value.genTimestamp = Date.now();
    QRData.value.statusMessage = '';
    QRData.value.isNeedRefresh = false;
    startPolling();
}

let timerId: ReturnType<typeof setInterval> | null = null;

function startPolling() {
    timerId = setInterval(async () => {
        if (Date.now() - QRData.value.genTimestamp > 180000) {
            QRData.value.leftTime = 0;
            QRData.value.isNeedRefresh = true;
            clearInterval(timerId!);
            timerId = null;
            message.add({ text: t('account.qrExpired'), color: 'error', timeout: 1500 });
            return;
        }
        QRData.value.leftTime = Math.max(0, Math.floor((180000 - (Date.now() - QRData.value.genTimestamp)) / 1000));

        const state = await GetQRLoginState(QRData.value.qr.qrcode_key);
        if (state.code === 0) {
            clearInterval(timerId!);
            timerId = null;
            // Save the refresh_token for future cookie refresh
            if (state.refresh_token) {
                await auth.saveRefreshToken(state.refresh_token)
            }
            message.add({ text: t('account.loginSuccess'), color: 'success', timeout: 3000 });
            // Refresh auth store
            await auth.checkLoginStatus();
        } else if (state.code === 86038) {
            message.add({ text: t('account.qrExpired'), color: 'error', timeout: 1500 });
            clearInterval(timerId!);
            timerId = null;
            QRData.value.isNeedRefresh = true;
        } else {
            QRData.value.statusMessage = state.message;
        }
    }, 1000);
}

onUnmounted(() => {
    if (timerId) clearInterval(timerId);
});
</script>

<style lang="css" scoped>
.v-card-text p {
    margin: 0 0;
}
</style>

<template>
    <h1>{{ t('account.title') }}</h1>
    <v-divider thickness="3" />

    <!-- Already logged in -->
    <v-card v-if="auth.isLogin" class="pa-4 mt-4" color="success" variant="tonal">
        <v-card-title>{{ t('account.loggedIn') }}</v-card-title>
        <v-card-text>
            <p><strong>{{ t('account.username') }}:</strong> {{ auth.username }}</p>
            <p><strong>{{ t('account.uid') }}:</strong> {{ auth.uid }}</p>
        </v-card-text>
    </v-card>

    <!-- QR Login -->
    <div v-if="!auth.isLogin" class="mt-4"
        style="display: flex; flex-direction: column; align-items: center; gap: 12px;">
        <vue-qr v-if="!QRData.isNeedRefresh" :text="QRData.qr.url" :size="200" :margin="10"
            :background-dimming="'rgba(0,0,0,255)'" style="border-radius: 5px;" />

        <v-btn v-if="QRData.isNeedRefresh" @click="QRLogin" prepend-icon="mdi-qrcode" color="primary">
            QR Code Login
        </v-btn>

        <v-badge v-else bordered location="top center" color="warning" :content="QRData.statusMessage" inline>
            <v-btn disabled variant="tonal">Expires in: {{ QRData.leftTime }}s</v-btn>
        </v-badge>
    </div>
</template>
