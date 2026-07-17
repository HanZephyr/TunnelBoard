<script setup>
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ApplyTrayLocale,
  CheckForUpdates as CheckForUpdatesAPI,
  GetAutoRunEnabled,
  GetConfigPath,
  GetUpdateCheckEnabled,
  OpenConfigDir,
  SaveUILocale,
  SetAutoRunEnabled,
  SetUpdateCheckEnabled
} from '../../../wailsjs/go/main/App'
import { BrowserOpenURL } from '../../../wailsjs/runtime/runtime'
import { callBackend, errorMessage } from '../../utils/backend'

const props = defineProps({
  theme: {
    type: String,
    required: true
  },
  appMeta: {
    type: Object,
    required: true
  }
})

const emit = defineEmits(['theme-change', 'notify'])

const { t, locale } = useI18n()

const DEFAULT_RELEASES_PAGE_URL = 'https://github.com/HanZephyr/TunnelBoard/releases'
const releasePageUrl = ref(DEFAULT_RELEASES_PAGE_URL)
const autoRunEnabled = ref(false)
const updateCheckEnabled = ref(true)
const configPath = ref('')
const isCheckingUpdates = ref(false)
const updateCheckDialog = reactive({
  visible: false,
  mode: 'idle',
  latestVersion: '',
  releaseNotes: '',
  message: ''
})

onMounted(async () => {
  try {
    autoRunEnabled.value = await callBackend(GetAutoRunEnabled)
  } catch (_) {
    autoRunEnabled.value = false
  }
  try {
    updateCheckEnabled.value = await callBackend(GetUpdateCheckEnabled)
  } catch (_) {
    updateCheckEnabled.value = true
  }
  try {
    configPath.value = await callBackend(GetConfigPath)
  } catch (_) {
    configPath.value = ''
  }
})

async function onUpdateCheckChange(event) {
  const previous = updateCheckEnabled.value
  const enabled = !!event.target.checked
  updateCheckEnabled.value = enabled
  try {
    await callBackend(SetUpdateCheckEnabled, enabled)
  } catch (err) {
    updateCheckEnabled.value = previous
    emit('notify', errorMessage(err))
  }
}

async function onAutoRunChange(event) {
  const previous = autoRunEnabled.value
  const enabled = !!event.target.checked
  autoRunEnabled.value = enabled
  try {
    await callBackend(SetAutoRunEnabled, enabled)
  } catch (err) {
    autoRunEnabled.value = previous
    emit('notify', errorMessage(err))
  }
}

async function onLocaleChange(event) {
  const newLocale = event.target.value
  locale.value = newLocale
  if (typeof window !== 'undefined') {
    window.localStorage.setItem('tunnelboard.locale', newLocale)
  }
  try {
    await callBackend(ApplyTrayLocale, newLocale)
  } catch (_) {
    /* tray locale sync is best-effort */
  }
  try {
    await callBackend(SaveUILocale, newLocale)
  } catch (_) {
    /* locale persist is best-effort */
  }
}

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

async function checkForUpdates() {
  if (isCheckingUpdates.value) return
  isCheckingUpdates.value = true
  try {
    const result = await callBackend(CheckForUpdatesAPI, props.appMeta.version)

    if (!result?.hasUpdate) {
      releasePageUrl.value = DEFAULT_RELEASES_PAGE_URL
      updateCheckDialog.mode = 'upToDate'
      updateCheckDialog.latestVersion = ''
      updateCheckDialog.releaseNotes = ''
      updateCheckDialog.message = ''
      updateCheckDialog.visible = true
      return
    }

    releasePageUrl.value = String(result.releasePageUrl || DEFAULT_RELEASES_PAGE_URL).trim() || DEFAULT_RELEASES_PAGE_URL
    updateCheckDialog.mode = 'updateAvailable'
    updateCheckDialog.latestVersion = String(result.latestVersion || '').trim()
    updateCheckDialog.releaseNotes = String(result.releaseNotes || '').trim()
    updateCheckDialog.message = ''
    updateCheckDialog.visible = true
  } catch (err) {
    releasePageUrl.value = DEFAULT_RELEASES_PAGE_URL
    updateCheckDialog.mode = 'error'
    updateCheckDialog.latestVersion = ''
    updateCheckDialog.releaseNotes = ''
    updateCheckDialog.message = errorMessage(err, 'Failed to check updates from GitHub Releases API.')
    updateCheckDialog.visible = true
  } finally {
    isCheckingUpdates.value = false
  }
}

function closeUpdateCheckDialog() {
  updateCheckDialog.visible = false
}

async function onOpenConfigDir() {
  try {
    await callBackend(OpenConfigDir)
  } catch (err) {
    emit('notify', errorMessage(err))
  }
}
</script>

