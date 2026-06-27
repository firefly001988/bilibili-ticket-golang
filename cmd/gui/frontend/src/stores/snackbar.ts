import { defineStore } from "pinia"
import { ref } from "vue"
import type { SnackbarMessage } from "vuetify/lib/components/VSnackbarQueue/VSnackbarQueue.mjs"

export const useMessagesStore = defineStore('messages', () => {
    const queue = ref<SnackbarMessage[]>([])
    let zIndexCounter = 1000
    function add(message: SnackbarMessage) {
        if (typeof message === 'string') {
            message = { text: message, color: 'info' }
        }
        queue.value.push({
            ...message,
            zIndex: zIndexCounter++,
        })
    }

    return { queue, add }
})
