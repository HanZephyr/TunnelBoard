<script setup>
import { useI18n } from 'vue-i18n'
import BaseDialog from '../common/BaseDialog.vue'
import SSHHostFields from '../hosts/SSHHostFields.vue'

defineProps({
  show: { type: Boolean, default: false },
  editingHostId: { type: Number, default: null },
  form: { type: Object, required: true },
  validationError: { type: String, default: '' },
  restartPreview: { type: Object, default: null },
  busy: { type: Boolean, default: false },
  testStatus: { type: String, default: 'idle' },
  testMessage: { type: String, default: '' }
})
defineEmits(['close', 'submit', 'test-connection', 'confirm-restart', 'back-to-edit'])
const { t } = useI18n()
</script>

<template>
  <BaseDialog :visible="show" :title="editingHostId ? t('hosts.modal.editTitle') : t('hosts.modal.newTitle')" :busy="busy" class="host-dialog" @close="$emit('close')">
    <div v-if="restartPreview" class="alert alert-warning mb-0" role="alert">
      <div class="fw-semibold mb-2">{{ t('hosts.restart.title') }}</div>
      <p class="mb-2">{{ t('hosts.restart.message', { count: restartPreview.affectedForwardIds?.length || 0 }) }}</p>
      <p v-if="restartPreview.runningForwardIds?.length" class="mb-0">
        {{ t('hosts.restart.running', { count: restartPreview.runningForwardIds.length }) }}
      </p>
    </div>
    <fieldset v-else class="border-0 p-0 m-0 w-100" :disabled="busy">
      <SSHHostFields :draft="form" mode="full" id-prefix="host-dialog" />
    </fieldset>
    <div v-if="validationError" class="form-error mt-2" role="alert">{{ validationError }}</div>
    <template #footer>
      <div v-if="!restartPreview" class="dialog-left-actions">
        <button type="button" class="btn btn-outline-primary" :disabled="busy" @click="$emit('test-connection')">
          <span v-if="testStatus === 'testing'" class="spinner-border spinner-border-sm me-1" aria-hidden="true"></span>
          {{ testStatus === 'testing' ? t('hosts.modal.testing') : t('hosts.modal.testConnection') }}
        </button>
        <span v-if="testMessage" class="field-note ms-2" :class="testStatus === 'success' ? 'text-success' : 'text-danger'" role="status" aria-live="polite">{{ testMessage }}</span>
      </div>
      <button v-if="restartPreview" type="button" class="btn btn-outline-secondary" data-dialog-initial-focus :disabled="busy" @click="$emit('back-to-edit')">{{ t('hosts.restart.back') }}</button>
      <button v-else type="button" class="btn btn-outline-secondary" data-dialog-initial-focus :disabled="busy" @click="$emit('close')">{{ t('app.common.cancel') }}</button>
      <button v-if="restartPreview" type="button" class="btn btn-warning" :disabled="busy" @click="$emit('confirm-restart')">{{ t('hosts.restart.confirm') }}</button>
      <button v-else type="button" class="btn btn-primary" :disabled="busy" @click="$emit('submit')">{{ t('app.common.save') }}</button>
    </template>
  </BaseDialog>
</template>
