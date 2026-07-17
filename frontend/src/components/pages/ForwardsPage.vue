<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CreateFolder,
  DeleteSelection,
  MoveForward,
  SaveForward
} from '../../../wailsjs/go/main/App'
import { callBackend, errorMessage, isValidPort } from '../../utils/backend'
import TooltipText from '../common/TooltipText.vue'
import IconActionButton from '../common/IconActionButton.vue'
import ConfirmDialog from '../common/ConfirmDialog.vue'
import ForwardModal from '../modals/ForwardModal.vue'

const props = defineProps({
  folders: {
    type: Array,
    default: () => []
  },
  sshHosts: {
    type: Array,
    default: () => []
  },
  forwards: {
    type: Array,
    default: () => []
  }
})

const emit = defineEmits(['vault-changed', 'notify'])

const { t } = useI18n()

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
  confirmDialog.visible = true
}

function closeConfirm() {
  confirmDialog.visible = false
  confirmDialog.onConfirm = null
}

async function handleConfirm() {
  const handler = confirmDialog.onConfirm
  closeConfirm()
  if (typeof handler === 'function') {
    await handler()
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
  folderDialog.parentId = parentId
  folderDialog.name = ''
  folderDialog.error = ''
  folderDialog.visible = true
}

async function saveFolder() {
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

// ---- Forward 删除（单条 / 批量）----
function deleteForward(forward) {
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
  const targetId = Number(moveTargetId.value)
  moveTargetId.value = ''
  if (!targetId) return
  const ids = [...selectedForwardIds.value]
  if (!ids.length) return
  const target = props.folders.find((folder) => folder.id === targetId)
  openConfirm({
    message: t('forwards.confirmations.moveSelected', { count: ids.length, folder: target?.name || '' }),
    confirmClass: 'btn-primary',
    onConfirm: async () => {
      try {
        for (const id of ids) {
          await callBackend(MoveForward, id, targetId)
        }
        emit('vault-changed')
        emit('notify', t('forwards.notify.movedCount', { count: ids.length }))
      } catch (err) {
        emit('notify', errorMessage(err))
      }
    }
  })
}

// ---- 展示辅助 ----
const modeLabel = (mode) => {
  const key = `forwards.mode.${mode}`
  const label = t(key)
  return label === key ? mode : label
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
  <section class="page-fade">
    <div class="forwards-layout">
      <div class="panel-card folder-tree-panel">
        <div class="panel-head">
          <h2 class="panel-title mb-0">{{ t('forwards.foldersTitle') }}</h2>
          <button type="button" class="btn btn-sm btn-outline-primary" @click="openFolderDialog(0)">
            <i class="bi bi-folder-plus me-1" aria-hidden="true"></i>{{ t('forwards.newFolder') }}
          </button>
        </div>

        <div v-if="!topFolders.length" class="folder-tree-empty">{{ t('forwards.noFoldersHint') }}</div>

        <ul v-else class="folder-tree">
          <li v-for="folder in topFolders" :key="folder.id">
            <div
              class="folder-row"
              :class="{ active: selectedFolderId === folder.id }"
              @click="selectedFolderId = folder.id"
            >
              <i class="bi bi-folder2 folder-icon" aria-hidden="true"></i>
              <span class="folder-name cell-ellipsis" :title="folder.name">{{ folder.name }}</span>
              <span class="folder-count">{{ folderForwardCount(folder.id) }}</span>
              <span class="folder-actions" @click.stop>
                <button
                  type="button"
                  class="folder-action-btn"
                  :title="t('forwards.newSubfolder')"
                  :aria-label="t('forwards.newSubfolder')"
                  @click="openFolderDialog(folder.id)"
                >
                  <i class="bi bi-plus-lg" aria-hidden="true"></i>
                </button>
                <button
                  type="button"
                  class="folder-action-btn danger"
                  :title="t('app.common.delete')"
                  :aria-label="t('app.common.delete')"
                  @click="deleteFolder(folder)"
                >
                  <i class="bi bi-trash3" aria-hidden="true"></i>
                </button>
              </span>
            </div>
            <div
              v-for="child in childFolders(folder.id)"
              :key="child.id"
              class="folder-row child"
              :class="{ active: selectedFolderId === child.id }"
              @click="selectedFolderId = child.id"
            >
              <i class="bi bi-folder2 folder-icon" aria-hidden="true"></i>
              <span class="folder-name cell-ellipsis" :title="child.name">{{ child.name }}</span>
              <span class="folder-count">{{ folderForwardCount(child.id) }}</span>
              <span class="folder-actions" @click.stop>
                <button
                  type="button"
                  class="folder-action-btn danger"
                  :title="t('app.common.delete')"
                  :aria-label="t('app.common.delete')"
                  @click="deleteFolder(child)"
                >
                  <i class="bi bi-trash3" aria-hidden="true"></i>
                </button>
              </span>
            </div>
          </li>
        </ul>
      </div>

      <div class="panel-card forwards-table-panel">
        <div class="panel-head">
          <h2 class="panel-title mb-0">{{ selectedFolder ? selectedFolder.name : t('forwards.tableTitle') }}</h2>
          <div v-if="selectedForwardIds.size" class="batch-bar">
            <span class="batch-count">{{ t('forwards.selectedCount', { count: selectedForwardIds.size }) }}</span>
            <select
              v-model="moveTargetId"
              class="form-select form-select-sm batch-move-select"
              :aria-label="t('forwards.moveToPlaceholder')"
              @change="onMoveTargetChange"
            >
              <option value="">{{ t('forwards.moveToPlaceholder') }}</option>
              <option v-for="target in moveTargets" :key="target.id" :value="target.id">{{ target.label }}</option>
            </select>
            <button type="button" class="btn btn-sm btn-outline-danger" @click="deleteSelectedForwards">
              <i class="bi bi-trash3 me-1" aria-hidden="true"></i>{{ t('forwards.deleteSelected') }}
            </button>
          </div>
        </div>

        <div v-if="!selectedFolder" class="folder-tree-empty py-4">{{ t('forwards.selectFolderHint') }}</div>
        <div v-else-if="!visibleForwards.length" class="folder-tree-empty py-4">{{ t('forwards.emptyFolder') }}</div>

        <div v-else class="page-table-wrap forwards-table-wrap">
          <table class="table forwards-table align-middle mb-0">
            <thead>
              <tr>
                <th class="forward-check-cell">
                  <input
                    type="checkbox"
                    class="form-check-input"
                    :checked="allVisibleSelected"
                    :aria-label="t('forwards.table.name')"
                    @change="toggleSelectAll"
                  />
                </th>
                <th>{{ t('forwards.table.name') }}</th>
                <th class="forward-mode-cell">{{ t('forwards.table.mode') }}</th>
                <th class="forward-route-cell">{{ t('forwards.table.local') }}</th>
                <th class="forward-route-cell">{{ t('forwards.table.remote') }}</th>
                <th>{{ t('forwards.table.chain') }}</th>
                <th class="forward-autostart-cell">{{ t('forwards.table.autoStart') }}</th>
                <th class="forwards-action-cell">{{ t('forwards.table.actions') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="forward in visibleForwards" :key="forward.id">
                <td>
                  <input
                    type="checkbox"
                    class="form-check-input"
                    :checked="selectedForwardIds.has(forward.id)"
                    :aria-label="forward.name"
                    @change="toggleSelectForward(forward.id)"
                  />
                </td>
                <td><TooltipText :text="forward.name" /></td>
                <td><span class="status-badge">{{ modeLabel(forward.mode) }}</span></td>
                <td><TooltipText :text="`${forward.localHost}:${forward.localPort}`" /></td>
                <td>
                  <TooltipText
                    :text="forward.mode === 'dynamic' ? t('app.common.none') : `${forward.remoteHost}:${forward.remotePort}`"
                  />
                </td>
                <td><TooltipText :text="chainLabel(forward)" /></td>
                <td>
                  <span v-if="forward.autoStart" class="status-badge running">{{ t('forwards.autoStartYes') }}</span>
                  <span v-else>{{ t('app.common.none') }}</span>
                </td>
                <td>
                  <div class="d-flex gap-1">
                    <IconActionButton
                      icon-class="bi-pencil"
                      :title="t('app.common.edit')"
                      :aria-label="t('app.common.edit')"
                      @click="editForward(forward)"
                    />
                    <IconActionButton
                      icon-class="bi-trash3"
                      button-class="btn-outline-danger"
                      :title="t('app.common.delete')"
                      :aria-label="t('app.common.delete')"
                      @click="deleteForward(forward)"
                    />
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </section>

  <div v-if="folderDialog.visible" class="overlay">
    <div class="dialog-card compact-dialog action-confirm-dialog">
      <div class="dialog-head">
        <h3 class="dialog-title">
          {{ folderDialog.parentId ? t('forwards.newSubfolder') : t('forwards.newFolder') }}
        </h3>
      </div>
      <div class="dialog-body">
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
      </div>
      <div class="dialog-footer">
        <button type="button" class="btn btn-outline-secondary" @click="folderDialog.visible = false">
          {{ t('app.common.cancel') }}
        </button>
        <button type="button" class="btn btn-primary" @click="saveFolder">
          {{ t('app.common.create') }}
        </button>
      </div>
    </div>
  </div>

  <ForwardModal
    :show="forwardModalOpen"
    :editing-forward-id="editingForwardId"
    :form="forwardForm"
    :folders="folderOptions"
    :ssh-hosts="sshHosts"
    :validation-error="forwardValidationError"
    @close="forwardModalOpen = false"
    @submit="saveForward"
  />

  <ConfirmDialog
    :visible="confirmDialog.visible"
    :title="confirmDialog.title"
    :message="confirmDialog.message"
    :confirm-label="confirmDialog.confirmLabel"
    :confirm-class="confirmDialog.confirmClass"
    :show-cancel="confirmDialog.showCancel"
    @confirm="handleConfirm"
    @close="closeConfirm"
  />
</template>
