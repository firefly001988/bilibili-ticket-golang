import { createRouter, createWebHashHistory } from 'vue-router'

const routes = [
  { path: '/', component: () => import('@/pages/index.vue') },
  { path: '/notify', component: () => import('@/pages/notify.vue') },
  { path: '/account/list', component: () => import('@/pages/account/list.vue') },
  { path: '/account/buyers', component: () => import('@/pages/account/buyers.vue') },
  { path: '/cluster/worker', component: () => import('@/pages/cluster/worker.vue') },
  { path: '/cluster/task-group/:id', component: () => import('@/pages/cluster/task-group.vue') },
  { path: '/cluster/logs', component: () => import('@/pages/cluster/logs.vue') },
  { path: '/pay-qr', component: () => import('@/pages/pay-qr.vue') },
]

const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

export default router