<template>
  <section class="page-fade">
    <div class="row g-3">
      <div class="col-12 col-xl-6">
        <div class="panel-card config-card">
          <div class="panel-head mb-2">
            <h2 class="panel-title mb-0">{{ t('settings.general') }}</h2>
          </div>

          <div class="config-row">
            <div>
              <div class="config-name">{{ t('settings.language') }}</div>
              <div class="config-desc">{{ t('settings.languageDesc') }}</div>
            </div>
            <div>
              <select class="form-select form-select-sm" :value="locale" @change="onLocaleChange">
                <option value="en">English</option>
                <option value="zh-CN">简体中文</option>
                <option value="zh-TW">繁體中文（台灣）</option>
                <option value="zh-HK">繁體中文（香港）</option>
                <option value="ru">Русский</option>
              </select>
            </div>
          </div>

          <div class="config-row">
            <div>
              <div class="config-name">{{ t('settings.theme') }}</div>
              <div class="config-desc">{{ t('settings.themeDesc') }}</div>
            </div>
            <div class="form-check form-switch m-0">
              <input
                id="themeSwitch"
                class="form-check-input"
                type="checkbox"
                :checked="theme === 'dark'"
                @change="$emit('theme-change', $event.target.checked)"
              />
            </div>
          </div>

          <div class="config-row">
            <div>
              <div class="config-name">{{ t('settings.autoRun') }}</div>
              <div class="config-desc">{{ t('settings.autoRunDesc') }}</div>
            </div>
            <div class="form-check form-switch m-0">
              <input
                id="autoRunSwitch"
                class="form-check-input"
                type="checkbox"
                :checked="autoRunEnabled"
                @change="onAutoRunChange"
              />
            </div>
          </div>
        </div>
      </div>

      <div class="col-12 col-xl-6">
        <div class="panel-card config-card mb-3">
          <div class="panel-head mb-2">
            <h2 class="panel-title mb-0">{{ t('settings.updates') }}</h2>
          </div>

          <div class="config-row align-items-center">
            <div>
              <div class="config-name">{{ t('settings.automaticUpdateChecks') }}</div>
              <div class="config-desc">{{ t('settings.automaticUpdateChecksDesc') }}</div>
            </div>
            <div class="form-check form-switch m-0">
              <input
                id="updateCheckSwitch"
                class="form-check-input"
                type="checkbox"
                role="switch"
                :checked="updateCheckEnabled"
                @change="onUpdateCheckChange"
              />
            </div>
          </div>

          <div class="config-row align-items-center">
            <div>
              <div class="config-name">{{ t('settings.currentVersion') }}</div>
              <div class="config-desc">{{ appMeta.version }}</div>
            </div>
            <button
              type="button"
              class="btn btn-sm btn-secondary position-relative check-updates-btn"
              :disabled="isCheckingUpdates"
              @click="checkForUpdates"
            >
              <span :class="{ invisible: isCheckingUpdates }">{{ t('settings.checkUpdates') }}</span>
              <span v-if="isCheckingUpdates" class="position-absolute top-50 start-50 translate-middle text-nowrap small">
                {{ t('settings.checkingUpdates') }}
              </span>
            </button>
          </div>
        </div>

        <div class="panel-card config-card">
          <div class="panel-head mb-2">
            <h2 class="panel-title mb-0">{{ t('settings.data') }}</h2>
          </div>

          <div class="config-row align-items-center">
            <div class="flex-grow-1 min-w-0 pe-2">
              <div class="config-name">{{ t('settings.dataDir') }}</div>
              <div class="config-desc">
                <span v-if="configPath" class="text-break d-block">{{ configPath }}</span>
                <template v-else>{{ t('settings.dataDirUnavailable') }}</template>
              </div>
              <div class="config-desc">{{ t('settings.dataDirDesc') }}</div>
            </div>
            <button
              type="button"
              class="btn btn-sm btn-secondary flex-shrink-0"
              :disabled="!configPath"
              @click="onOpenConfigDir"
            >
              {{ t('settings.openDir') }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </section>

  <div
    v-if="updateCheckDialog.visible"
    class="modal fade show"
    style="display: block"
    tabindex="-1"
    aria-modal="true"
    role="dialog"
  >
    <div class="modal-dialog modal-dialog-centered">
      <div class="modal-content compact-dialog update-check-dialog-content">
        <div class="modal-header dialog-head">
          <h3 class="modal-title dialog-title">{{ t('settings.updateResultTitle') }}</h3>
          <button
            type="button"
            class="btn-close"
            :aria-label="t('app.common.close')"
            @click="closeUpdateCheckDialog"
          />
        </div>
        <div class="modal-body dialog-body">
          <p v-if="updateCheckDialog.mode === 'upToDate'" class="mb-0 update-check-dialog-text">
            {{ t('settings.noUpdatesAvailable') }}
          </p>
          <template v-else-if="updateCheckDialog.mode === 'updateAvailable'">
            <p class="mb-0 update-check-dialog-text">
              {{ t('settings.latestVersionIs', { version: updateCheckDialog.latestVersion }) }}
            </p>
            <div v-if="updateCheckDialog.releaseNotes" class="update-release-notes mt-3">
              <div class="config-name mb-1">{{ t('settings.releaseNotes') }}</div>
              <div class="config-desc update-release-notes-content">{{ updateCheckDialog.releaseNotes }}</div>
            </div>
          </template>
          <p v-else class="mb-0 update-check-dialog-text">{{ updateCheckDialog.message }}</p>
        </div>
        <div class="modal-footer dialog-actions">
          <div class="dialog-right-actions">
            <button
              v-if="updateCheckDialog.mode === 'updateAvailable'"
              type="button"
              class="btn btn-primary"
              @click="openReleasePage(); closeUpdateCheckDialog()"
            >
              {{ t('settings.openDownloadPage') }}
            </button>
            <button
              type="button"
              class="btn btn-outline-secondary"
              @click="closeUpdateCheckDialog"
            >
              {{ t('app.common.close') }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
  <div v-if="updateCheckDialog.visible" class="modal-backdrop fade show" />
</template>
