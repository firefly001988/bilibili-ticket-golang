/**
 * router/index.ts
 *
 * Automatic routes for `./src/pages/*.vue`
 */

// Composables
import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  { path: '/', component: () => import('@/pages/index.vue'), id: 'index' },
  { path: '/account', component: () => import('@/pages/account.vue'), id: 'account' },
  { path: '/ticket-overview', component: () => import('@/pages/ticket-overview.vue'), id: 'ticket-overview' },
  { path: '/ticket-project', component: () => import('@/pages/ticket-project.vue'), id: 'ticket-project' },
  { path: '/scheduler', component: () => import('@/pages/scheduler.vue'), id: 'scheduler' },
  { path: '/notify', component: () => import('@/pages/notify.vue'), id: 'notify' },
  { path: '/update', component: () => import('@/pages/update.vue'), id: 'update' },
  { path: '/plugin-download', component: () => import('@/pages/plugins-download.vue'), id: 'plugins' },
  { path: '/plugin-management', component: () => import('@/pages/plugin-management.vue'), id: 'plugin-management' },
]

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
})

// Workaround for https://github.com/vitejs/vite/issues/11804
router.onError((err, to) => {
  if (err?.message?.includes?.('Failed to fetch dynamically imported module')) {
    if (localStorage.getItem('vuetify:dynamic-reload')) {
      console.error('Dynamic import error, reloading page did not fix it', err)
    } else {
      console.log('Reloading page to fix dynamic import error')
      localStorage.setItem('vuetify:dynamic-reload', 'true')
      location.assign(to.fullPath)
    }
  } else {
    console.error(err)
  }
})

router.isReady().then(() => {
  localStorage.removeItem('vuetify:dynamic-reload')
})

export default router
