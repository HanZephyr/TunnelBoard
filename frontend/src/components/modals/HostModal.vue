<script setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps({
  show: {
    type: Boolean,
    default: false
  },
  editingHostId: {
    type: Number,
    default: null
  },
  // 由父组件持有的响应式表单对象
  form: {
    type: Object,
    required: true
  },
  validationError: {
    type: String,
    default: ''
  }
})

defineEmits(['close', 'submit'])

const { t } = useI18n()

const showAdvanced = ref(false)

const showsKeyPath = computed(() => props.form.authType === 'ssh_key')
const showsPassword = computed(() => props.form.authType === 'password' || props.form.authType === 'ssh_key')
const showsAgentSocket = computed(() => props.form.authType === 'ssh_agent')

watch(
  () => [props.form.authType, props.show],
  () => {
    if (!showsKeyPath.value) props.form.keyPath = ''
    if (!showsPassword.value) props.form.password = ''
    if (!showsAgentSocket.value) props.form.agentSocketPath = ''
    if (!props.show) showAdvanced.value = false
  }
)
</script>

<template>
  <div v-if="show" class="overlay">
    <div class="dialog-card compact-dialog host-dialog">
      <div class="dialog-head">
        <h3 class="dialog-title">
          {{ editingHostId ? t('hosts.modal.editTitle') : t('hosts.modal.newTitle') }}
        </h3>
      </div>
      <div class="dialog-body">
        <div class="row g-2">
          <div class="col-12 col-md-6">
            <label class="form-label" for="hostName">{{ t('hosts.modal.name') }}</label>
            <input
              id="hostName"
              v-model="form.name"
              type="text"
              class="form-control"
              :placeholder="t('hosts.modal.namePlaceholder')"
            />
          </div>
          <div class="col-12 col-md-6">
            <label class="form-label" for="hostUser">{{ t('hosts.modal.user') }}</label>
            <input id="hostUser" v-model="form.user" type="text" class="form-control" placeholder="e.g. ubuntu" />
          </div>

          <div class="col-12 col-md-8">
            <label class="form-label" for="hostAddress">{{ t('hosts.modal.host') }}</label>
            <input
              id="hostAddress"
              v-model="form.host"
              type="text"
              class="form-control"
              :placeholder="t('hosts.modal.hostPlaceholder')"
            />
          </div>
          <div class="col-12 col-md-4">
            <label class="form-label" for="hostPort">{{ t('hosts.modal.port') }}</label>
            <input
              id="hostPort"
              v-model="form.port"
              type="number"
              min="1"
              max="65535"
              class="form-control"
            />
          </div>

          <div class="col-12">
            <label class="form-label" for="hostAuthType">{{ t('hosts.modal.authType') }}</label>
            <select id="hostAuthType" v-model="form.authType" class="form-select">
              <option value="password">{{ t('hosts.auth.password') }}</option>
              <option value="ssh_key">{{ t('hosts.auth.sshKey') }}</option>
              <option value="ssh_agent">{{ t('hosts.auth.sshAgent') }}</option>
            </select>
          </div>

          <div v-if="showsKeyPath" class="col-12">
            <label class="form-label" for="hostKeyPath">{{ t('hosts.modal.keyPath') }}</label>
            <input
              id="hostKeyPath"
              v-model="form.keyPath"
              type="text"
              class="form-control"
              :placeholder="t('hosts.modal.keyPathPlaceholder')"
            />
          </div>

          <div v-if="showsPassword" class="col-12">
            <label class="form-label" for="hostPassword">
              {{ form.authType === 'ssh_key' ? t('hosts.modal.keyPassphrase') : t('hosts.modal.password') }}
            </label>
            <input
              id="hostPassword"
              v-model="form.password"
              type="password"
              class="form-control"
              :placeholder="
                form.authType === 'ssh_key'
                  ? t('hosts.modal.keyPassphrasePlaceholder')
                  : t('hosts.modal.passwordPlaceholder')
              "
            />
          </div>

          <div v-if="showsAgentSocket" class="col-12">
            <label class="form-label" for="hostAgentSocket">{{ t('hosts.modal.agentSocketPath') }}</label>
            <input
              id="hostAgentSocket"
              v-model="form.agentSocketPath"
              type="text"
              class="form-control"
              :placeholder="t('hosts.modal.agentSocketPlaceholder')"
            />
          </div>

          <div class="col-12">
            <button type="button" class="advanced-toggle" @click="showAdvanced = !showAdvanced">
              <span class="advanced-chevron" :class="{ open: showAdvanced }">&#9656;</span>
              {{ t('hosts.modal.advanced') }}
            </button>
            <div v-if="showAdvanced" class="advanced-box mt-1">
              <div class="row g-2">
                <div class="col-12 col-md-6">
                  <label class="form-label" for="hostKeepAlive">{{ t('hosts.modal.keepAlive') }}</label>
                  <input
                    id="hostKeepAlive"
                    v-model="form.keepAliveIntervalMs"
                    type="number"
                    min="0"
                    class="form-control"
                  />
                </div>
                <div class="col-12 col-md-6">
                  <label class="form-label" for="hostTimeout">{{ t('hosts.modal.timeout') }}</label>
                  <input
                    id="hostTimeout"
                    v-model="form.timeoutMs"
                    type="number"
                    min="0"
                    class="form-control"
                  />
                </div>
                <div class="col-12">
                  <label class="form-label" for="hostKeyAlgorithms">{{ t('hosts.modal.hostKeyAlgorithms') }}</label>
                  <input
                    id="hostKeyAlgorithms"
                    v-model="form.hostKeyAlgorithms"
                    type="text"
                    class="form-control"
                    :placeholder="t('hosts.modal.hostKeyAlgorithmsPlaceholder')"
                  />
                </div>
                <div class="col-12">
                  <label class="form-label" for="hostNotes">{{ t('hosts.modal.notes') }}</label>
                  <textarea
                    id="hostNotes"
                    v-model="form.notes"
                    class="form-control"
                    rows="2"
                    :placeholder="t('hosts.modal.notesPlaceholder')"
                  ></textarea>
                </div>
              </div>
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
