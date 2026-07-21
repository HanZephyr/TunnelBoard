<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  ApplyTrayLocale,
  ExportBackupWithDialog,
  ExportDiagnosticsWithDialog,
  GetAutoRunEnabled,
  GetConfigPath,
  GetUpdateCheckEnabled,
  OpenConfigDir,
  RestoreBackup,
  StageImportCommand,
  CommitImportCommand,
  SaveImportKeyFileCommand,
  SaveUILocale,
  SelectBackupFile,
  SetAutoRunEnabled,
  SetUpdateCheckEnabled
} from '../../../wailsjs/go/main/App'
import { BrowserOpenURL } from '../../../wailsjs/runtime/runtime'
import { callBackend, errorMessage } from '../../utils/backend'
import { ensureLocaleMessages } from '../../i18n'
import BaseDialog from '../common/BaseDialog.vue'
import { DEFAULT_RELEASES_PAGE_URL, officialReleaseUrl } from '../../modules/releaseUrl'
import { createApplicationClient } from '../../utils/applicationClient'

const props = defineProps({
  theme: {
    type: String,
    required: true
  },
  appMeta: {
    type: Object,
    required: true
  },
  configurationLocked: { type: Boolean, default: false }
})

const emit = defineEmits(['theme-change', 'notify', 'vault-changed', 'update-outcome'])

const i18n = useI18n()
const { t, locale } = i18n
const application = createApplicationClient()

const releasePageUrl = ref(DEFAULT_RELEASES_PAGE_URL)
const autoRunEnabled = ref(false)
const updateCheckEnabled = ref(false)
const updatePreferencePhase = ref('loading')
const updatePreferenceError = ref('')
const configPath = ref('')
const isCheckingUpdates = ref(false)
const updateCheckDialog = reactive({
  visible: false,
  mode: 'idle',
  latestVersion: '',
  releaseNotes: '',
  message: ''
})

async function loadUpdatePreference() {
  updatePreferencePhase.value = 'loading'
  updatePreferenceError.value = ''
  updateCheckEnabled.value = false
  try {
    updateCheckEnabled.value = (await callBackend(GetUpdateCheckEnabled)) === true
    updatePreferencePhase.value = 'ready'
  } catch (error) {
    updateCheckEnabled.value = false
    updatePreferencePhase.value = 'error'
    updatePreferenceError.value = errorMessage(error)
  }
}

onMounted(async () => {
  try {
    autoRunEnabled.value = await callBackend(GetAutoRunEnabled)
  } catch (_) {
    autoRunEnabled.value = false
  }
  await loadUpdatePreference()
  try {
    configPath.value = await callBackend(GetConfigPath)
  } catch (_) {
    configPath.value = ''
  }
})

