<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CreateFolder,
  DeleteSelection,
  EnrollHostKey,
  GetRuntimeSnapshot,
  ReplaceHostKey,
  SaveForward,
  StartForward,
  StartManyForwards,
  StopForward
} from '../../../wailsjs/go/main/App'
import { callBackend, errorMessage, isValidPort } from '../../utils/backend'
import { formatLatency as formatLatencyUtil } from '../../utils/format'
import { createApplicationClient, createCommandMeta } from '../../utils/applicationClient'
import TooltipText from '../common/TooltipText.vue'
import IconActionButton from '../common/IconActionButton.vue'
import StatusChip from '../common/StatusChip.vue'
import ConfirmDialog from '../common/ConfirmDialog.vue'
import BaseDialog from '../common/BaseDialog.vue'
import ForwardModal from '../modals/ForwardModal.vue'
import HostKeyDialog from '../modals/HostKeyDialog.vue'

const props = defineProps({
  folders: {
    type: Array,
    default: () => []
  },
  sshHosts: {
    type: Array,
    default: () => []
  },
  sshHostDefaults: { type: Object, default: () => ({}) },
  forwards: {
    type: Array,
    default: () => []
  },
  configurationLocked: { type: Boolean, default: false },
  vaultRevision: { type: String, default: '' }
})

const emit = defineEmits(['vault-changed', 'notify'])

const { t } = useI18n()
const application = createApplicationClient()

// ---- 文件夹树（最多两层，parentId=0 为顶层）----
function bySortThenId(a, b) {
  return (a.sort - b.sort) || (a.id - b.id)
}

const topFolders = computed(() => props.folders.filter((folder) => !folder.parentId).sort(bySortThenId))

function childFolders(parentId) {
  return props.folders.filter((folder) => folder.parentId === parentId).sort(bySortThenId)
}

const forwardCountByFolder = computed(() => {
  const counts = new Map()
  for (const forward of props.forwards) {
    counts.set(forward.folderId, (counts.get(forward.folderId) || 0) + 1)
  }
  return counts
})

function folderForwardCount(folderId) {
  return forwardCountByFolder.value.get(folderId) || 0
}

// 扁平化选项（子文件夹带父级前缀），用于移动下拉与表单文件夹选择
const folderOptions = computed(() => {
  const options = []
  for (const top of topFolders.value) {
    options.push({ id: top.id, label: top.name })
    for (const child of childFolders(top.id)) {
      options.push({ id: child.id, label: `${top.name} / ${child.name}` })
    }
  }
  return options
})

const selectedFolderId = ref(0)

watch(
  () => props.folders,
  () => {
    if (props.folders.some((folder) => folder.id === selectedFolderId.value)) return
    selectedFolderId.value = topFolders.value[0]?.id || 0
  },
  { immediate: true }
)

const selectedFolder = computed(
  () => props.folders.find((folder) => folder.id === selectedFolderId.value) || null
)

const visibleForwards = computed(() =>
  props.forwards.filter((forward) => forward.folderId === selectedFolderId.value)
)

// ---- 表格多选 ----
const selectedForwardIds = ref(new Set())

watch(visibleForwards, () => {
  selectedForwardIds.value = new Set()
})

const allVisibleSelected = computed(
  () =>
    visibleForwards.value.length > 0 &&
    visibleForwards.value.every((forward) => selectedForwardIds.value.has(forward.id))
)

function toggleSelectAll() {
  if (allVisibleSelected.value) {
    selectedForwardIds.value = new Set()
    return
  }
  selectedForwardIds.value = new Set(visibleForwards.value.map((forward) => forward.id))
}

// clearSelection 清空批量选择（批量工具条右侧 × 按钮）。
function clearSelection() {
  selectedForwardIds.value = new Set()
}

function toggleSelectForward(forwardId) {
  const next = new Set(selectedForwardIds.value)
  if (next.has(forwardId)) {
    next.delete(forwardId)
  } else {
    next.add(forwardId)
  }
  selectedForwardIds.value = next
}

// ---- 通用确认框 ----
const confirmDialog = reactive({
  visible: false,
  title: '',
  message: '',
  confirmLabel: '',
  confirmClass: 'btn-danger',
  showCancel: true,
  busy: false,
  onConfirm: null
})

