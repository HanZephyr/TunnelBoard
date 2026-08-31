<script setup>
import { computed, onBeforeUnmount, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { createSSHAuthDrafts, switchSSHHostAuthDraft } from '../../modules/sshHostEditor'
import { GenerateSSHKeyPair, SelectSSHKeyFile, SelectSSHKeySavePath } from '../../../wailsjs/go/main/App'

const props = defineProps({
  draft: { type: Object, required: true },
  mode: { type: String, default: 'full' },
  idPrefix: { type: String, default: 'sshHost' }
})
const { t } = useI18n()
const showAdvanced = ref(false)
const small = computed(() => props.mode === 'compact' ? 'form-control-sm' : '')
const selectSmall = computed(() => props.mode === 'compact' ? 'form-select-sm' : '')
const showsKeyPath = computed(() => props.draft.authType === 'ssh_key')
const showsSecret = computed(() => props.draft.authType === 'password' || props.draft.authType === 'ssh_key')
const showsAgent = computed(() => props.draft.authType === 'ssh_agent')
const authDrafts = reactive(createSSHAuthDrafts(props.draft))

const generatingKey = ref(false)
const generatedPublicKey = ref('')
const generatedPublicKeyPath = ref('')
const keyActionError = ref('')
const publicKeyCopied = ref(false)
let copyResetTimer = null

function changeAuthType(event) {
  switchSSHHostAuthDraft(props.draft, authDrafts, event.target.value)
}

async function browseKeyFile() {
  keyActionError.value = ''
  try {
    const selected = await SelectSSHKeyFile()
    if (selected) props.draft.keyPath = selected
  } catch (error) {
    keyActionError.value = t('hosts.modal.browseFailed', { message: errorMessage(error) })
  }
}

async function generateKeyPair() {
  keyActionError.value = ''
  generatedPublicKey.value = ''
  generatedPublicKeyPath.value = ''
  generatingKey.value = true
  try {
    // 保存对话框提供目标路径：默认 ~/.ssh，用户可自由修改；取消则中止。
    const destination = await SelectSSHKeySavePath()
    if (!destination) return
    const passphrase = String(props.draft.secretInput || '')
    const result = await GenerateSSHKeyPair({ destination, ...(passphrase ? { passphrase } : {}) })
    props.draft.keyPath = result.keyPath
    props.draft.secretAction = passphrase ? 'replace' : 'clear'
    generatedPublicKey.value = result.publicKey
    generatedPublicKeyPath.value = result.publicKeyPath || `${result.keyPath}.pub`
    publicKeyCopied.value = false
  } catch (error) {
    const message = errorMessage(error)
    keyActionError.value = message.includes('already exists')
      ? t('hosts.modal.keyExists')
      : t('hosts.modal.keygenFailed', { message })
  } finally {
    generatingKey.value = false
  }
}

async function copyPublicKey() {
  try {
    await navigator.clipboard.writeText(generatedPublicKey.value)
    publicKeyCopied.value = true
    clearTimeout(copyResetTimer)
    copyResetTimer = setTimeout(() => { publicKeyCopied.value = false }, 2000)
  } catch (error) {
    keyActionError.value = t('hosts.modal.copyFailed', { message: errorMessage(error) })
  }
}

function errorMessage(error) {
  return String(error?.message || error || '')
}

onBeforeUnmount(() => {
  if (copyResetTimer) clearTimeout(copyResetTimer)
})
</script>

<template>
  <div class="row g-2">
    <div class="col-12 col-md-6">
      <label class="form-label" :for="`${idPrefix}-name`">{{ t('hosts.modal.name') }}</label>
      <input :id="`${idPrefix}-name`" v-model="draft.name" type="text" class="form-control" :class="small" :placeholder="t('hosts.modal.namePlaceholder')" />
    </div>
    <div class="col-12 col-md-6">
      <label class="form-label" :for="`${idPrefix}-user`">{{ t('hosts.modal.user') }}</label>
      <input :id="`${idPrefix}-user`" v-model="draft.user" type="text" class="form-control" :class="small" autocomplete="username" />
    </div>
    <div class="col-12 col-md-8">
      <label class="form-label" :for="`${idPrefix}-host`">{{ t('hosts.modal.host') }}</label>
      <input :id="`${idPrefix}-host`" v-model="draft.host" type="text" class="form-control" :class="small" :placeholder="t('hosts.modal.hostPlaceholder')" />
    </div>
    <div class="col-12 col-md-4">
      <label class="form-label" :for="`${idPrefix}-port`">{{ t('hosts.modal.port') }}</label>
      <input :id="`${idPrefix}-port`" v-model="draft.port" type="number" min="1" max="65535" class="form-control" :class="small" />
    </div>
    <div class="col-12">
      <label class="form-label" :for="`${idPrefix}-auth`">{{ t('hosts.modal.authType') }}</label>
      <select :id="`${idPrefix}-auth`" :value="draft.authType" class="form-select" :class="selectSmall" @change="changeAuthType">
        <option value="password">{{ t('hosts.auth.password') }}</option>
        <option value="ssh_key">{{ t('hosts.auth.sshKey') }}</option>
        <option value="ssh_agent">{{ t('hosts.auth.sshAgent') }}</option>
      </select>
    </div>
    <div v-if="showsKeyPath" class="col-12">
      <label class="form-label" :for="`${idPrefix}-key`">{{ t('hosts.modal.keyPath') }}</label>
      <div class="input-group">
        <input :id="`${idPrefix}-key`" v-model="draft.keyPath" type="text" class="form-control" :class="small" :placeholder="t('hosts.modal.keyPathPlaceholder')" />
        <button type="button" class="btn btn-outline-secondary" :disabled="generatingKey" @click="browseKeyFile">{{ t('hosts.modal.browseKeyFile') }}</button>
        <button type="button" class="btn btn-outline-primary" :disabled="generatingKey" @click="generateKeyPair">
          <span v-if="generatingKey" class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>
          {{ generatingKey ? t('hosts.modal.generatingKey') : t('hosts.modal.generateKey') }}
        </button>
      </div>
      <div v-if="keyActionError" class="form-error mt-1" role="alert">{{ keyActionError }}</div>
      <div v-if="generatedPublicKey" class="generated-key-box mt-2">
        <div class="d-flex align-items-center justify-content-between mb-1">
          <span class="field-note">{{ t('hosts.modal.publicKeyTitle') }}</span>
          <button type="button" class="btn btn-sm btn-outline-secondary" @click="copyPublicKey">
            {{ publicKeyCopied ? t('hosts.modal.publicKeyCopied') : t('hosts.modal.copyPublicKey') }}
          </button>
        </div>
        <div class="field-note mb-1">{{ t('hosts.modal.publicKeySavedTo', { path: generatedPublicKeyPath }) }}</div>
        <code class="generated-key-value d-block">{{ generatedPublicKey }}</code>
      </div>
    </div>
    <div v-if="showsSecret" class="col-12">
      <label class="form-label" :for="`${idPrefix}-secret`">{{ draft.authType === 'ssh_key' ? t('hosts.modal.keyPassphrase') : t('hosts.modal.password') }}</label>
      <select v-if="draft.hasSecret" v-model="draft.secretAction" class="form-select mb-2" :class="selectSmall" :aria-label="t('hosts.modal.secretAction')">
        <option value="keep">{{ t('hosts.modal.keepSecret') }}</option>
        <option value="replace">{{ t('hosts.modal.replaceSecret') }}</option>
        <option value="clear">{{ t('hosts.modal.clearSecret') }}</option>
      </select>
      <input v-if="!draft.hasSecret || draft.secretAction === 'replace'" :id="`${idPrefix}-secret`" v-model="draft.secretInput" type="password" class="form-control" :class="small" autocomplete="new-password" />
    </div>
    <div v-if="showsAgent" class="col-12">
      <label class="form-label" :for="`${idPrefix}-agent`">{{ t('hosts.modal.agentSocketPath') }}</label>
      <input :id="`${idPrefix}-agent`" v-model="draft.agentSocketPath" type="text" class="form-control" :class="small" :placeholder="t('hosts.modal.agentSocketPlaceholder')" />
    </div>
    <div class="col-12">
      <button type="button" class="advanced-toggle" :aria-expanded="showAdvanced" @click="showAdvanced = !showAdvanced">{{ t('hosts.modal.advanced') }}</button>
      <div v-if="showAdvanced" class="advanced-box mt-1 row g-2">
        <div class="col-6"><label class="form-label" :for="`${idPrefix}-keepalive`">{{ t('hosts.modal.keepAlive') }}</label><input :id="`${idPrefix}-keepalive`" v-model="draft.keepAliveIntervalMs" type="number" min="0" class="form-control" :class="small" /></div>
        <div class="col-6"><label class="form-label" :for="`${idPrefix}-timeout`">{{ t('hosts.modal.timeout') }}</label><input :id="`${idPrefix}-timeout`" v-model="draft.timeoutMs" type="number" min="0" class="form-control" :class="small" /></div>
        <div class="col-12"><label class="form-label" :for="`${idPrefix}-algorithms`">{{ t('hosts.modal.hostKeyAlgorithms') }}</label><input :id="`${idPrefix}-algorithms`" v-model="draft.hostKeyAlgorithms" class="form-control" :class="small" /></div>
        <div class="col-12"><label class="form-label" :for="`${idPrefix}-notes`">{{ t('hosts.modal.notes') }}</label><textarea :id="`${idPrefix}-notes`" v-model="draft.notes" class="form-control" :class="small" rows="2" /></div>
      </div>
    </div>
  </div>
</template>
