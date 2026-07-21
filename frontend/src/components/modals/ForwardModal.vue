<script setup>
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { CheckLocalPortAvailable, SaveSSHHost } from '../../../wailsjs/go/main/App'
import { callBackend, errorMessage, isValidPort } from '../../utils/backend'
import BaseDialog from '../common/BaseDialog.vue'
import SSHHostFields from '../hosts/SSHHostFields.vue'
import { createSSHHostDraft, toSaveSSHHostCommand, validateSSHHostDraft } from '../../modules/sshHostEditor'
import { createApplicationClient } from '../../utils/applicationClient'

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
const application = createApplicationClient()

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
const newHostForm = reactive(createSSHHostDraft())

watch(
  () => props.show,
  (visible) => {
    if (!visible) newHostOpen.value = false
  }
)

function openNewHostForm() {
  Object.assign(newHostForm, createSSHHostDraft())
  newHostError.value = ''
  newHostOpen.value = true
}

function cancelNewHostForm() {
  newHostOpen.value = false
  newHostError.value = ''
}

function validateNewHost() {
  const code = validateSSHHostDraft(newHostForm)
  return code ? t(`hosts.errors.${code}`) : ''
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
  const command = toSaveSSHHostCommand(newHostForm)
  try {
    const result = await application.saveSSHHost(command, (payload) => callBackend(SaveSSHHost, payload))
    const saved = result?.host || result
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
let portCheckGeneration = 0
watch(
  () => [props.show, props.form.mode, props.form.localHost, props.form.localPort],
  () => {
    const generation = ++portCheckGeneration
    portWarning.value = ''
    if (portCheckTimer) clearTimeout(portCheckTimer)
    if (!props.show || props.form.mode === 'remote') return
    const port = Number(props.form.localPort)
    if (!Number.isInteger(port) || port < 1 || port > 65535) return
    const host = String(props.form.localHost || '')
    portCheckTimer = setTimeout(async () => {
      try {
        const result = await application.previewLocalListener({ host, port, editingForwardId: props.editingForwardId || 0 }, (localHost, localPort) => CheckLocalPortAvailable(localHost, localPort))
        if (generation !== portCheckGeneration || !props.show) return
        portWarning.value = result?.status === 'occupied' ? t('forwards.modal.portConflict', { port }) : result?.status === 'unknown' ? String(result?.message || '') : ''
      } catch (_) {
        if (generation !== portCheckGeneration || !props.show) return
        portWarning.value = t('forwards.modal.portConflict', { port })
      }
    }, 400)
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  portCheckGeneration += 1
  if (portCheckTimer) clearTimeout(portCheckTimer)
})
</script>

<template>
  <BaseDialog :visible="show" :title="editingForwardId ? t('forwards.modal.editTitle') : t('forwards.modal.newTitle')" class="forward-dialog" @close="$emit('close')">
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
                <p class="field-note" role="note">{{ t('forwards.modal.newHostPersists') }}</p>
                <SSHHostFields :draft="newHostForm" mode="compact" id-prefix="forward-new-host" />
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
    <template #footer>
        <button type="button" class="btn btn-outline-secondary" @click="$emit('close')">
          {{ t('app.common.cancel') }}
        </button>
        <button type="button" class="btn btn-primary" @click="$emit('submit')">
          {{ t('app.common.save') }}
        </button>
    </template>
  </BaseDialog>
</template>