function openConfirm({
  title = '',
  message,
  confirmLabel = '',
  confirmClass = 'btn-danger',
  showCancel = true,
  onConfirm = null
}) {
  confirmDialog.title = title
  confirmDialog.message = message
  confirmDialog.confirmLabel = confirmLabel
  confirmDialog.confirmClass = confirmClass
  confirmDialog.showCancel = showCancel
  confirmDialog.onConfirm = onConfirm
  confirmDialog.busy = false
  confirmDialog.visible = true
}

function closeConfirm() {
  confirmDialog.visible = false
  confirmDialog.onConfirm = null
}

async function handleConfirm() {
  if (confirmDialog.busy) return
  const handler = confirmDialog.onConfirm
  if (typeof handler === 'function') {
    confirmDialog.busy = true
    try {
      await handler()
    } finally {
      confirmDialog.busy = false
      closeConfirm()
    }
  } else {
    closeConfirm()
  }
}

// ---- 文件夹新建 ----
const folderDialog = reactive({
  visible: false,
  parentId: 0,
  name: '',
  error: ''
})

function openFolderDialog(parentId) {
  if (props.configurationLocked) return
  folderDialog.parentId = parentId
  folderDialog.name = ''
  folderDialog.error = ''
  folderDialog.visible = true
}

async function saveFolder() {
  if (props.configurationLocked) return
  const name = folderDialog.name.trim()
  if (!name) {
    folderDialog.error = t('forwards.folderNameRequired')
    return
  }
  try {
    const created = await callBackend(CreateFolder, name, folderDialog.parentId)
    folderDialog.visible = false
    emit('vault-changed')
    if (created?.id) {
      selectedFolderId.value = created.id
    }
    emit('notify', t('forwards.notify.folderCreated', { name: created?.name || name }))
  } catch (err) {
    folderDialog.error = errorMessage(err)
  }
}

// ---- 文件夹删除：空文件夹直接删；非空捕获后端报错后二次确认级联 ----
async function deleteFolder(folder) {
  if (props.configurationLocked) return
  try {
    await callBackend(DeleteSelection, {
      folderIds: [folder.id],
      sshHostIds: [],
      forwardIds: [],
      cascadeFolders: false
    })
    emit('vault-changed')
    emit('notify', t('forwards.notify.folderDeleted', { name: folder.name }))
  } catch (err) {
    const message = errorMessage(err)
    if (!message.includes('folder is not empty')) {
      emit('notify', message)
      return
    }
    const cascadeFolderIds = new Set([folder.id, ...childFolders(folder.id).map((child) => child.id)])
    const subCount = cascadeFolderIds.size - 1
    const forwardCount = props.forwards.filter((forward) => cascadeFolderIds.has(forward.folderId)).length
    openConfirm({
      message: t('forwards.confirmations.deleteFolderNotEmpty', {
        name: folder.name,
        folders: subCount,
        forwards: forwardCount
      }),
      confirmLabel: t('forwards.confirmations.cascadeConfirm'),
      onConfirm: async () => {
        try {
          await callBackend(DeleteSelection, {
            folderIds: [folder.id],
            sshHostIds: [],
            forwardIds: [],
            cascadeFolders: true
          })
          emit('vault-changed')
          emit('notify', t('forwards.notify.folderDeleted', { name: folder.name }))
        } catch (retryErr) {
          emit('notify', errorMessage(retryErr))
        }
      }
    })
  }
}

// ---- Forward 新建 / 编辑 ----
const forwardModalOpen = ref(false)
const editingForwardId = ref(null)
const forwardValidationError = ref('')
const forwardForm = reactive(defaultForwardForm())

function defaultForwardForm() {
  return {
    name: '',
    mode: 'local',
    folderId: 0,
    chainHostIds: [],
    localHost: '127.0.0.1',
    localPort: '',
    remoteHost: '',
    remotePort: '',
    autoStart: false,
    description: ''
  }
}

function openNewForward() {
  if (props.configurationLocked) return
  if (!props.folders.length) {
    emit('notify', t('forwards.notify.needFolder'))
    return
  }
  editingForwardId.value = null
  Object.assign(forwardForm, defaultForwardForm(), {
    folderId: selectedFolderId.value || topFolders.value[0]?.id || 0
  })
  forwardValidationError.value = ''
  forwardModalOpen.value = true
}

defineExpose({ openNewForward })

