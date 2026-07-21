<script setup>
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

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

watch(() => props.draft.authType, (type) => {
  if (type !== 'ssh_key') props.draft.keyPath = ''
  if (type !== 'ssh_agent') props.draft.agentSocketPath = ''
  if (type === 'ssh_agent') props.draft.secretInput = ''
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
      <select :id="`${idPrefix}-auth`" v-model="draft.authType" class="form-select" :class="selectSmall">
        <option value="password">{{ t('hosts.auth.password') }}</option>
        <option value="ssh_key">{{ t('hosts.auth.sshKey') }}</option>
        <option value="ssh_agent">{{ t('hosts.auth.sshAgent') }}</option>
      </select>
    </div>
    <div v-if="showsKeyPath" class="col-12">
      <label class="form-label" :for="`${idPrefix}-key`">{{ t('hosts.modal.keyPath') }}</label>
      <input :id="`${idPrefix}-key`" v-model="draft.keyPath" type="text" class="form-control" :class="small" :placeholder="t('hosts.modal.keyPathPlaceholder')" />
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
    <div v-if="mode === 'full'" class="col-12">
      <button type="button" class="advanced-toggle" :aria-expanded="showAdvanced" @click="showAdvanced = !showAdvanced">{{ t('hosts.modal.advanced') }}</button>
      <div v-if="showAdvanced" class="advanced-box mt-1 row g-2">
        <div class="col-6"><label class="form-label" :for="`${idPrefix}-keepalive`">{{ t('hosts.modal.keepAlive') }}</label><input :id="`${idPrefix}-keepalive`" v-model="draft.keepAliveIntervalMs" type="number" min="0" class="form-control" /></div>
        <div class="col-6"><label class="form-label" :for="`${idPrefix}-timeout`">{{ t('hosts.modal.timeout') }}</label><input :id="`${idPrefix}-timeout`" v-model="draft.timeoutMs" type="number" min="0" class="form-control" /></div>
        <div class="col-12"><label class="form-label" :for="`${idPrefix}-algorithms`">{{ t('hosts.modal.hostKeyAlgorithms') }}</label><input :id="`${idPrefix}-algorithms`" v-model="draft.hostKeyAlgorithms" class="form-control" /></div>
        <div class="col-12"><label class="form-label" :for="`${idPrefix}-notes`">{{ t('hosts.modal.notes') }}</label><textarea :id="`${idPrefix}-notes`" v-model="draft.notes" class="form-control" rows="2" /></div>
      </div>
    </div>
  </div>
</template>
