<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { CheckLocalPortAvailable, SaveSSHHost } from '../../../wailsjs/go/main/App'
import { callBackend, errorMessage, isValidPort } from '../../utils/backend'

const props = defineProps({
  show: {
    type: Boolean,
    default: false
  },
  editingForwardId: {
    type: Number,
    default: null
  },
  // 由父组件持有的响应式表单对象（沿用项目既有的受控模态框模式）
  form: {
    type: Object,
    required: true
  },
  // 扁平化的文件夹选项 [{ id, label }]，子文件夹 label 带父级前缀
  folders: {
    type: Array,
    default: () => []
  },
  sshHosts: {
    type: Array,
    default: () => []
  },
  validationError: {
    type: String,
    default: ''
  }
})

const emit = defineEmits(['close', 'submit', 'host-created'])

const { t } = useI18n()

const chainCandidateId = ref('')

const availableHosts = computed(() => {
  const selected = new Set(props.form.chainHostIds)
  return props.sshHosts.filter((host) => !selected.has(host.id))
})

function hostLabel(id) {
  const host = props.sshHosts.find((item) => item.id === id)
  if (!host) return `#${id}`
  return `${host.name} (${host.user}@${host.host}:${host.port})`
}

function addChainHost() {
  const id = Number(chainCandidateId.value)
  chainCandidateId.value = ''
  if (!Number.isInteger(id) || id <= 0) return
  if (props.form.chainHostIds.includes(id)) return
  props.form.chainHostIds.push(id)
}

function moveChainHost(index, offset) {
  const ids = props.form.chainHostIds
  const target = index + offset
  if (index < 0 || index >= ids.length || target < 0 || target >= ids.length) return
  const temp = ids[target]
  ids[target] = ids[index]
  ids[index] = temp
}

function removeChainHost(index) {
  if (index < 0 || index >= props.form.chainHostIds.length) return
  props.form.chainHostIds.splice(index, 1)
}

// ---- 就地新建主机（弹窗内嵌展开区；调用既有 SaveSSHHost 绑定，id=0）----
// payload 构造与 HostModal.buildHostPayload 完全一致（同一绑定、同一参数形状）
const newHostOpen = ref(false)
const newHostSaving = ref(false)
const newHostError = ref('')
const newHostForm = reactive({
  name: '',
  host: '',
  port: 22,
  user: '',
  authType: 'ssh_key',
  keyPath: '',
  password: '',
  agentSocketPath: ''
})

const newHostShowsKeyPath = computed(() => newHostForm.authType === 'ssh_key')
const newHostShowsPassword = computed(() => newHostForm.authType === 'password' || newHostForm.authType === 'ssh_key')
const newHostShowsAgentSocket = computed(() => newHostForm.authType === 'ssh_agent')

watch(
  () => newHostForm.authType,
  () => {
    if (!newHostShowsKeyPath.value) newHostForm.keyPath = ''
    if (!newHostShowsPassword.value) newHostForm.password = ''
    if (!newHostShowsAgentSocket.value) newHostForm.agentSocketPath = ''
  }
)

watch(
  () => props.show,
  (visible) => {
    if (!visible) newHostOpen.value = false
  }
)

function openNewHostForm() {
  Object.assign(newHostForm, {
    name: '',
    host: '',
    port: 22,
    user: '',
    authType: 'ssh_key',
    keyPath: '',
    password: '',
    agentSocketPath: ''
  })
  newHostError.value = ''
  newHostOpen.value = true
}

function cancelNewHostForm() {
  newHostOpen.value = false
  newHostError.value = ''
}

function validateNewHost() {
  if (!newHostForm.name.trim()) return t('hosts.errors.nameRequired')
  if (!newHostForm.host.trim()) return t('hosts.errors.hostRequired')
  if (!newHostForm.user.trim()) return t('hosts.errors.userRequired')
  if (!isValidPort(newHostForm.port)) return t('hosts.errors.portRange')
  if (newHostForm.authType === 'ssh_key' && !newHostForm.keyPath.trim()) return t('hosts.errors.keyPathRequired')
  if (newHostForm.authType === 'password' && !newHostForm.password) return t('hosts.errors.passwordRequired')
  return ''
}

