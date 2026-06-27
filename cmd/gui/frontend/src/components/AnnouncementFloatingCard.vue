<script lang="ts" setup>
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Announcement } from '@/utils/announcementParser'
import { Priority } from '@/utils/announcementParser'
import MarkdownRender from 'markstream-vue'

const { t } = useI18n()

const props = defineProps<{
    announcement: Announcement
}>()

const expanded = ref(false)

const priorityLabel = computed(() => {
    const p = props.announcement.priority
    if (p === Priority.CRITICAL) return t('announcement.priority.critical')
    if (p === Priority.WARN) return t('announcement.priority.warn')
    if (p === Priority.SUCCESS) return t('announcement.priority.success')
    return t('announcement.priority.info')
})

const priorityColor = computed(() => {
    const p = props.announcement.priority
    if (p === Priority.CRITICAL) return 'error'
    if (p === Priority.WARN) return 'warning'
    if (p === Priority.SUCCESS) return 'success'
    return 'info'
})

const priorityIcon = computed(() => {
    const p = props.announcement.priority
    if (p === Priority.CRITICAL) return 'mdi-alert-octagon'
    if (p === Priority.WARN) return 'mdi-alert'
    if (p === Priority.SUCCESS) return 'mdi-check-circle'
    return 'mdi-information'
})

const formattedTime = computed(() => {
    const ts = props.announcement.timestamp
    if (!ts) return ''
    return new Date(ts).toLocaleString('zh-CN', {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
    })
})
</script>

<template>
    <v-card class="announcement-card" :elevation="expanded ? 4 : 2" rounded="lg" :border="true" hover>
        <div class="announcement-header d-flex align-center pa-4" :class="{ 'cursor-pointer': !expanded }"
            @click="expanded = !expanded">
            <v-avatar :color="priorityColor" size="40" class="flex-shrink-0">
                <v-icon :icon="priorityIcon" size="22" color="white" />
            </v-avatar>

            <div class="ml-3 overflow-hidden flex-grow-1">
                <div class="d-flex align-center flex-wrap ga-2">
                    <span class="text-subtitle-2 font-weight-bold text-truncate">
                        {{ announcement.title }}
                    </span>
                    <v-chip :color="priorityColor" size="x-small" variant="elevated">
                        {{ priorityLabel }}
                    </v-chip>
                </div>
                <div class="d-flex align-center mt-1 flex-wrap ga-1">
                    <span class="text-caption text-medium-emphasis">{{ formattedTime }}</span>
                    <template v-if="announcement.tags.length">
                        <v-chip v-for="tag in announcement.tags" :key="tag.name" size="x-small" variant="outlined"
                            :style="{ borderColor: tag.color || undefined, color: tag.color || undefined }">
                            {{ tag.name }}
                        </v-chip>
                    </template>
                </div>
            </div>

            <v-btn :icon="expanded ? 'mdi-chevron-up' : 'mdi-chevron-down'" variant="text" size="small"
                density="compact" class="flex-shrink-0" @click.stop="expanded = !expanded" />
        </div>

        <div class="announcement-body" :class="{ 'announcement-body--expanded': expanded }">
            <div class="announcement-body-inner">
                <v-divider />
                <v-card-text class="pt-1 pb-0">
                    <div class="announcement-content">
                        <MarkdownRender :content="announcement.content" :final="true" />
                    </div>
                </v-card-text>
            </div>
        </div>
    </v-card>
</template>

<style lang="scss" scoped>
.announcement-card {
    width: 100%;
    transition: box-shadow 0.2s ease, transform 0.2s ease;

    &:hover {
        transform: translateY(-2px);
    }
}

.announcement-header {
    user-select: none;
}

.announcement-body {
    display: grid;
    grid-template-rows: 0fr;
    transition: grid-template-rows 0.25s ease;

    &--expanded {
        grid-template-rows: 1fr;
    }

    &-inner {
        overflow: hidden;
    }
}

.announcement-content {
    :deep(p) {
        margin-bottom: 0.5rem;
    }

    :deep(a) {
        color: rgb(var(--v-theme-primary));
    }

    :deep(code) {
        background: rgba(var(--v-theme-surface-variant), 0.5);
        padding: 2px 6px;
        border-radius: 4px;
        font-size: 0.875em;
    }
}
</style>
