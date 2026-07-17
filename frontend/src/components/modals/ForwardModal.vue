<script setup>
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

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

defineEmits(['close', 'submit'])

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
