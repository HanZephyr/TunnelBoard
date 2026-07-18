<script setup>
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { DeleteSelection, SaveSSHHost } from '../../../wailsjs/go/main/App'
import { callBackend, errorMessage, isValidPort } from '../../utils/backend'
import TooltipText from '../common/TooltipText.vue'
import IconActionButton from '../common/IconActionButton.vue'
import ConfirmDialog from '../common/ConfirmDialog.vue'
import HostModal from '../modals/HostModal.vue'

const props = defineProps({
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

const sortedHosts = computed(() => [...props.sshHosts].sort((a, b) => a.id - b.id))

function authLabel(authType) {
  const key = {
    password: 'hosts.auth.password',
    ssh_key: 'hosts.auth.sshKey',
    ssh_agent: 'hosts.auth.sshAgent'
  }[authType]
  return key ? t(key) : t('hosts.auth.unknown')
}

// 认证方式 chip 语义色：ssh_key=accent（推荐）、ssh_agent=ok、password=warn（仅展示，不影响逻辑）
const AUTH_BADGE_CLASS = {
  password: 'busy',
  ssh_key: 'accent',
  ssh_agent: 'running'
}

function authBadgeClass(authType) {
  return AUTH_BADGE_CLASS[authType] || ''
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

// ---- 新建 / 编辑 ----
const hostModalOpen = ref(false)
const editingHostId = ref(null)
const hostValidationError = ref('')
const hostForm = reactive(defaultHostForm())

function defaultHostForm() {
  return {
    name: '',
    host: '',
    port: 22,
    user: '',
    authType: 'ssh_key',
    keyPath: '',
    agentSocketPath: '',
    password: '',
    keepAliveIntervalMs: 5000,
    timeoutMs: 5000,
    hostKeyAlgorithms: '',
    notes: ''
  }
}

function openNewHost() {
  editingHostId.value = null
  Object.assign(hostForm, defaultHostForm())
  hostValidationError.value = ''
  hostModalOpen.value = true
}

defineExpose({ openNewHost })

function editHost(host) {
  editingHostId.value = host.id
  Object.assign(hostForm, defaultHostForm(), {
    name: host.name,
    host: host.host,
    port: host.port,
    user: host.user,
    authType: host.authType || 'ssh_key',
    keyPath: host.keyPath || '',
    agentSocketPath: host.agentSocketPath || '',
    password: host.password || '',
    keepAliveIntervalMs: host.keepAliveIntervalMs ?? 0,
    timeoutMs: host.timeoutMs ?? 0,
    hostKeyAlgorithms: host.hostKeyAlgorithms || '',
    notes: host.notes || ''
  })
  hostValidationError.value = ''
  hostModalOpen.value = true
}

function buildHostPayload() {
  const authType = hostForm.authType
  return {
    id: editingHostId.value || 0,
    name: hostForm.name.trim(),
    host: hostForm.host.trim(),
    port: Number(hostForm.port),
    user: hostForm.user.trim(),
    authType,
    keyPath: authType === 'ssh_key' ? hostForm.keyPath.trim() : '',
    agentSocketPath: authType === 'ssh_agent' ? hostForm.agentSocketPath.trim() : '',
    password: authType === 'password' || authType === 'ssh_key' ? hostForm.password : '',
    keepAliveIntervalMs: Number(hostForm.keepAliveIntervalMs) || 0,
    timeoutMs: Number(hostForm.timeoutMs) || 0,
    hostKeyAlgorithms: hostForm.hostKeyAlgorithms.trim(),
    notes: hostForm.notes.trim()
  }
}

function validateHostPayload(payload) {
  if (!payload.name) return t('hosts.errors.nameRequired')
  if (!payload.host) return t('hosts.errors.hostRequired')
  if (!payload.user) return t('hosts.errors.userRequired')
  if (!isValidPort(payload.port)) return t('hosts.errors.portRange')
  if (payload.authType === 'ssh_key' && !payload.keyPath) return t('hosts.errors.keyPathRequired')
  if (payload.authType === 'password' && !payload.password) return t('hosts.errors.passwordRequired')
  return ''
}

async function saveHost() {
  const payload = buildHostPayload()
  const error = validateHostPayload(payload)
  if (error) {
    hostValidationError.value = error
    return
  }
  try {
    const saved = await callBackend(SaveSSHHost, payload)
    hostModalOpen.value = false
    emit('vault-changed')
    emit('notify', t('hosts.notify.saved', { name: saved?.name || payload.name }))
  } catch (err) {
    hostValidationError.value = errorMessage(err)
  }
}

// ---- 删除（被 Forward 引用时后端拒绝，展示错误提示）----
function deleteHost(host) {
  openConfirm({
    title: t('hosts.confirmations.deleteHostTitle'),
    message: t('hosts.confirmations.deleteHost', { name: host.name }),
    onConfirm: async () => {
      try {
        await callBackend(DeleteSelection, {
          folderIds: [],
          sshHostIds: [host.id],
          forwardIds: [],
          cascadeFolders: false
        })
        emit('vault-changed')
        emit('notify', t('hosts.notify.deleted', { name: host.name }))
      } catch (err) {
        const message = errorMessage(err)
        if (message.includes('referenced by forwards')) {
          openConfirm({
            title: t('hosts.confirmations.inUseTitle'),
            message: t('hosts.confirmations.inUse', { name: host.name }),
            confirmLabel: t('app.common.close'),
            confirmClass: 'btn-primary',
            showCancel: false
          })
          return
        }
        emit('notify', message)
      }
    }
  })
}
</script>

<template>
  <section class="page-fade">
    <div class="panel-card">
      <div class="panel-head">
        <h2 class="panel-title mb-0">{{ t('hosts.tableTitle') }}</h2>
      </div>

      <div v-if="!sortedHosts.length" class="empty-state">
        <i class="bi bi-hdd-network empty-state-icon" aria-hidden="true"></i>
        <p class="empty-state-text">{{ t('hosts.empty') }}</p>
        <button type="button" class="btn btn-primary header-action-btn" @click="openNewHost">
          <i class="bi bi-plus-lg" aria-hidden="true"></i>{{ t('app.header.newHost') }}
        </button>
      </div>

      <div v-else class="host-card-grid">
        <div v-for="host in sortedHosts" :key="host.id" class="host-card">
          <div class="host-card-head">
            <TooltipText :text="host.name" class-name="host-card-name" />
            <div class="card-corner-actions">
              <IconActionButton
                icon-class="bi-pencil"
                button-class="icon-ghost-btn"
                :title="t('app.common.edit')"
                :aria-label="t('app.common.edit')"
                @click="editHost(host)"
              />
              <IconActionButton
                icon-class="bi-trash3"
                button-class="icon-ghost-btn danger"
                :title="t('app.common.delete')"
                :aria-label="t('app.common.delete')"
                @click="deleteHost(host)"
              />
            </div>
          </div>
          <div class="font-mono host-card-conn cell-ellipsis" :title="`${host.user}@${host.host}:${host.port}`">
            {{ host.user }}@{{ host.host }}:{{ host.port }}
          </div>
          <div class="host-card-meta">
            <span class="status-badge" :class="authBadgeClass(host.authType)">{{ authLabel(host.authType) }}</span>
            <span v-if="host.notes" class="host-card-notes cell-ellipsis" :title="host.notes">{{ host.notes }}</span>
          </div>
        </div>
      </div>
    </div>
  </section>

  <HostModal
    :show="hostModalOpen"
    :editing-host-id="editingHostId"
    :form="hostForm"
    :validation-error="hostValidationError"
    @close="hostModalOpen = false"
    @submit="saveHost"
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
