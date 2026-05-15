/**
 * main.ts
 *
 * Bootstraps Vuetify and other plugins then mounts the App`
 */
import { install } from 'vue-qr'

// Plugins
import { registerPlugins } from '@/plugins'

// Components
import App from './App.vue'

// Composables
import { createApp } from 'vue'

// Styles
import 'unfonts.css'
import router from './router'

const app = createApp(App)

registerPlugins(app)

app.use(router)

app.use({ install })

app.mount('#app')
