<script setup>
import { useI18n } from 'vue-i18n'
import BaseDialog from '../common/BaseDialog.vue'
import SSHHostFields from '../hosts/SSHHostFields.vue'

defineProps({
  show: { type: Boolean, default: false },
  editingHostId: { type: Number, default: null },
  form: { type: Object, required: true },
  validationError: { type: String, default: '' },
  busy: { type: Boolean, default: false }
})
defineEmits(['close', 'submit'])
const { t } = useI18n()
</script>

<template>
  <BaseDialog :visible="show" :title="editingHostId ? t('hosts.modal.editTitle') : t('hosts.modal.newTitle')" :busy="busy" class="host-dialog" @close="$emit('close')">
    <SSHHostFields :draft="form" mode="full" id-prefix="host-dialog" />
    <div v-if="validationError" class="form-error mt-2" role="alert">{{ validationError }}</div>
    <template #footer>
      <button type="button" class="btn btn-outline-secondary" data-dialog-initial-focus :disabled="busy" @click="$emit('close')">{{ t('app.common.cancel') }}</button>
      <button type="button" class="btn btn-primary" :disabled="busy" @click="$emit('submit')">{{ t('app.common.save') }}</button>
    </template>
  </BaseDialog>
</template>