function editForward(forward) {
  if (props.configurationLocked) return
  editingForwardId.value = forward.id
  Object.assign(forwardForm, {
    name: forward.name,
    mode: forward.mode,
    folderId: forward.folderId,
    chainHostIds: Array.isArray(forward.chainHostIds) ? [...forward.chainHostIds] : [],
    localHost: forward.localHost,
    localPort: forward.localPort,
    remoteHost: forward.remoteHost,
    remotePort: forward.remotePort || '',
    autoStart: !!forward.autoStart,
    description: forward.description || ''
  })
  forwardValidationError.value = ''
  forwardModalOpen.value = true
}

function validateForwardPayload(payload) {
  if (!payload.name) return t('forwards.errors.nameRequired')
  if (!payload.folderId) return t('forwards.errors.folderRequired')
  if (!payload.chainHostIds.length) return t('forwards.errors.chainRequired')
  if (!payload.localHost) return t('forwards.errors.localHostRequired')
  if (!isValidPort(payload.localPort)) return t('forwards.errors.portRange')
  if (payload.mode !== 'dynamic' && (!payload.remoteHost || !isValidPort(payload.remotePort))) {
    return t('forwards.errors.remoteRequired')
  }
  return ''
}

async function saveForward() {
  if (props.configurationLocked) return
  const payload = {
    id: editingForwardId.value || 0,
    folderId: Number(forwardForm.folderId),
    name: forwardForm.name.trim(),
    mode: forwardForm.mode,
    chainHostIds: forwardForm.chainHostIds.map((id) => Number(id)),
    localHost: forwardForm.localHost.trim(),
    localPort: Number(forwardForm.localPort),
    remoteHost: forwardForm.mode === 'dynamic' ? '' : forwardForm.remoteHost.trim(),
    remotePort: forwardForm.mode === 'dynamic' ? 0 : Number(forwardForm.remotePort),
    autoStart: !!forwardForm.autoStart,
    description: forwardForm.description.trim()
  }
  const error = validateForwardPayload(payload)
  if (error) {
    forwardValidationError.value = error
    return
  }
  try {
    const saved = await callBackend(SaveForward, payload)
    forwardModalOpen.value = false
    emit('vault-changed')
    emit('notify', t('forwards.notify.saved', { name: saved?.name || payload.name }))
  } catch (err) {
    forwardValidationError.value = errorMessage(err)
  }
}

// ---- 弹窗内就地新建主机成功：触发与 vault-changed 相同的 reload（Hosts 页读同一份 vault）----
function onHostCreated() {
  emit('vault-changed')
}

// ---- Forward 删除（单条 / 批量）----
function deleteForward(forward) {
  if (props.configurationLocked) return
  openConfirm({
    message: t('forwards.confirmations.deleteForward', { name: forward.name }),
    onConfirm: async () => {
      try {
        await callBackend(DeleteSelection, {
          folderIds: [],
          sshHostIds: [],
          forwardIds: [forward.id],
          cascadeFolders: false
        })
        emit('vault-changed')
        emit('notify', t('forwards.notify.deleted'))
      } catch (err) {
        emit('notify', errorMessage(err))
      }
    }
  })
}

function deleteSelectedForwards() {
  const ids = [...selectedForwardIds.value]
  if (!ids.length) return
  openConfirm({
    title: t('forwards.confirmations.deleteSelectedTitle'),
    message: t('forwards.confirmations.deleteSelected', { count: ids.length }),
    onConfirm: async () => {
      try {
        await callBackend(DeleteSelection, {
          folderIds: [],
          sshHostIds: [],
          forwardIds: ids,
          cascadeFolders: false
        })
        emit('vault-changed')
        emit('notify', t('forwards.notify.deletedCount', { count: ids.length }))
      } catch (err) {
        emit('notify', errorMessage(err))
      }
    }
  })
}

// ---- Forward 批量移动到其他文件夹 ----
const moveTargetId = ref('')

const moveTargets = computed(() => folderOptions.value.filter((option) => option.id !== selectedFolderId.value))

