/**
 * Debug mode composable — reads the backend global.Debug flag once
 * and provides a debugLog() wrapper to conditionally output to console.
 *
 * Usage:
 *   const { debugLog } = useDebug();
 *   debugLog('[optional tag]', arg1, arg2, ...);
 *   debugLog.group('[tag]', () => { ... });  // group collapsed when debug is off
 */

import { ref } from 'vue';
import { IsDebug } from '../../wailsjs/go/biliutils/BiliClient';

const isDebug = ref(false);
let loaded = false;

export function useDebug() {
    async function init() {
        if (loaded) return;
        try {
            isDebug.value = await IsDebug();
        } catch {
            // If the backend call fails (e.g. early in startup), default to false.
            isDebug.value = false;
        }
        loaded = true;
    }

    // Auto-init on first call
    if (!loaded) {
        init();
    }

    function debugLog(...args: any[]) {
        if (isDebug.value) {
            console.log(...args);
        }
    }

    /**
     * Wraps console.group / console.groupEnd.
     * When debug is off, the callback is NOT invoked at all.
     *
     * @param label  The group label shown in the console.
     * @param fn     A callback that performs the grouped console calls.
     */
    function debugGroup(label: string, fn: () => void) {
        if (isDebug.value) {
            console.group(label);
            fn();
            console.groupEnd();
        }
    }

    return { isDebug, debugLog, debugGroup, init };
}