async function onUpdateCheckChange(event) {
  if (props.configurationLocked) return
  if (updatePreferencePhase.value !== 'ready') return
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
  if (props.configurationLocked) return
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
  if (props.configurationLocked) return
  const newLocale = event.target.value
  try {
    await ensureLocaleMessages(i18n, newLocale)
  } catch (error) {
    event.target.value = locale.value
    emit('notify', errorMessage(error))
    return
  }
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
	const result = await application.checkForUpdates('manual')
	if (result?.skipped) {
		throw new Error(t('settings.updateCheckFailed'))
	}

    if (!result?.hasUpdate) {
      releasePageUrl.value = DEFAULT_RELEASES_PAGE_URL
      updateCheckDialog.mode = 'upToDate'
      updateCheckDialog.latestVersion = ''
      updateCheckDialog.releaseNotes = ''
      updateCheckDialog.message = ''
      updateCheckDialog.visible = true
      emit('update-outcome', { status: 'up_to_date' })
      return
    }

    releasePageUrl.value = officialReleaseUrl(result.releasePageUrl)
    updateCheckDialog.mode = 'updateAvailable'
    updateCheckDialog.latestVersion = String(result.latestVersion || '').trim()
    updateCheckDialog.releaseNotes = String(result.releaseNotes || '').trim()
    updateCheckDialog.message = ''
    updateCheckDialog.visible = true
    emit('update-outcome', { status: 'available', latestVersion: updateCheckDialog.latestVersion, releaseNotes: updateCheckDialog.releaseNotes, releasePageUrl: releasePageUrl.value })
  } catch (err) {
    releasePageUrl.value = DEFAULT_RELEASES_PAGE_URL
    updateCheckDialog.mode = 'error'
    updateCheckDialog.latestVersion = ''
    updateCheckDialog.releaseNotes = ''
    updateCheckDialog.message = errorMessage(err, t('settings.updateCheckFailed'))
    updateCheckDialog.visible = true
    emit('update-outcome', { status: 'failed', message: updateCheckDialog.message })
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

async function onExportDiagnostics() {
  try {
    await callBackend(ExportDiagnosticsWithDialog)
  } catch (err) {
    emit('notify', errorMessage(err))
  }
}

// ---- 备份与恢复 ----
const exportForm = reactive({
  password: '',
  confirmPassword: '',
  includeKeyFiles: false
})
const isExporting = ref(false)
const exportWarnings = ref([])

const importState = reactive({
  srcPath: '',
  password: '',
  token: '',
  preview: null,
  folderName: '',
  resolutions: [],
  keyFiles: []
})
const isPreviewing = ref(false)
const isImporting = ref(false)
const importSummary = ref(null)

const restoreState = reactive({
  srcPath: '',
  password: '',
  confirmed: false
})
const isRestoring = ref(false)

const previewCounts = computed(() => {
  const counts = importState.preview?.counts || {}
  return [
    { key: 'folders', label: t('settings.backup.countFolders'), value: counts.folders || 0 },
    { key: 'sshHosts', label: t('settings.backup.countSshHosts'), value: counts.sshHosts || 0 },
    { key: 'forwards', label: t('settings.backup.countForwards'), value: counts.forwards || 0 },
    { key: 'webRoutes', label: t('settings.backup.countWebRoutes'), value: counts.webRoutes || 0 },
    { key: 'hostKeys', label: t('settings.backup.countHostKeys'), value: counts.hostKeys || 0 }
  ]
})

const previewConflicts = computed(() => {
  const conflicts = importState.preview?.hostConflicts
  return Array.isArray(conflicts) ? conflicts : []
})

const previewKeyFiles = computed(() => {
  const keyFiles = importState.preview?.keyFiles
  return Array.isArray(keyFiles) ? keyFiles : []
})

const summaryKeyFiles = computed(() => importState.keyFiles)

const canRestore = computed(
  () => !!(restoreState.srcPath && restoreState.password && restoreState.confirmed)
)

async function onExportBackup() {
  if (isExporting.value || !exportForm.password) return
  if (exportForm.password !== exportForm.confirmPassword) {
    emit('notify', t('settings.backup.passwordMismatch'))
    return
  }
  isExporting.value = true
  try {
    const warnings = await callBackend(ExportBackupWithDialog, exportForm.password, exportForm.includeKeyFiles)
    // 空数组：用户取消保存对话框或纯成功，按约定静默；非空则展示风险提示。
    exportWarnings.value = Array.isArray(warnings) ? warnings : []
    exportForm.password = ''
    exportForm.confirmPassword = ''
  } catch (err) {
    emit('notify', errorMessage(err))
  } finally {
    isExporting.value = false
  }
}

async function onSelectImportFile() {
  try {
    const selected = await callBackend(SelectBackupFile)
    const srcPath = String(selected || '').trim()
    if (!srcPath) return // 用户取消：静默
    importState.srcPath = srcPath
    importState.preview = null
    importState.token = ''
    importState.password = ''
    importState.keyFiles = []
    importState.resolutions = []
    importSummary.value = null
  } catch (err) {
    emit('notify', errorMessage(err))
  }
}

async function onPreviewImport() {
  if (isPreviewing.value || !importState.srcPath || !importState.password) return
  isPreviewing.value = true
  try {
    const staged = await callBackend(StageImportCommand, {
      path: importState.srcPath,
      password: importState.password
    })
    const preview = staged?.preview || null
    importState.token = String(staged?.token || '')
    importState.preview = preview
    importState.folderName = String(preview?.folderName || '')
    importState.keyFiles = Array.isArray(preview?.keyFiles) ? preview.keyFiles : []
    const conflicts = Array.isArray(preview?.hostConflicts) ? preview.hostConflicts : []
    importState.resolutions = conflicts.map(() => 'rename')
    importSummary.value = null
    importState.password = ''
  } catch (err) {
    importState.preview = null
    emit('notify', errorMessage(err))
  } finally {
    isPreviewing.value = false
  }
}

async function onApplyImport() {
  if (props.configurationLocked) return
  if (isImporting.value || !importState.preview) return
  const folderName = importState.folderName.trim()
  if (!folderName) return
  isImporting.value = true
  try {
    const plan = {
      folderName,
      hostResolutions: previewConflicts.value.map((conflict, index) => ({
        host: conflict?.imported?.host || '',
        port: Number(conflict?.imported?.port || 0),
        user: conflict?.imported?.user || '',
        action: importState.resolutions[index] === 'skip' ? 'skip' : 'rename'
      }))
    }
    const result = await callBackend(CommitImportCommand, { meta: {}, token: importState.token, plan })
    const summary = result?.summary || {}
    importSummary.value = summary
    importState.keyFiles = Array.isArray(result?.keyFiles) ? result.keyFiles : []
    const imported = summary?.imported || {}
    let message = t('settings.backup.importResult', {
      folders: imported.folders || 0,
      sshHosts: imported.sshHosts || 0,
      forwards: imported.forwards || 0,
      webRoutes: imported.webRoutes || 0,
      hostKeys: imported.hostKeys || 0,
      skipped: summary?.skippedHosts || 0
    })
    if ((summary?.routesDeactivated || 0) > 0) {
      message += ' ' + t('settings.backup.routesDeactivatedNote')
    }
    emit('notify', message)
    // 防止重复点击造成重复导入；私钥另存只保留后端 lease token，不保留密码。
    importState.preview = null
    emit('vault-changed')
  } catch (err) {
    emit('notify', errorMessage(err))
  } finally {
    isImporting.value = false
  }
}

async function onSaveImportKeyFile(keyFile) {
  if (props.configurationLocked) return
  if (!keyFile?.id || !importState.token) return
  try {
    // 保存对话框的关闭即反馈；用户取消时后端同样返回成功，故此处静默。
    await callBackend(SaveImportKeyFileCommand, {
      token: importState.token,
      keyId: keyFile.id,
      suggestedName: keyFile.name
    })
    importState.keyFiles = importState.keyFiles.filter((item) => item.id !== keyFile.id)
  } catch (err) {
    emit('notify', errorMessage(err))
  }
}

async function onSelectRestoreFile() {
  try {
    const selected = await callBackend(SelectBackupFile)
    const srcPath = String(selected || '').trim()
    if (!srcPath) return // 用户取消：静默
    restoreState.srcPath = srcPath
  } catch (err) {
    emit('notify', errorMessage(err))
  }
}

async function onRestoreBackup() {
  if (props.configurationLocked) return
  if (isRestoring.value || !canRestore.value) return
  isRestoring.value = true
  try {
    await callBackend(RestoreBackup, restoreState.srcPath, restoreState.password, restoreState.confirmed)
    emit('notify', t('settings.backup.restoreSuccess'))
    restoreState.srcPath = ''
    restoreState.password = ''
    restoreState.confirmed = false
    // Vault 已整体替换，导入区状态一并失效。
    importState.srcPath = ''
    importState.password = ''
    importState.preview = null
    importState.resolutions = []
    importSummary.value = null
    emit('vault-changed')
  } catch (err) {
    emit('notify', errorMessage(err))
  } finally {
    isRestoring.value = false
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
              <select class="form-select form-select-sm" :value="locale" :disabled="configurationLocked" @change="onLocaleChange">
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
                :disabled="configurationLocked"
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
              <div id="updatePreferenceDescription" class="config-desc">
                <template v-if="updatePreferencePhase === 'loading'">{{ t('settings.updatePreferenceLoading') }}</template>
                <template v-else-if="updatePreferencePhase === 'error'">{{ t('settings.updatePreferenceLoadFailed') }}</template>
                <template v-else>{{ updateCheckEnabled ? t('settings.automaticUpdateChecksEnabled') : t('settings.automaticUpdateChecksDisabled') }}</template>
              </div>
              <div v-if="updatePreferencePhase === 'error'" class="config-desc text-danger mt-1" role="alert">
                <span>{{ updatePreferenceError }}</span>
                <button type="button" class="btn btn-sm btn-outline-danger ms-2" @click="loadUpdatePreference">{{ t('settings.updatePreferenceRetry') }}</button>
              </div>
            </div>
            <div class="form-check form-switch m-0">
              <input
                id="updateCheckSwitch"
                class="form-check-input"
                type="checkbox"
                role="switch"
                aria-describedby="updatePreferenceDescription"
                :checked="updateCheckEnabled"
                :disabled="configurationLocked || updatePreferencePhase !== 'ready'"
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

          <div class="config-row align-items-center">
            <div class="flex-grow-1 min-w-0 pe-2">
              <div class="config-name">{{ t('settings.diagnostics') }}</div>
              <div class="config-desc">{{ t('settings.diagnosticsDesc') }}</div>
            </div>
            <button
              type="button"
              class="btn btn-sm btn-secondary flex-shrink-0"
              @click="onExportDiagnostics"
            >
              {{ t('settings.exportDiagnostics') }}
            </button>
          </div>
        </div>
      </div>

      <div class="col-12">
        <div class="panel-card">
          <div class="panel-head mb-2">
            <h2 class="panel-title mb-0">{{ t('settings.backup.title') }}</h2>
          </div>

          <div class="mb-4">
            <div class="config-name">{{ t('settings.backup.exportTitle') }}</div>
            <div class="config-desc mb-2">{{ t('settings.backup.exportDesc') }}</div>
            <div class="row g-2 align-items-end">
              <div class="col-12 col-md-4">
                <label class="form-label small mb-1" for="backupExportPassword">{{ t('settings.backup.password') }}</label>
                <input
                  id="backupExportPassword"
                  v-model="exportForm.password"
                  type="password"
                  class="form-control form-control-sm"
                  autocomplete="new-password"
                />
              </div>
              <div class="col-12 col-md-4">
                <label class="form-label small mb-1" for="backupExportPasswordConfirm">{{ t('settings.backup.confirmPassword') }}</label>
                <input
                  id="backupExportPasswordConfirm"
                  v-model="exportForm.confirmPassword"
                  type="password"
                  class="form-control form-control-sm"
                  autocomplete="new-password"
                />
              </div>
              <div class="col-12 col-md-4">
                <button
                  type="button"
                  class="btn btn-sm btn-primary"
                  :disabled="!exportForm.password || isExporting"
                  @click="onExportBackup"
                >
                  {{ isExporting ? t('settings.backup.exporting') : t('settings.backup.exportButton') }}
                </button>
              </div>
            </div>
            <div class="form-check mt-2">
              <input
                id="backupIncludeKeyFiles"
                v-model="exportForm.includeKeyFiles"
                class="form-check-input"
                type="checkbox"
              />
              <label class="form-check-label" for="backupIncludeKeyFiles">
                {{ t('settings.backup.includeKeyFiles') }}
              </label>
              <div class="config-desc">{{ t('settings.backup.includeKeyFilesRisk') }}</div>
            </div>
            <details v-if="exportWarnings.length" class="mt-2">
              <summary class="config-name">{{ t('settings.backup.exportWarnings') }} ({{ exportWarnings.length }})</summary>
              <ul class="config-desc mt-1 mb-0">
                <li v-for="(warning, index) in exportWarnings" :key="index">{{ warning }}</li>
              </ul>
            </details>
          </div>

          <div class="mb-4">
            <div class="config-name">{{ t('settings.backup.importTitle') }}</div>
            <div class="config-desc mb-2">{{ t('settings.backup.importDesc') }}</div>
            <div class="d-flex flex-wrap align-items-center gap-2 mb-2">
              <button type="button" class="btn btn-sm btn-secondary flex-shrink-0" @click="onSelectImportFile">
                {{ t('settings.backup.selectFile') }}
              </button>
              <span class="config-desc text-break mb-0">{{ importState.srcPath || t('settings.backup.noFileSelected') }}</span>
            </div>
            <div v-if="importState.srcPath" class="row g-2 align-items-end">
              <div class="col-12 col-md-4">
                <label class="form-label small mb-1" for="backupImportPassword">{{ t('settings.backup.password') }}</label>
                <input
                  id="backupImportPassword"
                  v-model="importState.password"
                  type="password"
                  class="form-control form-control-sm"
                  autocomplete="off"
                />
              </div>
              <div class="col-12 col-md-4">
                <button
                  type="button"
                  class="btn btn-sm btn-secondary"
                  :disabled="!importState.password || isPreviewing"
                  @click="onPreviewImport"
                >
                  {{ isPreviewing ? t('settings.backup.previewing') : t('settings.backup.previewButton') }}
                </button>
              </div>
            </div>

            <div v-if="importState.preview" class="mt-3">
              <div class="config-name mb-1">{{ t('settings.backup.countsTitle') }}</div>
              <div class="d-flex flex-wrap gap-2 mb-2">
                <span v-for="item in previewCounts" :key="item.key" class="badge text-bg-secondary">
                  {{ item.label }}: {{ item.value }}
                </span>
              </div>
              <div class="row g-2 align-items-end mb-2">
                <div class="col-12 col-md-4">
                  <label class="form-label small mb-1" for="backupImportFolderName">{{ t('settings.backup.folderName') }}</label>
                  <input
                    id="backupImportFolderName"
                    v-model="importState.folderName"
                    type="text"
                    class="form-control form-control-sm"
                  />
                </div>
              </div>

              <template v-if="previewConflicts.length">
                <div class="config-name mb-1">{{ t('settings.backup.conflictsTitle') }} ({{ previewConflicts.length }})</div>
                <div class="table-responsive mb-2">
                  <table class="table table-sm align-middle mb-0">
                    <thead>
                      <tr>
                        <th scope="col">{{ t('settings.backup.conflictHost') }}</th>
                        <th scope="col" style="width: 10rem">{{ t('settings.backup.conflictAction') }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="(conflict, index) in previewConflicts" :key="index">
                        <td>
                          <div>{{ conflict.imported?.name || conflict.imported?.host }}</div>
                          <div class="config-desc">{{ conflict.imported?.user }}@{{ conflict.imported?.host }}:{{ conflict.imported?.port }}</div>
                        </td>
                        <td>
                          <select v-model="importState.resolutions[index]" class="form-select form-select-sm">
                            <option value="rename">{{ t('settings.backup.actionRename') }}</option>
                            <option value="skip">{{ t('settings.backup.actionSkip') }}</option>
                          </select>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </template>

              <template v-if="previewKeyFiles.length">
                <div class="config-name mb-1">{{ t('settings.backup.keyFilesTitle') }} ({{ previewKeyFiles.length }})</div>
                <ul class="config-desc mt-1 mb-2">
                  <li v-for="keyFile in previewKeyFiles" :key="keyFile.id">{{ keyFile.name }} · {{ keyFile.size }} B</li>
                </ul>
              </template>

              <button
                type="button"
                class="btn btn-sm btn-primary"
                :disabled="configurationLocked || !importState.folderName.trim() || isImporting"
                @click="onApplyImport"
              >
                {{ isImporting ? t('settings.backup.importing') : t('settings.backup.importButton') }}
              </button>
            </div>

            <div v-if="summaryKeyFiles.length" class="mt-3">
              <div class="config-name mb-1">{{ t('settings.backup.keyFilesTitle') }} ({{ summaryKeyFiles.length }})</div>
              <div
                v-for="keyFile in summaryKeyFiles"
                :key="keyFile.id"
                class="d-flex flex-wrap align-items-center gap-2 mb-1"
              >
                <span class="config-desc text-break mb-0">{{ keyFile.name }} · {{ keyFile.size }} B</span>
                <button
                  type="button"
                  class="btn btn-sm btn-outline-secondary flex-shrink-0"
                  @click="onSaveImportKeyFile(keyFile)"
                >
                  {{ t('settings.backup.saveKeyFile') }}
                </button>
              </div>
            </div>
          </div>

          <div class="danger-zone">
            <div class="config-name">{{ t('settings.backup.restoreTitle') }} · {{ t('settings.backup.restoreDanger') }}</div>
            <div class="config-desc mb-2">{{ t('settings.backup.restoreDesc') }}</div>
            <div class="d-flex flex-wrap align-items-center gap-2 mb-2">
              <button type="button" class="btn btn-sm btn-outline-danger flex-shrink-0" @click="onSelectRestoreFile">
                {{ t('settings.backup.selectFile') }}
              </button>
              <span class="config-desc text-break mb-0">{{ restoreState.srcPath || t('settings.backup.noFileSelected') }}</span>
            </div>
            <div class="row g-2 align-items-end">
              <div class="col-12 col-md-4">
                <label class="form-label small mb-1" for="backupRestorePassword">{{ t('settings.backup.password') }}</label>
                <input
                  id="backupRestorePassword"
                  v-model="restoreState.password"
                  type="password"
                  class="form-control form-control-sm"
                  autocomplete="off"
                />
              </div>
            </div>
            <div class="form-check mt-2">
              <input
                id="backupRestoreConfirmed"
                v-model="restoreState.confirmed"
                class="form-check-input"
                type="checkbox"
              />
              <label class="form-check-label" for="backupRestoreConfirmed">
                {{ t('settings.backup.restoreConfirm') }}
              </label>
            </div>
            <button
              type="button"
              class="btn btn-sm btn-danger mt-2"
              :disabled="configurationLocked || !canRestore || isRestoring"
              @click="onRestoreBackup"
            >
              {{ isRestoring ? t('settings.backup.restoring') : t('settings.backup.restoreButton') }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </section>

  <BaseDialog :visible="updateCheckDialog.visible" :title="t('settings.updateResultTitle')" :busy="isCheckingUpdates" class="update-check-dialog-content" @close="closeUpdateCheckDialog">
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
    <template #footer>
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
    </template>
  </BaseDialog>
</template>