function onMoveTargetChange() {
  if (props.configurationLocked) return
  const targetId = Number(moveTargetId.value)
  if (!targetId) return
  const ids = [...selectedForwardIds.value]
  if (!ids.length) return
  const target = props.folders.find((folder) => folder.id === targetId)
  openConfirm({
    message: t('forwards.confirmations.moveSelected', { count: ids.length, folder: target?.name || '' }),
    confirmClass: 'btn-primary',
    onConfirm: async () => {
      try {
        const result = await application.moveForwards({ meta: createCommandMeta(props.vaultRevision), forwardIds: ids, targetFolderId: targetId })
        moveTargetId.value = ''
        clearSelection()
        emit('vault-changed', result)
        emit('notify', t('forwards.notify.movedCount', { count: ids.length }))
      } catch (err) {
        emit('notify', errorMessage(err))
      }
    }
  })
}

// ---- 运行时状态（仅存内存；Vault 数据重载不清除）----
const runtimeMap = ref({})
const pendingIds = ref(new Set())
const batchPending = ref(false)
let runtimeTimer = null

function runtimeOf(forwardId) {
  return runtimeMap.value[forwardId] || { status: 'stopped', lastError: '', latencyMs: 0 }
}

function isActiveStatus(status) {
  return status === 'running' || status === 'reconnecting'
}

function statusLabel(status) {
  const key = `forwards.status.${status}`
  const label = t(key)
  return label === key ? status : label
}

function formatLatency(latencyMs) {
  return formatLatencyUtil(latencyMs, t('app.common.none'))
}

// runtimeFetchError 非空表示状态快照获取失败：页面向用户显式提示，而不是静默回退为"全部已停止"。
const runtimeFetchError = ref('')

async function refreshRuntime() {
  try {
    const snapshot = await callBackend(GetRuntimeSnapshot)
    const next = {}
    for (const item of Array.isArray(snapshot) ? snapshot : []) {
      next[item.forwardId] = {
        status: item.status || 'stopped',
        lastError: item.lastError || '',
        latencyMs: Number(item.latencyMs) || 0
      }
    }
    runtimeMap.value = next
    runtimeFetchError.value = ''
  } catch (err) {
    /* 保留现有状态，下一轮轮询再试；但让失败可见 */
    runtimeFetchError.value = errorMessage(err)
  }
}

function setPending(forwardId, pending) {
  const next = new Set(pendingIds.value)
  if (pending) {
    next.add(forwardId)
  } else {
    next.delete(forwardId)
  }
  pendingIds.value = next
}

function forwardName(forwardId) {
  const forward = props.forwards.find((item) => item.id === forwardId)
  return forward ? forward.name : `#${forwardId}`
}

// 指纹错误消息格式见 internal/biz/runtime.go hostKeyVerifier（line 88-91）：
//   unknown:  "biz: ssh host key unknown: <host>:<port> fingerprint <fp>"
//   mismatch: "biz: ssh host key mismatch: <host>:<port> fingerprint changed (stored <fp>, got <fp>)"
const HOSTKEY_UNKNOWN_RE = /ssh host key unknown: (.+):(\d+) fingerprint (\S+)/
const HOSTKEY_MISMATCH_RE = /ssh host key mismatch: (.+):(\d+) fingerprint changed \(stored (\S+), got (\S+)\)/

function parseHostKeyError(message) {
  if (typeof message !== 'string') return null
  const mismatch = message.match(HOSTKEY_MISMATCH_RE)
  if (mismatch) {
    return {
      kind: 'mismatch',
      host: mismatch[1],
      port: Number(mismatch[2]),
      storedFingerprint: mismatch[3],
      fingerprint: mismatch[4]
    }
  }
  const unknown = message.match(HOSTKEY_UNKNOWN_RE)
  if (unknown) {
    return {
      kind: 'unknown',
      host: unknown[1],
      port: Number(unknown[2]),
      storedFingerprint: '',
      fingerprint: unknown[3]
    }
  }
  return null
}

// ---- 指纹确认队列：批量启动出现多条指纹错误时逐条依次弹窗 ----
const hostKeyQueue = ref([])
const hostKeyBusy = ref(false)

const activeHostKey = computed(() => hostKeyQueue.value[0] || null)

function enqueueHostKey(item) {
  hostKeyQueue.value = [...hostKeyQueue.value, item]
}

function shiftHostKeyQueue() {
  hostKeyQueue.value = hostKeyQueue.value.slice(1)
}

