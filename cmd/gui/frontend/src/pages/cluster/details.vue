<script lang="ts" setup>
import { useI18n } from 'vue-i18n';

const { t } = useI18n();

</script>

<template>
    <v-container>
        <h1>{{ t('nav.clusterDetails') }}</h1>
    </v-container>
</template>