<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ApplyTrayLocale,
  CheckForUpdates as CheckForUpdatesAPI,
  GetAppVersion,
  GetUpdateCheckEnabled,
  GetVaultData,
  SaveUILocale
} from '../wailsjs/go/main/App'
import { BrowserOpenURL } from '../wailsjs/runtime/runtime'
import { callBackend } from './utils/backend'
import AppSidebar from './components/layout/AppSidebar.vue'
import AppTopHeader from './components/layout/AppTopHeader.vue'
import ForwardsPage from './components/pages/ForwardsPage.vue'
import HostsPage from './components/pages/HostsPage.vue'
import RoutesPage from './components/pages/RoutesPage.vue'
import SettingsPage from './components/pages/SettingsPage.vue'
import './styles/app-shell.css'

const { t, locale } = useI18n()

const pages = computed(() => [
  { key: 'forwards', title: t('app.sidebar.forwards'), subtitle: t('app.sidebar.forwardsSubtitle'), icon: 'bi-diagram-3' },
  { key: 'hosts', title: t('app.sidebar.hosts'), subtitle: t('app.sidebar.hostsSubtitle'), icon: 'bi-hdd-network' },
  { key: 'settings', title: t('app.sidebar.settings'), subtitle: t('app.sidebar.settingsSubtitle'), icon: 'bi-sliders2' },
  { key: 'routes', title: t('app.sidebar.routes'), subtitle: t('app.sidebar.routesSubtitle'), icon: 'bi-globe2' }
])

const savedTheme = typeof window !== 'undefined' ? window.localStorage.getItem('tunnelboard.theme') : null
const savedSidebarCollapsed = typeof window !== 'undefined' ? window.localStorage.getItem('tunnelboard.sidebar.collapsed') : null
const theme = ref(savedTheme === 'dark' ? 'dark' : 'light')
const sidebarCollapsed = ref(savedSidebarCollapsed === '1')
const activePage = ref('forwards')

const appMeta = reactive({
  version: '0.0.0'
})
const DEFAULT_RELEASES_PAGE_URL = 'https://github.com/HanZephyr/TunnelBoard/releases'
const releasePageUrl = ref(DEFAULT_RELEASES_PAGE_URL)
const hasNewVersion = ref(false)

const currentPage = computed(() => pages.value.find((page) => page.key === activePage.value))

watchEffect(() => {
  if (typeof document !== 'undefined') {
    document.documentElement.setAttribute('data-theme', theme.value)
    document.documentElement.setAttribute('data-bs-theme', theme.value)
  }
})

watch(theme, (newTheme) => {
  if (typeof window !== 'undefined') {
    window.localStorage.setItem('tunnelboard.theme', newTheme)
  }
})

watch(sidebarCollapsed, (collapsed) => {
  if (typeof window !== 'undefined') {
    window.localStorage.setItem('tunnelboard.sidebar.collapsed', collapsed ? '1' : '0')
  }
})

function toggleSidebar() {
  sidebarCollapsed.value = !sidebarCollapsed.value
}

function switchPage(pageKey) {
  activePage.value = pageKey
}

function setThemeBySwitch(enabled) {
  theme.value = enabled ? 'dark' : 'light'
}

// ---- Vault 数据（唯一数据源，页面变更后统一重载）----
const folders = ref([])
const sshHosts = ref([])
const forwards = ref([])
const webRoutes = ref([])

async function loadVault() {
  try {
    const data = await callBackend(GetVaultData)
    folders.value = Array.isArray(data?.folders) ? data.folders : []
    sshHosts.value = Array.isArray(data?.sshHosts) ? data.sshHosts : []
    forwards.value = Array.isArray(data?.forwards) ? data.forwards : []
    webRoutes.value = Array.isArray(data?.webRoutes) ? data.webRoutes : []
  } catch (_) {
    folders.value = []
    sshHosts.value = []
    forwards.value = []
    webRoutes.value = []
  }
}

// ---- 全局 toast ----
const toastMessage = ref('')
const showToast = ref(false)
const TOAST_DURATION_MS = 3800
let toastTimer = null

watch(toastMessage, (message) => {
  if (!message) {
    hideToast()
    return
  }
  showToast.value = true
  if (toastTimer !== null) {
    window.clearTimeout(toastTimer)
  }
  toastTimer = window.setTimeout(() => {
    showToast.value = false
    toastTimer = null
  }, TOAST_DURATION_MS)
})

function notify(message) {
  toastMessage.value = message || ''
}

