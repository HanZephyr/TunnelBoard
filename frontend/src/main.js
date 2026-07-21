import { createApp } from 'vue'
import App from './App.vue'
import { createApplicationI18n } from './i18n'
import 'bootstrap/dist/css/bootstrap.min.css'
import 'bootstrap-icons/font/bootstrap-icons.css'
import './style.css';

async function bootstrap() {
  const i18n = await createApplicationI18n()
  const app = createApp(App)
  app.use(i18n)
  app.mount('#app')
}

void bootstrap()