async function saveNewHost() {
  if (newHostSaving.value) return
  const error = validateNewHost()
  if (error) {
    newHostError.value = error
    return
  }
  newHostSaving.value = true
  newHostError.value = ''
  const authType = newHostForm.authType
  const payload = {
    id: 0,
    name: newHostForm.name.trim(),
    host: newHostForm.host.trim(),
    port: Number(newHostForm.port),
    user: newHostForm.user.trim(),
    authType,
    keyPath: authType === 'ssh_key' ? newHostForm.keyPath.trim() : '',
    agentSocketPath: authType === 'ssh_agent' ? newHostForm.agentSocketPath.trim() : '',
    password: authType === 'password' || authType === 'ssh_key' ? newHostForm.password : '',
    keepAliveIntervalMs: 5000,
    timeoutMs: 5000,
    hostKeyAlgorithms: '',
    notes: ''
  }
  try {
    const saved = await callBackend(SaveSSHHost, payload)
    const newId = Number(saved?.id)
    if (Number.isInteger(newId) && newId > 0 && !props.form.chainHostIds.includes(newId)) {
      props.form.chainHostIds.push(newId)
    }
    newHostOpen.value = false
    emit('host-created')
  } catch (err) {
    newHostError.value = errorMessage(err)
  } finally {
    newHostSaving.value = false
  }
}

// 端口冲突预警：编辑时经实际绑定预检本地监听端口（remote 模式在服务端绑定，不做本机预检）。
const portWarning = ref('')
let portCheckTimer = null
watch(
  () => [props.show, props.form.mode, props.form.localHost, props.form.localPort],
  () => {
    portWarning.value = ''
    if (portCheckTimer) clearTimeout(portCheckTimer)
    if (!props.show || props.form.mode === 'remote') return
    const port = Number(props.form.localPort)
    if (!Number.isInteger(port) || port < 1 || port > 65535) return
    portCheckTimer = setTimeout(async () => {
      try {
        await CheckLocalPortAvailable(String(props.form.localHost || ''), port)
        portWarning.value = ''
      } catch (_) {
        portWarning.value = t('forwards.modal.portConflict', { port })
      }
    }, 400)
  },
  { immediate: true }
)
</script>

