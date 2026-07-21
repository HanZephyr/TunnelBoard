<script setup>
import BaseDialog from './BaseDialog.vue'

defineProps({
  visible: {
    type: Boolean,
    default: false
  },
  title: {
    type: String,
    default: ''
  },
  message: {
    type: String,
    default: ''
  },
  confirmLabel: {
    type: String,
    default: ''
  },
  confirmClass: {
    type: String,
    default: 'btn-danger'
  },
  showCancel: {
    type: Boolean,
    default: true
  },
  busy: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['confirm', 'close'])
</script>

<template>
  <BaseDialog :visible="visible" :title="title || $t('app.common.confirm')" :busy="busy" class="action-confirm-dialog" @close="emit('close')">
    <p class="action-dialog-message mb-0">{{ message }}</p>
    <template #footer>
        <button
          v-if="showCancel"
          type="button"
          class="btn btn-outline-secondary"
          data-dialog-initial-focus
          :disabled="busy"
          @click="emit('close')"
        >
          {{ $t('app.common.cancel') }}
        </button>
        <button type="button" class="btn" :class="confirmClass" :disabled="busy" @click="emit('confirm')">
          {{ confirmLabel || $t('app.common.confirm') }}
        </button>
    </template>
  </BaseDialog>
</template>