async function confirmHostKey() {
  const item = activeHostKey.value
  if (!item || hostKeyBusy.value) return
  hostKeyBusy.value = true
  try {
    // 错误消息中不含密钥类型，解析不到时按约定传空字符串
    if (item.kind === 'mismatch') {
      await callBackend(ReplaceHostKey, item.host, item.port, '', item.fingerprint)
    } else {
      await callBackend(EnrollHostKey, item.host, item.port, '', item.fingerprint)
    }
    shiftHostKeyQueue()
    // 用户已信任指纹，自动重试启动
    const forward = props.forwards.find((entry) => entry.id === item.forwardId)
    if (forward) {
      await startForward(forward)
    } else {
      void refreshRuntime()
    }
  } catch (err) {
    emit('notify', errorMessage(err))
    shiftHostKeyQueue()
  } finally {
    hostKeyBusy.value = false
  }
}

function cancelHostKey() {
  // 取消则不动：不入库、不启动，仅把该条移出队列
  shiftHostKeyQueue()
}

// ---- Forward 启停（单条 / 批量）----
async function startForward(forward) {
  if (pendingIds.value.has(forward.id)) return
  setPending(forward.id, true)
  try {
    await callBackend(StartForward, forward.id)
  } catch (err) {
    const message = errorMessage(err)
    const hostKey = parseHostKeyError(message)
    if (hostKey) {
      enqueueHostKey({ ...hostKey, forwardId: forward.id })
    } else {
      emit('notify', t('forwards.notify.startFailed', { name: forward.name, error: message }))
    }
  } finally {
    setPending(forward.id, false)
    void refreshRuntime()
  }
}

async function stopForward(forward) {
  if (pendingIds.value.has(forward.id)) return
  setPending(forward.id, true)
  try {
    await callBackend(StopForward, forward.id)
  } catch (err) {
    emit('notify', t('forwards.notify.stopFailed', { name: forward.name, error: errorMessage(err) }))
  } finally {
    setPending(forward.id, false)
    void refreshRuntime()
  }
}

function toggleForward(forward) {
  if (isActiveStatus(runtimeOf(forward.id).status)) {
    void stopForward(forward)
  } else if (!props.configurationLocked) {
    void startForward(forward)
  }
}

async function startSelectedForwards() {
  if (props.configurationLocked) return
  const ids = [...selectedForwardIds.value]
  if (!ids.length || batchPending.value) return
  batchPending.value = true
  for (const id of ids) setPending(id, true)
  try {
    const failures = await callBackend(StartManyForwards, ids)
    for (const [id, message] of Object.entries(failures || {})) {
      const forwardId = Number(id)
      const hostKey = parseHostKeyError(message)
      if (hostKey) {
        enqueueHostKey({ ...hostKey, forwardId })
      } else {
        emit('notify', t('forwards.notify.startFailed', { name: forwardName(forwardId), error: message }))
      }
    }
  } catch (err) {
    emit('notify', errorMessage(err))
  } finally {
    for (const id of ids) setPending(id, false)
    batchPending.value = false
    void refreshRuntime()
  }
}

async function stopSelectedForwards() {
  const ids = [...selectedForwardIds.value]
  if (!ids.length || batchPending.value) return
  batchPending.value = true
  for (const id of ids) setPending(id, true)
  try {
    for (const id of ids) {
      try {
        await callBackend(StopForward, id)
      } catch (err) {
        emit('notify', t('forwards.notify.stopFailed', { name: forwardName(id), error: errorMessage(err) }))
      }
    }
  } finally {
    for (const id of ids) setPending(id, false)
    batchPending.value = false
    void refreshRuntime()
  }
}

onMounted(() => {
  void refreshRuntime()
  runtimeTimer = window.setInterval(refreshRuntime, 5000)
})

onBeforeUnmount(() => {
  if (runtimeTimer !== null) {
    window.clearInterval(runtimeTimer)
    runtimeTimer = null
  }
})

// ---- 展示辅助 ----
const modeLabel = (mode) => {
  const key = `forwards.mode.${mode}`
  const label = t(key)
  return label === key ? mode : label
}

// 模式 chip 语义色：local=accent、remote=ok、dynamic=warn（仅展示，不影响逻辑）
const MODE_BADGE_CLASS = {
  local: 'accent',
  remote: 'running',
  dynamic: 'busy'
}

function modeBadgeClass(mode) {
  return MODE_BADGE_CLASS[mode] || ''
}

