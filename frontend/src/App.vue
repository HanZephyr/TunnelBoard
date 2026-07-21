<script setup>
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, reactive, ref, watch, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ApplyTrayLocale,
  CheckForUpdates as CheckForUpdatesAPI,
  GetAppVersion,
  GetUpdateCheckEnabled,
  SaveUILocale
} from '../wailsjs/go/main/App'
import { BrowserOpenURL } from '../wailsjs/runtime/runtime'
import { callBackend } from './utils/backend'
import AppSidebar from './components/layout/AppSidebar.vue'
import AppTopHeader from './components/layout/AppTopHeader.vue'
import OverviewPage from './components/pages/OverviewPage.vue'
import BaseDialog from './components/common/BaseDialog.vue'
import { createAppSnapshotStore } from './stores/appSnapshotStore'
import { createUpdatePreferenceStore } from './stores/updatePreferenceStore'
import { createUpdateNoticeStore } from './stores/updateNoticeStore'
import { DEFAULT_RELEASES_PAGE_URL, officialReleaseUrl } from './modules/releaseUrl'
import { createApplicationClient } from './utils/applicationClient'
import './styles/app-shell.css'

const ForwardsPage = defineAsyncComponent(() => import('./components/pages/ForwardsPage.vue'))
const HostsPage = defineAsyncComponent(() => import('./components/pages/HostsPage.vue'))
const LogsPage = defineAsyncComponent(() => import('./components/pages/LogsPage.vue'))
const RoutesPage = defineAsyncComponent(() => import('./components/pages/RoutesPage.vue'))
const SettingsPage = defineAsyncComponent(() => import('./components/pages/SettingsPage.vue'))

const { t, locale } = useI18n()

const pages = computed(() => [
  { key: 'overview', title: t('app.sidebar.overview'), subtitle: t('app.sidebar.overviewSubtitle'), icon: 'bi-grid-1x2-fill' },
  { key: 'forwards', title: t('app.sidebar.forwards'), subtitle: t('app.sidebar.forwardsSubtitle'), icon: 'bi-diagram-3' },
  { key: 'hosts', title: t('app.sidebar.hosts'), subtitle: t('app.sidebar.hostsSubtitle'), icon: 'bi-hdd-network' },
  { key: 'routes', title: t('app.sidebar.routes'), subtitle: t('app.sidebar.routesSubtitle'), icon: 'bi-globe2' },
  { key: 'logs', title: t('app.sidebar.logs'), subtitle: t('app.sidebar.logsSubtitle'), icon: 'bi-journal-text' },
  { key: 'settings', title: t('app.sidebar.settings'), subtitle: t('app.sidebar.settingsSubtitle'), icon: 'bi-sliders2' }
])

const savedTheme = typeof window !== 'undefined' ? window.localStorage.getItem('tunnelboard.theme') : null
const savedSidebarCollapsed = typeof window !== 'undefined' ? window.localStorage.getItem('tunnelboard.sidebar.collapsed') : null
const theme = ref(savedTheme === 'dark' ? 'dark' : 'light')
const sidebarCollapsed = ref(savedSidebarCollapsed === '1')
const activePage = ref('overview')

const appMeta = reactive({
  version: '0.0.0'
})
const releasePageUrl = ref(DEFAULT_RELEASES_PAGE_URL)
const hasNewVersion = ref(false)
const updateDetailsVisible = ref(false)
const updateNotice = createUpdateNoticeStore()
const updatePreference = createUpdatePreferenceStore()
const application = createApplicationClient()

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
const routeStatuses = ref([])
const vaultRevision = ref('')
const snapshotStore = createAppSnapshotStore()
const snapshotPhase = ref('loading')
const snapshotError = ref('')
const hasSnapshot = ref(false)

async function loadVault() {
  await snapshotStore.refresh(async () => {
    const raw = await application.getSnapshot()
    const catalog = raw?.catalog || raw?.Catalog || raw
    return {
      ...catalog,
      vaultRevision: raw?.revisions?.vault || raw?.Revisions?.Vault || raw?.vaultRevision || 0,
      eventSequence: raw?.eventSequence || raw?.EventSequence || 0,
      routeStatuses: Array.isArray(raw?.routes) ? raw.routes : Array.isArray(raw?.Routes) ? raw.Routes : raw?.routeStatuses || []
    }
  })
  snapshotPhase.value = snapshotStore.state.phase
  snapshotError.value = snapshotStore.state.error
  const data = snapshotStore.state.snapshot
  hasSnapshot.value = data !== null
  if (data) {
    folders.value = Array.isArray(data?.folders) ? data.folders : []
    sshHosts.value = Array.isArray(data?.sshHosts) ? data.sshHosts : []
    forwards.value = Array.isArray(data?.forwards) ? data.forwards : []
    webRoutes.value = Array.isArray(data?.webRoutes) ? data.webRoutes : []
    routeStatuses.value = Array.isArray(data?.routeStatuses) ? data.routeStatuses : []
    vaultRevision.value = String(data?.vaultRevision || '')
  }
}

