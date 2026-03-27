import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/pages/LoginPage.vue'),
      meta: { title: 'Login - Altclaw' },
    },
    {
      path: '/',
      name: 'ide',
      component: () => import('@/layouts/IdeLayout.vue'),
      meta: { requiresAuth: true, title: 'Altclaw' },
    },
    {
      path: '/:pathMatch(.*)*',
      redirect: '/'
    }
  ],
})

router.beforeEach(async (to) => {
  // In Wails GUI mode, the webview is a trusted local context — skip auth gating.
  if ((window as any).chrome?.webview) return

  if (to.meta.requiresAuth || to.matched.some(record => record.meta.requiresAuth)) {
    try {
      const resp = await fetch('/api/config')
      if (!resp.ok) return '/login'
    } catch {
      return '/login'
    }
  }
})

router.afterEach((to) => {
  if (to.meta && to.meta.title) {
    document.title = to.meta.title as string
  } else {
    document.title = 'Altclaw'
  }
})

export default router
