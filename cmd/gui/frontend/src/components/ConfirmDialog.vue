<script lang="ts" setup>
import { useConfirmStore } from '@/composables/useConfirm'

const confirm = useConfirmStore()
</script>

<template>
    <v-dialog v-model="confirm.state.show" max-width="400">
        <v-card class="mt-8" :title="confirm.state.title">
            <v-card-text>{{ confirm.state.message }}</v-card-text>
            <v-card-actions>
                <v-spacer />
                <v-btn variant="text" @click="confirm.resolve(false)">取消</v-btn>
                <v-btn color="primary" variant="tonal" @click="confirm.resolve(true)">确定</v-btn>
            </v-card-actions>
        </v-card>
    </v-dialog>
</template>
