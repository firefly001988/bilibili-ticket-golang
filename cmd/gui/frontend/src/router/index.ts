/**
 * router/index.ts
 *
 * Automatic routes for `./src/pages/*.vue`
 */

// Composables
import { createRouter, createWebHashHistory } from 'vue-router'

const routes = [
  { path: '/', component: () => import('@/pages/index.vue'), id: 'index' },
  { path: '/account', component: () => import('@/pages/account.vue'), id: 'account' },
  { path: '/ticket-overview', component: () => import('@/pages/ticket-overview.vue'), id: 'ticket-overview' },
  { path: '/ticket-project', component: () => import('@/pages/ticket-project.vue'), id: 'ticket-project' },
  {
    path: '/scheduler', component: () => import('@/pages/cluster-scheduler.vue'), id: 'scheduler', redirect: '/scheduler/tasks', children: [
      { path: 'tasks', component: () => import('@/pages/cluster-tasks.vue') },
      { path: 'accounts', component: () => import('@/pages/cluster-accounts.vue') },
      { path: 'workers', component: () => import('@/pages/cluster-workers.vue') },
      { path: 'attempts', component: () => import('@/pages/cluster-attempts.vue') },
    ]
  },
  { path: '/notify', component: () => import('@/pages/notify.vue'), id: 'notify' },
  { path: '/settings', component: () => import('@/pages/settings.vue'), id: 'settings' },
  { path: '/update', component: () => import('@/pages/update.vue'), id: 'update' },
  { path: '/bws-reservation', component: () => import('@/pages/bws-reservation.vue'), id: 'bws-reservation' },
  { path: '/plugin-download', component: () => import('@/pages/plugins-download.vue'), id: 'plugins' },
  { path: '/plugin-management', component: () => import('@/pages/plugin-management.vue'), id: 'plugin-management' },
  { path: '/worker-config', component: () => import('@/pages/worker-config.vue'), id: 'worker-config' },
  { path: '/pay-qr', component: () => import('@/pages/pay-qr.vue'), id: 'pay-qr' },
]

const router = createRouter({
  history: createWebHashHistory(),
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