function routeFlowLabel(forward) {
  const local = `${forward.localHost}:${forward.localPort}`
  if (forward.mode === 'dynamic') return local
  return `${local} → ${forward.remoteHost}:${forward.remotePort}`
}

function hostName(id) {
  const host = props.sshHosts.find((item) => item.id === id)
  return host ? host.name : `#${id}`
}

function chainLabel(forward) {
  const ids = Array.isArray(forward.chainHostIds) ? forward.chainHostIds : []
  if (!ids.length) return t('app.common.none')
  return ids.map((id) => hostName(id)).join(' -> ')
}
</script>

<template>
  <section class="page-fade forwards-page">
    <aside class="folder-rail" aria-labelledby="forward-folders-title">
      <div class="folder-rail-head">
        <h2 id="forward-folders-title" class="folder-rail-title">{{ t('forwards.foldersTitle') }}</h2>
        <button
          type="button"
          class="btn icon-ghost-btn"
          :title="t('forwards.newFolder')"
          :aria-label="t('forwards.newFolder')"
          :disabled="configurationLocked"
          @click="openFolderDialog(0)"
        >
          <i class="bi bi-folder-plus" aria-hidden="true"></i>
        </button>
      </div>

      <div class="folder-rail-body">
        <div v-if="!topFolders.length" class="empty-state folder-panel-empty">
          <i class="bi bi-folder2 empty-state-icon" aria-hidden="true"></i>
          <p class="empty-state-text">{{ t('forwards.noFoldersHint') }}</p>
        </div>

        <ul v-else class="folder-tree">
          <li v-for="folder in topFolders" :key="folder.id">
            <div
              class="folder-row"
              :class="{ active: selectedFolderId === folder.id }"
            >
              <button type="button" class="folder-select-btn" :aria-current="selectedFolderId === folder.id ? 'page' : undefined" @click="selectedFolderId = folder.id">
                <i class="bi bi-folder2 folder-icon" aria-hidden="true"></i>
                <span class="folder-name cell-ellipsis" :title="folder.name">{{ folder.name }}</span>
                <span class="folder-count">{{ folderForwardCount(folder.id) }}</span>
              </button>
              <span class="folder-actions">
                <button
                  type="button"
                  class="folder-action-btn"
                  :title="t('forwards.newSubfolder')"
                  :aria-label="t('forwards.newSubfolderFor', { name: folder.name })"
                  :disabled="configurationLocked"
                  @click="openFolderDialog(folder.id)"
                >
                  <i class="bi bi-plus-lg" aria-hidden="true"></i>
                </button>
                <button
                  type="button"
                  class="folder-action-btn danger"
                  :title="t('app.common.delete')"
                  :aria-label="t('forwards.deleteFolderNamed', { name: folder.name })"
                  @click="deleteFolder(folder)"
                >
                  <i class="bi bi-trash3" aria-hidden="true"></i>
                </button>
              </span>
            </div>
            <ul v-if="childFolders(folder.id).length" class="folder-children">
              <li v-for="child in childFolders(folder.id)" :key="child.id">
                <div class="folder-row child" :class="{ active: selectedFolderId === child.id }">
                  <button type="button" class="folder-select-btn" :aria-current="selectedFolderId === child.id ? 'page' : undefined" @click="selectedFolderId = child.id">
                    <i class="bi bi-folder2 folder-icon" aria-hidden="true"></i>
                    <span class="folder-name cell-ellipsis" :title="child.name">{{ child.name }}</span>
                    <span class="folder-count">{{ folderForwardCount(child.id) }}</span>
                  </button>
                  <span class="folder-actions">
                    <button type="button" class="folder-action-btn danger" :title="t('app.common.delete')" :aria-label="t('forwards.deleteFolderNamed', { name: child.name })" @click="deleteFolder(child)">
                      <i class="bi bi-trash3" aria-hidden="true"></i>
                    </button>
                  </span>
                </div>
              </li>
            </ul>
          </li>
        </ul>
      </div>
    </aside>

    <div class="forwards-content">
        <div v-if="runtimeFetchError" class="alert alert-warning py-2 px-3 mb-2 small" role="alert">
          <i class="bi bi-exclamation-triangle me-1" aria-hidden="true"></i>
          {{ t('forwards.statusUnavailable') }}：{{ runtimeFetchError }}
        </div>
        <div class="panel-head">
          <div class="d-flex align-items-center gap-2 min-w-0">
            <input
              v-if="visibleForwards.length"
              type="checkbox"
              class="form-check-input m-0"
              :checked="allVisibleSelected"
              :aria-label="t('forwards.table.name')"
              @change="toggleSelectAll"
            />
            <h2 class="panel-title mb-0">{{ selectedFolder ? selectedFolder.name : t('forwards.tableTitle') }}</h2>
          </div>
        </div>

        <div v-if="selectedForwardIds.size" class="batch-bar">
          <span class="batch-count">{{ t('forwards.selectedCount', { count: selectedForwardIds.size }) }}</span>
          <button
            type="button"
            class="batch-action ok"
            :disabled="configurationLocked || batchPending"
            @click="startSelectedForwards"
          >
            <i class="bi bi-play-fill" aria-hidden="true"></i>{{ t('forwards.startSelected') }}
          </button>
          <button
            type="button"
            class="batch-action warn"
            :disabled="batchPending"
            @click="stopSelectedForwards"
          >
            <i class="bi bi-stop-fill" aria-hidden="true"></i>{{ t('forwards.stopSelected') }}
          </button>
          <select
            v-model="moveTargetId"
            class="form-select form-select-sm batch-move-select"
            :aria-label="t('forwards.moveToPlaceholder')"
            :disabled="configurationLocked || batchPending"
          >
            <option value="">{{ t('forwards.moveToPlaceholder') }}</option>
            <option v-for="target in moveTargets" :key="target.id" :value="target.id">{{ target.label }}</option>
          </select>
          <button type="button" class="batch-action" :disabled="configurationLocked || batchPending || !moveTargetId" @click="onMoveTargetChange">
            <i class="bi bi-folder-symlink" aria-hidden="true"></i>{{ t('forwards.moveSelected') }}
          </button>
          <button type="button" class="batch-action danger" :disabled="configurationLocked || batchPending" @click="deleteSelectedForwards">
            <i class="bi bi-trash3" aria-hidden="true"></i>{{ t('forwards.deleteSelected') }}
          </button>
          <button
            type="button"
            class="btn icon-ghost-btn batch-clear"
            :aria-label="t('forwards.clearSelection')"
            @click="clearSelection"
          >
            <i class="bi bi-x-lg" aria-hidden="true"></i>
          </button>
        </div>

        <div v-if="!selectedFolder" class="empty-state">
          <i class="bi bi-folder2-open empty-state-icon" aria-hidden="true"></i>
          <p class="empty-state-text">{{ t('forwards.selectFolderHint') }}</p>
        </div>
        <div v-else-if="!visibleForwards.length" class="empty-state">
          <i class="bi bi-inbox empty-state-icon" aria-hidden="true"></i>
          <p class="empty-state-text">{{ t('forwards.emptyFolder') }}</p>
          <button type="button" class="btn btn-primary header-action-btn" :disabled="configurationLocked" @click="openNewForward">
            <i class="bi bi-plus-lg" aria-hidden="true"></i>{{ t('app.header.newForward') }}
          </button>
        </div>

        <div v-else class="forward-card-list">
          <div
            v-for="forward in visibleForwards"
            :key="forward.id"
            class="forward-card"
            :class="{ selected: selectedForwardIds.has(forward.id) }"
          >
            <div class="forward-card-head">
              <input
                type="checkbox"
                class="form-check-input forward-card-check"
                :checked="selectedForwardIds.has(forward.id)"
                :aria-label="forward.name"
                @change="toggleSelectForward(forward.id)"
              />
              <TooltipText :text="forward.name" class-name="forward-card-name" />
              <span class="status-badge" :class="modeBadgeClass(forward.mode)">{{ modeLabel(forward.mode) }}</span>
              <i
                v-if="forward.autoStart"
                class="bi bi-lightning-charge-fill forward-autostart"
                :title="t('forwards.table.autoStart')"
                aria-hidden="true"
              ></i>
              <div class="forward-card-side">
                <StatusChip
                  :status="runtimeOf(forward.id).status"
                  :label="statusLabel(runtimeOf(forward.id).status)"
                />
                <span class="forward-latency">{{ formatLatency(runtimeOf(forward.id).latencyMs) }}</span>
                <div class="forward-card-actions">
                  <button
                    v-if="pendingIds.has(forward.id)"
                    type="button"
                    class="btn icon-ghost-btn"
                    disabled
                  >
                    <span class="spinner-border spinner-border-sm" aria-hidden="true"></span>
                  </button>
                  <IconActionButton
                    v-else-if="isActiveStatus(runtimeOf(forward.id).status)"
                    icon-class="bi-stop-fill"
                    button-class="icon-ghost-btn danger"
                    :title="t('forwards.actions.stop')"
                    :aria-label="t('forwards.actions.stop')"
                    @click="toggleForward(forward)"
                  />
                  <IconActionButton
                    v-else
                    icon-class="bi-play-fill"
                    button-class="icon-ghost-btn accent"
                    :title="t('forwards.actions.start')"
                    :aria-label="t('forwards.actions.start')"
                    @click="toggleForward(forward)"
                  />
                  <IconActionButton
                    icon-class="bi-pencil"
                    button-class="icon-ghost-btn"
                    :title="t('app.common.edit')"
                    :aria-label="t('app.common.edit')"
                    @click="editForward(forward)"
                  />
                  <IconActionButton
                    icon-class="bi-trash3"
                    button-class="icon-ghost-btn danger"
                    :title="t('app.common.delete')"
                    :aria-label="t('app.common.delete')"
                    @click="deleteForward(forward)"
                  />
                </div>
              </div>
            </div>
            <div class="forward-card-sub">
              <span class="font-mono forward-route-label" :title="routeFlowLabel(forward)">
                {{ routeFlowLabel(forward) }}
              </span>
              <span class="forward-chain" :title="chainLabel(forward)">{{ chainLabel(forward) }}</span>
            </div>
            <div
              v-if="runtimeOf(forward.id).status === 'error' && runtimeOf(forward.id).lastError"
              class="forward-card-error cell-ellipsis"
              :title="runtimeOf(forward.id).lastError"
            >
              {{ runtimeOf(forward.id).lastError }}
            </div>
          </div>
        </div>
    </div>
  </section>

  <BaseDialog :visible="folderDialog.visible" :title="folderDialog.parentId ? t('forwards.newSubfolder') : t('forwards.newFolder')" class="action-confirm-dialog" @close="folderDialog.visible = false">
        <label class="form-label" for="folderName">{{ t('forwards.folderName') }}</label>
        <input
          id="folderName"
          v-model="folderDialog.name"
          type="text"
          class="form-control"
          :placeholder="t('forwards.folderNamePlaceholder')"
          @keyup.enter="saveFolder"
        />
        <div v-if="folderDialog.error" class="form-error mt-2">{{ folderDialog.error }}</div>
    <template #footer>
        <button type="button" class="btn btn-outline-secondary" @click="folderDialog.visible = false">
          {{ t('app.common.cancel') }}
        </button>
        <button type="button" class="btn btn-primary" @click="saveFolder">
          {{ t('app.common.create') }}
        </button>
    </template>
  </BaseDialog>

  <ForwardModal
    :show="forwardModalOpen"
    :editing-forward-id="editingForwardId"
    :form="forwardForm"
    :folders="folderOptions"
    :ssh-hosts="sshHosts"
    :ssh-host-defaults="sshHostDefaults"
    :vault-revision="vaultRevision"
    :validation-error="forwardValidationError"
    @close="forwardModalOpen = false"
    @submit="saveForward"
    @host-created="onHostCreated"
  />

  <ConfirmDialog
    :visible="confirmDialog.visible"
    :title="confirmDialog.title"
    :message="confirmDialog.message"
    :confirm-label="confirmDialog.confirmLabel"
    :confirm-class="confirmDialog.confirmClass"
    :show-cancel="confirmDialog.showCancel"
    :busy="confirmDialog.busy"
    @confirm="handleConfirm"
    @close="closeConfirm"
  />

  <HostKeyDialog
    :show="!!activeHostKey"
    :kind="activeHostKey?.kind || 'unknown'"
    :host="activeHostKey?.host || ''"
    :port="activeHostKey?.port || 0"
    :fingerprint="activeHostKey?.fingerprint || ''"
    :stored-fingerprint="activeHostKey?.storedFingerprint || ''"
    :busy="hostKeyBusy"
    @confirm="confirmHostKey"
    @cancel="cancelHostKey"
  />
</template>