<template>
  <div v-if="show" class="overlay">
    <div class="dialog-card compact-dialog forward-dialog">
      <div class="dialog-head">
        <h3 class="dialog-title">
          {{ editingForwardId ? t('forwards.modal.editTitle') : t('forwards.modal.newTitle') }}
        </h3>
      </div>
      <div class="dialog-body">
        <div class="row g-2">
          <div class="col-12 col-md-7">
            <label class="form-label" for="forwardName">{{ t('forwards.modal.name') }}</label>
            <input
              id="forwardName"
              v-model="form.name"
              type="text"
              class="form-control"
              :placeholder="t('forwards.modal.namePlaceholder')"
            />
          </div>
          <div class="col-12 col-md-5">
            <label class="form-label" for="forwardMode">{{ t('forwards.modal.mode') }}</label>
            <select id="forwardMode" v-model="form.mode" class="form-select">
              <option value="local">{{ t('forwards.mode.local') }}</option>
              <option value="remote">{{ t('forwards.mode.remote') }}</option>
              <option value="dynamic">{{ t('forwards.mode.dynamic') }}</option>
            </select>
          </div>

          <div class="col-12">
            <label class="form-label" for="forwardFolder">{{ t('forwards.modal.folder') }}</label>
            <select id="forwardFolder" v-model="form.folderId" class="form-select" :disabled="!folders.length">
              <option v-for="option in folders" :key="option.id" :value="option.id">{{ option.label }}</option>
            </select>
            <div v-if="!folders.length" class="field-note">{{ t('forwards.modal.noFoldersHint') }}</div>
          </div>

          <div class="col-12">
            <label class="form-label">{{ t('forwards.modal.chain') }}</label>
            <div class="chain-editor">
              <div v-if="!form.chainHostIds.length" class="chain-empty">{{ t('forwards.modal.chainEmpty') }}</div>
              <div v-for="(hostId, index) in form.chainHostIds" :key="hostId" class="chain-item">
                <span class="chain-index">{{ index + 1 }}</span>
                <span class="chain-host cell-ellipsis" :title="hostLabel(hostId)">{{ hostLabel(hostId) }}</span>
                <span class="chain-actions">
                  <button
                    type="button"
                    class="chain-action-btn"
                    :disabled="index === 0"
                    :title="t('app.common.moveUp')"
                    :aria-label="t('app.common.moveUp')"
                    @click="moveChainHost(index, -1)"
                  >
                    <i class="bi bi-arrow-up" aria-hidden="true"></i>
                  </button>
                  <button
                    type="button"
                    class="chain-action-btn"
                    :disabled="index === form.chainHostIds.length - 1"
                    :title="t('app.common.moveDown')"
                    :aria-label="t('app.common.moveDown')"
                    @click="moveChainHost(index, 1)"
                  >
                    <i class="bi bi-arrow-down" aria-hidden="true"></i>
                  </button>
                  <button
                    type="button"
                    class="chain-action-btn"
                    :title="t('app.common.remove')"
                    :aria-label="t('app.common.remove')"
                    @click="removeChainHost(index)"
                  >
                    <i class="bi bi-x-lg" aria-hidden="true"></i>
                  </button>
                </span>
              </div>
              <select
                v-model="chainCandidateId"
                class="form-select form-select-sm"
                :disabled="!availableHosts.length"
                @change="addChainHost"
              >
                <option value="">{{ t('forwards.modal.chainPlaceholder') }}</option>
                <option v-for="host in availableHosts" :key="host.id" :value="host.id">
                  {{ host.name }} ({{ host.user }}@{{ host.host }}:{{ host.port }})
                </option>
              </select>
              <button
                v-if="!newHostOpen"
                type="button"
                class="newhost-toggle"
                @click="openNewHostForm"
              >
                <i class="bi bi-plus-lg" aria-hidden="true"></i>{{ t('forwards.modal.newHostToggle') }}
              </button>
              <div v-else class="newhost-form">
                <div class="row g-2">
                  <div class="col-12 col-md-6">
                    <label class="form-label" for="newHostName">{{ t('hosts.modal.name') }}</label>
                    <input
                      id="newHostName"
                      v-model="newHostForm.name"
                      type="text"
                      class="form-control form-control-sm"
                      :placeholder="t('hosts.modal.namePlaceholder')"
                    />
                  </div>
                  <div class="col-12 col-md-6">
                    <label class="form-label" for="newHostUser">{{ t('hosts.modal.user') }}</label>
                    <input
                      id="newHostUser"
                      v-model="newHostForm.user"
                      type="text"
                      class="form-control form-control-sm"
                      placeholder="e.g. ubuntu"
                    />
                  </div>
                  <div class="col-12 col-md-8">
                    <label class="form-label" for="newHostAddress">{{ t('hosts.modal.host') }}</label>
                    <input
                      id="newHostAddress"
                      v-model="newHostForm.host"
                      type="text"
                      class="form-control form-control-sm"
                      :placeholder="t('hosts.modal.hostPlaceholder')"
                    />
                  </div>
                  <div class="col-12 col-md-4">
                    <label class="form-label" for="newHostPort">{{ t('hosts.modal.port') }}</label>
                    <input
                      id="newHostPort"
                      v-model="newHostForm.port"
                      type="number"
                      min="1"
                      max="65535"
                      class="form-control form-control-sm"
                    />
                  </div>
                  <div class="col-12">
                    <label class="form-label" for="newHostAuthType">{{ t('hosts.modal.authType') }}</label>
                    <select id="newHostAuthType" v-model="newHostForm.authType" class="form-select form-select-sm">
                      <option value="password">{{ t('hosts.auth.password') }}</option>
                      <option value="ssh_key">{{ t('hosts.auth.sshKey') }}</option>
                      <option value="ssh_agent">{{ t('hosts.auth.sshAgent') }}</option>
                    </select>
                  </div>
                  <div v-if="newHostShowsKeyPath" class="col-12">
                    <label class="form-label" for="newHostKeyPath">{{ t('hosts.modal.keyPath') }}</label>
                    <input
                      id="newHostKeyPath"
                      v-model="newHostForm.keyPath"
                      type="text"
                      class="form-control form-control-sm"
                      :placeholder="t('hosts.modal.keyPathPlaceholder')"
                    />
                  </div>
                  <div v-if="newHostShowsPassword" class="col-12">
                    <label class="form-label" for="newHostPassword">
                      {{ newHostForm.authType === 'ssh_key' ? t('hosts.modal.keyPassphrase') : t('hosts.modal.password') }}
                    </label>
                    <input
                      id="newHostPassword"
                      v-model="newHostForm.password"
                      type="password"
                      class="form-control form-control-sm"
                      :placeholder="
                        newHostForm.authType === 'ssh_key'
                          ? t('hosts.modal.keyPassphrasePlaceholder')
                          : t('hosts.modal.passwordPlaceholder')
                      "
                    />
                  </div>
                  <div v-if="newHostShowsAgentSocket" class="col-12">
                    <label class="form-label" for="newHostAgentSocket">{{ t('hosts.modal.agentSocketPath') }}</label>
                    <input
                      id="newHostAgentSocket"
                      v-model="newHostForm.agentSocketPath"
                      type="text"
                      class="form-control form-control-sm"
                      :placeholder="t('hosts.modal.agentSocketPlaceholder')"
                    />
                  </div>
                </div>
                <div class="newhost-footer">
                  <div v-if="newHostError" class="form-error">{{ newHostError }}</div>
                  <div class="newhost-actions">
                    <button
                      type="button"
                      class="btn btn-sm btn-outline-secondary"
                      :disabled="newHostSaving"
                      @click="cancelNewHostForm"
                    >
                      {{ t('app.common.cancel') }}
                    </button>
                    <button
                      type="button"
                      class="btn btn-sm btn-primary"
                      :disabled="newHostSaving"
                      @click="saveNewHost"
                    >
                      <span v-if="newHostSaving" class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>
                      {{ t('forwards.modal.newHostSave') }}
                    </button>
                  </div>
                </div>
              </div>
            </div>
            <div class="field-note">
              {{ sshHosts.length ? t('forwards.modal.chainHint') : t('forwards.modal.noHostsHint') }}
            </div>
          </div>

          <div class="col-12 col-md-7">
            <label class="form-label" for="forwardLocalHost">{{ t('forwards.modal.localHost') }}</label>
            <input
              id="forwardLocalHost"
              v-model="form.localHost"
              type="text"
              class="form-control"
              placeholder="127.0.0.1"
            />
          </div>
          <div class="col-12 col-md-5">
            <label class="form-label" for="forwardLocalPort">{{ t('forwards.modal.localPort') }}</label>
            <input
              id="forwardLocalPort"
              v-model="form.localPort"
              type="number"
              min="1"
              max="65535"
              class="form-control"
            />
            <div v-if="portWarning" class="field-note text-warning">{{ portWarning }}</div>
          </div>

          <template v-if="form.mode !== 'dynamic'">
            <div class="col-12 col-md-7">
              <label class="form-label" for="forwardRemoteHost">{{ t('forwards.modal.remoteHost') }}</label>
              <input
                id="forwardRemoteHost"
                v-model="form.remoteHost"
                type="text"
                class="form-control"
                placeholder="e.g. db.internal"
              />
            </div>
            <div class="col-12 col-md-5">
              <label class="form-label" for="forwardRemotePort">{{ t('forwards.modal.remotePort') }}</label>
              <input
                id="forwardRemotePort"
                v-model="form.remotePort"
                type="number"
                min="1"
                max="65535"
                class="form-control"
              />
            </div>
          </template>

          <div class="col-12">
            <label class="form-label" for="forwardDescription">{{ t('forwards.modal.description') }}</label>
            <textarea
              id="forwardDescription"
              v-model="form.description"
              class="form-control"
              rows="2"
              :placeholder="t('forwards.modal.descriptionPlaceholder')"
            ></textarea>
          </div>

          <div class="col-12">
            <div class="form-check">
              <input id="forwardAutoStart" v-model="form.autoStart" class="form-check-input" type="checkbox" />
              <label class="form-check-label" for="forwardAutoStart">{{ t('forwards.modal.autoStart') }}</label>
            </div>
          </div>
        </div>

        <div v-if="validationError" class="form-error mt-2">{{ validationError }}</div>
      </div>
      <div class="dialog-footer">
        <button type="button" class="btn btn-outline-secondary" @click="$emit('close')">
          {{ t('app.common.cancel') }}
        </button>
        <button type="button" class="btn btn-primary" @click="$emit('submit')">
          {{ t('app.common.save') }}
        </button>
      </div>
    </div>
  </div>
</template>
