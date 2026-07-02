<script lang="ts" setup>
import { onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import AnnouncementCard from '@/components/AnnouncementFloatingCard.vue'
import { useAnnouncements } from '@/composables/useAnnouncements'

const { t } = useI18n()

const { announcements, refresh, loading, error, mirrorOptions, activeMirrorIndex, switchMirror } = useAnnouncements()

onMounted(() => {
    refresh()
})
</script>

<template>
    <v-container>
        <!-- Header -->
        <div class="page-title-bar">
            <div>
                <h1 class="page-title">{{ t('index.title') }}</h1>
            </div>
            <v-spacer />
            <v-select v-if="error && mirrorOptions.length > 1" :model-value="activeMirrorIndex" :items="mirrorOptions"
                item-title="title" item-value="value" :label="t('index.mirrorLabel')" variant="outlined"
                density="compact" hide-details style="max-width: 180px;" class="mr-2"
                @update:model-value="switchMirror" />
            <v-btn icon="mdi-refresh" variant="text" size="small" :loading="loading" @click="refresh()" />
        </div>

        <!-- Error banner -->
        <v-alert v-if="error" type="warning" variant="tonal" class="mb-4">
            {{ t('index.errorBanner', { error }) }}
        </v-alert>

        <!-- Loading skeleton -->
        <v-row v-if="loading && announcements.length === 0">
            <v-col v-for="n in 3" :key="n" cols="12" sm="6" lg="4">
                <v-skeleton-loader type="card" />
            </v-col>
        </v-row>

        <!-- Announcement waterfall -->
        <v-row v-if="announcements.length > 0">
            <v-col v-for="ann in announcements" :key="ann.timestamp" cols="12" sm="6" lg="4">
                <v-slide-y-transition>
                    <AnnouncementCard :announcement="ann" />
                </v-slide-y-transition>
            </v-col>
        </v-row>

        <!-- Empty state -->
        <v-card v-if="!loading && !error && announcements.length === 0" variant="outlined" class="pa-8 text-center"
            rounded="lg">
            <v-icon icon="mdi-check-circle" size="48" color="success" class="mb-3" />
            <p class="text-body-1 text-medium-emphasis">{{ t('index.empty') }}</p>
        </v-card>
    </v-container>
</template>