async function onVaultChanged(result) {
  if (result?.acceptedRevision || result?.eventSequence) {
    snapshotStore.acceptRevision(result?.acceptedRevision, result?.eventSequence)
  }
  await loadVault()
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

function onUpdateOutcome(outcome) {
  updateNotice.accept(outcome)
  if (updateNotice.state.status === 'available') {
    hasNewVersion.value = true
    releasePageUrl.value = officialReleaseUrl(updateNotice.state.releasePageUrl)
  }
}

async function checkForUpdatesSilently() {
  try {
    const result = await callBackend(CheckForUpdatesAPI, appMeta.version)
    if (result?.hasUpdate) {
      hasNewVersion.value = true
      releasePageUrl.value = officialReleaseUrl(result.releasePageUrl)
      updateNotice.accept({ status: 'available', latestVersion: result.latestVersion, releaseNotes: result.releaseNotes, releasePageUrl: releasePageUrl.value })
    }
  } catch (error) {
    updateNotice.accept({ status: 'failed', message: String(error) })
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
  await updatePreference.load(() => callBackend(GetUpdateCheckEnabled))
  if (updatePreference.shouldAutoCheck()) {
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
      @open-update-details="updateDetailsVisible = true"
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
        <div v-if="snapshotPhase === 'loading'" class="alert alert-info" role="status">{{ $t('app.snapshot.loading') }}</div>
        <div v-else-if="snapshotPhase === 'error'" class="alert alert-danger" role="alert">
          {{ $t('app.snapshot.loadFailed') }}：{{ snapshotError }}
          <button type="button" class="btn btn-sm btn-outline-danger ms-2" @click="loadVault">{{ $t('app.snapshot.retry') }}</button>
        </div>
        <div v-else-if="snapshotPhase === 'stale'" class="alert alert-warning" role="alert">
          {{ $t('app.snapshot.stale') }}
          <button type="button" class="btn btn-sm btn-outline-warning ms-2" @click="loadVault">{{ $t('app.snapshot.retry') }}</button>
        </div>
        <OverviewPage
          v-if="activePage === 'overview' && snapshotPhase !== 'loading' && snapshotPhase !== 'error'"
          :folders="folders"
          :ssh-hosts="sshHosts"
          :forwards="forwards"
          :web-routes="webRoutes"
          :route-statuses="routeStatuses"
          :vault-revision="vaultRevision"
          @notify="notify"
          @go-forwards="switchPage('forwards')"
        />

        <ForwardsPage
          v-if="activePage === 'forwards' && hasSnapshot"
          ref="forwardsPageRef"
          :folders="folders"
          :ssh-hosts="sshHosts"
          :forwards="forwards"
          :configuration-locked="snapshotPhase !== 'ready'"
          :vault-revision="vaultRevision"
          @vault-changed="onVaultChanged"
          @notify="notify"
        />

        <HostsPage
          v-if="activePage === 'hosts' && hasSnapshot"
          ref="hostsPageRef"
          :ssh-hosts="sshHosts"
          :forwards="forwards"
          :configuration-locked="snapshotPhase !== 'ready'"
          :vault-revision="vaultRevision"
          @vault-changed="onVaultChanged"
          @notify="notify"
        />

        <RoutesPage
          v-if="activePage === 'routes' && hasSnapshot"
          ref="routesPageRef"
          :forwards="forwards"
          :web-routes="webRoutes"
          :route-statuses="routeStatuses"
          :vault-revision="vaultRevision"
          :configuration-locked="snapshotPhase !== 'ready'"
          @vault-changed="onVaultChanged"
          @notify="notify"
        />

        <LogsPage v-if="activePage === 'logs'" />

        <SettingsPage
          v-if="activePage === 'settings'"
          :theme="theme"
          :app-meta="appMeta"
          :configuration-locked="snapshotPhase !== 'ready'"
          @theme-change="setThemeBySwitch"
          @vault-changed="loadVault"
          @notify="notify"
          @update-outcome="onUpdateOutcome"
        />
      </main>

      <BaseDialog :visible="updateDetailsVisible" :title="$t('app.update.available')" @close="updateDetailsVisible = false">
        <p>{{ $t('app.update.latestVersion', { version: updateNotice.state.latestVersion }) }}</p>
        <p v-if="updateNotice.state.releaseNotes" class="update-release-notes-content">{{ updateNotice.state.releaseNotes }}</p>
        <template #footer>
          <button type="button" class="btn btn-outline-secondary" data-dialog-initial-focus @click="updateDetailsVisible = false">{{ $t('app.common.close') }}</button>
          <button type="button" class="btn btn-primary" @click="openReleasePage">{{ $t('app.update.openRelease') }}</button>
        </template>
      </BaseDialog>

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