function hideToast() {
  showToast.value = false
  if (toastTimer !== null) {
    window.clearTimeout(toastTimer)
    toastTimer = null
  }
}

// ---- 更新检查（启动时静默检查；手动检查在 Settings 页）----
function openExternalUrl(url) {
  if (!url) return
  try {
    BrowserOpenURL(url)
  } catch (_) {
    if (typeof window !== 'undefined') {
      window.open(url, '_blank', 'noopener,noreferrer')
    }
  }
}

function openReleasePage() {
  openExternalUrl(releasePageUrl.value || DEFAULT_RELEASES_PAGE_URL)
}

async function checkForUpdatesSilently() {
  try {
    const result = await callBackend(CheckForUpdatesAPI, appMeta.version)
    if (result?.hasUpdate) {
      hasNewVersion.value = true
      releasePageUrl.value = String(result.releasePageUrl || DEFAULT_RELEASES_PAGE_URL).trim() || DEFAULT_RELEASES_PAGE_URL
    }
  } catch (_) {
    // silent check, ignore errors
  }
}

// ---- 顶栏按钮路由到当前页面 ----
const forwardsPageRef = ref(null)
const hostsPageRef = ref(null)
const routesPageRef = ref(null)

function onNewForward() {
  forwardsPageRef.value?.openNewForward()
}

function onNewHost() {
  hostsPageRef.value?.openNewHost()
}

function onNewRoute() {
  routesPageRef.value?.openNewRoute()
}

onMounted(async () => {
  await loadVault()
  try {
    appMeta.version = await callBackend(GetAppVersion)
  } catch (_) {
    /* version display falls back to placeholder */
  }
  try {
    await callBackend(ApplyTrayLocale, locale.value)
  } catch (_) {
    /* tray locale sync is best-effort */
  }
  try {
    await callBackend(SaveUILocale, locale.value)
  } catch (_) {
    /* locale persist is best-effort */
  }
  let updateCheckEnabled = true
  try {
    updateCheckEnabled = await callBackend(GetUpdateCheckEnabled)
  } catch (_) {
    /* default to checking when the preference cannot be loaded */
  }
  if (updateCheckEnabled) {
    void checkForUpdatesSilently()
  }
})

onBeforeUnmount(() => {
  if (toastTimer !== null) {
    window.clearTimeout(toastTimer)
    toastTimer = null
  }
})
</script>

<template>
  <div class="app-shell">
    <AppSidebar
      :pages="pages"
      :active-page="activePage"
      :app-version="appMeta.version"
      :has-new-version="hasNewVersion"
      :collapsed="sidebarCollapsed"
      @switch-page="switchPage"
      @open-release-page="openReleasePage"
      @toggle-collapse="toggleSidebar"
    />

    <section class="content-shell">
      <AppTopHeader
        :current-page="currentPage"
        :active-page="activePage"
        @new-forward="onNewForward"
        @new-host="onNewHost"
        @new-route="onNewRoute"
      />

      <main class="page-body">
        <ForwardsPage
          v-if="activePage === 'forwards'"
          ref="forwardsPageRef"
          :folders="folders"
          :ssh-hosts="sshHosts"
          :forwards="forwards"
          @vault-changed="loadVault"
          @notify="notify"
        />

        <HostsPage
          v-if="activePage === 'hosts'"
          ref="hostsPageRef"
          :ssh-hosts="sshHosts"
          :forwards="forwards"
          @vault-changed="loadVault"
          @notify="notify"
        />

        <RoutesPage
          v-if="activePage === 'routes'"
          ref="routesPageRef"
          :forwards="forwards"
          :web-routes="webRoutes"
          @vault-changed="loadVault"
          @notify="notify"
        />

        <SettingsPage
          v-if="activePage === 'settings'"
          :theme="theme"
          :app-meta="appMeta"
          @theme-change="setThemeBySwitch"
          @vault-changed="loadVault"
          @notify="notify"
        />
      </main>

      <div v-if="showToast && toastMessage" class="toast-container position-absolute bottom-0 start-50 translate-middle-x p-3 toast-config-container">
        <div class="toast show align-items-center text-bg-dark border-0 toast-config-item" role="alert" aria-live="assertive" aria-atomic="true">
          <div class="d-flex">
            <div class="toast-body">
              {{ toastMessage }}
            </div>
            <button
              type="button"
              class="btn-close btn-close-white me-2 m-auto"
              :aria-label="$t('app.common.close')"
              @click="hideToast"
            />
          </div>
        </div>
      </div>
    </section>
  </div>
</template>
